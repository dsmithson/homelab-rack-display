// Package panel captures the rotation page from a headless Chromium and
// pushes frames to a physical display.
package panel

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Output is a display that accepts full frames (drm.Display, fbdev.Framebuffer).
type Output interface {
	Bounds() image.Rectangle
	Show(*image.RGBA) error
}

// Capturer screenshots URL every Interval and forwards changed frames to the
// attached output (see SetOutput).
type Capturer struct {
	URL      string
	Chrome   string // browser binary; empty = chromedp's default lookup
	Width    int
	Height   int
	Interval time.Duration
	Log      *slog.Logger

	mu  sync.RWMutex
	out Output // nil = capture only (e.g. for /frame.png)
	png []byte
	at  time.Time
}

// SetOutput attaches (or replaces) the physical display; the next frame is
// pushed in full.
func (c *Capturer) SetOutput(o Output) {
	c.mu.Lock()
	c.out = o
	c.png = nil
	c.mu.Unlock()
}

// LatestPNG returns the most recent frame.
func (c *Capturer) LatestPNG() ([]byte, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.png, c.at
}

// Run captures until ctx is done, restarting the browser if it fails.
func (c *Capturer) Run(ctx context.Context) {
	for {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.Log.Error("browser session ended; restarting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Capturer) session(ctx context.Context) error {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(c.Width, c.Height),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("force-device-scale-factor", "1"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("font-render-hinting", "none"),
		// No separate GPU process: nothing to accelerate headless, and it is
		// one less child that can fail to launch (it does under qemu).
		chromedp.Flag("in-process-gpu", true),
	)
	if c.Chrome != "" {
		opts = append(opts, chromedp.ExecPath(c.Chrome))
	}
	if os.Geteuid() == 0 {
		// Root in a container: the sandbox can't be used, and without it
		// the zygote only adds another process to go wrong.
		opts = append(opts, chromedp.NoSandbox, chromedp.Flag("no-zygote", true))
	}
	actx, cancelA := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelA()
	bctx, cancelB := chromedp.NewContext(actx)
	defer cancelB()

	// The device-metrics override pins the viewport to exactly WxH, which
	// --window-size alone does not guarantee in new headless mode.
	if err := chromedp.Run(bctx,
		emulation.SetDeviceMetricsOverride(int64(c.Width), int64(c.Height), 1, false),
		chromedp.Navigate(c.URL),
	); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	c.Log.Info("browser started", "url", c.URL)

	img := image.NewRGBA(image.Rect(0, 0, c.Width, c.Height))
	var last []byte
	var lastErr time.Time
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		var buf []byte
		err := chromedp.Run(bctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).Do(ctx)
			return err
		}))
		if err != nil {
			return fmt.Errorf("screenshot: %w", err)
		}
		c.mu.Lock()
		out, fresh := c.out, c.png == nil
		c.png, c.at = buf, time.Now()
		c.mu.Unlock()
		if bytes.Equal(buf, last) && !fresh {
			continue
		}
		last = buf
		if out == nil {
			continue
		}
		src, err := png.Decode(bytes.NewReader(buf))
		if err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
		if err := out.Show(img); err != nil && time.Since(lastErr) > time.Minute {
			lastErr = time.Now()
			c.Log.Error("panel output failed", "err", err)
		}
	}
}

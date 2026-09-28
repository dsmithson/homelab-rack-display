// displaytest drives the rack display with an animated test pattern to prove
// the output path works end to end.
//
//	displaytest -list                  # show DRM cards and drivers
//	displaytest                        # auto-pick a udl/evdi card
//	displaytest -out /dev/dri/card6    # specific DRM card
//	displaytest -out /dev/fb1          # legacy fbdev fallback
//	displaytest -out preview.png       # render one frame to a PNG
//	displaytest -url http://host/frame.png   # mirror a remote rack-display
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"homelab-rack-display/internal/drm"
	"homelab-rack-display/internal/fbdev"
	"homelab-rack-display/internal/render"
)

type output interface {
	Bounds() image.Rectangle
	Show(*image.RGBA) error
	Close() error
}

func main() {
	out := flag.String("out", "auto", "auto | /dev/dri/cardN | /dev/fbN | file.png")
	list := flag.Bool("list", false, "list DRM cards and exit")
	size := flag.String("size", "", "mode WxH to request (DRM) or PNG size; default = preferred mode / 1440x240")
	fps := flag.Float64("fps", 5, "frames per second")
	duration := flag.Duration("duration", 0, "exit after this long (0 = until Ctrl-C)")
	images := flag.String("images", "", "directory of PNGs to show as a slideshow instead of the test pattern")
	dwell := flag.Duration("dwell", 8*time.Second, "time per image in -images mode")
	frameURL := flag.String("url", "", "poll this PNG URL (e.g. rack-display /frame.png) and mirror it")
	flag.Parse()

	if *list {
		cards, err := drm.ListCards()
		if err != nil {
			log.Fatal(err)
		}
		for _, c := range cards {
			fmt.Printf("%-18s %s\n", c.Path, c.Driver)
		}
		return
	}

	var w, h int
	if *size != "" {
		if _, err := fmt.Sscanf(*size, "%dx%d", &w, &h); err != nil {
			log.Fatalf("bad -size %q: %v", *size, err)
		}
	}

	tp, err := render.NewTestPattern()
	if err != nil {
		log.Fatal(err)
	}
	host, _ := os.Hostname()

	if strings.HasSuffix(*out, ".png") {
		if w == 0 {
			w, h = 1440, 240
		}
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		tp.Draw(img, 0, time.Now(), host+"  preview")
		if err := writePNG(*out, img); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", *out)
		return
	}

	dev, desc, err := openOutput(*out, w, h)
	if err != nil {
		log.Fatal(err)
	}
	defer dev.Close()
	log.Printf("driving %s at %v", desc, dev.Bounds().Size())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	var deadline <-chan time.Time
	if *duration > 0 {
		deadline = time.After(*duration)
	}
	tick := time.NewTicker(time.Duration(float64(time.Second) / *fps))
	defer tick.Stop()

	if *frameURL != "" {
		if err := mirror(dev, *frameURL, time.Duration(float64(time.Second) / *fps), sig, deadline); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *images != "" {
		if err := slideshow(dev, *images, *dwell, sig, deadline); err != nil {
			log.Fatal(err)
		}
		return
	}

	img := image.NewRGBA(dev.Bounds())
	info := fmt.Sprintf("%s  %s", host, desc)
	for frame := 0; ; frame++ {
		start := time.Now()
		tp.Draw(img, frame, start, info)
		if err := dev.Show(img); err != nil {
			log.Fatalf("frame %d: %v", frame, err)
		}
		if frame%50 == 0 {
			log.Printf("frame %d (%.1f ms)", frame, float64(time.Since(start).Microseconds())/1000)
		}
		select {
		case <-tick.C:
		case <-sig:
			return
		case <-deadline:
			return
		}
	}
}

func openOutput(out string, w, h int) (output, string, error) {
	if out == "auto" {
		p, err := drm.FindCard("udl", "evdi")
		if err != nil {
			return nil, "", err
		}
		out = p
	}
	if strings.HasPrefix(out, "/dev/fb") {
		fb, err := fbdev.Open(out)
		return fb, out + " (fbdev)", err
	}
	d, err := drm.Open(out, drm.Options{Width: w, Height: h})
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", out, err)
	}
	return d, fmt.Sprintf("%s (drm %s)", out, d.Mode), nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// slideshow cycles through every PNG in dir (sorted by name).
func slideshow(dev output, dir string, dwell time.Duration, sig <-chan os.Signal, deadline <-chan time.Time) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil || len(paths) == 0 {
		return fmt.Errorf("no PNGs in %s", dir)
	}
	sort.Strings(paths)
	img := image.NewRGBA(dev.Bounds())
	for i := 0; ; i = (i + 1) % len(paths) {
		f, err := os.Open(paths[i])
		if err != nil {
			return err
		}
		src, err := png.Decode(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", paths[i], err)
		}
		draw.Draw(img, img.Bounds(), image.Black, image.Point{}, draw.Src)
		draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
		if err := dev.Show(img); err != nil {
			return err
		}
		log.Printf("showing %s", filepath.Base(paths[i]))
		select {
		case <-time.After(dwell):
		case <-sig:
			return nil
		case <-deadline:
			return nil
		}
	}
}

// mirror polls a PNG URL and shows it whenever it changes.
func mirror(dev output, url string, every time.Duration, sig <-chan os.Signal, deadline <-chan time.Time) error {
	img := image.NewRGBA(dev.Bounds())
	var last []byte
	hc := &http.Client{Timeout: 5 * time.Second}
	for n := 0; ; {
		if resp, err := hc.Get(url); err != nil {
			log.Printf("fetch: %v", err)
		} else {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && !bytes.Equal(b, last) {
				if src, err := png.Decode(bytes.NewReader(b)); err == nil {
					last = b
					draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
					if err := dev.Show(img); err != nil {
						return err
					}
					if n++; n%30 == 1 {
						log.Printf("frame %d", n)
					}
				}
			}
		}
		select {
		case <-time.After(every):
		case <-sig:
			return nil
		case <-deadline:
			return nil
		}
	}
}

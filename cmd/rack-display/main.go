// rack-display serves the homelab status screens over HTTP and, optionally,
// renders them to the 1440x240 rack panel via headless Chromium + DRM.
//
//	rack-display -config config/display.json                 # web only
//	rack-display -config ... -panel auto                     # + drive the udl panel
//	rack-display -config ... -panel none                     # + capture for /frame.png only
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"homelab-rack-display/design"
	"homelab-rack-display/internal/collector"
	"homelab-rack-display/internal/config"
	"homelab-rack-display/internal/drm"
	"homelab-rack-display/internal/fbdev"
	"homelab-rack-display/internal/grafana"
	"homelab-rack-display/internal/panel"
	"homelab-rack-display/internal/prom"
	"homelab-rack-display/internal/server"
)

func main() {
	cfgPath := flag.String("config", "config/display.json", "config file")
	listen := flag.String("listen", ":8080", "HTTP listen address")
	webDir := flag.String("web", "", "serve web assets from this directory instead of the embedded copy (dev)")
	panelOut := flag.String("panel", "", `"" = off, "auto" = find udl/evdi card, "none" = capture without output, or /dev/dri/cardN | /dev/fbN`)
	chrome := flag.String("chrome", os.Getenv("CHROME_PATH"), "Chromium binary for the panel renderer")
	interval := flag.Duration("interval", time.Second, "panel capture interval")
	recycle := flag.Duration("browser-recycle", 6*time.Hour, "restart the panel's Chromium this often to cap its memory growth (0 = never)")
	promURL := flag.String("prometheus", "", "override prometheus.url from config")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *cfgPath, *listen, *webDir, *panelOut, *chrome, *interval, *recycle, *promURL); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, cfgPath, listen, webDir, panelOut, chrome string, interval, recycle time.Duration, promURL string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if promURL != "" {
		cfg.Prometheus.URL = promURL
	}

	var web fs.FS = design.FS
	if webDir != "" {
		web = os.DirFS(webDir)
	}
	screens := server.ScreenNames(web)
	for _, s := range cfg.Rotation {
		if !contains(screens, s.Screen) {
			return fmt.Errorf("rotation references unknown screen %q (have %s)", s.Screen, strings.Join(screens, ", "))
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hc := &http.Client{Timeout: 10 * time.Second}
	token := ""
	if cfg.Grafana.TokenEnv != "" {
		token = os.Getenv(cfg.Grafana.TokenEnv)
	}
	if token == "" {
		log.Warn("no Grafana token; alerts will show as unavailable", "env", cfg.Grafana.TokenEnv)
	}
	coll := collector.New(cfg,
		&prom.Client{URL: cfg.Prometheus.URL, HTTP: hc},
		&grafana.Client{URL: cfg.Grafana.URL, Token: token, HTTP: hc},
		log)
	go coll.Run(ctx)

	srv := &server.Server{Cfg: cfg, Coll: coll, Web: web, Log: log}

	if panelOut != "" {
		port := listen[strings.LastIndex(listen, ":")+1:]
		cap := &panel.Capturer{
			URL:    "http://127.0.0.1:" + port + "/?panel",
			Chrome: chrome, Width: 1440, Height: 240, Interval: interval, Recycle: recycle,
			Log: log.With("component", "panel"),
		}
		srv.Frames = cap
		go cap.Run(ctx)
		if panelOut != "none" {
			// The display may be absent (module not loaded, USB unplugged);
			// keep serving the web view and retry rather than exiting.
			go func() {
				for {
					out, closeOut, err := openPanel(panelOut)
					if err == nil {
						log.Info("panel attached", "size", out.Bounds().Size())
						cap.SetOutput(out)
						<-ctx.Done()
						closeOut()
						return
					}
					log.Error("panel unavailable; retrying in 30s", "err", err)
					select {
					case <-ctx.Done():
						return
					case <-time.After(30 * time.Second):
					}
				}
			}()
		}
	}

	hs := &http.Server{Addr: listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	log.Info("listening", "addr", listen, "screens", len(screens), "panel", panelOut)
	if err := hs.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// openPanel opens the physical display and returns it with a closer.
func openPanel(spec string) (panel.Output, func(), error) {
	if spec == "auto" {
		p, err := drm.FindCard("udl", "evdi")
		if err != nil {
			return nil, nil, err
		}
		spec = p
	}
	if strings.HasPrefix(spec, "/dev/fb") {
		fb, err := fbdev.Open(spec)
		if err != nil {
			return nil, nil, err
		}
		return fb, func() { fb.Close() }, nil
	}
	d, err := drm.Open(spec, drm.Options{})
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", spec, err)
	}
	return d, func() { d.Close() }, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

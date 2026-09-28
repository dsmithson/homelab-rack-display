// Package server serves the screens with live data injected, the rotation
// page, a JSON API, and the latest panel frame.
package server

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"homelab-rack-display/internal/collector"
	"homelab-rack-display/internal/config"
)

// FrameSource provides the most recent panel frame as PNG (nil if none yet).
type FrameSource interface {
	LatestPNG() ([]byte, time.Time)
}

// Server wires the HTTP handlers.
type Server struct {
	Cfg    *config.Config
	Coll   *collector.Collector
	Web    fs.FS // index.html, tokens.css, display.js, fonts/, screens/
	Frames FrameSource
	Log    *slog.Logger
}

var dataScript = regexp.MustCompile(`(?s)(<script type="application/json" id="data">).*?(</script>)`)

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static := http.FileServerFS(s.Web)

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		s.serveFile(w, "index.html", "text/html; charset=utf-8")
	})
	mux.HandleFunc("GET /screens/{$}", s.screenIndex)
	mux.HandleFunc("GET /screens/{name}", s.screen)
	mux.HandleFunc("GET /api/data", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.document(r))
	})
	mux.HandleFunc("GET /api/playlist", s.playlist)
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		_, updated := s.Coll.Snapshot()
		writeJSON(w, map[string]any{"updated": updated, "errors": s.Coll.Errors()})
	})
	mux.HandleFunc("GET /frame.png", s.frame)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, updated := s.Coll.Snapshot(); updated.IsZero() {
			http.Error(w, "no data yet", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("GET /", static)
	return mux
}

// document is the data snapshot plus per-request extras (rotation position,
// thresholds). Snapshot maps are shared, so copy the top level only.
func (s *Server) document(r *http.Request) map[string]any {
	snap, _ := s.Coll.Snapshot()
	doc := make(map[string]any, len(snap)+2)
	for k, v := range snap {
		doc[k] = v
	}
	if s.Cfg.Thresholds != nil {
		doc["__thresholds"] = s.Cfg.Thresholds
	}
	q := r.URL.Query()
	if of, err := strconv.Atoi(q.Get("of")); err == nil && of > 0 {
		slot, _ := strconv.Atoi(q.Get("slot"))
		doc["rotation"] = map[string]int{"count": of, "index": slot}
	}
	return doc
}

func (s *Server) screen(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !strings.HasSuffix(name, ".html") {
		name += ".html"
	}
	b, err := fs.ReadFile(s.Web, "screens/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	js, err := json.Marshal(s.document(r)) // escapes <, > and & so it can't close the script tag
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := dataScript.ReplaceAll(b, []byte("${1}\n"+strings.ReplaceAll(string(js), "$", "$$")+"\n${2}"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(out)
}

func (s *Server) screenIndex(w http.ResponseWriter, r *http.Request) {
	entries, _ := fs.ReadDir(s.Web, "screens")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>Screens</title>
<style>body{background:#0b0f17;color:#e6edf3;font:16px sans-serif;margin:24px}a{color:#58a6ff}
iframe{width:1440px;height:240px;border:0;display:block;margin:6px 0 24px;max-width:100%}</style><h1>Screens</h1>`)
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), ".html")
		if n == e.Name() {
			continue
		}
		n = html.EscapeString(n)
		fmt.Fprintf(w, `<h3><a href="/screens/%[1]s?live=15">%[1]s</a> · <a href="/?screen=%[1]s">pinned</a></h3><iframe src="/screens/%[1]s" loading="lazy"></iframe>`, n)
	}
}

type slotOut struct {
	Screen  string `json:"screen"`
	URL     string `json:"url"`
	DwellMS int64  `json:"dwell_ms"`
}

// playlist resolves one pass of the rotation against current data.
func (s *Server) playlist(w http.ResponseWriter, r *http.Request) {
	doc, _ := s.Coll.Snapshot()
	var out []slotOut
	add := func(screen string, d time.Duration, i, n int) {
		out = append(out, slotOut{
			Screen: screen, DwellMS: d.Milliseconds(),
			URL: fmt.Sprintf("/screens/%s.html?slot=%d&of=%d", screen, i, n),
		})
	}

	if pin := r.URL.Query().Get("screen"); pin != "" {
		add(pin, 15*time.Second, 0, 0)
		writeJSON(w, map[string]any{"slots": out})
		return
	}

	n := len(s.Cfg.Rotation)
	prio := false
	if p := s.Cfg.Priority; p != nil {
		prio = s.match(doc, p.When)
	}
	for i, slot := range s.Cfg.Rotation {
		screen, dwell := slot.Screen, slot.Dwell.D()
		for _, v := range slot.Variants {
			if s.match(doc, v.When) {
				screen = v.Screen
				if v.Dwell.D() > 0 {
					dwell = v.Dwell.D()
				}
				break
			}
		}
		if prio && screen == s.Cfg.Priority.Screen {
			continue // shown via interleave instead
		}
		add(screen, dwell, i, n)
		if prio {
			add(s.Cfg.Priority.Screen, s.Cfg.Priority.Dwell.D(), -1, n)
		}
	}
	writeJSON(w, map[string]any{"slots": out})
}

func (s *Server) match(doc map[string]any, expr string) bool {
	ok, err := collector.Eval(doc, expr)
	if err != nil {
		s.Log.Warn("bad condition", "expr", expr, "err", err)
	}
	return ok
}

func (s *Server) frame(w http.ResponseWriter, r *http.Request) {
	if s.Frames == nil {
		http.Error(w, "panel capture disabled", http.StatusNotFound)
		return
	}
	b, at := s.Frames.LatestPNG()
	if b == nil {
		http.Error(w, "no frame yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Last-Modified", at.UTC().Format(http.TimeFormat))
	w.Write(b)
}

func (s *Server) serveFile(w http.ResponseWriter, name, ctype string) {
	b, err := fs.ReadFile(s.Web, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.Encode(v)
}

// ScreenNames lists available screens (for validation/logging).
func ScreenNames(web fs.FS) []string {
	entries, _ := fs.ReadDir(web, "screens")
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".html"); ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

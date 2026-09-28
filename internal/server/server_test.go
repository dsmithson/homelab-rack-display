package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"homelab-rack-display/internal/collector"
	"homelab-rack-display/internal/config"
	"homelab-rack-display/internal/grafana"
	"homelab-rack-display/internal/prom"
)

func newServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	web := fstest.MapFS{
		"screens/a.html": {Data: []byte(`<body><script type="application/json" id="data">{"mock":1}</script></body>`)},
		"screens/b.html": {Data: []byte(`<body></body>`)},
		"screens/c.html": {Data: []byte(`<body></body>`)},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	coll := collector.New(cfg, &prom.Client{URL: "http://127.0.0.1:1"}, &grafana.Client{}, log)
	return &Server{Cfg: cfg, Coll: coll, Web: web, Log: log}
}

func TestScreenInjectsData(t *testing.T) {
	cfg := &config.Config{
		Data:       map[string]config.Binding{"x.y": {Value: "</script><b>"}},
		Thresholds: map[string]config.Threshold{"x.y": {Warn: ">1"}},
	}
	s := newServer(t, cfg)
	s.Coll.Refresh(t.Context())

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/screens/a.html?slot=2&of=7", nil))
	body := rec.Body.String()
	if strings.Contains(body, `"mock"`) {
		t.Error("mock data not replaced")
	}
	if strings.Count(body, "</script>") != 1 {
		t.Errorf("injected value escaped the script tag: %s", body)
	}
	start := strings.Index(body, `id="data">`) + len(`id="data">`)
	end := strings.Index(body, "</script>")
	var doc map[string]any
	if err := json.Unmarshal([]byte(body[start:end]), &doc); err != nil {
		t.Fatalf("injected JSON invalid: %v", err)
	}
	if doc["x"].(map[string]any)["y"] != "</script><b>" {
		t.Errorf("value round-trip failed: %v", doc["x"])
	}
	if r := doc["rotation"].(map[string]any); r["index"] != 2.0 || r["count"] != 7.0 {
		t.Errorf("rotation = %v", r)
	}
	if _, ok := doc["__thresholds"]; !ok {
		t.Error("thresholds missing")
	}
}

func TestPlaylistVariantsAndPriority(t *testing.T) {
	sec := func(n int) config.Duration { return config.Duration(time.Duration(n) * time.Second) }
	cfg := &config.Config{
		Data: map[string]config.Binding{"n": {Value: 0.0}, "crit": {Value: 1.0}},
		Rotation: []config.Slot{
			{Screen: "a", Dwell: sec(10), Variants: []config.Variant{{When: "n == 0", Screen: "b", Dwell: sec(5)}}},
			{Screen: "c", Dwell: sec(10)},
		},
		Priority: &config.Variant{When: "crit > 0", Screen: "c", Dwell: sec(20)},
	}
	s := newServer(t, cfg)
	s.Coll.Refresh(t.Context())

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/playlist", nil))
	var out struct{ Slots []slotOut }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, sl := range out.Slots {
		got = append(got, sl.Screen)
	}
	// a -> variant b; priority c interleaved; the rotation's own c is skipped.
	if strings.Join(got, ",") != "b,c" || out.Slots[0].DwellMS != 5000 || out.Slots[1].DwellMS != 20000 {
		t.Errorf("playlist = %+v", out.Slots)
	}
}

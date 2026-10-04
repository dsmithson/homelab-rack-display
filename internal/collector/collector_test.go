package collector

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"homelab-rack-display/internal/config"
	"homelab-rack-display/internal/grafana"
	"homelab-rack-display/internal/prom"
)

func TestEval(t *testing.T) {
	doc := map[string]any{
		"jellyfin": map[string]any{"streams": 0.0},
		"alerts":   grafana.Summary{Critical: 1},
	}
	cases := map[string]bool{
		"jellyfin.streams == 0":                     true,
		"jellyfin.streams > 0":                      false,
		"alerts.critical > 0":                       true,
		"alerts.warning > 0 || alerts.critical > 0": true,
		"alerts.warning > 0 && alerts.critical > 0": false,
		"missing.path == 0":                         true,
	}
	for expr, want := range cases {
		got, err := Eval(doc, expr)
		if err != nil || got != want {
			t.Errorf("Eval(%q) = %v, %v; want %v", expr, got, err, want)
		}
	}
	if _, err := Eval(doc, "jellyfin.streams ==="); err == nil {
		t.Error("expected error for malformed condition")
	}
}

func TestTemplate(t *testing.T) {
	labels := map[string]string{"series_title": "", "title": "Dune", "type": "Movie"}
	if got := template("{series_title}", "{title}", labels, nil); got != "Dune" {
		t.Errorf("fallback: got %q", got)
	}
	labels["series_title"] = "Severance"
	labels["series_season"], labels["series_episode"] = "S2", "E5"
	if got := template("{series_season} · {series_episode} — {title}", "{type}", labels, nil); got != "S2 · E5 — Dune" {
		t.Errorf("series: got %q", got)
	}
	if got := template("{vcodec} · {acodec}", "", labels, map[string]any{"vcodec": nil}); got != "" {
		t.Errorf("unresolved without else: got %q, want empty", got)
	}
	row := map[string]any{"avail": 0.0, "spec": 1.0}
	if got := template("{avail}/{spec}", "", nil, row); got != "0/1" {
		t.Errorf("row refs: got %q", got)
	}
}

func TestTransform(t *testing.T) {
	col := config.Column{Hex: true, Regex: `^Disk (\d+)$`, Replace: "D$1"}
	if got := transform("0x4469736B2033", col); got != "D3" {
		t.Errorf("hex+regex: got %v", got)
	}
	short := config.Column{Regex: `^turing0\d-(\d+)$`, Replace: "$1", Map: map[string]any{"ds-k8s-media-01": "M1"}}
	if got := transform("turing02-04", short); got != "04" {
		t.Errorf("regex: got %v", got)
	}
	if got := transform("ds-k8s-media-01", short); got != "M1" {
		t.Errorf("map: got %v", got)
	}
	status := config.Column{Map: map[string]any{"1": "ok", "*": "crit"}}
	if transform(1.0, status) != "ok" || transform(3.0, status) != "crit" {
		t.Error("numeric map with fallback")
	}
}

func TestAlign(t *testing.T) {
	end := time.Unix(1000, 0)
	pts := []prom.Point{{T: time.Unix(700, 0), V: 1}, {T: time.Unix(900, 0), V: 3}}
	got := prom.Align(pts, end, 400*time.Second, 100*time.Second)
	want := []float64{0, 1, 1, 3, 3} // 600 700 800 900 1000; gap carried forward
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Align = %v, want %v", got, want)
		}
	}
}

// fakeProm answers instant queries from a fixed map of query -> vector.
func fakeProm(t *testing.T, results map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		res, ok := results[q]
		if !ok {
			res = "[]"
		}
		io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":`+res+`}}`)
	}))
}

func TestRefreshTableJoin(t *testing.T) {
	srv := fakeProm(t, map[string]string{
		"rtt":   `[{"metric":{"target_name":"Quad9"},"value":[0,"22.21"]},{"metric":{"target_name":"Cloudflare"},"value":[0,"11.66"]}]`,
		"loss":  `[{"metric":{"target_name":"Cloudflare"},"value":[0,"5"]}]`,
		"up":    `[{"metric":{},"value":[0,"1"]}]`,
		"local": `[{"metric":{"target_name":"Quad9"},"value":[0,"1"]}]`,
	})
	defer srv.Close()
	one := 1
	cfg := &config.Config{Data: map[string]config.Binding{
		"svc.up":   {Query: "up"},
		"svc.none": {Query: "nothing", Default: 0.0},
		"ping": {Table: &config.Table{
			Query: "rtt", Key: []string{"target_name"}, SortBy: "target",
			Columns: map[string]config.Column{
				"target":   {Label: "target_name"},
				"rtt_ms":   {Round: &one},
				"loss_pct": {Query: "loss", Default: 0.0},
				// "*" maps any joined value; the default must stay literal.
				"kind": {Query: "local", Map: map[string]any{"*": "local"}, Default: "cloud"},
			},
		}},
	}}
	c := New(cfg, &prom.Client{URL: srv.URL}, &grafana.Client{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.Refresh(context.Background())
	doc, _ := c.Snapshot()
	b, _ := json.Marshal(doc)
	got := string(b)
	for _, want := range []string{
		`"ping":[{"kind":"cloud","loss_pct":5,"rtt_ms":11.7,"target":"Cloudflare"},{"kind":"local","loss_pct":0,"rtt_ms":22.2,"target":"Quad9"}]`,
		`"svc":{"none":0,"up":1}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doc missing %s\ngot %s", want, got)
		}
	}
	if len(c.Errors()) != 0 {
		t.Errorf("unexpected errors: %v", c.Errors())
	}
}

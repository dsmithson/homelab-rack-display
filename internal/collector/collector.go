// Package collector evaluates the configured bindings against Prometheus and
// Grafana and assembles the nested data document the screens bind to.
package collector

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"homelab-rack-display/internal/config"
	"homelab-rack-display/internal/grafana"
	"homelab-rack-display/internal/prom"
)

// Collector periodically refreshes a snapshot of all bindings.
type Collector struct {
	cfg     *config.Config
	prom    *prom.Client
	grafana *grafana.Client
	log     *slog.Logger

	mu      sync.RWMutex
	data    map[string]any
	updated time.Time
	errs    map[string]string
}

// New creates a collector. The grafana client may have an empty token.
func New(cfg *config.Config, p *prom.Client, g *grafana.Client, log *slog.Logger) *Collector {
	return &Collector{cfg: cfg, prom: p, grafana: g, log: log, data: map[string]any{}}
}

// Run refreshes immediately and then every cfg.Refresh until ctx is done.
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.cfg.Refresh.D())
	defer t.Stop()
	for {
		c.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Snapshot returns the latest data document (do not mutate).
func (c *Collector) Snapshot() (map[string]any, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data, c.updated
}

// Errors returns binding key -> last error.
func (c *Collector) Errors() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.errs
}

// Refresh evaluates every binding once (concurrently, bounded).
func (c *Collector) Refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	now := time.Now()

	type result struct {
		key string
		val any
		err error
	}
	results := make(chan result, len(c.cfg.Data))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for key, b := range c.cfg.Data {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v, err := c.eval(ctx, b, now)
			results <- result{key, v, err}
		}()
	}
	wg.Wait()
	close(results)

	doc := map[string]any{}
	errs := map[string]string{}
	for r := range results {
		if r.err != nil {
			errs[r.key] = r.err.Error()
			c.log.Warn("binding failed", "key", r.key, "err", r.err)
			continue
		}
		setPath(doc, r.key, r.val)
	}
	doc["meta"] = map[string]any{"updated": now.Unix(), "errors": len(errs)}

	c.mu.Lock()
	c.data, c.updated, c.errs = doc, now, errs
	c.mu.Unlock()
}

func (c *Collector) eval(ctx context.Context, b config.Binding, now time.Time) (any, error) {
	switch {
	case b.Value != nil:
		return b.Value, nil
	case b.GrafanaAlerts != nil:
		s, err := c.grafana.Alerts(ctx, b.GrafanaAlerts.MaxFiring)
		if err != nil {
			return grafana.Summary{Unavailable: true, Error: err.Error(), Firing: []grafana.Firing{}}, nil
		}
		return s, nil
	case b.Table != nil || len(b.Tables) > 0:
		tables := b.Tables
		if b.Table != nil {
			tables = append([]config.Table{*b.Table}, tables...)
		}
		var rows []map[string]any
		for _, t := range tables {
			r, err := c.table(ctx, t, now)
			if err != nil {
				return nil, err
			}
			rows = append(rows, r...)
		}
		if len(tables) > 1 {
			if col := tables[0].Dedupe; col != "" {
				seen := map[string]bool{}
				kept := rows[:0]
				for _, r := range rows {
					k := fmt.Sprint(r[col])
					if !seen[k] {
						seen[k] = true
						kept = append(kept, r)
					}
				}
				rows = kept
			}
			rows = sortLimit(rows, tables[0])
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		return rows, nil
	case b.Query != "" && b.Range.D() > 0:
		ser, err := c.prom.QueryRange(ctx, b.Query, now, b.Range.D(), step(b.Range, b.Step))
		if err != nil {
			return nil, err
		}
		var pts []prom.Point
		if len(ser) > 0 {
			pts = ser[0].Points
		}
		vals := prom.Align(pts, now, b.Range.D(), step(b.Range, b.Step))
		for i := range vals {
			vals[i] = num(vals[i], b.Scale, b.Round)
		}
		return vals, nil
	case b.Query != "":
		s, err := c.prom.Query(ctx, b.Query, now)
		if err != nil {
			return nil, err
		}
		if len(s) == 0 {
			return b.Default, nil
		}
		if b.Label != "" {
			return mapVal(s[0].Labels[b.Label], b.Map), nil
		}
		return mapVal(num(s[0].Value, b.Scale, b.Round), b.Map), nil
	}
	return nil, fmt.Errorf("binding has no source")
}

// table builds one row per series of t.Query and fills columns.
func (c *Collector) table(ctx context.Context, t config.Table, now time.Time) ([]map[string]any, error) {
	base, err := c.prom.Query(ctx, t.Query, now)
	if err != nil {
		return nil, err
	}

	// Pre-run joined column queries, indexed by key.
	joined := map[string]map[string]any{}
	for name, col := range t.Columns {
		if col.Query == "" {
			continue
		}
		idx := map[string]any{}
		if col.Range.D() > 0 {
			st := step(col.Range, col.Step)
			ser, err := c.prom.QueryRange(ctx, col.Query, now, col.Range.D(), st)
			if err != nil {
				return nil, fmt.Errorf("column %s: %w", name, err)
			}
			for _, s := range ser {
				vals := prom.Align(s.Points, now, col.Range.D(), st)
				for i := range vals {
					vals[i] = num(vals[i], col.Scale, col.Round)
				}
				idx[keyOf(s.Labels, t.Key)] = vals
			}
		} else {
			samples, err := c.prom.Query(ctx, col.Query, now)
			if err != nil {
				return nil, fmt.Errorf("column %s: %w", name, err)
			}
			for _, s := range samples {
				if col.Label != "" {
					idx[keyOf(s.Labels, t.Key)] = s.Labels[col.Label]
				} else {
					idx[keyOf(s.Labels, t.Key)] = s.Value
				}
			}
		}
		joined[name] = idx
	}

	// Columns without templates first, so templates can reference them.
	names := make([]string, 0, len(t.Columns))
	for n := range t.Columns {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		ti, tj := t.Columns[names[i]].Template != "", t.Columns[names[j]].Template != ""
		if ti != tj {
			return !ti
		}
		return names[i] < names[j]
	})

	rows := make([]map[string]any, 0, len(base))
	for _, s := range base {
		row := map[string]any{}
		k := keyOf(s.Labels, t.Key)
		for _, name := range names {
			col := t.Columns[name]
			var v any
			switch {
			case col.Const != nil:
				v = col.Const
			case col.Query != "": // joined; Label (if set) is read from the joined series
				jv, ok := joined[name][k]
				if !ok {
					// Default is literal: a "*" map fallback must not rewrite it.
					row[name] = col.Default
					continue
				}
				v = jv
			case col.Label != "":
				v = s.Labels[col.Label]
			case col.Template != "":
				v = template(col.Template, col.Else, s.Labels, row)
			default:
				v = s.Value
			}
			row[name] = transform(v, col)
		}
		row["_key"] = k
		rows = append(rows, row)
	}
	rows = sortLimit(rows, t)
	for _, r := range rows {
		delete(r, "_key")
	}
	return rows, nil
}

func sortLimit(rows []map[string]any, t config.Table) []map[string]any {
	if len(t.Order) > 0 {
		pos := map[string]int{}
		for i, k := range t.Order {
			pos[k] = i
		}
		rank := func(r map[string]any) int {
			k, _ := r["_key"].(string)
			if p, ok := pos[k]; ok {
				return p
			}
			return len(pos)
		}
		sort.SliceStable(rows, func(i, j int) bool { return rank(rows[i]) < rank(rows[j]) })
	} else if t.SortBy != "" {
		sort.SliceStable(rows, func(i, j int) bool {
			less := compare(rows[i][t.SortBy], rows[j][t.SortBy])
			if t.Desc {
				return less > 0
			}
			return less < 0
		})
	}
	if t.Limit > 0 && len(rows) > t.Limit {
		rows = rows[:t.Limit]
	}
	return rows
}

func compare(a, b any) int {
	fa, oka := a.(float64)
	fb, okb := b.(float64)
	if oka && okb {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func keyOf(labels map[string]string, key []string) string {
	parts := make([]string, len(key))
	for i, k := range key {
		parts[i] = labels[k]
	}
	return strings.Join(parts, "\x00")
}

var tmplRef = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// template substitutes {name} from row columns, then labels. If any reference
// is empty, els is expanded instead (or "" when there is no els), so a
// template never renders half-filled like " · ".
func template(t, els string, labels map[string]string, row map[string]any) string {
	missing := false
	out := tmplRef.ReplaceAllStringFunc(t, func(m string) string {
		name := m[1 : len(m)-1]
		if v, ok := row[name]; ok && v != nil && fmt.Sprint(v) != "" {
			return fmt.Sprint(v)
		}
		if v := labels[name]; v != "" {
			return v
		}
		missing = true
		return ""
	})
	if missing {
		if els == "" {
			return ""
		}
		return template(els, "", labels, row)
	}
	return out
}

func transform(v any, col config.Column) any {
	if s, ok := v.(string); ok {
		if col.Hex && strings.HasPrefix(s, "0x") {
			if b, err := hex.DecodeString(s[2:]); err == nil {
				s = string(b)
			}
		}
		if col.Regex != "" {
			if re, err := regexp.Compile(col.Regex); err == nil {
				s = re.ReplaceAllString(s, col.Replace)
			}
		}
		v = s
	}
	if f, ok := v.(float64); ok {
		v = num(f, col.Scale, col.Round)
	}
	return mapVal(v, col.Map)
}

func num(f, scale float64, round *int) float64 {
	if scale != 0 {
		f *= scale
	}
	if round != nil {
		p := math.Pow(10, float64(*round))
		f = math.Round(f*p) / p
	}
	return f
}

func mapVal(v any, m map[string]any) any {
	if m == nil {
		return v
	}
	var k string
	if f, ok := v.(float64); ok {
		k = strconv.FormatFloat(f, 'f', -1, 64)
	} else {
		k = fmt.Sprint(v)
	}
	if r, ok := m[k]; ok {
		return r
	}
	if r, ok := m["*"]; ok {
		return r
	}
	return v
}

func step(rng, st config.Duration) time.Duration {
	if st.D() > 0 {
		return st.D()
	}
	return max(rng.D()/60, 15*time.Second)
}

func setPath(doc map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	m := doc
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

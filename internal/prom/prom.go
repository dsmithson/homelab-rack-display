// Package prom is a minimal Prometheus HTTP API client (instant and range
// queries only).
package prom

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client queries one Prometheus server.
type Client struct {
	URL  string
	HTTP *http.Client
}

// Sample is one series of an instant vector.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Series is one series of a range query.
type Series struct {
	Labels map[string]string
	Points []Point
}

// Point is a timestamped value.
type Point struct {
	T time.Time
	V float64
}

type response struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

// Query runs an instant query. Scalars are returned as a single sample.
func (c *Client) Query(ctx context.Context, q string, at time.Time) ([]Sample, error) {
	v := url.Values{"query": {q}, "time": {fmt.Sprintf("%d", at.Unix())}}
	var r response
	if err := c.get(ctx, "/api/v1/query", v, &r); err != nil {
		return nil, err
	}
	switch r.Data.ResultType {
	case "vector":
		var raw []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		if err := json.Unmarshal(r.Data.Result, &raw); err != nil {
			return nil, err
		}
		out := make([]Sample, 0, len(raw))
		for _, s := range raw {
			out = append(out, Sample{Labels: s.Metric, Value: parseVal(s.Value[1])})
		}
		return out, nil
	case "scalar":
		var raw [2]any
		if err := json.Unmarshal(r.Data.Result, &raw); err != nil {
			return nil, err
		}
		return []Sample{{Labels: map[string]string{}, Value: parseVal(raw[1])}}, nil
	default:
		return nil, fmt.Errorf("unsupported result type %q", r.Data.ResultType)
	}
}

// QueryRange runs a range query over [end-rng, end] at step.
func (c *Client) QueryRange(ctx context.Context, q string, end time.Time, rng, step time.Duration) ([]Series, error) {
	start := end.Add(-rng)
	v := url.Values{
		"query": {q},
		"start": {fmt.Sprintf("%d", start.Unix())},
		"end":   {fmt.Sprintf("%d", end.Unix())},
		"step":  {fmt.Sprintf("%d", int(step.Seconds()))},
	}
	var r response
	if err := c.get(ctx, "/api/v1/query_range", v, &r); err != nil {
		return nil, err
	}
	var raw []struct {
		Metric map[string]string `json:"metric"`
		Values [][2]any          `json:"values"`
	}
	if err := json.Unmarshal(r.Data.Result, &raw); err != nil {
		return nil, err
	}
	out := make([]Series, 0, len(raw))
	for _, s := range raw {
		ser := Series{Labels: s.Metric}
		for _, p := range s.Values {
			ts, _ := p[0].(float64)
			ser.Points = append(ser.Points, Point{T: time.Unix(int64(ts), 0), V: parseVal(p[1])})
		}
		out = append(out, ser)
	}
	return out, nil
}

// Align resamples points onto a fixed grid of n = rng/step+1 slots ending at
// end, carrying the last value forward over gaps (leading gaps are 0).
func Align(points []Point, end time.Time, rng, step time.Duration) []float64 {
	n := int(rng/step) + 1
	out := make([]float64, n)
	start := end.Add(-rng)
	j, last := 0, 0.0
	for i := range n {
		t := start.Add(time.Duration(i) * step)
		for j < len(points) && !points[j].T.After(t) {
			last = points[j].V
			j++
		}
		out[i] = last
	}
	return out
}

func (c *Client) get(ctx context.Context, path string, v url.Values, out *response) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+path+"?"+v.Encode(), nil)
	if err != nil {
		return err
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("prometheus %s: HTTP %d: %w", path, resp.StatusCode, err)
	}
	if out.Status != "success" {
		return fmt.Errorf("prometheus: %s", out.Error)
	}
	return nil
}

func parseVal(v any) float64 {
	s, _ := v.(string)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

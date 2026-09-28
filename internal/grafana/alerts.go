// Package grafana reads the state of Grafana-managed alert rules.
package grafana

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Client talks to Grafana's Prometheus-compatible ruler API.
type Client struct {
	URL   string
	Token string // service-account token (Viewer is enough)
	HTTP  *http.Client
}

// Firing is one firing alert instance as shown on the display.
type Firing struct {
	Severity string  `json:"severity"`
	Name     string  `json:"name"`
	Summary  string  `json:"summary"`
	ForS     float64 `json:"for_s"`
}

// Summary is the alerts object bound into the screens.
type Summary struct {
	Critical    int      `json:"critical"`
	Warning     int      `json:"warning"`
	RulesTotal  int      `json:"rules_total"`
	Firing      []Firing `json:"firing"`
	Unavailable bool     `json:"unavailable,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type rulesResponse struct {
	Status string `json:"status"`
	Data   struct {
		Groups []struct {
			Rules []struct {
				Name   string `json:"name"`
				Alerts []struct {
					Labels      map[string]string `json:"labels"`
					Annotations map[string]string `json:"annotations"`
					State       string            `json:"state"`
					ActiveAt    time.Time         `json:"activeAt"`
				} `json:"alerts"`
			} `json:"rules"`
		} `json:"groups"`
	} `json:"data"`
}

// Alerts returns firing (not pending) alerts, critical first then oldest.
func (c *Client) Alerts(ctx context.Context, max int) (Summary, error) {
	if c.Token == "" {
		return Summary{Unavailable: true, Error: "no Grafana token", Firing: []Firing{}}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/prometheus/grafana/api/v1/rules", nil)
	if err != nil {
		return Summary{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Summary{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Summary{}, fmt.Errorf("grafana rules: HTTP %d", resp.StatusCode)
	}
	var r rulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Summary{}, err
	}

	var s Summary
	now := time.Now()
	for _, g := range r.Data.Groups {
		for _, rule := range g.Rules {
			s.RulesTotal++
			for _, a := range rule.Alerts {
				if a.State != "Alerting" && a.State != "firing" {
					continue
				}
				sev := a.Labels["severity"]
				if sev == "critical" {
					s.Critical++
				} else {
					sev = "warning"
					s.Warning++
				}
				f := Firing{Severity: sev, Name: rule.Name, Summary: a.Annotations["summary"]}
				if !a.ActiveAt.IsZero() {
					f.ForS = now.Sub(a.ActiveAt).Seconds()
				}
				s.Firing = append(s.Firing, f)
			}
		}
	}
	sort.SliceStable(s.Firing, func(i, j int) bool {
		if (s.Firing[i].Severity == "critical") != (s.Firing[j].Severity == "critical") {
			return s.Firing[i].Severity == "critical"
		}
		return s.Firing[i].ForS > s.Firing[j].ForS
	})
	if max > 0 && len(s.Firing) > max {
		s.Firing = s.Firing[:max]
	}
	if s.Firing == nil {
		s.Firing = []Firing{}
	}
	return s, nil
}

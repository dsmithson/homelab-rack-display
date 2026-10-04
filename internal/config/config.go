// Package config defines the rack display's JSON configuration: where data
// comes from, how each screen value is computed, and the screen rotation.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the root document (see config/display.json).
type Config struct {
	Prometheus struct {
		URL string `json:"url"`
	} `json:"prometheus"`
	Grafana struct {
		URL      string `json:"url"`
		TokenEnv string `json:"token_env"` // env var holding a Viewer service-account token
	} `json:"grafana"`
	Refresh  Duration `json:"refresh"` // how often all bindings are re-evaluated
	Timezone string   `json:"timezone"`

	// Data maps a dotted key (matching data-bind in the screens) to a binding.
	Data map[string]Binding `json:"data"`

	// Thresholds override the screens' data-warn/data-crit defaults, keyed by
	// data path (list items: "nodes.cpu_pct"). Values like ">85" or "<25".
	Thresholds map[string]Threshold `json:"thresholds,omitempty"`

	Rotation []Slot   `json:"rotation"`
	Priority *Variant `json:"priority,omitempty"` // interleaved after every slot while `when` holds
}

// Binding produces one value in the data document. Exactly one of Query,
// Table, GrafanaAlerts or Value should be set.
type Binding struct {
	// Query is a PromQL expression. Instant queries yield the first sample's
	// value; with Range set, a range query yields an array of numbers.
	Query   string         `json:"query,omitempty"`
	Range   Duration       `json:"range,omitempty"`
	Step    Duration       `json:"step,omitempty"`
	Default any            `json:"default,omitempty"` // used when the query returns nothing
	Scale   float64        `json:"scale,omitempty"`   // multiply numeric results
	Round   *int           `json:"round,omitempty"`   // decimal places
	Map     map[string]any `json:"map,omitempty"`     // exact value -> replacement ("*" = fallback)
	Label   string         `json:"label,omitempty"`   // return this label of the first series instead of its value

	Table  *Table  `json:"table,omitempty"`
	Tables []Table `json:"tables,omitempty"` // concatenated, then SortBy/Limit of the first apply

	GrafanaAlerts *AlertsSpec `json:"grafana_alerts,omitempty"`

	Value any `json:"value,omitempty"` // literal
}

// Table turns a vector query into a list of objects: one row per series of
// Query, columns taken from its labels/value or joined from other queries on
// the Key labels.
type Table struct {
	Query   string            `json:"query"`
	Key     []string          `json:"key"`
	Columns map[string]Column `json:"columns"`
	SortBy  string            `json:"sort_by,omitempty"`
	Desc    bool              `json:"desc,omitempty"`
	Order   []string          `json:"order,omitempty"` // explicit row order by first key label
	Limit   int               `json:"limit,omitempty"`
	Dedupe  string            `json:"dedupe,omitempty"` // drop later rows repeating this column (multi-table)
}

// Column computes one field of a table row. Sources (first match wins):
// Const, Query (joined on key; Range makes it an array; with Label, that
// label of the joined series), Label, Template, otherwise the row's value. Transforms apply after (not to a joined Default): Hex, Regex/Replace,
// Scale, Round, Map.
type Column struct {
	Const    any      `json:"const,omitempty"`
	Label    string   `json:"label,omitempty"`
	Template string   `json:"template,omitempty"` // "{label_or_column}"; empty refs trigger Else
	Else     string   `json:"else,omitempty"`
	Query    string   `json:"query,omitempty"`
	Range    Duration `json:"range,omitempty"`
	Step     Duration `json:"step,omitempty"`
	Default  any      `json:"default,omitempty"`

	Hex     bool           `json:"hex,omitempty"` // decode "0x4469736B2031" -> "Disk 1"
	Regex   string         `json:"regex,omitempty"`
	Replace string         `json:"replace,omitempty"`
	Scale   float64        `json:"scale,omitempty"`
	Round   *int           `json:"round,omitempty"`
	Map     map[string]any `json:"map,omitempty"`
}

// Threshold marks a value warn/crit on the display.
type Threshold struct {
	Warn string `json:"warn,omitempty"`
	Crit string `json:"crit,omitempty"`
}

// AlertsSpec configures the Grafana-managed alert summary.
type AlertsSpec struct {
	MaxFiring int `json:"max_firing,omitempty"`
}

// Slot is one entry in the rotation.
type Slot struct {
	Screen   string    `json:"screen"` // file name in design/screens without .html
	Dwell    Duration  `json:"dwell"`
	Variants []Variant `json:"variants,omitempty"` // first matching variant replaces the slot
}

// Variant swaps in another screen while its condition holds.
type Variant struct {
	When   string   `json:"when"` // e.g. "jellyfin.streams == 0" or "a > 0 || b > 0"
	Screen string   `json:"screen"`
	Dwell  Duration `json:"dwell,omitempty"`
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Refresh.D() == 0 {
		c.Refresh = Duration(15 * time.Second)
	}
	if len(c.Rotation) == 0 {
		return nil, fmt.Errorf("%s: rotation is empty", path)
	}
	for i, s := range c.Rotation {
		if s.Dwell.D() == 0 {
			c.Rotation[i].Dwell = Duration(10 * time.Second)
		}
	}
	return &c, nil
}

// Duration is a time.Duration that unmarshals from "15s"-style strings.
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = Duration(v)
	return err
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

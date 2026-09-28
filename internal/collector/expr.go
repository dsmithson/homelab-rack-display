package collector

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Eval evaluates a tiny condition language against the data document:
//
//	<path> <op> <number>     op: == != > >= < <=
//	cond || cond, cond && cond (&& binds tighter; no parentheses)
//
// Missing paths compare as 0.
func Eval(doc map[string]any, expr string) (bool, error) {
	for _, or := range strings.Split(expr, "||") {
		all := true
		for _, and := range strings.Split(or, "&&") {
			ok, err := evalCmp(doc, strings.TrimSpace(and))
			if err != nil {
				return false, err
			}
			if !ok {
				all = false
				break
			}
		}
		if all {
			return true, nil
		}
	}
	return false, nil
}

func evalCmp(doc map[string]any, s string) (bool, error) {
	f := strings.Fields(s)
	if len(f) != 3 {
		return false, fmt.Errorf("condition %q: want <path> <op> <number>", s)
	}
	rhs, err := strconv.ParseFloat(f[2], 64)
	if err != nil {
		return false, fmt.Errorf("condition %q: %w", s, err)
	}
	lhs := Lookup(doc, f[0])
	switch f[1] {
	case "==":
		return lhs == rhs, nil
	case "!=":
		return lhs != rhs, nil
	case ">":
		return lhs > rhs, nil
	case ">=":
		return lhs >= rhs, nil
	case "<":
		return lhs < rhs, nil
	case "<=":
		return lhs <= rhs, nil
	}
	return false, fmt.Errorf("condition %q: unknown operator %q", s, f[1])
}

// Lookup resolves a dotted path to a number (0 if missing or non-numeric).
// Values that are structs (e.g. alert summaries) are handled via JSON.
func Lookup(doc map[string]any, path string) float64 {
	var cur any = doc
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			m = toMap(cur)
			if m == nil {
				return 0
			}
		}
		cur = m[p]
	}
	switch v := cur.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

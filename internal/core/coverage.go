package core

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	observedMethods sync.Map
	assertedMethods sync.Map
)

func noteObserved(component, method string) {
	if component == "" || component == "Unknown" {
		return
	}
	val, _ := observedMethods.LoadOrStore(component+"."+method, new(int64))
	atomic.AddInt64(val.(*int64), 1)
}

func noteAsserted(component, method string) {
	if component == "" {
		return
	}
	assertedMethods.Store(component+"."+method, struct{}{})
}

// ResetCoverage clears the accumulated interaction-coverage data.
func ResetCoverage() {
	observedMethods.Range(func(k, _ any) bool { observedMethods.Delete(k); return true })
	assertedMethods.Range(func(k, _ any) bool { assertedMethods.Delete(k); return true })
}

// MethodCoverage is the coverage of a single spied method.
type MethodCoverage struct {
	Method   string
	Calls    int64
	Asserted bool
}

// Coverage returns the interaction coverage of every spied method observed so
// far, sorted by name.
func Coverage() []MethodCoverage {
	var out []MethodCoverage
	observedMethods.Range(func(k, v any) bool {
		_, asserted := assertedMethods.Load(k.(string))
		out = append(out, MethodCoverage{
			Method:   k.(string),
			Calls:    atomic.LoadInt64(v.(*int64)),
			Asserted: asserted,
		})
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out
}

// CoverageReport renders the interaction coverage as a human-readable summary.
func CoverageReport() string {
	data := Coverage()
	if len(data) == 0 {
		return "Interaction coverage: no spied calls were recorded."
	}

	verified := 0
	for _, m := range data {
		if m.Asserted {
			verified++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%sInteraction coverage%s: %d/%d spied methods verified (%.0f%%)\n",
		bold, reset, verified, len(data), 100*float64(verified)/float64(len(data)))
	for _, m := range data {
		if m.Asserted {
			fmt.Fprintf(&b, "  %s✓%s %s (x%d)\n", green, reset, m.Method, m.Calls)
		} else {
			fmt.Fprintf(&b, "  %s✘ %s (x%d) — called but never asserted%s\n", red, m.Method, m.Calls, reset)
		}
	}
	return b.String()
}

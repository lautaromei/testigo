package core

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// ExportedFinding is the machine-readable form of a scored finding, consumed by
// the offline eval (cmd/audit-eval) to align predictions against the boundary
// mutation oracle. It is written, one JSON object per line (JSONL), to the file
// named by TESTIGO_AUDIT_JSON when that env var is set.
type ExportedFinding struct {
	Rule       string  `json:"rule"`
	Kind       string  `json:"kind"` // "scored" | "hazard"
	Score      float64 `json:"score"`
	Observable bool    `json:"observable"`
	Site       string  `json:"site"`
	Type       string  `json:"odc_type"`
	Trigger    string  `json:"odc_trigger"`
	Qualifier  string  `json:"odc_qualifier"`
	Impact     string  `json:"odc_impact"`
	Message    string  `json:"message"`
}

// exportObservedMethods writes the set of callee method names the audit
// observed this run (bare method segment of each "Component.Method" key), as a
// JSON array. It defines the boundary-observable candidate universe (AUDIT_PLAN
// §9.1): the offline eval restricts its mutation samples to these methods so
// trivial mutants on non-doubled calls don't flood the metrics with TNs.
func exportObservedMethods(path string) {
	auditState.mu.Lock()
	names := map[string]bool{}
	for key := range auditState.acc.methods {
		name := key
		if i := strings.LastIndex(key, "."); i >= 0 {
			name = key[i+1:]
		}
		if name != "" {
			names[name] = true
		}
	}
	auditState.mu.Unlock()

	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)

	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(list)
}

func exportFindingsJSON(path string, findings []scoredFinding) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, sf := range findings {
		kind := "scored"
		if sf.kind == hazard {
			kind = "hazard"
		}
		_ = enc.Encode(ExportedFinding{
			Rule:       sf.rule,
			Kind:       kind,
			Score:      sf.score,
			Observable: sf.observable,
			Site:       sf.site,
			Type:       sf.odc.Type,
			Trigger:    sf.odc.Trigger,
			Qualifier:  sf.odc.Qualifier,
			Impact:     sf.odc.Impact,
			Message:    sf.message,
		})
	}
}

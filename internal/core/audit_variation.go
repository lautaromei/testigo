package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type unpinnedArgDetector struct{}

func (unpinnedArgDetector) name() string { return "unpinned-arg" }

func (unpinnedArgDetector) odc() ODC {
	return ODC{
		Type:      "Interface",
		Trigger:   "Variation",
		Qualifier: "Missing",
		Impact:    "Capability",
	}
}

func (unpinnedArgDetector) kind() findingKind { return scored }

func (d unpinnedArgDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	type site struct {
		method string
		index  int
		stat   *argStat
	}
	var sites []site
	for method, args := range a.args {
		if a.methods[method] == nil || !a.methods[method].asserted {
			continue
		}
		for index, stat := range args {
			if stat.observed > 0 && !stat.pinned && !stat.looksIncidental() {
				sites = append(sites, site{method: method, index: index, stat: stat})
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].method != sites[j].method {
			return sites[i].method < sites[j].method
		}
		return sites[i].index < sites[j].index
	})

	findings := make([]scoredFinding, 0, len(sites))
	for _, site := range sites {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.66,
			observable: true,
			site:       fmt.Sprintf("%s arg#%d", site.method, site.index),
			message:    fmt.Sprintf("%s arg #%d was observed %d time(s), but no suite assertion pins its value", site.method, site.index, site.stat.observed),
			fix: aiFix{
				Problem:      "call_argument_never_pinned",
				SuggestedFix: "Add WithParams with a concrete expected value for this argument in at least one test, or use a narrower Matcher if exact equality is intentionally too strict.",
			},
		})
	}
	return findings
}

type boundaryBlindDetector struct{}

func (boundaryBlindDetector) name() string { return "boundary-blind" }

func (boundaryBlindDetector) odc() ODC {
	return ODC{
		Type:      "Checking",
		Trigger:   "Variation",
		Qualifier: "Missing",
		Impact:    "Reliability",
	}
}

func (boundaryBlindDetector) kind() findingKind { return scored }

func (d boundaryBlindDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	type site struct {
		method string
		index  int
		stat   *argStat
	}
	var sites []site
	for method, args := range a.args {
		if a.methods[method] == nil || !a.methods[method].asserted {
			continue
		}
		for index, stat := range args {
			if stat.observed > 0 && stat.pinned && stat.numeric && len(stat.numericValues) == 1 && !stat.looksIncidental() {
				sites = append(sites, site{method: method, index: index, stat: stat})
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].method != sites[j].method {
			return sites[i].method < sites[j].method
		}
		return sites[i].index < sites[j].index
	})

	findings := make([]scoredFinding, 0, len(sites))
	for _, site := range sites {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.74,
			observable: true,
			site:       fmt.Sprintf("%s arg#%d", site.method, site.index),
			message:    fmt.Sprintf("%s arg #%d is numeric but only exercised at one value; <=/< or threshold changes can survive", site.method, site.index),
			fix: aiFix{
				Problem:      "numeric_argument_boundary_not_varied",
				SuggestedFix: "Add cases around this numeric value, especially the neighboring or threshold values, and assert the interaction or outcome.",
			},
		})
	}
	return findings
}

// argumentSwapBlindDetector hunts the "wrong variable / swapped argument" real
// fault class (e.g. transfer(to, from)): a call with two or more arguments of
// the same type that no test ever pins to mutually distinct values. Under
// testigo's strict runtime every call and outcome is asserted, so a swap is only
// caught when the two positions are pinned to different concrete values; if they
// are not, an ARG_SWAP mutant survives. This is observable at the double
// boundary and validated offline by the ARG_SWAP operator (srcmut).
type argumentSwapBlindDetector struct{}

func (argumentSwapBlindDetector) name() string { return "argument-swap-blind" }

func (argumentSwapBlindDetector) odc() ODC {
	return ODC{
		Type:      "Interface",
		Trigger:   "Variation",
		Qualifier: "Incorrect",
		Impact:    "Reliability",
	}
}

func (argumentSwapBlindDetector) kind() findingKind { return scored }

func (d argumentSwapBlindDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	type site struct {
		method   string
		typeName string
		indices  []int
	}
	var sites []site
	for method, args := range a.args {
		if a.methods[method] == nil || !a.methods[method].asserted {
			continue
		}
		byType := map[string][]int{}
		for index, stat := range args {
			if stat.observed > 0 && stat.typeName != "" && !stat.looksIncidental() {
				byType[stat.typeName] = append(byType[stat.typeName], index)
			}
		}
		for typeName, idxs := range byType {
			if len(idxs) < 2 || argSwapDistinguished(args, idxs) {
				continue
			}
			sort.Ints(idxs)
			sites = append(sites, site{method: method, typeName: typeName, indices: idxs})
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].method != sites[j].method {
			return sites[i].method < sites[j].method
		}
		return sites[i].indices[0] < sites[j].indices[0]
	})

	findings := make([]scoredFinding, 0, len(sites))
	for _, s := range sites {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.70,
			observable: true,
			site:       fmt.Sprintf("%s args#%s", s.method, joinInts(s.indices)),
			message:    fmt.Sprintf("%s has %d same-typed (%s) arguments that no test pins to distinct values; swapping them survives", s.method, len(s.indices), s.typeName),
			fix: aiFix{
				Problem:      "same_typed_arguments_swappable",
				SuggestedFix: "In at least one test, pin these positions with WithParams to mutually distinct concrete values so a swapped call is caught.",
			},
		})
	}
	return findings
}

// argSwapDistinguished reports whether some pair of the same-typed positions is
// pinned to distinct concrete values, so swapping the two would break a pinned
// expectation and be caught. If no such pair exists, the swap is invisible.
func argSwapDistinguished(args map[int]*argStat, idxs []int) bool {
	for x := 0; x < len(idxs); x++ {
		for y := x + 1; y < len(idxs); y++ {
			si, sj := args[idxs[x]], args[idxs[y]]
			if si.pinned && sj.pinned && !equalValueSets(si.pinnedValues, sj.pinnedValues) {
				return true
			}
		}
	}
	return false
}

func equalValueSets(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

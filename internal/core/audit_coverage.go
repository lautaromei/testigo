package core

import (
	"fmt"
	"sort"
)

type outcomeUnderCoverDetector struct{}

func (outcomeUnderCoverDetector) name() string { return "outcome-under-cover" }

func (outcomeUnderCoverDetector) odc() ODC {
	return ODC{Type: "Checking", Trigger: "Coverage", Qualifier: "Missing", Impact: "Reliability"}
}

func (outcomeUnderCoverDetector) kind() findingKind { return scored }

// inspect flags state-changing commands (write-verb methods) whose call is
// pinned but whose resulting state is never checked: the method returns nothing
// (returnCount == 0) and the suite recorded no value/state assertion at all. The
// interaction is covered, the outcome is not.
func (d outcomeUnderCoverDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.valueAsserts > 0 {
		return nil
	}

	var methods []string
	for method, stat := range a.methods {
		if stat.asserted && stat.returnCount == 0 && auditWriteVerb(method) {
			methods = append(methods, method)
		}
	}
	sort.Strings(methods)

	findings := make([]scoredFinding, 0, len(methods))
	for _, method := range methods {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.69,
			observable: true,
			site:       method,
			message:    fmt.Sprintf("%s is a state-changing command that is pinned as a call, but no value/state assertion checks its effect", method),
			fix: aiFix{
				Problem:      "command_effect_under_covered",
				SuggestedFix: "Assert the resulting state with assert.Equal/Len/Contains or assert.That(...).DidChange() so the command's effect is pinned, not just its occurrence.",
			},
		})
	}
	return findings
}

type outcomeUnpinnedDetector struct{}

func (outcomeUnpinnedDetector) name() string { return "outcome-unpinned" }

func (outcomeUnpinnedDetector) odc() ODC {
	return ODC{Type: "Checking", Trigger: "Coverage", Qualifier: "Missing", Impact: "Reliability"}
}

func (outcomeUnpinnedDetector) kind() findingKind { return scored }

// inspect flags verified calls whose returned outcomes are not backed by enough
// suite-level value/state assertions. This complements the boundary detectors:
// the interaction happened, but corrupting the returned value can still survive.
func (d outcomeUnpinnedDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	type site struct {
		method      string
		returnCount int
	}
	var sites []site
	for method, stat := range a.methods {
		if !stat.asserted || stat.returnCount == 0 || a.valueAsserts >= stat.returnCount {
			continue
		}
		sites = append(sites, site{method: method, returnCount: stat.returnCount})
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].method < sites[j].method })

	findings := make([]scoredFinding, 0, len(sites))
	for _, site := range sites {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.70,
			observable: true,
			site:       site.method,
			message:    fmt.Sprintf("%s returns %d value(s), but the suite only made %d value/state assertion(s); return corruption can survive", site.method, site.returnCount, a.valueAsserts),
			fix: aiFix{
				Problem:      "returned_outcome_not_pinned",
				SuggestedFix: "Capture and assert each returned outcome from the subject call, including both success values and errors.",
			},
		})
	}
	return findings
}

type discardedReturnDetector struct{}

func (discardedReturnDetector) name() string { return "discarded-return" }

func (discardedReturnDetector) odc() ODC {
	return ODC{Type: "Checking", Trigger: "Coverage", Qualifier: "Missing", Impact: "Reliability"}
}

func (discardedReturnDetector) kind() findingKind { return scored }

// inspect promotes the existing runtime warning for `_`-discarded subject
// returns into a suite audit finding. A discarded return has no assertion site,
// so return-corruption mutants can survive even when the interaction is pinned.
func (d discardedReturnDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	seen := map[string]bool{}
	var ignored []ignoredReturn
	for _, item := range a.discardedReturns {
		key := fmt.Sprintf("%s:%d:%s", item.file, item.line, item.method)
		if seen[key] {
			continue
		}
		seen[key] = true
		ignored = append(ignored, item)
	}
	sort.Slice(ignored, func(i, j int) bool {
		if ignored[i].method != ignored[j].method {
			return ignored[i].method < ignored[j].method
		}
		if ignored[i].file != ignored[j].file {
			return ignored[i].file < ignored[j].file
		}
		return ignored[i].line < ignored[j].line
	})

	findings := make([]scoredFinding, 0, len(ignored))
	for _, item := range ignored {
		site := item.method
		if site == "" {
			site = fmt.Sprintf("%s:%d", item.file, item.line)
		}
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.73,
			observable: true,
			site:       site,
			message:    fmt.Sprintf("%s discards returned value(s) at %s:%d: %s", site, item.file, item.line, item.src),
			fix: aiFix{
				Problem:      "discarded_subject_return_value",
				SuggestedFix: "Replace _ with a named variable and assert it, or explicitly assert that the discarded outcome is irrelevant.",
			},
		})
	}
	return findings
}

type looseCountDetector struct{}

func (looseCountDetector) name() string { return "loose-count" }

func (looseCountDetector) odc() ODC {
	return ODC{
		Type:      "Algorithm",
		Trigger:   "Coverage",
		Qualifier: "Missing",
		Impact:    "Reliability",
	}
}

func (looseCountDetector) kind() findingKind { return scored }

func (d looseCountDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	var methods []string
	for method, stat := range a.methods {
		if stat.looseCounted && !stat.exactCounted {
			methods = append(methods, method)
		}
	}
	sort.Strings(methods)

	findings := make([]scoredFinding, 0, len(methods))
	for _, method := range methods {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.62,
			observable: true,
			site:       method,
			message:    fmt.Sprintf("%s is only asserted with an at-least count; duplicate calls can survive", method),
			fix: aiFix{
				Problem:      "call_count_only_asserted_loosely",
				SuggestedFix: "Add a suite test that exact-counts this interaction with Once, Times, or Never when duplicate calls would be wrong.",
			},
		})
	}
	return findings
}

type tautologyDetector struct{}

func (tautologyDetector) name() string { return "tautology" }

func (tautologyDetector) odc() ODC {
	return ODC{Type: "Checking", Trigger: "Coverage", Qualifier: "Incorrect", Impact: "Maintainability"}
}

func (tautologyDetector) kind() findingKind { return hazard }

// inspect flags assertions that pass regardless of the subject's logic: a method
// that is asserted (typically with Never) yet was never observed. Asserting the
// absence of a call the code never makes holds for every implementation, so the
// expectation kills no mutants.
func (d tautologyDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	var methods []string
	for method, stat := range a.methods {
		if stat.asserted && stat.observed == 0 {
			methods = append(methods, method)
		}
	}
	sort.Strings(methods)

	findings := make([]scoredFinding, 0, len(methods))
	for _, method := range methods {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.55,
			observable: true,
			site:       method,
			message:    fmt.Sprintf("%s is asserted but was never called; a Never-style expectation on an uncalled method passes for any implementation", method),
			fix: aiFix{
				Problem:      "assertion_passes_regardless_of_logic",
				SuggestedFix: "Drive the subject down the branch that would call this method and assert the negative there, or remove the tautological expectation.",
			},
		})
	}
	return findings
}

type errorPathUnexercisedDetector struct{}

func (errorPathUnexercisedDetector) name() string { return "error-path-unexercised" }

func (errorPathUnexercisedDetector) odc() ODC {
	return ODC{Type: "Checking", Trigger: "Coverage", Qualifier: "Missing", Impact: "Reliability"}
}

func (errorPathUnexercisedDetector) kind() findingKind { return scored }

// inspect flags methods whose stubbed signature returns two or more values
// (conventionally a (result, error) tuple) that are pinned as calls while the
// suite makes no value assertion. The error return is never asserted, so the
// failure path is unexercised.
func (d errorPathUnexercisedDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.valueAsserts > 0 {
		return nil
	}

	var methods []string
	for method, stat := range a.methods {
		if stat.asserted && stat.returnCount >= 2 {
			methods = append(methods, method)
		}
	}
	sort.Strings(methods)

	findings := make([]scoredFinding, 0, len(methods))
	for _, method := range methods {
		stat := a.methods[method]
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.72,
			observable: true,
			site:       method,
			message:    fmt.Sprintf("%s returns %d values (likely a result/error pair) but no suite assertion exercises its error path", method, stat.returnCount),
			fix: aiFix{
				Problem:      "error_return_path_unexercised",
				SuggestedFix: "Add a case where this call fails and assert the returned error with assert.Error/ErrorIs, and assert the success path separately.",
			},
		})
	}
	return findings
}

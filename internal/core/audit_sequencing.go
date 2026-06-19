package core

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// auditOrderAsserts counts ordering assertions made during the suite (Before,
// After, Within, CallsOrdered). order-insensitive reads it to tell whether any
// sequence was pinned at all. It is reset by resetAuditStateForTest.
var auditOrderAsserts atomic.Int64

func auditNoteOrderAssertion() { auditOrderAsserts.Add(1) }

type orderInsensitiveDetector struct{}

func (orderInsensitiveDetector) name() string { return "order-insensitive" }

func (orderInsensitiveDetector) odc() ODC {
	return ODC{Type: "Timing/Serialization", Trigger: "Sequencing", Qualifier: "Missing", Impact: "Reliability"}
}

func (orderInsensitiveDetector) kind() findingKind { return scored }

// inspect flags suites that pin two or more distinct effectful interactions
// (write/emit verbs) but never assert the order between any of them. When the
// outcome depends on sequencing, a swap of two such calls would survive.
func (d orderInsensitiveDetector) inspect(a *acc) []scoredFinding {
	if auditOrderAsserts.Load() > 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	var methods []string
	seen := map[string]bool{}
	for method, stat := range a.methods {
		if seen[method] || !stat.asserted {
			continue
		}
		if auditWriteVerb(method) || auditEmitVerb(method) {
			seen[method] = true
			methods = append(methods, method)
		}
	}
	if len(methods) < 2 {
		return nil
	}
	sort.Strings(methods)

	return []scoredFinding{{
		rule:       d.name(),
		kind:       d.kind(),
		odc:        d.odc(),
		score:      0.60,
		observable: true,
		site:       "suite",
		message:    fmt.Sprintf("%d effectful interactions are pinned but no ordering is asserted between them: %s", len(methods), strings.Join(methods, ", ")),
		fix: aiFix{
			Problem:      "interaction_order_not_pinned",
			SuggestedFix: "If the sequence matters, pin it with Before/After/Within or CallsOrdered so a reordering of these calls would fail.",
		},
	}}
}

type lateAsyncCallDetector struct{}

func (lateAsyncCallDetector) name() string { return "late-async-call" }

func (lateAsyncCallDetector) odc() ODC {
	return ODC{Type: "Timing/Serialization", Trigger: "Sequencing", Qualifier: "Incorrect", Impact: "Reliability"}
}

func (lateAsyncCallDetector) kind() findingKind { return hazard }

// inspect flags emitted events/notifications recorded on a goroutine other than
// the primary (test) goroutine. Such a call is produced asynchronously and may
// race with the end of the test, so an assertion on it can be flaky or miss the
// call entirely.
func (d lateAsyncCallDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	primary, ok := primaryGoroutine(a.calls)
	if !ok {
		return nil
	}

	type site struct{ method, where string }
	var sites []site
	seen := map[string]bool{}
	for _, c := range a.calls {
		if c.goroutineID == primary || !auditEmitVerb(c.method) {
			continue
		}
		if seen[c.method] {
			continue
		}
		seen[c.method] = true
		sites = append(sites, site{method: c.method, where: c.site})
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].method < sites[j].method })

	findings := make([]scoredFinding, 0, len(sites))
	for _, s := range sites {
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      1,
			observable: true,
			site:       s.where,
			message:    fmt.Sprintf("%s was emitted from a background goroutine; an assertion on it can race with test end", s.method),
			fix: aiFix{
				Problem:      "async_event_emitted_after_test_body",
				SuggestedFix: "Synchronize on the async work (wait for a signal, channel, or done callback) before asserting, instead of relying on timing.",
			},
		})
	}
	return findings
}

type overwriteDeadStoreDetector struct{}

func (overwriteDeadStoreDetector) name() string { return "overwrite-dead-store" }

func (overwriteDeadStoreDetector) odc() ODC {
	return ODC{Type: "Assignment", Trigger: "Sequencing", Qualifier: "Extraneous", Impact: "Performance"}
}

func (overwriteDeadStoreDetector) kind() findingKind { return scored }

func (d overwriteDeadStoreDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	byKey := map[string][]callDigest{}
	readKeys := map[string]bool{}
	for _, c := range a.calls {
		key, ok := auditCallKey(c)
		if !ok {
			continue
		}
		if auditReadVerb(c.method) {
			readKeys[key] = true
		}
		if auditWriteVerb(c.method) {
			byKey[key] = append(byKey[key], c)
		}
	}

	var keys []string
	for key, calls := range byKey {
		if len(calls) > 1 && !readKeys[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	findings := make([]scoredFinding, 0, len(keys))
	for _, key := range keys {
		calls := byKey[key]
		sort.Slice(calls, func(i, j int) bool { return calls[i].seq < calls[j].seq })
		last := calls[len(calls)-1]
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.61,
			observable: true,
			site:       last.site,
			message:    fmt.Sprintf("%s was written %d time(s) without an observed read of the same key", key, len(calls)),
			fix: aiFix{
				Problem:      "write_overwritten_without_read",
				SuggestedFix: "Assert the intermediate state, remove the redundant write, or add a test that reads the key after the write.",
			},
		})
	}
	return findings
}

type unrestoredExternalStateDetector struct{}

func (unrestoredExternalStateDetector) name() string { return "unrestored-external-state" }

func (unrestoredExternalStateDetector) odc() ODC {
	return ODC{Type: "Assignment", Trigger: "Sequencing", Qualifier: "Missing", Impact: "Reliability"}
}

func (unrestoredExternalStateDetector) kind() findingKind { return hazard }

// inspect flags external state that is acquired (Open/Begin/Lock/Set/Create) on
// a key but never released (Close/Rollback/Commit/Unlock/Delete) on that same
// key within the suite. The leftover state can leak into later tests and make
// them order-dependent.
func (d unrestoredExternalStateDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	acquired := map[string]callDigest{}
	released := map[string]bool{}
	for _, c := range a.calls {
		key, ok := auditResourceKey(c)
		if !ok {
			continue
		}
		if auditReleaseVerb(c.method) {
			released[key] = true
		}
		if auditAcquireVerb(c.method) {
			if _, seen := acquired[key]; !seen {
				acquired[key] = c
			}
		}
	}

	var keys []string
	for key := range acquired {
		if !released[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	findings := make([]scoredFinding, 0, len(keys))
	for _, key := range keys {
		c := acquired[key]
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      1,
			observable: true,
			site:       c.site,
			message:    fmt.Sprintf("%s acquires external state (%s) that is never released within the suite", c.method, key),
			fix: aiFix{
				Problem:      "external_state_not_restored",
				SuggestedFix: "Release the resource in t.Cleanup (Close/Rollback/Unlock/Delete) so it cannot leak into later tests.",
			},
		})
	}
	return findings
}

// auditResourceKey identifies the external resource a call acts on by its first
// non-context argument value, independent of the method name, so an acquire and
// its matching release pair up even though they are different methods.
func auditResourceKey(c callDigest) (string, bool) {
	for _, arg := range c.snapshots {
		key := auditValueKey(arg)
		if strings.Contains(key, "context.") {
			continue
		}
		return key, true
	}
	return "", false
}

func auditAcquireVerb(method string) bool {
	return auditMethodHasVerb(method, "Open", "Begin", "Lock", "Acquire", "Connect", "Start")
}

func auditReleaseVerb(method string) bool {
	return auditMethodHasVerb(method, "Close", "Rollback", "Commit", "Unlock", "Release", "Disconnect", "Stop")
}

func auditCallKey(c callDigest) (string, bool) {
	for _, arg := range c.snapshots {
		key := auditValueKey(arg)
		if strings.Contains(key, "context.") {
			continue
		}
		return c.method + ":" + key, true
	}
	return "", false
}

func auditReadVerb(method string) bool {
	return auditMethodHasVerb(method, "Get", "Query", "Read", "Load", "Find", "List")
}

func auditWriteVerb(method string) bool {
	return auditMethodHasVerb(method, "Set", "Insert", "Update", "Write", "Store", "Put", "Save", "Create", "Close")
}

func auditMethodHasVerb(method string, verbs ...string) bool {
	name := method
	if idx := strings.LastIndex(method, "."); idx >= 0 {
		name = method[idx+1:]
	}
	for _, verb := range verbs {
		if strings.Contains(name, verb) {
			return true
		}
	}
	return false
}

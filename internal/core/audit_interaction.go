package core

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type argAliasingDetector struct{}

func (argAliasingDetector) name() string { return "arg-aliasing" }

func (argAliasingDetector) odc() ODC {
	return ODC{Type: "Interface", Trigger: "Interaction", Qualifier: "Incorrect", Impact: "Reliability"}
}

func (argAliasingDetector) kind() findingKind { return hazard }

func (argAliasingDetector) inspect(*acc) []scoredFinding {
	return nil
}

type sharedBackingMemoryDetector struct{}

func (sharedBackingMemoryDetector) name() string { return "shared-backing-memory" }

func (sharedBackingMemoryDetector) odc() ODC {
	return ODC{Type: "Relationship", Trigger: "Interaction", Qualifier: "Incorrect", Impact: "Reliability"}
}

func (sharedBackingMemoryDetector) kind() findingKind { return hazard }

// inspect flags the same backing reference (slice, map, pointer, or channel)
// passed to two or more recorded calls. The callee retains an alias, so a later
// mutation through that reference silently changes what an earlier callee
// observed — a common source of flaky, order-dependent doubles.
func (d sharedBackingMemoryDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	byAddr := map[uintptr][]callDigest{}
	order := []uintptr{}
	for _, c := range a.calls {
		for _, arg := range c.params {
			addr, ok := auditBackingAddr(arg)
			if !ok {
				continue
			}
			if _, seen := byAddr[addr]; !seen {
				order = append(order, addr)
			}
			byAddr[addr] = append(byAddr[addr], c)
		}
	}

	findings := make([]scoredFinding, 0)
	for _, addr := range order {
		uses := byAddr[addr]
		distinct := map[string]bool{}
		methods := make([]string, 0, len(uses))
		for _, u := range uses {
			distinct[u.method+"@"+u.site] = true
			methods = append(methods, u.method)
		}
		if len(distinct) < 2 {
			continue
		}
		sort.Strings(methods)
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      1,
			observable: true,
			site:       uses[0].site,
			message:    fmt.Sprintf("the same backing reference is passed to multiple calls (%s); a later mutation aliases what earlier callees saw", strings.Join(uniqueStrings(methods), ", ")),
			fix: aiFix{
				Problem:      "shared_backing_memory_across_calls",
				SuggestedFix: "Copy the slice/map before passing it to each call, or pass immutable values, so callees cannot observe each other's mutations.",
			},
		})
	}
	return findings
}

// auditBackingAddr returns the backing pointer of a reference-typed argument
// (slice, map, pointer, or channel) so two arguments sharing storage can be
// matched. Non-reference values return ok=false.
func auditBackingAddr(v any) (uintptr, bool) {
	if v == nil {
		return 0, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.Len() == 0 {
			return 0, false
		}
		return rv.Pointer(), true
	case reflect.Map, reflect.Ptr, reflect.Chan:
		if rv.IsNil() {
			return 0, false
		}
		return rv.Pointer(), true
	default:
		return 0, false
	}
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

type crossGoroutineMutationDetector struct{}

func (crossGoroutineMutationDetector) name() string { return "cross-goroutine-mutation" }

func (crossGoroutineMutationDetector) odc() ODC {
	return ODC{Type: "Timing/Serialization", Trigger: "Interaction", Qualifier: "Incorrect", Impact: "Security"}
}

func (crossGoroutineMutationDetector) kind() findingKind { return hazard }

// inspect flags state-mutating calls (write verbs) recorded on a goroutine other
// than the primary (test) goroutine. A double mutated from a background
// goroutine while the test reads it on the main goroutine is an unsynchronized
// data race.
func (d crossGoroutineMutationDetector) inspect(a *acc) []scoredFinding {
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
		// Emitted events are reported by late-async-call; here we want genuine
		// state mutations, so skip methods that look like notifications even
		// when their name also contains a write verb (e.g. "AuctionClosed").
		if c.goroutineID == primary || auditEmitVerb(c.method) || !auditWriteVerb(c.method) {
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
			message:    fmt.Sprintf("%s mutates state from a background goroutine; reading it on the test goroutine is an unsynchronized data race", s.method),
			fix: aiFix{
				Problem:      "cross_goroutine_mutation",
				SuggestedFix: "Guard the shared state with a mutex/channel, or join the goroutine before asserting, so the mutation is synchronized with the read.",
			},
		})
	}
	return findings
}

// primaryGoroutine returns the goroutine ID that recorded the most calls (the
// test's own goroutine). ok is false when no call carries a goroutine ID, so
// callers can skip cross-goroutine analysis rather than guess.
func primaryGoroutine(calls []callDigest) (uint64, bool) {
	counts := map[uint64]int{}
	for _, c := range calls {
		if c.goroutineID != 0 {
			counts[c.goroutineID]++
		}
	}
	if len(counts) == 0 {
		return 0, false
	}
	var best uint64
	bestN := -1
	for id, n := range counts {
		if n > bestN || (n == bestN && id < best) {
			best, bestN = id, n
		}
	}
	return best, true
}

func auditEmitVerb(method string) bool {
	return auditMethodHasVerb(method, "Publish", "Emit", "Fire", "Dispatch", "Notify", "Send", "On", "Handle", "BidPlaced", "Outbid", "AuctionClosed")
}

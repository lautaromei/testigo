package core

type uncheckedStatementDetector struct{}

func (uncheckedStatementDetector) name() string { return "unchecked-statement" }

func (uncheckedStatementDetector) odc() ODC {
	return ODC{Type: "Checking", Trigger: "Statement", Qualifier: "Missing", Impact: "Reliability"}
}

func (uncheckedStatementDetector) kind() findingKind { return scored }

// inspect is intentionally a stub.
//
// Detector 19 (checked coverage, AUDIT_PLAN.md §5.E/§5.1) is NOT a double-metadata
// detector like the other 18. It reads covered blocks (`go test -coverprofile`)
// minus the static SSA backward slice of every asserted return — i.e. statements
// the suite runs but never lets influence an oracle. The real engine is
// proto/checkedcov-ssa (interprocedural go/ssa slice). It is observable=false,
// gated behind TESTIGO_AUDIT_CHECKED, and evaluated against the checked-coverage
// oracle, not the boundary-mutation oracle.
//
// The previous body here was a runtime proxy over double interactions, which
// classifies in the wrong layer. Disabled until wired to proto/checkedcov-ssa.
//
//	func (d uncheckedStatementDetector) inspect(a *acc) []scoredFinding {
//		a.mu.Lock()
//		defer a.mu.Unlock()
//		if a.valueAsserts > 0 {
//			return nil
//		}
//		var methods []string
//		for method, stat := range a.methods {
//			if !stat.asserted {
//				continue
//			}
//			args := a.args[method]
//			if len(args) == 0 {
//				continue
//			}
//			anyPinned := false
//			meaningful := false
//			for _, arg := range args {
//				if arg.pinned {
//					anyPinned = true
//					break
//				}
//				if !arg.looksIncidental() {
//					meaningful = true
//				}
//			}
//			if !anyPinned && meaningful {
//				methods = append(methods, method)
//			}
//		}
//		sort.Strings(methods)
//		...emit one finding per method...
//	}
func (uncheckedStatementDetector) inspect(*acc) []scoredFinding {
	return nil
}

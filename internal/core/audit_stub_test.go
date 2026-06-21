package core

import "testing"

func sites(findings []scoredFinding) map[string]bool {
	out := map[string]bool{}
	for _, f := range findings {
		out[f.site] = true
	}
	return out
}

func TestOutcomeUnderCoverDetector(t *testing.T) {
	d := outcomeUnderCoverDetector{}
	a := &acc{methods: map[string]*methodStat{
		"pkg.Repo.Save":  {observed: 1, asserted: true, returnCount: 0}, // command, no return
		"pkg.Repo.Get":   {observed: 1, asserted: true, returnCount: 1}, // returns -> skip
		"pkg.Log.Debug":  {observed: 1, asserted: true, returnCount: 0}, // not a write verb
		"pkg.Repo.Close": {observed: 1, asserted: false, returnCount: 0},
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["pkg.Repo.Save"] {
		t.Fatalf("expected only pkg.Repo.Save, got %+v", got)
	}

	a.valueAsserts = 1
	if findings := d.inspect(a); len(findings) != 0 {
		t.Fatalf("value assertions present should silence detector, got %+v", findings)
	}
}

func TestOutcomeUnpinnedDetector(t *testing.T) {
	d := outcomeUnpinnedDetector{}
	a := &acc{methods: map[string]*methodStat{
		"pkg.Repo.Save": {observed: 1, asserted: true, returnCount: 2},
		"pkg.Repo.Get":  {observed: 1, asserted: true, returnCount: 1},
		"pkg.Log.Debug": {observed: 1, asserted: true, returnCount: 0},
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 2 || !got["pkg.Repo.Save"] || !got["pkg.Repo.Get"] {
		t.Fatalf("expected returning methods to be flagged, got %+v", got)
	}

	a.valueAsserts = 1
	findings = d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["pkg.Repo.Save"] {
		t.Fatalf("expected only method needing two outcome assertions, got %+v", got)
	}

	a.valueAsserts = 2
	if findings := d.inspect(a); len(findings) != 0 {
		t.Fatalf("enough value assertions should silence detector, got %+v", findings)
	}
}

func TestDiscardedReturnDetector(t *testing.T) {
	d := discardedReturnDetector{}
	a := &acc{discardedReturns: []ignoredReturn{
		{file: "repo_test.go", line: 12, src: "got, _ := subject.Save()", method: "pkg.Repo.Save"},
		{file: "repo_test.go", line: 12, src: "got, _ := subject.Save()", method: "pkg.Repo.Save"},
		{file: "repo_test.go", line: 18, src: "_, err := subject.Get()", method: "pkg.Repo.Get"},
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 2 || !got["pkg.Repo.Save"] || !got["pkg.Repo.Get"] {
		t.Fatalf("expected one finding per discarded-return method, got %+v", got)
	}
	if findings[0].rule != "discarded-return" || findings[0].kind != scored {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
}

func TestErrorPathUnexercisedDetector(t *testing.T) {
	d := errorPathUnexercisedDetector{}
	a := &acc{methods: map[string]*methodStat{
		"pkg.Repo.Save": {asserted: true, returnCount: 2}, // (result, error)
		"pkg.Repo.Ping": {asserted: true, returnCount: 1}, // single return -> skip
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["pkg.Repo.Save"] {
		t.Fatalf("expected only pkg.Repo.Save, got %+v", got)
	}

	a.valueAsserts = 1
	if findings := d.inspect(a); len(findings) != 0 {
		t.Fatalf("value assertions present should silence detector, got %+v", findings)
	}
}

func TestTautologyDetector(t *testing.T) {
	d := tautologyDetector{}
	a := &acc{methods: map[string]*methodStat{
		"pkg.Bus.Publish": {observed: 0, asserted: true}, // never called but asserted
		"pkg.Repo.Save":   {observed: 1, asserted: true},
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["pkg.Bus.Publish"] {
		t.Fatalf("expected only pkg.Bus.Publish, got %+v", got)
	}
	if findings[0].kind != hazard {
		t.Fatalf("tautology should be a hazard, got %+v", findings[0])
	}
}

func TestOrderInsensitiveDetector(t *testing.T) {
	auditOrderAsserts.Store(0)
	d := orderInsensitiveDetector{}
	a := &acc{methods: map[string]*methodStat{
		"pkg.Repo.Save":   {asserted: true},
		"pkg.Bus.Publish": {asserted: true},
		"pkg.Repo.Get":    {asserted: true}, // read verb, not effectful
	}}

	findings := d.inspect(a)
	if len(findings) != 1 || findings[0].site != "suite" {
		t.Fatalf("expected one suite-level finding, got %+v", findings)
	}

	auditOrderAsserts.Store(1)
	if findings := d.inspect(a); len(findings) != 0 {
		t.Fatalf("ordering already asserted should silence detector, got %+v", findings)
	}
	auditOrderAsserts.Store(0)
}

func TestLateAsyncCallDetector(t *testing.T) {
	d := lateAsyncCallDetector{}
	a := &acc{calls: []callDigest{
		{method: "pkg.Repo.Save", site: "s_test.go:1", goroutineID: 1},
		{method: "pkg.Repo.Get", site: "s_test.go:2", goroutineID: 1},
		{method: "pkg.Bus.Publish", site: "s_test.go:3", goroutineID: 7}, // emitted off-goroutine
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["s_test.go:3"] {
		t.Fatalf("expected only the background Publish, got %+v", got)
	}
}

func TestUnrestoredExternalStateDetector(t *testing.T) {
	d := unrestoredExternalStateDetector{}
	a := &acc{calls: []callDigest{
		{method: "pkg.Tx.Begin", site: "s_test.go:1", snapshots: []any{"conn-1"}},
		{method: "pkg.File.Open", site: "s_test.go:2", snapshots: []any{"path-a"}},
		{method: "pkg.File.Close", site: "s_test.go:3", snapshots: []any{"path-a"}},
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["s_test.go:1"] {
		t.Fatalf("expected only the unreleased Begin, got %+v", got)
	}
}

func TestSharedBackingMemoryDetector(t *testing.T) {
	d := sharedBackingMemoryDetector{}
	shared := []string{"a", "b"}
	a := &acc{calls: []callDigest{
		{method: "pkg.Repo.Save", site: "s_test.go:1", params: []any{shared}},
		{method: "pkg.Bus.Publish", site: "s_test.go:2", params: []any{shared}},
		{method: "pkg.Repo.Get", site: "s_test.go:3", params: []any{[]string{"x"}}},
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["s_test.go:1"] {
		t.Fatalf("expected one shared-backing finding, got %+v", got)
	}
}

func TestCrossGoroutineMutationDetector(t *testing.T) {
	d := crossGoroutineMutationDetector{}
	a := &acc{calls: []callDigest{
		{method: "pkg.Repo.Get", site: "s_test.go:1", goroutineID: 1},
		{method: "pkg.Repo.Save", site: "s_test.go:2", goroutineID: 1},
		{method: "pkg.Repo.Update", site: "s_test.go:3", goroutineID: 9}, // write off-goroutine
	}}

	findings := d.inspect(a)
	if got := sites(findings); len(got) != 1 || !got["s_test.go:3"] {
		t.Fatalf("expected only the background Update, got %+v", got)
	}
}

// unchecked-statement (detector 19, checked coverage) is a stub until wired to
// proto/checkedcov-ssa; see audit_checked.go. No unit test while it returns nil.

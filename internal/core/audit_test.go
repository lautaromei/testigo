package core

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lautaromei/testigo/random"
)

func TestAuditReportNoFindingsIsSilent(t *testing.T) {
	resetAuditStateForTest()
	t.Setenv("TESTIGO_AUDIT", "warn")

	var out bytes.Buffer
	if fail := AuditReportTo(&out); fail {
		t.Fatal("empty audit report should not fail")
	}
	if out.Len() != 0 {
		t.Fatalf("empty audit report should be silent, got %q", out.String())
	}
}

func TestAuditRegistryIncludesAllPlannedHeuristics(t *testing.T) {
	want := []string{
		"outcome-under-cover",
		"outcome-unpinned",
		"discarded-return",
		"unpinned-arg",
		"boundary-blind",
		"loose-count",
		"order-insensitive",
		"late-async-call",
		"overwrite-dead-store",
		"arg-aliasing",
		"shared-backing-memory",
		"cross-goroutine-mutation",
		"unrestored-external-state",
		"tautology",
		"argument-swap-blind",
		"duplicate-test-case",
		"error-path-unexercised",
		"unchecked-statement",
	}

	got := map[string]bool{}
	auditState.mu.Lock()
	for _, d := range auditState.detectors {
		got[d.name()] = true
	}
	auditState.mu.Unlock()

	for _, name := range want {
		if !got[name] {
			t.Fatalf("audit detector %q is not registered", name)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d registered detectors, got %d: %+v", len(want), len(got), got)
	}
}

func TestAuditAccumulateEmitsImmediateHazards(t *testing.T) {
	a := &acc{
		methods: map[string]*methodStat{},
		args:    map[string]map[int]*argStat{},
	}
	accumulateAuditCall(a, &CallRecord{
		CalleeComponent: "pkg.Repo",
		CalleeMethod:    "Save",
		CallSiteFile:    "repo_test.go",
		CallSiteLine:    12,
		Params:          []any{[]string{"mutated"}},
		Snapshots:       []any{[]string{"original"}},
		GoroutineID:     99,
	})
	accumulateAuditHazards(a, &CallRecord{
		CalleeComponent: "pkg.Repo",
		CalleeMethod:    "Save",
		CallSiteFile:    "repo_test.go",
		CallSiteLine:    12,
		Params:          []any{[]string{"mutated"}},
		Snapshots:       []any{[]string{"original"}},
		GoroutineID:     99,
	})

	if len(a.findings) != 1 || a.findings[0].rule != "arg-aliasing" {
		t.Fatalf("expected only arg-aliasing hazard, got %+v", a.findings)
	}
}

func TestAuditAccumulateIgnoresContextArguments(t *testing.T) {
	a := &acc{
		methods: map[string]*methodStat{},
		args:    map[string]map[int]*argStat{},
	}
	ctx := context.Background()

	accumulateAuditCall(a, &CallRecord{
		CalleeComponent: "pkg.Repo",
		CalleeMethod:    "Get",
		Params:          []any{ctx, "usr_1"},
		Snapshots:       []any{ctx, "usr_1"},
	})
	accumulateAuditExpectation(a, &CalledFunc{
		calleeComponent: "pkg.Repo",
		funcName:        "Get",
		expectedArgs:    []any{ctx, "usr_1"},
	})

	args := a.args["pkg.Repo.Get"]
	if _, ok := args[0]; ok {
		t.Fatalf("context argument should not be tracked by audit variation heuristics")
	}
	if args[1] == nil || !args[1].pinned || args[1].observed != 1 {
		t.Fatalf("non-context argument should still be tracked, got %+v", args[1])
	}
}

func TestAuditArmAccumulatesValueOnlyTests(t *testing.T) {
	defer isolateCurrentTestRegistries()()
	resetAuditStateForTest()

	ft := &fakeT{}
	Equal(ft, true, true)

	ft.runCleanupsLIFO()

	if ft.failed {
		t.Fatalf("value-only assertion should not fail final check: %s", ft.message)
	}
}

func TestAuditCleanupRunsAfterFinalCheckWhileCallsAreLive(t *testing.T) {
	resetAuditStateForTest()
	type snapshot struct {
		calls        int
		valueAsserts int
	}
	seen := make(chan snapshot, 1)

	oldHook := auditAccumulateHook
	auditAccumulateHook = func(_ testing.TB, tv *testVerifier, callCount int) {
		tv.mu.Lock()
		valueAsserts := tv.valueAsserts
		tv.mu.Unlock()
		seen <- snapshot{calls: callCount, valueAsserts: valueAsserts}
	}
	defer func() {
		auditAccumulateHook = oldHook
	}()

	t.Run("child", func(t *testing.T) {
		subject := NewDouble(t, &TestSubject{spy: &Spy{}})
		subject.DoSomething("hello", 123)
		Expect(t).Called(subject.DoSomething).WithParams("hello", 123).Once()
		Equal(t, true, true)
	})

	var got snapshot
	select {
	case got = <-seen:
	case <-time.After(time.Second):
		t.Fatal("audit cleanup did not run")
	}
	if got.calls != 1 {
		t.Fatalf("expected audit cleanup to see the live recorded call, got %d", got.calls)
	}
	if got.valueAsserts != 1 {
		t.Fatalf("expected final-check signal to be available to audit cleanup, got %d value assertions", got.valueAsserts)
	}
}

func TestAuditAccumulateRecordsCallsArgsAndExpectations(t *testing.T) {
	resetAuditStateForTest()
	ResetCoverage()
	defer ResetCoverage()

	a := &acc{
		methods: map[string]*methodStat{},
		args:    map[string]map[int]*argStat{},
	}
	call := &CallRecord{
		CalleeComponent: "pkg.Repo",
		CalleeMethod:    "Save",
		Snapshots:       []any{"id-1", 42},
	}
	accumulateAuditCall(a, call)
	accumulateAuditExpectation(a, &CalledFunc{
		calleeComponent: "pkg.Repo",
		funcName:        "Save",
		expectedArgs:    []any{"id-1", Anything},
		atLeast:         true,
	})

	method := a.methods["pkg.Repo.Save"]
	if method == nil || method.observed != 1 || !method.asserted || !method.looseCounted || method.exactCounted {
		t.Fatalf("unexpected method stat: %+v", method)
	}
	if !a.args["pkg.Repo.Save"][0].pinned {
		t.Fatal("expected arg 0 to be pinned by concrete WithParams value")
	}
	if a.args["pkg.Repo.Save"][1].pinned {
		t.Fatal("expected Anything matcher not to pin arg 1")
	}
	if got := a.args["pkg.Repo.Save"][1].values["int:42"]; got != 1 {
		t.Fatalf("expected observed arg value to be counted, got %d", got)
	}
	if got := a.args["pkg.Repo.Save"][1].numericValues[42]; got != 1 {
		t.Fatalf("expected observed numeric arg value to be counted, got %d", got)
	}
}

func TestLooseCountDetector(t *testing.T) {
	d := looseCountDetector{}
	a := &acc{methods: map[string]*methodStat{
		"pkg.Bus.Publish": {observed: 2, asserted: true, looseCounted: true},
		"pkg.Repo.Save":   {observed: 2, asserted: true, looseCounted: true, exactCounted: true},
	}}

	findings := d.inspect(a)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].rule != "loose-count" || findings[0].site != "pkg.Bus.Publish" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
}

func TestUnpinnedArgDetector(t *testing.T) {
	d := unpinnedArgDetector{}
	a := &acc{
		methods: map[string]*methodStat{
			"pkg.Repo.Save": {observed: 2, asserted: true},
			"pkg.Log.Debug": {observed: 1},
		},
		args: map[string]map[int]*argStat{
			"pkg.Repo.Save": {
				0: {observed: 2, pinned: true},
				1: {observed: 2},
			},
			"pkg.Log.Debug": {
				0: {observed: 1},
			},
		},
	}

	findings := d.inspect(a)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].rule != "unpinned-arg" || findings[0].site != "pkg.Repo.Save arg#1" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
	if findings[0].odc.Trigger != "Variation" {
		t.Fatalf("unexpected ODC trigger: %+v", findings[0].odc)
	}
}

func TestBoundaryBlindDetector(t *testing.T) {
	d := boundaryBlindDetector{}
	a := &acc{
		methods: map[string]*methodStat{
			"pkg.Pricer.Quote": {observed: 3, asserted: true},
		},
		args: map[string]map[int]*argStat{
			"pkg.Pricer.Quote": {
				0: {observed: 2, pinned: true, numeric: true, numericValues: map[float64]int{30: 2}},
				1: {observed: 2, pinned: true, numeric: true, numericValues: map[float64]int{10: 1, 20: 1}},
				2: {observed: 1, numeric: true, numericValues: map[float64]int{5: 1}},
			},
		},
	}

	findings := d.inspect(a)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].rule != "boundary-blind" || findings[0].site != "pkg.Pricer.Quote arg#0" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
	if findings[0].odc.Type != "Checking" {
		t.Fatalf("unexpected ODC type: %+v", findings[0].odc)
	}
}

func TestArgumentSwapBlindDetector(t *testing.T) {
	d := argumentSwapBlindDetector{}
	a := &acc{
		methods: map[string]*methodStat{
			"pkg.Bank.Transfer": {observed: 1, asserted: true},
			"pkg.Bank.Move":     {observed: 1, asserted: true},
		},
		args: map[string]map[int]*argStat{
			// two same-typed args, never pinned to distinct values -> swap survives
			"pkg.Bank.Transfer": {
				0: {observed: 1, typeName: "string", values: map[string]int{"string:\"acct-a\"": 1}},
				1: {observed: 1, typeName: "string", values: map[string]int{"string:\"acct-b\"": 1}},
				2: {observed: 1, typeName: "int", numeric: true},
			},
			// same-typed args pinned to distinct concrete values -> cancelled
			"pkg.Bank.Move": {
				0: {observed: 1, typeName: "string", pinned: true, pinnedValues: map[string]int{"string:\"src\"": 1}},
				1: {observed: 1, typeName: "string", pinned: true, pinnedValues: map[string]int{"string:\"dst\"": 1}},
			},
		},
	}

	findings := d.inspect(a)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].rule != "argument-swap-blind" || findings[0].site != "pkg.Bank.Transfer args#0,1" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
	if findings[0].odc.Qualifier != "Incorrect" {
		t.Fatalf("unexpected ODC: %+v", findings[0].odc)
	}
}

func TestDuplicateTestCaseDetector(t *testing.T) {
	d := duplicateTestCaseDetector{}
	a := &acc{
		testCases: []testCaseDigest{
			{name: "TestService/Create/valid_a", signature: "call:Repo.Save(string:\"a\")|expect:Repo.Save:1:false:string:\"a\"", callCount: 1, expectationCount: 1, valueAsserts: 1},
			{name: "TestService/Create/valid_b", signature: "call:Repo.Save(string:\"a\")|expect:Repo.Save:1:false:string:\"a\"", callCount: 1, expectationCount: 1, valueAsserts: 1},
			{name: "TestService/Create/invalid", signature: "call:Repo.Save(string:\"b\")|expect:Repo.Save:1:false:string:\"b\"", callCount: 1, expectationCount: 1, valueAsserts: 1},
		},
	}

	findings := d.inspect(a)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].rule != "duplicate-test-case" || findings[0].site != "TestService/Create/valid_a <=> TestService/Create/valid_b" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
	if findings[0].kind != hazard {
		t.Fatalf("duplicate-test-case should be a hazard, got %+v", findings[0])
	}
	if findings[0].odc.Qualifier != "Extraneous" {
		t.Fatalf("unexpected ODC qualifier: %+v", findings[0].odc)
	}
}

func TestDuplicateTestCaseDetectorIgnoresDistinctSignatures(t *testing.T) {
	d := duplicateTestCaseDetector{}
	a := &acc{
		testCases: []testCaseDigest{
			{name: "TestService/Create/valid", signature: "call:Repo.Save(string:\"a\")", callCount: 1},
			{name: "TestService/Create/invalid", signature: "call:Repo.Save(string:\"b\")", callCount: 1},
		},
	}

	if findings := d.inspect(a); len(findings) != 0 {
		t.Fatalf("expected no findings, got %+v", findings)
	}
}

func TestRandomNewIDIsRecordedAndSuppressesVariationNoise(t *testing.T) {
	resetAuditStateForTest()

	id := random.NewID("usr")
	if !auditIsGeneratedID(id) {
		t.Fatalf("random.NewID result %q was not recorded as a generated ID", id)
	}

	a := &acc{
		methods: map[string]*methodStat{},
		args:    map[string]map[int]*argStat{},
	}
	accumulateAuditCall(a, &CallRecord{
		CalleeComponent: "pkg.Repo",
		CalleeMethod:    "Get",
		Snapshots:       []any{id},
	})
	accumulateAuditExpectation(a, &CalledFunc{
		calleeComponent: "pkg.Repo",
		funcName:        "Get",
		expectedArgs:    []any{id},
	})

	stat := a.args["pkg.Repo.Get"][0]
	if stat == nil || !stat.looksIncidental() {
		t.Fatalf("generated-ID argument should be incidental, got %+v", stat)
	}

	if findings := (unpinnedArgDetector{}).inspect(a); len(findings) != 0 {
		t.Fatalf("unpinned-arg should skip generated-ID argument, got %+v", findings)
	}
}

func TestAuditIncidentalArgTreatsTimeAsNonDeterministic(t *testing.T) {
	resetAuditStateForTest()

	a := &acc{
		methods: map[string]*methodStat{},
		args:    map[string]map[int]*argStat{},
	}
	accumulateAuditCall(a, &CallRecord{
		CalleeComponent: "pkg.Repo",
		CalleeMethod:    "Close",
		Snapshots:       []any{"meaningful", time.Now()},
	})
	accumulateAuditExpectation(a, &CalledFunc{
		calleeComponent: "pkg.Repo",
		funcName:        "Close",
		expectedArgs:    []any{Anything, Anything},
	})

	if stat := a.args["pkg.Repo.Close"][1]; stat == nil || !stat.looksIncidental() {
		t.Fatalf("time.Time argument should be incidental, got %+v", stat)
	}
	if stat := a.args["pkg.Repo.Close"][0]; stat == nil || stat.looksIncidental() {
		t.Fatalf("plain string argument should remain meaningful, got %+v", stat)
	}

	findings := (unpinnedArgDetector{}).inspect(a)
	if len(findings) != 1 || findings[0].site != "pkg.Repo.Close arg#0" {
		t.Fatalf("expected only the meaningful arg to be flagged, got %+v", findings)
	}
}

func TestAuditReportRendersDiagnosisProfile(t *testing.T) {
	report := renderAuditReport([]scoredFinding{
		{
			rule:  "boundary-blind",
			kind:  scored,
			odc:   ODC{Type: "Checking", Trigger: "Variation", Qualifier: "Missing", Impact: "Reliability"},
			score: 0.74,
			site:  "pkg.Pricer.Quote arg#0",
			fix:   aiFix{Problem: "numeric_argument_boundary_not_varied", SuggestedFix: "Add boundary cases."},
		},
		{
			rule:  "unpinned-arg",
			kind:  scored,
			odc:   ODC{Type: "Interface", Trigger: "Variation", Qualifier: "Missing", Impact: "Capability"},
			score: 0.66,
			site:  "pkg.Bus.Publish arg#1",
			fix:   aiFix{Problem: "argument_value_never_pinned", SuggestedFix: "Pin the argument."},
		},
	})

	for _, want := range []string{
		"testigo audit",
		"trigger",
		"impact",
		"hotspot",
		"scored (",
		"fix:",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

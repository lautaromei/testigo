package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lautaromei/testigo/random"
)

// ODC classifies an audit finding using Orthogonal Defect Classification axes.
type ODC struct {
	Type      string
	Trigger   string
	Qualifier string
	Impact    string
}

type findingKind int

const (
	scored findingKind = iota
	hazard
)

type aiFix struct {
	Problem      string
	Evidence     string
	SuggestedFix string
}

type scoredFinding struct {
	rule       string
	kind       findingKind
	odc        ODC
	score      float64
	observable bool
	site       string
	message    string
	fix        aiFix
}

type acc struct {
	methods          map[string]*methodStat
	args             map[string]map[int]*argStat
	calls            []callDigest
	testCases        []testCaseDigest
	seenCalls        map[uint64]bool
	findings         []scoredFinding
	discardedReturns []ignoredReturn
	valueAsserts     int
	mu               sync.Mutex
}

type methodStat struct {
	observed     int64
	asserted     bool
	exactCounted bool
	looseCounted bool
	returnCount  int
}

type argStat struct {
	observed      int
	pinned        bool
	numeric       bool
	incidental    int
	meaningful    int
	typeName      string // observed Go type (%T); groups same-typed positions for swap-blindness
	values        map[string]int
	numericValues map[float64]int
	pinnedValues  map[string]int // concrete values pinned by some assertion (for swap distinctness)
}

// looksIncidental reports whether every observed value for this argument is an
// inferred identifier (e.g. an ID generated as "<prefix>_<token>") or a
// non-deterministic value such as a time.Time. Pinning or varying these adds
// noise rather than mutation-killing signal, so the Variation detectors skip
// them.
func (s *argStat) looksIncidental() bool {
	return s.observed > 0 && s.meaningful == 0 && s.incidental > 0
}

type callDigest struct {
	method      string
	caller      string
	site        string
	params      []any
	snapshots   []any
	seq         uint64
	time        time.Time
	goroutineID uint64
}

type testCaseDigest struct {
	name             string
	signature        string
	callCount        int
	expectationCount int
	valueAsserts     int
}

type detector interface {
	name() string
	odc() ODC
	kind() findingKind
	inspect(*acc) []scoredFinding
}

var auditState = struct {
	mu        sync.Mutex
	acc       acc
	detectors []detector
	disabled  map[string]bool
	ignored   map[string]bool
}{
	disabled: map[string]bool{},
	ignored:  map[string]bool{},
}

// auditGeneratedIDs holds every identifier produced by a testigo ID generator
// (random.NewID). Audit Variation detectors treat an argument whose value
// equals a recorded ID as incidental — a generated identifier rather than an
// un-varied business value — so they do not flag it as noise.
var auditGeneratedIDs sync.Map // string -> struct{}

// NoteGeneratedID records id as a generated identifier. It is wired into
// random.NewID so identifiers flow into the audit layer automatically.
func NoteGeneratedID(id string) {
	if id == "" {
		return
	}
	auditGeneratedIDs.Store(id, struct{}{})
}

func auditIsGeneratedID(id string) bool {
	_, ok := auditGeneratedIDs.Load(id)
	return ok
}

func init() {
	random.SetIDRecorder(NoteGeneratedID)
	registerAuditDetector(outcomeUnderCoverDetector{})
	registerAuditDetector(looseCountDetector{})
	registerAuditDetector(tautologyDetector{})
	registerAuditDetector(errorPathUnexercisedDetector{})
	registerAuditDetector(unpinnedArgDetector{})
	registerAuditDetector(boundaryBlindDetector{})
	registerAuditDetector(argumentSwapBlindDetector{})
	registerAuditDetector(duplicateTestCaseDetector{})
	registerAuditDetector(orderInsensitiveDetector{})
	registerAuditDetector(lateAsyncCallDetector{})
	registerAuditDetector(overwriteDeadStoreDetector{})
	registerAuditDetector(unrestoredExternalStateDetector{})
	registerAuditDetector(argAliasingDetector{})
	registerAuditDetector(sharedBackingMemoryDetector{})
	registerAuditDetector(crossGoroutineMutationDetector{})
	registerAuditDetector(uncheckedStatementDetector{})
}

// auditExperimentalRules ship OFF by default: they fired below the precision
// floor in calibration (AUDIT_PLAN §8.1) but retain enough signal to keep
// measuring. Enable with TESTIGO_AUDIT_EXPERIMENTAL=on (the eval harness sets
// this so calibration still sees them).
var auditExperimentalRules = map[string]bool{
	"unpinned-arg": true, // prec ~0.43: over-fires on args constrained indirectly via outcome
}

func auditExperimentalOn() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TESTIGO_AUDIT_EXPERIMENTAL")), "on")
}

func registerAuditDetector(d detector) {
	auditState.mu.Lock()
	defer auditState.mu.Unlock()
	auditState.detectors = append(auditState.detectors, d)
}

func resetAuditStateForTest() {
	auditState.mu.Lock()
	defer auditState.mu.Unlock()
	auditState.acc = acc{}
	auditState.disabled = map[string]bool{}
	auditState.ignored = map[string]bool{}
	auditOrderAsserts.Store(0)
	auditGeneratedIDs.Range(func(k, _ any) bool {
		auditGeneratedIDs.Delete(k)
		return true
	})
}

var auditArmed sync.Map

var auditAccumulateHook func(testing.TB, *testVerifier, int)

func auditArm(t testing.TB) *testVerifier {
	if t == nil {
		return nil
	}
	testID := getTestID()
	if testID == "" {
		return nil
	}
	_, loaded := auditArmed.LoadOrStore(t, true)
	if !loaded {
		if !tryCleanup(t, func() {
			defer auditArmed.Delete(t)
			defer testVerifiers.Delete(testID)
			auditAccumulate(t, testID)
		}) {
			auditArmed.Delete(t)
			return nil
		}
	}
	if val, ok := testVerifiers.Load(testID); ok {
		return val.(*testVerifier)
	}
	return nil
}

func tryCleanup(t testing.TB, fn func()) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	t.Cleanup(fn)
	return true
}

func auditAccumulate(t testing.TB, testID string) {
	t.Helper()
	var tv *testVerifier
	if val, ok := testVerifiers.Load(testID); ok {
		tv = val.(*testVerifier)
	}
	_, callsByFunc := collectTestCalls()
	callCount := 0
	for _, calls := range callsByFunc {
		callCount += len(calls)
	}
	if auditAccumulateHook != nil {
		auditAccumulateHook(t, tv, callCount)
	}
	auditState.mu.Lock()
	defer auditState.mu.Unlock()
	if auditState.acc.methods == nil {
		auditState.acc.methods = map[string]*methodStat{}
	}
	if auditState.acc.args == nil {
		auditState.acc.args = map[string]map[int]*argStat{}
	}
	if auditState.acc.seenCalls == nil {
		auditState.acc.seenCalls = map[uint64]bool{}
	}
	for _, calls := range callsByFunc {
		for _, call := range calls {
			accumulateAuditCall(&auditState.acc, call)
		}
	}
	for _, calls := range callsByFunc {
		for _, call := range calls {
			accumulateAuditHazards(&auditState.acc, call)
		}
	}
	if tv != nil {
		tv.mu.Lock()
		auditState.acc.valueAsserts += tv.valueAsserts
		for _, p := range tv.pendings {
			accumulateAuditExpectation(&auditState.acc, p.exp)
		}
		auditState.acc.discardedReturns = append(auditState.acc.discardedReturns, ignoredReturnedValues(testVerifierExps(tv))...)
		if testCase := auditTestCaseDigest(auditTestName(t, testID), callsByFunc, tv); testCase.signature != "" {
			auditState.acc.testCases = append(auditState.acc.testCases, testCase)
		}
		tv.mu.Unlock()
	}
	for _, c := range Coverage() {
		stat := auditState.acc.methods[c.Method]
		if stat == nil {
			stat = &methodStat{}
			auditState.acc.methods[c.Method] = stat
		}
		stat.observed = c.Calls
		stat.asserted = stat.asserted || c.Asserted
	}
}

func accumulateAuditHazards(a *acc, call *CallRecord) {
	method := callMethodKey(call)
	if method == "" {
		return
	}
	if auditParamsAliased(call.Params, call.Snapshots) {
		addAuditFinding(a, scoredFinding{
			rule:       "arg-aliasing",
			kind:       hazard,
			odc:        ODC{Type: "Interface", Trigger: "Interaction", Qualifier: "Incorrect", Impact: "Reliability"},
			score:      1,
			observable: true,
			site:       call.site(),
			message:    fmt.Sprintf("%s arguments differ from their call-time snapshot", method),
			fix: aiFix{
				Problem:      "argument_aliasing_after_call",
				SuggestedFix: "Pass immutable values or copy mutable arguments before mutating them after the call.",
			},
		})
	}
}

func testVerifierExps(tv *testVerifier) []*CalledFunc {
	exps := make([]*CalledFunc, 0, len(tv.pendings))
	for _, p := range tv.pendings {
		exps = append(exps, p.exp)
	}
	return exps
}

func accumulateAuditCall(a *acc, call *CallRecord) {
	if call != nil && call.Seq != 0 {
		if a.seenCalls == nil {
			a.seenCalls = map[uint64]bool{}
		}
		if a.seenCalls[call.Seq] {
			return
		}
		a.seenCalls[call.Seq] = true
	}
	method := callMethodKey(call)
	if method == "" {
		return
	}
	stat := a.methods[method]
	if stat == nil {
		stat = &methodStat{}
		a.methods[method] = stat
	}
	stat.observed++
	a.calls = append(a.calls, callDigest{
		method:      method,
		caller:      call.CallerComponent + "." + call.CallerMethod,
		site:        call.site(),
		params:      call.Params,
		snapshots:   call.Snapshots,
		seq:         call.Seq,
		time:        call.Time,
		goroutineID: call.GoroutineID,
	})

	if a.args[method] == nil {
		a.args[method] = map[int]*argStat{}
	}
	for i, arg := range call.recorded() {
		if auditIgnoredArg(arg) {
			continue
		}
		stat := a.args[method][i]
		if stat == nil {
			stat = &argStat{values: map[string]int{}, numericValues: map[float64]int{}, pinnedValues: map[string]int{}}
			a.args[method][i] = stat
		}
		stat.observed++
		if arg != nil && stat.typeName == "" {
			stat.typeName = fmt.Sprintf("%T", arg)
		}
		if auditIncidentalArg(arg) {
			stat.incidental++
		} else {
			stat.meaningful++
		}
		if n, ok := auditNumericValue(arg); ok {
			stat.numeric = true
			stat.numericValues[n]++
		}
		stat.values[auditValueKey(arg)]++
	}
}

func accumulateAuditExpectation(a *acc, exp *CalledFunc) {
	if exp == nil || exp.err != nil {
		return
	}
	method := expectationMethodKey(exp)
	if method == "" {
		return
	}
	stat := a.methods[method]
	if stat == nil {
		stat = &methodStat{}
		a.methods[method] = stat
	}
	stat.asserted = true
	if exp.returnCount > stat.returnCount {
		stat.returnCount = exp.returnCount
	}
	if exp.atLeast {
		stat.looseCounted = true
	} else {
		stat.exactCounted = true
	}

	if len(exp.expectedArgs) == 0 {
		return
	}
	if a.args[method] == nil {
		a.args[method] = map[int]*argStat{}
	}
	for i, arg := range exp.expectedArgs {
		if auditIgnoredArg(arg) {
			continue
		}
		if arg == Anything {
			continue
		}
		if _, ok := arg.(Matcher); ok {
			continue
		}
		stat := a.args[method][i]
		if stat == nil {
			stat = &argStat{values: map[string]int{}, numericValues: map[float64]int{}, pinnedValues: map[string]int{}}
			a.args[method][i] = stat
		}
		stat.pinned = true
		if stat.pinnedValues == nil {
			stat.pinnedValues = map[string]int{}
		}
		stat.pinnedValues[auditValueKey(arg)]++
	}
}

func callMethodKey(call *CallRecord) string {
	if call == nil || call.CalleeComponent == "" || call.CalleeMethod == "" {
		return ""
	}
	return call.CalleeComponent + "." + call.CalleeMethod
}

func expectationMethodKey(exp *CalledFunc) string {
	if exp == nil || exp.calleeComponent == "" || exp.funcName == "" {
		return ""
	}
	return exp.calleeComponent + "." + exp.funcName
}

func auditValueKey(v any) string {
	return fmt.Sprintf("%T:%#v", v, v)
}

// auditIncidentalArg reports whether v is an incidental argument value the
// Variation detectors should ignore: a generated identifier (recorded via
// random.NewID) or a non-deterministic time value. Neither carries
// mutation-killing signal when pinned or varied.
func auditIncidentalArg(v any) bool {
	switch x := v.(type) {
	case time.Time:
		return true
	case *time.Time:
		return x != nil
	case string:
		return auditIsGeneratedID(x)
	case *string:
		return x != nil && auditIsGeneratedID(*x)
	}
	return false
}

func auditTestName(t testing.TB, fallback string) (name string) {
	defer func() {
		if recover() != nil || name == "" {
			name = "test-" + fallback
		}
	}()
	if t == nil {
		return "test-" + fallback
	}
	return t.Name()
}

func auditTestCaseDigest(name string, callsByFunc map[string][]*CallRecord, tv *testVerifier) testCaseDigest {
	if tv == nil {
		return testCaseDigest{}
	}
	parts := make([]string, 0)
	callCount := 0
	var calls []*CallRecord
	for _, group := range callsByFunc {
		calls = append(calls, group...)
	}
	sort.Slice(calls, func(i, j int) bool {
		if calls[i].Seq != calls[j].Seq {
			return calls[i].Seq < calls[j].Seq
		}
		return callMethodKey(calls[i]) < callMethodKey(calls[j])
	})
	for _, call := range calls {
		method := callMethodKey(call)
		if method == "" {
			continue
		}
		callCount++
		parts = append(parts, "call:"+method+"("+auditArgsSignature(call.recorded())+")")
	}

	expectationCount := 0
	expectations := make([]string, 0, len(tv.pendings))
	for _, p := range tv.pendings {
		if p == nil || p.exp == nil || p.exp.err != nil {
			continue
		}
		method := expectationMethodKey(p.exp)
		if method == "" {
			continue
		}
		expectationCount++
		expectations = append(expectations, fmt.Sprintf("expect:%s:%d:%t:%s", method, p.exp.times, p.exp.atLeast, auditArgsSignature(p.exp.expectedArgs)))
	}
	sort.Strings(expectations)
	parts = append(parts, expectations...)
	if tv.valueAsserts > 0 {
		parts = append(parts, fmt.Sprintf("value-asserts:%d", tv.valueAsserts))
	}
	if callCount == 0 && expectationCount == 0 {
		return testCaseDigest{}
	}
	return testCaseDigest{
		name:             name,
		signature:        strings.Join(parts, "|"),
		callCount:        callCount,
		expectationCount: expectationCount,
		valueAsserts:     tv.valueAsserts,
	}
}

func auditArgsSignature(args []any) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if auditIgnoredArg(arg) {
			parts = append(parts, "context")
			continue
		}
		switch arg {
		case Anything:
			parts = append(parts, "anything")
			continue
		}
		if _, ok := arg.(Matcher); ok {
			parts = append(parts, "matcher")
			continue
		}
		parts = append(parts, auditValueKey(arg))
	}
	return strings.Join(parts, ",")
}

func auditIgnoredArg(v any) bool {
	_, ok := v.(context.Context)
	return ok
}

func auditNumericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, uintptr,
		float32, float64:
		return numericToFloat64(n), true
	default:
		return 0, false
	}
}

func addAuditFinding(a *acc, f scoredFinding) {
	for _, existing := range a.findings {
		if existing.rule == f.rule && existing.site == f.site {
			return
		}
	}
	a.findings = append(a.findings, f)
}

func auditParamsAliased(params, snapshots []any) bool {
	if len(params) != len(snapshots) {
		return false
	}
	for i := range params {
		if !reflect.DeepEqual(params[i], snapshots[i]) {
			return true
		}
	}
	return false
}

func numericToFloat64(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint8:
		return float64(n)
	case uint16:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	case uintptr:
		return float64(n)
	case float32:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}

// AuditReport runs suite-level audit detectors and writes the report to stderr.
// It returns true when the caller should fail the process.
func AuditReport() bool {
	return AuditReportTo(os.Stderr)
}

// AuditReportTo runs suite-level audit detectors and writes the report to w.
// It returns true when TESTIGO_AUDIT=error and an unsuppressed finding exists.
func AuditReportTo(w io.Writer) bool {
	mode := auditMode()
	if mode == "off" {
		return false
	}

	findings := auditFindings()
	if path := os.Getenv("TESTIGO_AUDIT_JSON"); path != "" {
		exportFindingsJSON(path, findings)
		exportObservedMethods(path + ".methods")
	}
	if len(findings) == 0 {
		return false
	}

	fmt.Fprint(w, renderAuditReport(findings))
	return mode == "error"
}

// AuditDisable turns off one audit rule by name.
func AuditDisable(rule string) {
	auditState.mu.Lock()
	defer auditState.mu.Unlock()
	auditState.disabled[rule] = true
}

// AuditIgnore suppresses one finding site for a rule. Site is usually file:line.
func AuditIgnore(rule, site string) {
	auditState.mu.Lock()
	defer auditState.mu.Unlock()
	auditState.ignored[auditIgnoreKey(rule, site)] = true
}

func auditFindings() []scoredFinding {
	auditState.mu.Lock()
	defer auditState.mu.Unlock()

	var out []scoredFinding
	for _, f := range auditState.acc.findings {
		if auditSuppressedLocked(f) {
			continue
		}
		out = append(out, f)
	}
	for _, d := range auditState.detectors {
		if auditState.disabled[d.name()] {
			continue
		}
		if auditExperimentalRules[d.name()] && !auditExperimentalOn() {
			continue
		}
		for _, f := range d.inspect(&auditState.acc) {
			if auditSuppressedLocked(f) {
				continue
			}
			out = append(out, f)
		}
	}
	// Apply the fitted calibration: a scored detector's printed score is its
	// offline-measured P(survive|fired), not the in-code prior (AUDIT_PLAN §9.3).
	for i := range out {
		if out[i].kind != scored {
			continue
		}
		if v, ok := auditCalibratedScores[out[i].rule]; ok {
			out[i].score = v
		}
	}
	return out
}

func auditSuppressedLocked(f scoredFinding) bool {
	return auditState.disabled[f.rule] || auditState.ignored[auditIgnoreKey(f.rule, f.site)]
}

func auditIgnoreKey(rule, site string) string {
	return rule + "\x00" + site
}

func auditMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TESTIGO_AUDIT"))) {
	case "", "warn":
		return "warn"
	case "off":
		return "off"
	case "error":
		return "error"
	default:
		return "warn"
	}
}

func renderAuditReport(findings []scoredFinding) string {
	var b strings.Builder
	scoredFindings, hazardFindings := splitFindingKinds(findings)

	fmt.Fprintf(&b, "%stestigo audit%s  %d surviving mutant(s): %s%d scored%s · %s%d hazard(s)%s\n",
		bold, reset, len(findings), yellow, len(scoredFindings), reset, red, len(hazardFindings), reset)

	if len(scoredFindings) > 0 {
		renderDistribution(&b, "trigger", scoredFindings, func(f scoredFinding) string { return f.odc.Trigger })
		renderDistribution(&b, "impact", scoredFindings, func(f scoredFinding) string { return f.odc.Impact })
		if cell, count := dominantODCCell(scoredFindings); cell != "" {
			fmt.Fprintf(&b, "  %shotspot%s  %s%s ×%d%s\n", bold, reset, yellow, cell, count, reset)
		}
		if trigger, count := dominantTrigger(scoredFindings); trigger != "" {
			fmt.Fprintf(&b, "  %s→%s %s\n", yellow, reset, triggerAdvice(trigger, count))
		}
	}

	if len(hazardFindings) > 0 {
		sortFindings(hazardFindings)
		fmt.Fprintf(&b, "\n  %shazards (%d)%s — must fix; not scored\n", red, len(hazardFindings), reset)
		for _, f := range hazardFindings {
			fmt.Fprintf(&b, "    %s✗ %-24s%s %s%s%s\n", red, f.rule, reset, white, f.site, reset)
			fmt.Fprintf(&b, "      %s\n", findingEvidence(f))
		}
	}

	if len(scoredFindings) > 0 {
		fmt.Fprintf(&b, "\n  %sscored (%d)%s — weak spots, by score\n", yellow, len(scoredFindings), reset)
		for _, g := range groupScored(scoredFindings) {
			fmt.Fprintf(&b, "    %s%.2f %-22s%s ×%d  %s%s%s\n", bold, g.score, g.rule, reset, len(g.sites), white, g.cell, reset)
			for _, s := range g.sites {
				fmt.Fprintf(&b, "      · %s\n", s)
			}
			if g.fix != "" {
				fmt.Fprintf(&b, "      %sfix:%s %s\n", bold, reset, g.fix)
			}
		}
	}

	return b.String()
}

type scoredGroup struct {
	rule  string
	score float64
	cell  string
	fix   string
	sites []string
}

func groupScored(findings []scoredFinding) []scoredGroup {
	byRule := map[string]*scoredGroup{}
	for _, f := range findings {
		g := byRule[f.rule]
		if g == nil {
			g = &scoredGroup{rule: f.rule, score: f.score, cell: f.odc.Type + "/" + f.odc.Trigger + "/" + f.odc.Qualifier, fix: f.fix.SuggestedFix}
			byRule[f.rule] = g
		}
		if f.score > g.score {
			g.score = f.score
		}
		g.sites = append(g.sites, f.site)
	}
	groups := make([]scoredGroup, 0, len(byRule))
	for _, g := range byRule {
		sort.Strings(g.sites)
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].score != groups[j].score {
			return groups[i].score > groups[j].score
		}
		return groups[i].rule < groups[j].rule
	})
	return groups
}

func renderDistribution(b *strings.Builder, title string, findings []scoredFinding, key func(scoredFinding) string) {
	counts := map[string]int{}
	for _, f := range findings {
		k := key(f)
		if k == "" {
			k = "Unknown"
		}
		counts[k]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})

	fmt.Fprintf(b, "  %s%s%s\n", bold, title, reset)
	for _, k := range keys {
		pct := 100 * float64(counts[k]) / float64(len(findings))
		fmt.Fprintf(b, "    %-12s %s%s%s %s%3.0f%%%s\n", k, auditBarColor(pct), auditBar(pct), reset, white, pct, reset)
	}
}

func auditBar(pct float64) string {
	filled := int((pct + 5) / 10)
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

func auditBarColor(pct float64) string {
	switch {
	case pct >= 70:
		return red
	case pct >= 35:
		return yellow
	default:
		return green
	}
}

func splitFindingKinds(findings []scoredFinding) (scoredOut, hazardOut []scoredFinding) {
	for _, f := range findings {
		if f.kind == hazard {
			hazardOut = append(hazardOut, f)
		} else {
			scoredOut = append(scoredOut, f)
		}
	}
	return scoredOut, hazardOut
}

func dominantODCCell(findings []scoredFinding) (string, int) {
	counts := map[string]int{}
	for _, f := range findings {
		cell := fmt.Sprintf("%s / %s / %s", f.odc.Type, f.odc.Trigger, f.odc.Qualifier)
		counts[cell]++
	}
	var best string
	for cell, count := range counts {
		if count > counts[best] || (count == counts[best] && (best == "" || cell < best)) {
			best = cell
		}
	}
	return best, counts[best]
}

func dominantTrigger(findings []scoredFinding) (string, int) {
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.odc.Trigger]++
	}
	var best string
	for trigger, count := range counts {
		if count > counts[best] || (count == counts[best] && (best == "" || trigger < best)) {
			best = trigger
		}
	}
	return best, counts[best]
}

func triggerAdvice(trigger string, count int) string {
	switch trigger {
	case "Variation":
		return fmt.Sprintf("Variation dominates (%d finding(s)); add tests that vary inputs, literals, and boundary values.", count)
	case "Coverage":
		return fmt.Sprintf("Coverage dominates (%d finding(s)); assert observed interactions and exact call counts.", count)
	case "Sequencing":
		return fmt.Sprintf("Sequencing dominates (%d finding(s)); pin ordering and state transitions that matter.", count)
	case "Interaction":
		return fmt.Sprintf("Interaction dominates (%d finding(s)); assert downstream effects, emitted events, and shared-state boundaries.", count)
	default:
		return fmt.Sprintf("%s dominates (%d finding(s)); inspect the ODC distribution below.", trigger, count)
	}
}

func sortFindings(findings []scoredFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].kind != findings[j].kind {
			return findings[i].kind < findings[j].kind
		}
		if findings[i].score != findings[j].score {
			return findings[i].score > findings[j].score
		}
		if findings[i].rule != findings[j].rule {
			return findings[i].rule < findings[j].rule
		}
		return findings[i].site < findings[j].site
	})
}

func findingEvidence(f scoredFinding) string {
	if f.message != "" {
		return f.message
	}
	return f.fix.Evidence
}

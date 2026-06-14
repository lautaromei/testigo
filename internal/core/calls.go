package core

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Expect starts a verification chain. The chain is checked when it ends (Once,
// Times, Never or AtLeastOnce); at test end testigo also flags any recorded
// call left unverified.
func Expect(t testing.TB) *Verification {
	return &Verification{t: t}
}

// That starts a verification chain naming the expected caller.
func That(caller any) *Verification {
	return Expect(mustCurrentT()).That(caller)
}

// Called starts a caller-less verification chain (used by core's own tests).
func Called(function any) *PendingCall {
	return Expect(mustCurrentT()).Called(function)
}

func mustCurrentT() testing.TB {
	if t := currentT(); t != nil {
		return t
	}
	panic("testigo: no test bound to this goroutine — assert inside testigo.Run (or after NewDouble), or start the chain with testigo.Expect(t)")
}

// Verification is the start of an Expect chain.
type Verification struct {
	t         testing.TB
	caller    any
	hasCaller bool
}

// That sets the expected caller of the upcoming Called.
func (v *Verification) That(caller any) *Verification {
	v.caller = caller
	v.hasCaller = true
	return v
}

// DidChange asserts the double named by That changed since NewDouble registered it.
func (v *Verification) DidChange() {
	v.t.Helper()
	noteValueAssertion()
	if !changed(v.registeredDouble()) {
		v.t.Errorf("expected %s'%s'%s to change, but it is unchanged", bold, doubleName(v.registeredDouble()), reset)
	}
}

// DidNotChange asserts the double named by That is unchanged since NewDouble.
func (v *Verification) DidNotChange() {
	v.t.Helper()
	noteValueAssertion()
	if changed(v.registeredDouble()) {
		v.t.Errorf("expected %s'%s'%s to stay unchanged, but it changed", bold, doubleName(v.registeredDouble()), reset)
	}
}

// Changed asserts a field of the double That named differs from the state
// NewDouble captured; pass a pointer to the field (&double.field).
func (v *Verification) Changed(fieldPtr any) *FieldChange {
	v.t.Helper()
	noteValueAssertion()
	rec := v.registeredDouble()
	name, initial, current, ok := fieldStateByPtr(rec, fieldPtr)
	fc := &FieldChange{t: v.t, double: doubleName(rec), field: name, initial: initial, current: current, found: ok}
	if !ok {
		v.t.Fatalf("testigo: Changed(...) needs a pointer to a field of the %s%s%s registered with That — e.g. Changed(&double.field)", bold, fc.double, reset)
		return fc
	}
	if reflect.DeepEqual(initial, current) {
		v.t.Errorf("expected %s'%s.%s'%s to change, but it stayed %v", bold, fc.double, name, reset, current)
	}
	return fc
}

// FieldChange continues a Changed chain, pinning the initial and/or current value of a field.
type FieldChange struct {
	t       testing.TB
	double  string
	field   string
	initial any
	current any
	found   bool
}

// From asserts the field's value at NewDouble time was old.
func (fc *FieldChange) From(old any) *FieldChange {
	fc.t.Helper()
	if !fc.found {
		return fc
	}
	if !reflect.DeepEqual(fc.initial, old) {
		fc.t.Errorf("expected %s'%s.%s'%s to change from %v, but it started at %v", bold, fc.double, fc.field, reset, old, fc.initial)
	}
	return fc
}

// To asserts the field's current value is want.
func (fc *FieldChange) To(want any) *FieldChange {
	fc.t.Helper()
	if !fc.found {
		return fc
	}
	if !reflect.DeepEqual(fc.current, want) {
		fc.t.Errorf("expected %s'%s.%s'%s to change to %v, but it is %v", bold, fc.double, fc.field, reset, want, fc.current)
	}
	return fc
}

func (v *Verification) registeredDouble() *doubleRecord {
	rv := reflect.ValueOf(v.caller)
	if !v.hasCaller || rv.Kind() != reflect.Ptr || rv.IsNil() {
		panic("testigo: DidChange/DidNotChange/Changed need That(double), where double is a pointer registered with NewDouble")
	}
	if rec := doubleAt(rv.Pointer()); rec != nil {
		return rec
	}
	panic("testigo: DidChange/DidNotChange/Changed: That(...) was not registered with NewDouble")
}

// Called specifies the spied function, expected exactly once unless you chain a count.
func (v *Verification) Called(function any) *PendingCall {
	exp := newExpectation(function)
	if v.hasCaller {
		exp.setCaller(v.caller)
	}

	exp.site, exp.siteFile, exp.siteLine = callerSite()
	p := &PendingCall{t: v.t, exp: exp}
	registerForFinalCheck(v.t, p)
	return p
}

func callerSite() (site, file string, line int) {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.File != "" && !isTestigoFrame(frame.Function) {
			return fmt.Sprintf("%s:%d", path.Base(frame.File), frame.Line), frame.File, frame.Line
		}
		if !more {
			return "", "", 0
		}
	}
}

func isTestigoFrame(fn string) bool {
	return strings.Contains(fn, "/testigo/internal/core.") ||
		strings.Contains(fn, "/testigo/assert.")
}

// PendingCall is an Expect chain waiting for its call count.
type PendingCall struct {
	t    testing.TB
	exp  *CalledFunc
	done bool
}

// WithParams sets the arguments the call must have received; omit to match any, or pass a Matcher.
func (p *PendingCall) WithParams(params ...any) *PendingCall {
	p.t.Helper()
	p.exp.expectedArgs = params
	p.finish(1, false)
	return p
}

// Once asserts the call happened exactly once (the default).
func (p *PendingCall) Once() {
	p.t.Helper()
	p.finish(1, false)
}

// Twice asserts the call happened exactly twice.
func (p *PendingCall) Twice() {
	p.t.Helper()
	p.finish(2, false)
}

// ThreeTimes asserts the call happened exactly three times.
func (p *PendingCall) ThreeTimes() {
	p.t.Helper()
	p.finish(3, false)
}

// Times asserts the call happened exactly n times.
func (p *PendingCall) Times(n int) {
	p.t.Helper()
	p.finish(n, false)
}

// Never asserts the call did not happen.
func (p *PendingCall) Never() {
	p.t.Helper()
	p.finish(0, false)
}

// AtLeastOnce asserts the call happened one or more times.
func (p *PendingCall) AtLeastOnce() {
	p.t.Helper()
	p.finish(1, true)
}

// Before asserts the spied call happened before every call to function, by global call order.
func (p *PendingCall) Before(function any) *PendingCall {
	p.t.Helper()
	p.done = true
	if ok, err := assertOrder(p.exp, function, orderBefore, 0); !ok {
		p.t.Fatal(err)
	}
	return p
}

// After asserts the spied call happened after every call to function — the
// mirror of Before.
func (p *PendingCall) After(function any) *PendingCall {
	p.t.Helper()
	p.done = true
	if ok, err := assertOrder(p.exp, function, orderAfter, 0); !ok {
		p.t.Fatal(err)
	}
	return p
}

// Within asserts the spied call and a call to function happened within window of each other.
func (p *PendingCall) Within(window time.Duration, function any) *PendingCall {
	p.t.Helper()
	p.done = true
	if ok, err := assertOrder(p.exp, function, orderWithin, window); !ok {
		p.t.Fatal(err)
	}
	return p
}

func (p *PendingCall) finish(times int, atLeast bool) {
	p.t.Helper()
	p.done = true
	p.exp.times = times
	p.exp.atLeast = atLeast

	if ok, err := assertCalls(false, p.exp); !ok {
		p.t.Fatal(err)
	}
}

type testVerifier struct {
	mu           sync.Mutex
	pendings     []*PendingCall
	valueAsserts int
}

var testVerifiers = sync.Map{}

func armFinalCheck(t testing.TB) *testVerifier {
	testID := getTestID()
	if testID == "" {
		return nil
	}

	val, loaded := testVerifiers.LoadOrStore(testID, &testVerifier{})
	tv := val.(*testVerifier)
	if !loaded {
		t.Cleanup(func() {
			testVerifiers.Delete(testID)
			finalCheck(t, tv)
		})
	}
	return tv
}

func registerForFinalCheck(t testing.TB, p *PendingCall) {
	tv := armFinalCheck(t)
	if tv == nil {
		return
	}

	tv.mu.Lock()
	tv.pendings = append(tv.pendings, p)
	tv.mu.Unlock()
}

func noteValueAssertion() {
	if testID := getTestID(); testID != "" {
		if val, ok := testVerifiers.Load(testID); ok {
			tv := val.(*testVerifier)
			tv.mu.Lock()
			tv.valueAsserts++
			tv.mu.Unlock()
		}
	}
}

func finalCheck(t testing.TB, tv *testVerifier) {
	t.Helper()

	tv.mu.Lock()
	defer tv.mu.Unlock()

	exps := make([]*CalledFunc, 0, len(tv.pendings))
	for _, p := range tv.pendings {
		if !p.done {
			p.done = true
			if ok, err := assertCalls(false, p.exp); !ok {
				reportFinal(t, err)
			}
		}
		exps = append(exps, p.exp)
	}

	if t.Failed() {
		return
	}

	if ok, err := checkUncoveredCalls(exps...); !ok {
		reportFinal(t, err)
		return
	}

	if ignored := ignoredReturnedValues(exps); len(ignored) > 0 {
		reportWarning(t, ignoredReturnedValuesFixMessage(ignored))
	}

	required, sources := requiredValueAssertions(exps)
	if tv.valueAsserts < required {
		reportFinal(t, errors.New(outcomeAssertionFixMessage(tv.valueAsserts, required, sources)))
	}
}

type ignoredReturn struct {
	file string
	line int
	src  string
}

func ignoredReturnedValues(exps []*CalledFunc) []ignoredReturn {
	seen := make(map[string]bool)
	var ignored []ignoredReturn
	for _, exp := range exps {
		if exp.err != nil || exp.siteFile == "" || exp.siteLine == 0 {
			continue
		}
		lines := sourceLines(exp.siteFile)
		for i := exp.siteLine - 2; i >= 0 && exp.siteLine-2-i < 40; i-- {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" || strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.HasPrefix(trimmed, "testigo.Run(") || strings.HasPrefix(trimmed, "t.Run(") || strings.HasPrefix(trimmed, "func Test") {
				break
			}
			if assignmentIgnoresReturnedCall(trimmed) {
				key := fmt.Sprintf("%s:%d", exp.siteFile, i+1)
				if !seen[key] {
					seen[key] = true
					ignored = append(ignored, ignoredReturn{file: path.Base(exp.siteFile), line: i + 1, src: trimmed})
				}
			}
		}
	}
	return ignored
}

func assignmentIgnoresReturnedCall(line string) bool {
	idx := strings.Index(line, ":=")
	opLen := 2
	if idx < 0 {
		idx = strings.Index(line, " = ")
		opLen = 3
	}
	if idx < 0 {
		return false
	}
	lhs, rhs := line[:idx], line[idx+opLen:]
	if !strings.Contains(rhs, "(") {
		return false
	}
	for _, name := range strings.Split(lhs, ",") {
		if strings.TrimSpace(name) == "_" {
			return true
		}
	}
	return false
}

func ignoredReturnedValuesFixMessage(ignored []ignoredReturn) string {
	var b strings.Builder
	b.WriteString("testigo: a subject call discards returned value(s) with _, so not every outcome can be asserted.\n")
	b.WriteString("\nAI_FIX:\n")
	b.WriteString("problem: discarded_subject_return_value\n")
	b.WriteString("evidence:\n")
	for _, item := range ignored {
		fmt.Fprintf(&b, "- %s:%d: %s\n", item.file, item.line, item.src)
	}
	b.WriteString("suggested_fix:\n")
	b.WriteString("- Replace _ with a named variable and assert it.\n")
	b.WriteString("- Example:\n")
	b.WriteString("  result, err := subjectUnderTest(...)\n")
	b.WriteString("  assert.Error(t, err)        // or assert.NoError(t, err)\n")
	b.WriteString("  assert.Equal(t, result, expectedResult)\n")
	return b.String()
}

func requiredValueAssertions(exps []*CalledFunc) (int, []string) {
	if len(exps) == 0 {
		return 0, nil
	}

	required := 1
	var sources []string
	for _, exp := range exps {
		if exp.err != nil {
			continue
		}
		switch {
		case exp.returnCount > required:
			required = exp.returnCount
			sources = []string{exp.displayName()}
		case exp.returnCount == required && exp.returnCount > 0:
			sources = append(sources, exp.displayName())
		}
	}
	return required, sources
}

func outcomeAssertionFixMessage(found, required int, sources []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "testigo: calls were verified, but only %d result/state assertion(s) were made (%d required).\n", found, required)
	b.WriteString("\nAI_FIX:\n")
	b.WriteString("problem: verified_calls_without_enough_outcome_assertions\n")
	fmt.Fprintf(&b, "found_result_state_assertions: %d\n", found)
	fmt.Fprintf(&b, "required_result_state_assertions: %d\n", required)
	if len(sources) > 0 {
		fmt.Fprintf(&b, "requirement_source: verified function return count for %s\n", strings.Join(sources, ", "))
	}
	b.WriteString("suggested_fix:\n")
	b.WriteString("- Capture every return value from the subject call instead of discarding it with _.\n")
	b.WriteString("- Add one assertion per returned outcome, for example:\n")
	b.WriteString("  result, err := subjectUnderTest(...)\n")
	b.WriteString("  assert.NoError(t, err)\n")
	b.WriteString("  assert.Equal(t, result, expectedResult)\n")
	b.WriteString("- If the outcome is stateful instead of returned, assert it with assert.Equal/Len/Contains or assert.That(...).DidChange().")
	return b.String()
}

func reportFinal(t testing.TB, err error) {
	t.Helper()
	fmt.Fprintln(t.Output(), err)
	t.Fail()
}

func reportWarning(t testing.TB, warning string) {
	t.Helper()
	fmt.Fprintln(t.Output(), warning)
}

func newExpectation(function any) *CalledFunc {
	v := reflect.ValueOf(function)
	if v.Kind() != reflect.Func {
		return &CalledFunc{
			err: fmt.Errorf("testigo: expected a function (e.g. cashier.charge), but received a value of type %T", function),
		}
	}

	fullName := runtime.FuncForPC(v.Pointer()).Name()
	component, methodName := splitFullFuncName(fullName)

	noteAsserted(component, methodName)

	return &CalledFunc{funcName: methodName, calleeComponent: component, returnCount: v.Type().NumOut(), times: 1}
}

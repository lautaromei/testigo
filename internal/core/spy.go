package core

import (
	"bytes"
	"fmt"
	"path"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const Anything = "*"

var testSpies = sync.Map{}

// CallRecord stores detailed information about a single function call.
type CallRecord struct {
	CallerComponent string
	CallerMethod    string
	CalleeComponent string
	CalleeMethod    string
	CallSiteFile    string
	CallSiteLine    int
	Params          []any
	Snapshots       []any
	Seq             uint64
	Time            time.Time
	GoroutineID     uint64
}

var callSeq atomic.Uint64

func (c *CallRecord) recorded() []any {
	if c.Snapshots != nil {
		return c.Snapshots
	}
	return c.Params
}

func (c *CallRecord) location() string {
	if c.CallSiteFile == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d: ", c.CallSiteFile, c.CallSiteLine)
}

func (c *CallRecord) site() string {
	if c.CallSiteFile == "" {
		return "?"
	}
	return fmt.Sprintf("%s:%d", c.CallSiteFile, c.CallSiteLine)
}

type failureRecord struct {
	failedAssertion *CalledFunc
	reason          string
	actualCount     int
	mismatchedCall  *CallRecord
	relatedCalls    []*CallRecord
	annotated       bool
}

type Spy struct {
	calls           []*CallRecord
	failures        []*failureRecord
	mu              *sync.RWMutex
	unexpectedCalls []*CallRecord
}

func NewSpy() *Spy {
	return &Spy{
		calls: make([]*CallRecord, 0),
		mu:    &sync.RWMutex{},
	}
}

// CalledFunc is a single expectation built by an Expect chain.
type CalledFunc struct {
	expectedArgs []any

	callerComponent string
	calleeComponent string
	funcName        string
	err             error
	returnCount     int
	times           int
	atLeast         bool
	site            string
	siteFile        string
	siteLine        int
}

func (a *CalledFunc) setCaller(caller any) {
	if a.err != nil {
		return
	}

	if name, ok := caller.(string); ok {
		a.callerComponent = name
		return
	}

	t := reflect.TypeOf(caller)
	if t != nil {
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t.Kind() == reflect.Struct {
			pkgPath := t.PkgPath()
			pkgName := path.Base(pkgPath)
			a.callerComponent = fmt.Sprintf("%s.%s", pkgName, t.Name())
		}
	}
}

func (a *CalledFunc) displayName() string {
	if a == nil {
		return ""
	}
	if a.calleeComponent == "" {
		return a.funcName
	}
	return a.calleeComponent + "." + a.funcName
}

func (m *Spy) Call(params ...any) {
	if m == nil {
		panic("testigo: Call called on nil Spy - ensure your struct embeds testigo.Spy")
	}

	if m.calls == nil {
		m.calls = make([]*CallRecord, 0)
		m.failures = make([]*failureRecord, 0)
	}

	if testID := getTestID(); testID != "" {
		val, _ := testSpies.LoadOrStore(testID, &sync.Map{})
		val.(*sync.Map).Store(m, true)
	}

	pcs := make([]uintptr, 5)
	n := runtime.Callers(2, pcs)
	if n < 1 {
		panic("could not get caller information")
	}

	frames := runtime.CallersFrames(pcs[:n])
	calleeFrame, _ := frames.Next()
	callerFrame, _ := frames.Next()

	calleeComponent, calleeMethod := splitFullFuncName(calleeFrame.Function)
	callerComponent, callerMethod := splitFullFuncName(callerFrame.Function)

	if n < 2 {
		callerComponent, callerMethod = "Unknown", "Unknown"
	}

	noteObserved(calleeComponent, calleeMethod)

	m.ensureMu()
	m.mu.Lock()
	defer m.mu.Unlock()
	callSiteFile := ""
	if callerFrame.File != "" {
		callSiteFile = path.Base(callerFrame.File)
	}

	m.calls = append(m.calls, &CallRecord{
		CallerComponent: callerComponent,
		CallerMethod:    callerMethod,
		CalleeComponent: calleeComponent,
		CalleeMethod:    calleeMethod,
		CallSiteFile:    callSiteFile,
		CallSiteLine:    callerFrame.Line,
		Params:          params,
		Snapshots:       snapshotParams(params),
		Seq:             callSeq.Add(1),
		Time:            time.Now(),
		GoroutineID:     currentGoroutineID(),
	})
}

func (m *Spy) ensureMu() {
	if m.mu == nil {
		newMu := &sync.RWMutex{}
		ptr := (*unsafe.Pointer)(unsafe.Pointer(&m.mu))
		atomic.CompareAndSwapPointer(ptr, nil, unsafe.Pointer(newMu))
	}
}

func (m *Spy) Clear() {
	m.ensureMu()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = nil
	m.calls = make([]*CallRecord, 0)
	m.unexpectedCalls = nil
}

// Reset clears the spy's recorded calls. It is the default Resetter behaviour
// every double embedding Spy inherits, so NewDouble can restore it with no
// per-test hook. Override it on a double that also owns external state (a
// database, a temp dir) to reset that state too.
func (m *Spy) Reset() { m.Clear() }

func (m *Spy) TotalCalls() int {
	if m.mu == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.calls)
}

func (m *Spy) checkAndGetCommonPackage() (string, bool) {
	if len(m.calls) == 0 {
		return "", true
	}

	var commonPackage string

	getPackage := func(componentName string) string {
		parts := strings.Split(componentName, ".")
		if len(parts) > 1 {
			return parts[0]
		}
		return ""
	}

	commonPackage = getPackage(m.calls[0].CalleeComponent)

	for _, call := range m.calls {
		if getPackage(call.CalleeComponent) != commonPackage || getPackage(call.CallerComponent) != commonPackage {
			return "", false
		}
	}
	return commonPackage, true
}

func (m *Spy) verifyExpectations(calls map[string][]*CallRecord, assertions []*CalledFunc) []string {
	var errs []string
	m.failures = make([]*failureRecord, 0)

	for _, assertion := range assertions {
		if assertion.err != nil {
			errs = append(errs, assertion.err.Error())
			continue
		}

		allCallsForFunc := calls[assertion.funcName]
		matchingCalls := m.filterMatchingCalls(allCallsForFunc, assertion)
		actualCount := len(matchingCalls)

		satisfied := actualCount == assertion.times
		if assertion.atLeast {
			satisfied = actualCount >= assertion.times
		}

		if !satisfied {
			errMsg := m.buildMismatchedCallError(assertion, actualCount, allCallsForFunc)
			errs = append(errs, errMsg)
			m.addFailure(assertion, errMsg, actualCount, allCallsForFunc)
			if actualCount == 0 && len(allCallsForFunc) > 0 {
				delete(calls, assertion.funcName)
			} else if actualCount > 0 {
				delete(calls, assertion.funcName)
			}
		} else {
			m.consumeMatchingCalls(calls, assertion.funcName, matchingCalls)
		}
	}
	return errs
}

func (m *Spy) addFailure(a *CalledFunc, reason string, actualCount int, allCallsForFunc []*CallRecord) {
	failure := &failureRecord{
		failedAssertion: a,
		reason:          reason,
		actualCount:     actualCount,
		relatedCalls:    allCallsForFunc,
	}
	if actualCount == 0 && len(allCallsForFunc) > 0 {
		failure.mismatchedCall = closestCall(a, allCallsForFunc)
	}

	m.failures = append(m.failures, failure)
}

func closestCall(a *CalledFunc, calls []*CallRecord) *CallRecord {
	if len(a.expectedArgs) == 0 {
		return calls[0]
	}
	best, bestScore := calls[0], -1
	for _, call := range calls {
		score := argDiffCount(a.expectedArgs, call.recorded())
		if bestScore == -1 || score < bestScore {
			best, bestScore = call, score
			if score == 0 {
				break // can't get closer than this
			}
		}
	}
	return best
}

func argDiffCount(expected, actual []any) int {
	if len(expected) != len(actual) {
		return 1 << 30
	}
	var diffs []valueDiff
	for i, exp := range expected {
		if exp == Anything {
			continue
		}
		if _, ok := exp.(Matcher); ok {
			continue
		}
		diffValues(reflect.ValueOf(actual[i]), reflect.ValueOf(exp), "", &diffs)
	}
	return len(diffs)
}

func (m *Spy) checkUnexpectedCalls(calls map[string][]*CallRecord) error {
	unexpectedCount := 0
	m.unexpectedCalls = make([]*CallRecord, 0)
	var unexpectedDetails []string
	for _, remainingCalls := range calls {
		if len(remainingCalls) > 0 {
			for _, call := range remainingCalls {
				if strings.HasPrefix(call.CallerMethod, "Test") {
					continue
				}

				unexpectedCount++
				nodeName := fmt.Sprintf("%s.%s", call.CalleeComponent, call.CalleeMethod)
				callStr := fmt.Sprintf("%s%s%sunexpected call: %s%s%s", call.location(), bold, red, nodeName, formatArgs(call.recorded()), reset)
				unexpectedDetails = append(unexpectedDetails, callStr)
				m.unexpectedCalls = append(m.unexpectedCalls, call)
			}
		}
	}
	if unexpectedCount > 0 {
		return fmt.Errorf("found %d unexpected call(s):\n- %s\n\n%s", unexpectedCount, strings.Join(unexpectedDetails, "\n- "), unexpectedCallsFix(unexpectedCount, m.unexpectedCalls))
	}
	return nil
}

func unexpectedCallsFix(count int, calls []*CallRecord) string {
	var b strings.Builder
	b.WriteString("AI_FIX:\n")
	b.WriteString("problem: recorded_calls_without_assert_that_called\n")
	fmt.Fprintf(&b, "unexpected_call_count: %d\n", count)
	b.WriteString("suggested_fix:\n")
	b.WriteString("- If the interaction is expected, add assert.That(...).Called(...).WithParams(...).Once() for each recorded call below.\n")
	b.WriteString("- If the interaction is not expected, change the production code so the call is not made.\n")
	b.WriteString("candidate_assertions:\n")
	for _, call := range calls {
		fmt.Fprintf(&b, "- callsite: %s:%d\n", call.CallSiteFile, call.CallSiteLine)
		fmt.Fprintf(&b, "  actual_call: %s.%s%s\n", call.CalleeComponent, call.CalleeMethod, formatArgs(call.recorded()))
		fmt.Fprintf(&b, "  suggested_assertion: assert.That(<%s instance>).Called(<%s instance>.%s).WithParams(%s).Once()\n",
			shortComponent(call.CallerComponent), shortComponent(call.CalleeComponent), call.CalleeMethod, suggestedParams(len(call.recorded())))
	}
	return b.String()
}

func shortComponent(component string) string {
	if dot := strings.LastIndex(component, "."); dot >= 0 && dot+1 < len(component) {
		return component[dot+1:]
	}
	return component
}

func suggestedParams(n int) string {
	if n == 0 {
		return ""
	}
	params := make([]string, n)
	for i := range params {
		params[i] = fmt.Sprintf("<expectedArg%d>", i+1)
	}
	return strings.Join(params, ", ")
}

func (m *Spy) buildMismatchedCallError(a *CalledFunc, actualCount int, allRecordedCallsForFunc []*CallRecord) string {
	cleanName := a.funcName
	if actualCount == 0 {
		if len(allRecordedCallsForFunc) > 0 {
			if a.callerComponent != "" {
				callersFound := make(map[string]bool)
				var firstMismatchLocation string
				for _, call := range allRecordedCallsForFunc {
					if expectedParamsMatch(a.expectedArgs, call.recorded()) {
						callersFound[call.CallerComponent] = true
					}
				}
				if len(callersFound) > 0 {
					for _, call := range allRecordedCallsForFunc {
						if expectedParamsMatch(a.expectedArgs, call.recorded()) {
							firstMismatchLocation = call.location()
							break
						}
					}
					var foundCallerNames []string
					for name := range callersFound {
						foundCallerNames = append(foundCallerNames, fmt.Sprintf("'%s'", name))
					}
					return fmt.Sprintf("%sexpected '%s' to be called by '%s', but it was called by %s instead.", firstMismatchLocation, cleanName, a.callerComponent, strings.Join(foundCallerNames, ", "))
				}
			}
			expectedArgsStr := "with any arguments"
			if len(a.expectedArgs) > 0 {
				expectedArgsStr = fmt.Sprintf("with arguments %v", a.expectedArgs)
			}

			var receivedCallsStr strings.Builder
			for i, call := range allRecordedCallsForFunc {
				if len(call.recorded()) == 0 {
					fmt.Fprintf(&receivedCallsStr, "\n    - Call %d: (no arguments)", i+1)
				} else {
					fmt.Fprintf(&receivedCallsStr, "\n    - Call %d: %v", i+1, call.recorded())
				}
			}
			return fmt.Sprintf("%sexpected '%s' to be called %s %s, but it was called 0 times with those arguments. %d call(s) were recorded with different arguments:%s", allRecordedCallsForFunc[0].location(), cleanName, a.timesDescription(), expectedArgsStr, len(allRecordedCallsForFunc), receivedCallsStr.String())
		}
		return fmt.Sprintf("%sexpected '%s' to be called %s, but it was not called.%s", a.locationPrefix(), cleanName, a.timesDescription(), m.missingSpyCallHint(a))
	}
	return fmt.Sprintf("%sexpected '%s' to be called %s, but it was called %d time(s)", allRecordedCallsForFunc[0].location(), cleanName, a.timesDescription(), actualCount)
}

func (m *Spy) missingSpyCallHint(a *CalledFunc) string {
	if a.calleeComponent == "" {
		return ""
	}
	for _, call := range m.calls {
		if call.CalleeComponent == a.calleeComponent {
			return ""
		}
	}
	if _, registered := spyComponents.Load(a.calleeComponent); !registered {
		typeName := a.calleeComponent
		if dot := strings.LastIndex(typeName, "."); dot != -1 {
			typeName = typeName[dot+1:]
		}
		example := fmt.Sprintf("\n      type %s struct {\n          testigo.Spy\n      }\n      func (d *%s) %s(...) {\n          d.Call(...) // first line\n      }", typeName, typeName, a.funcName)
		return fmt.Sprintf("\n%s%s  hint: '%s' is not a registered testigo double — to verify '%s', embed a testigo.Spy in '%s' and call Call(...) as the first line of '%s' (registering it with NewDouble is optional, but resets it between subtests)%s%s%s%s", bold, yellow, a.calleeComponent, a.funcName, a.calleeComponent, a.funcName, reset, white, example, reset)
	}
	return fmt.Sprintf("\n  hint: '%s' holds a testigo.Spy but recorded no calls at all — if '%s' did run, make sure its first line is Call(...)", a.calleeComponent, a.funcName)
}

func (a *CalledFunc) timesDescription() string {
	if a.atLeast {
		return fmt.Sprintf("at least %d time(s)", a.times)
	}
	return fmt.Sprintf("%d time(s)", a.times)
}

func (a *CalledFunc) locationPrefix() string {
	if a.site == "" {
		return ""
	}
	return a.site + ": "
}

func (m *Spy) filterMatchingCalls(recordedCalls []*CallRecord, a *CalledFunc) []*CallRecord {
	if len(recordedCalls) == 0 {
		return nil
	}

	matchingCalls := recordedCalls

	if a.callerComponent != "" {
		matchingCalls = filterByCaller(matchingCalls, a.callerComponent)
	}

	if len(a.expectedArgs) > 0 {
		var paramMatchingCalls []*CallRecord
		for _, call := range matchingCalls {
			if paramsMatch(a.expectedArgs, call.recorded()) {
				paramMatchingCalls = append(paramMatchingCalls, call)
			}
		}
		matchingCalls = paramMatchingCalls
	}
	return matchingCalls
}

func aliasingWarning(f *failureRecord) string {
	a := f.failedAssertion
	if a == nil || len(a.expectedArgs) == 0 {
		return ""
	}
	for _, call := range f.relatedCalls {
		if call.Snapshots == nil {
			continue
		}
		if paramsMatch(a.expectedArgs, call.Params) && !paramsMatch(a.expectedArgs, call.Snapshots) {
			return fmt.Sprintf("%s⚠ %sthe arguments of the call match the expectation NOW, but were different at call time — they were mutated after the call (aliasing)%s", yellow, call.location(), reset)
		}
	}
	return ""
}

func filterByCaller(calls []*CallRecord, callerComponent string) []*CallRecord {
	var filtered []*CallRecord
	for _, call := range calls {
		if isCallerMatch(callerComponent, call.CallerComponent) {
			filtered = append(filtered, call)
		}
	}
	return filtered
}

func isCallerMatch(expected, actual string) bool {
	if expected == actual {
		return true
	}

	expectedPkg, expectedType := splitPackageAndType(expected)
	actualPkg, actualType := splitPackageAndType(actual)

	if expectedPkg != actualPkg {
		return false
	}

	if strings.Contains(expectedType, actualType) {
		return true
	}

	return false
}

func splitPackageAndType(component string) (string, string) {
	lastDot := strings.LastIndex(component, ".")
	if lastDot == -1 {
		return "", component
	}
	return component[:lastDot], component[lastDot+1:]
}

func (m *Spy) consumeMatchingCalls(allCalls map[string][]*CallRecord, funcName string, callsToConsume []*CallRecord) {
	if len(callsToConsume) == 0 {
		return
	}

	consumeSet := make(map[*CallRecord]bool, len(callsToConsume))
	for _, call := range callsToConsume {
		consumeSet[call] = true
	}

	originalCalls := allCalls[funcName]
	var remainingCalls []*CallRecord
	for _, call := range originalCalls {
		if !consumeSet[call] {
			remainingCalls = append(remainingCalls, call)
		}
	}

	allCalls[funcName] = remainingCalls
}

func expectedParamsMatch(expected, actual []any) bool {
	return len(expected) == 0 || paramsMatch(expected, actual)
}

func paramsMatch(expected, actual []any) bool {
	if len(expected) != len(actual) {
		return false
	}

	for i, exp := range expected {
		if !argMatches(exp, actual[i]) {
			return false
		}
	}
	return true
}

func argMatches(expected, actual any) bool {
	if expected == Anything {
		return true
	}
	if matcher, ok := expected.(Matcher); ok {
		return matcher.Matches(actual)
	}
	return reflect.DeepEqual(expected, actual)
}

func cleanFuncName(fullName string, stripPackage bool) string {
	fullName = strings.TrimSuffix(fullName, "-fm")
	if stripPackage {
		parts := strings.Split(fullName, ".")
		if len(parts) > 1 {
			return strings.Join(parts[1:], ".")
		}
	}
	return fullName
}

func splitFullFuncName(fullName string) (component, method string) {
	if fullName == "" {
		return "Unknown", "Unknown"
	}
	fullName = strings.TrimSuffix(fullName, "-fm")

	lastDotIndex := strings.LastIndex(fullName, ".")
	if lastDotIndex == -1 {
		return "Unknown", fullName
	}

	methodName := fullName[lastDotIndex+1:]
	componentPath := fullName[:lastDotIndex]

	lastSlashIndex := strings.LastIndex(componentPath, "/")
	componentWithPackage := componentPath[lastSlashIndex+1:]

	componentWithPackage = strings.ReplaceAll(componentWithPackage, "(*", "")
	componentWithPackage = strings.ReplaceAll(componentWithPackage, ")", "")
	return componentWithPackage, methodName
}

type Matcher interface {
	Matches(x any) bool
	String() string
}

func assertCalls(checkUnexpected bool, that ...*CalledFunc) (bool, error) {
	if len(that) == 0 && !checkUnexpected {
		return true, nil
	}

	virtualSpy, copiedCalls := collectTestCalls()

	errs := virtualSpy.verifyExpectations(copiedCalls, that)

	if checkUnexpected {
		if unexpectedErr := virtualSpy.checkUnexpectedCalls(copiedCalls); unexpectedErr != nil {
			errs = append(errs, unexpectedErr.Error())
		}
	}

	if len(errs) > 0 {
		return false, virtualSpy.failureReport(errs)
	}

	return true, nil
}

type orderKind int

const (
	orderBefore orderKind = iota
	orderAfter
	orderWithin
)

func orderWord(k orderKind) string {
	switch k {
	case orderBefore:
		return "before"
	case orderAfter:
		return "after"
	default:
		return "within a time window of"
	}
}

func assertOrder(a *CalledFunc, otherFn any, kind orderKind, window time.Duration) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	other := newExpectation(otherFn)
	if other.err != nil {
		return false, other.err
	}
	auditNoteOrderAssertion()

	virtualSpy, callsByFunc := collectTestCalls()
	aCalls := virtualSpy.filterMatchingCalls(callsByFunc[a.funcName], a)
	bCalls := callsByFunc[other.funcName]

	if len(aCalls) == 0 {
		return false, fmt.Errorf("expected '%s' to be called %s '%s', but '%s' was never called", a.displayName(), orderWord(kind), other.displayName(), a.displayName())
	}
	if len(bCalls) == 0 {
		return false, fmt.Errorf("expected '%s' to be called %s '%s', but '%s' was never called", a.displayName(), orderWord(kind), other.displayName(), other.displayName())
	}

	switch kind {
	case orderBefore:
		lastA := maxSeqCall(aCalls)
		firstB := minSeqCall(bCalls)
		if lastA.Seq < firstB.Seq {
			return true, nil
		}
		return false, fmt.Errorf("expected '%s' to be called before '%s', but the last '%s' (%s) happened after the first '%s' (%s)",
			a.displayName(), other.displayName(), a.displayName(), lastA.site(), other.displayName(), firstB.site())
	case orderAfter:
		firstA := minSeqCall(aCalls)
		lastB := maxSeqCall(bCalls)
		if firstA.Seq > lastB.Seq {
			return true, nil
		}
		return false, fmt.Errorf("expected '%s' to be called after '%s', but the first '%s' (%s) happened before the last '%s' (%s)",
			a.displayName(), other.displayName(), a.displayName(), firstA.site(), other.displayName(), lastB.site())
	default:
		gap, ca, cb := closestPair(aCalls, bCalls)
		if gap <= window {
			return true, nil
		}
		return false, fmt.Errorf("expected '%s' and '%s' to be called within %s of each other, but the closest pair ('%s' at %s, '%s' at %s) were %s apart",
			a.displayName(), other.displayName(), window, a.displayName(), ca.site(), other.displayName(), cb.site(), gap)
	}
}

func maxSeqCall(calls []*CallRecord) *CallRecord {
	latest := calls[0]
	for _, c := range calls[1:] {
		if c.Seq > latest.Seq {
			latest = c
		}
	}
	return latest
}

func minSeqCall(calls []*CallRecord) *CallRecord {
	earliest := calls[0]
	for _, c := range calls[1:] {
		if c.Seq < earliest.Seq {
			earliest = c
		}
	}
	return earliest
}

func closestPair(aCalls, bCalls []*CallRecord) (time.Duration, *CallRecord, *CallRecord) {
	best := time.Duration(-1)
	var ca, cb *CallRecord
	for _, x := range aCalls {
		for _, y := range bCalls {
			gap := x.Time.Sub(y.Time)
			if gap < 0 {
				gap = -gap
			}
			if best < 0 || gap < best {
				best, ca, cb = gap, x, y
			}
		}
	}
	return best, ca, cb
}

func checkUncoveredCalls(that ...*CalledFunc) (bool, error) {
	virtualSpy, copiedCalls := collectTestCalls()

	for _, assertion := range that {
		if assertion.err != nil {
			continue
		}
		matching := virtualSpy.filterMatchingCalls(copiedCalls[assertion.funcName], assertion)
		virtualSpy.consumeMatchingCalls(copiedCalls, assertion.funcName, matching)
	}

	if unexpectedErr := virtualSpy.checkUnexpectedCalls(copiedCalls); unexpectedErr != nil {
		return false, virtualSpy.failureReport([]string{unexpectedErr.Error()})
	}

	return true, nil
}

func collectTestCalls() (*Spy, map[string][]*CallRecord) {
	spySet := make(map[*Spy]bool)
	if testID := getTestID(); testID != "" {
		if val, ok := testSpies.Load(testID); ok {
			val.(*sync.Map).Range(func(key, value any) bool {
				spySet[key.(*Spy)] = true
				return true
			})
		}
	}
	// Also gather spies the current test owns through NewDouble, so calls made
	// from worker goroutines (e.g. an HTTP handler under httptest) — which
	// register under their own goroutine ID, not the test's — are still seen.
	addOwnedSpies(spySet)

	var allCalls []*CallRecord
	for spy := range spySet {
		if spy.mu != nil {
			spy.mu.RLock()
			allCalls = append(allCalls, spy.calls...)
			spy.mu.RUnlock()
		}
	}

	virtualSpy := &Spy{
		calls:    allCalls,
		failures: make([]*failureRecord, 0),
	}

	callsByFunc := make(map[string][]*CallRecord)
	for _, call := range virtualSpy.calls {
		callsByFunc[call.CalleeMethod] = append(callsByFunc[call.CalleeMethod], call)
	}
	return virtualSpy, callsByFunc
}

func (m *Spy) failureReport(errs []string) error {
	commonPackage, allSame := m.checkAndGetCommonPackage()
	var graph string
	if allSame {
		graph = m.DrawGraph(commonPackage)
	} else {
		graph = m.DrawGraph("")
	}

	annotated := make(map[string]bool)
	var warnings []string
	for _, failure := range m.failures {
		if failure.annotated {
			annotated[failure.reason] = true
		}
		if w := aliasingWarning(failure); w != "" {
			warnings = append(warnings, w)
		}
	}

	var remaining []string
	for _, err := range errs {
		if !annotated[err] {
			remaining = append(remaining, err)
		}
	}

	report := graph
	if len(warnings) > 0 {
		report += "\n" + strings.Join(warnings, "\n") + "\n"
	}

	switch {
	case len(remaining) == 0:
		return fmt.Errorf("%s\nfound %d error(s) during expectation assertion, annotated in the graph above", report, len(errs))
	case len(remaining) < len(errs):
		return fmt.Errorf("%s\nfound %d error(s) during expectation assertion (%d annotated in the graph above):\n- %s", report, len(errs), len(errs)-len(remaining), strings.Join(remaining, "\n- "))
	default:
		return fmt.Errorf("%s\nfound %d error(s) during expectation assertion:\n- %s", report, len(errs), strings.Join(remaining, "\n- "))
	}
}

func getTestID() string {
	id := currentGoroutineID()
	if id == 0 {
		return ""
	}
	return strconv.FormatUint(id, 10)
}

func currentGoroutineID() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	id, err := strconv.ParseUint(string(b), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

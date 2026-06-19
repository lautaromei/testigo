package core

import (
	"path"
	"reflect"
	"sync"
	"testing"
	"unsafe"

	"github.com/lautaromei/testigo/random"
)

type doubleSet struct {
	mu     sync.Mutex
	resets []func()
}

func (s *doubleSet) add(reset func()) {
	s.mu.Lock()
	s.resets = append(s.resets, reset)
	s.mu.Unlock()
}

func (s *doubleSet) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, reset := range s.resets {
		reset()
	}
}

var doubleRegistry sync.Map

func setFor(t *testing.T) *doubleSet {
	if v, ok := doubleRegistry.Load(t); ok {
		return v.(*doubleSet)
	}
	actual, loaded := doubleRegistry.LoadOrStore(t, &doubleSet{})
	if !loaded {
		t.Cleanup(func() { doubleRegistry.Delete(t) })
	}
	return actual.(*doubleSet)
}

// Resetter is a double that knows how to restore its own state. Any double
// embedding Spy satisfies it through Spy.Reset (which clears recorded calls);
// a double backed by external state a struct copy cannot reach — a database, a
// temp dir — implements Reset to wipe and re-seed that state.
type Resetter interface{ Reset() }

// NewDouble registers a test double for the given test and captures its current
// state as the immutable baseline restored before each Run. Returns the same
// pointer for inline use.
//
// In-memory doubles are restored by a deep copy of that baseline, so a subtest
// may mutate the double in place and still start the next Run clean — no
// copy-on-write authoring needed. The copy keeps references to other registered
// doubles and to *Spy pointers by identity, so the wiring between doubles
// survives the restore. Any Spy the double holds is cleared so recorded calls
// never leak between subtests.
//
// A double that owns external state implements Resetter without embedding Spy:
// it is restored only by its Reset method (its fields, which may hold live
// handles like *sql.DB, are never copied).
func NewDouble[T any](t *testing.T, double *T) *T {
	registerSpyComponent(double)
	recordDouble(t, reflect.ValueOf(double))
	registerDoubleSpies(t, double)
	t.Cleanup(bindGoroutine(t))
	auditArm(t)

	_, isResetter := any(double).(Resetter)
	// An external-state double resets through Reset alone: copying its fields
	// would duplicate the live handles it wraps. Everything else (plain
	// in-memory values and spies) is restored from a deep-copied baseline.
	external := isResetter && len(spiesOf(double)) == 0

	var baseline reflect.Value
	if !external {
		baseline = cloneBaseline(reflect.ValueOf(double).Elem())
	}
	setFor(t).add(func() {
		if !external {
			reflect.ValueOf(double).Elem().Set(cloneBaseline(baseline))
			clearSpies(double)
		}
		if r, ok := any(double).(Resetter); ok {
			r.Reset()
		}
	})
	return double
}

// Run runs a subtest with every double registered for t restored to its
// initial state beforehand.
func Run(t *testing.T, name string, testFunc func(t *testing.T)) {
	t.Helper()
	set := setFor(t)
	parent := t
	t.Run(name, func(t *testing.T) {
		registerSubtest(t, parent)
		set.reset()
		defer bindGoroutine(t)()
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("testigo: re-run with TESTIGO_SEED=%s to reproduce this run's random values", random.Phrase())
			}
		})
		auditArm(t)
		armFinalCheck(t)
		testFunc(t)
	})
}

// Reset restores all doubles registered for t immediately, without running a
// subtest.
func Reset(t *testing.T) {
	setFor(t).reset()
}

var spyComponents sync.Map

func registerSpyComponent(double any) {
	t := reflect.TypeOf(double)
	if t == nil || t.Kind() != reflect.Ptr {
		return
	}
	t = t.Elem()
	if t.Kind() != reflect.Struct || !holdsSpy(t) {
		return
	}
	spyComponents.Store(path.Base(t.PkgPath())+"."+t.Name(), true)
}

func holdsSpy(t reflect.Type) bool {
	spyPtrType := reflect.TypeOf((*Spy)(nil))
	spyValueType := reflect.TypeOf(Spy{})
	for i := 0; i < t.NumField(); i++ {
		if ft := t.Field(i).Type; ft == spyPtrType || ft == spyValueType {
			return true
		}
	}
	return false
}

func clearSpies(target any) {
	v := reflect.ValueOf(target).Elem()
	if v.Kind() != reflect.Struct {
		return
	}

	spyPtrType := reflect.TypeOf((*Spy)(nil))
	spyValueType := reflect.TypeOf(Spy{})

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		switch {
		case field.Type() == spyPtrType && !field.IsNil():
			(*Spy)(unsafe.Pointer(field.Pointer())).Clear()
		case field.Type() == spyValueType && field.CanAddr():
			(*Spy)(unsafe.Pointer(field.Addr().Pointer())).Clear()
		}
	}
}

// testDoubleSpies maps a test to the set of Spies held by the doubles
// registered for it with NewDouble. Unlike testSpies — keyed by the goroutine
// that runs Call — this binds a spy to the test that owns it, so calls recorded
// from a worker goroutine (an HTTP handler under httptest, say) are still
// visible to that test's assertions, which run on a different goroutine.
var testDoubleSpies sync.Map // testing.TB -> *sync.Map[*Spy]bool

// subtestParent maps a testigo.Run subtest to its parent, so a spy registered
// on a parent test is reachable from assertions running inside a subtest.
var subtestParent sync.Map // testing.TB -> testing.TB

// registerDoubleSpies records every Spy held by double under t, so the test
// owns them regardless of which goroutine later calls them.
func registerDoubleSpies(t testing.TB, double any) {
	spies := spiesOf(double)
	if len(spies) == 0 {
		return
	}
	val, loaded := testDoubleSpies.LoadOrStore(t, &sync.Map{})
	owned := val.(*sync.Map)
	for _, s := range spies {
		owned.Store(s, true)
	}
	if !loaded {
		t.Cleanup(func() { testDoubleSpies.Delete(t) })
	}
}

// registerSubtest links a subtest to its parent for the lifetime of the
// subtest, letting addOwnedSpies walk up to a parent's registered doubles.
func registerSubtest(child, parent testing.TB) {
	subtestParent.Store(child, parent)
	child.Cleanup(func() { subtestParent.Delete(child) })
}

// spiesOf extracts the Spy pointers a double holds — embedded by value or kept
// as a *Spy field — mirroring clearSpies.
func spiesOf(target any) []*Spy {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return nil
	}

	spyPtrType := reflect.TypeOf((*Spy)(nil))
	spyValueType := reflect.TypeOf(Spy{})

	var spies []*Spy
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		switch {
		case field.Type() == spyPtrType && !field.IsNil():
			spies = append(spies, (*Spy)(unsafe.Pointer(field.Pointer())))
		case field.Type() == spyValueType && field.CanAddr():
			spies = append(spies, (*Spy)(unsafe.Pointer(field.Addr().Pointer())))
		}
	}
	return spies
}

// addOwnedSpies adds to set every Spy owned (via NewDouble) by the current test
// or any of its ancestors. This is what makes worker-goroutine calls visible:
// the asserting goroutine need only resolve to the owning test, not to the
// goroutine that ran Call.
func addOwnedSpies(set map[*Spy]bool) {
	for cur := currentT(); cur != nil; cur = parentTest(cur) {
		if val, ok := testDoubleSpies.Load(cur); ok {
			val.(*sync.Map).Range(func(key, _ any) bool {
				set[key.(*Spy)] = true
				return true
			})
		}
	}
}

func parentTest(t testing.TB) testing.TB {
	if v, ok := subtestParent.Load(t); ok {
		return v.(testing.TB)
	}
	return nil
}

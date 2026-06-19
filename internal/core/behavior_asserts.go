package core

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// PanicsAt is the explicit-skip form for wrapping packages; see EqualAt.
func PanicsAt(t testing.TB, extraSkip int, fn func()) {
	t.Helper()
	noteValueAssertion(t)
	if _, panicked := recovered(fn); panicked {
		return
	}
	label := argExpr(1+extraSkip, "Panics", 1)
	t.Error(labeled(label, "did not panic"))
}

// NotPanicsAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotPanicsAt(t testing.TB, extraSkip int, fn func()) {
	t.Helper()
	noteValueAssertion(t)
	value, panicked := recovered(fn)
	if !panicked {
		return
	}
	label := argExpr(1+extraSkip, "NotPanics", 1)
	t.Error(labeled(label, fmt.Sprintf("panicked with %s%v%s", red, value, reset)))
}

// PanicsWithAt is the explicit-skip form for wrapping packages; see EqualAt.
func PanicsWithAt(t testing.TB, extraSkip int, want any, fn func()) {
	t.Helper()
	noteValueAssertion(t)
	value, panicked := recovered(fn)
	label := argExpr(1+extraSkip, "PanicsWith", 2)
	if !panicked {
		t.Error(labeled(label, fmt.Sprintf("did not panic, want panic %s%v%s", green, want, reset)))
		return
	}
	if reflect.DeepEqual(value, want) {
		return
	}
	t.Error(labeled(label, gotWant(fmt.Sprint(value), fmt.Sprint(want))))
}

func recovered(fn func()) (value any, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			value, panicked = r, true
		}
	}()
	fn()
	return nil, false
}

// SameAt is the explicit-skip form for wrapping packages; see EqualAt.
func SameAt(t testing.TB, extraSkip int, expected, actual any) {
	t.Helper()
	noteValueAssertion(t)
	if samePointer(expected, actual) {
		return
	}
	label := argExpr(1+extraSkip, "Same", 2)
	t.Error(labeled(label, "points to a different object than expected"))
}

// NotSameAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotSameAt(t testing.TB, extraSkip int, expected, actual any) {
	t.Helper()
	noteValueAssertion(t)
	if !samePointer(expected, actual) {
		return
	}
	label := argExpr(1+extraSkip, "NotSame", 2)
	t.Error(labeled(label, "points to the same object, want a different one"))
}

func samePointer(a, b any) bool {
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	if av.Kind() != reflect.Ptr || bv.Kind() != reflect.Ptr {
		return false
	}
	if av.IsNil() || bv.IsNil() {
		return false
	}
	return av.Pointer() == bv.Pointer()
}

// ElementsMatchAt is the explicit-skip form for wrapping packages; see EqualAt.
func ElementsMatchAt(t testing.TB, extraSkip int, listA, listB any) {
	t.Helper()
	noteValueAssertion(t)
	a, okA := toSlice(listA)
	b, okB := toSlice(listB)
	label := argExpr(1+extraSkip, "ElementsMatch", 1)
	if !okA || !okB {
		t.Error(labeled(label, "got a non-list value, want a slice or array"))
		return
	}

	missing := difference(b, a)
	extra := difference(a, b)
	if len(missing) == 0 && len(extra) == 0 {
		return
	}
	t.Error(labeled(label, fmt.Sprintf("missing %s%v%s, extra %s%v%s", green, missing, reset, red, extra, reset)))
}

func difference(from, remove []any) []any {
	used := make([]bool, len(remove))
	var diff []any
	for _, item := range from {
		matched := false
		for i, r := range remove {
			if !used[i] && reflect.DeepEqual(item, r) {
				used[i], matched = true, true
				break
			}
		}
		if !matched {
			diff = append(diff, item)
		}
	}
	return diff
}

func toSlice(v any) ([]any, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

// Eventually fails the test when condition does not return true within waitFor,
// polling every tick.
func Eventually(t testing.TB, condition func() bool, waitFor, tick time.Duration) {
	t.Helper()
	noteValueAssertion(t)
	if pollUntil(condition, true, waitFor, tick) {
		return
	}
	t.Errorf("%scondition%s never became true within %v", bold, reset, waitFor)
}

// Never fails the test when condition returns true at any point within
// waitFor, polling every tick.
func Never(t testing.TB, condition func() bool, waitFor, tick time.Duration) {
	t.Helper()
	noteValueAssertion(t)
	if !pollUntil(condition, true, waitFor, tick) {
		return
	}
	t.Errorf("%scondition%s became true within %v, want it to stay false", bold, reset, waitFor)
}

func pollUntil(condition func() bool, target bool, waitFor, tick time.Duration) bool {
	deadline := time.NewTimer(waitFor)
	defer deadline.Stop()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	if condition() == target {
		return true
	}
	for {
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
			if condition() == target {
				return true
			}
		}
	}
}

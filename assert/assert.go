// Package assert verifies testigo doubles and general test values.
package assert

import (
	"cmp"
	"testing"
	"time"

	"github.com/lautaromei/testigo/internal/core"
)

// Verification is the start of an Expect chain.
type Verification = core.Verification

// PendingCall is a verification chain waiting for its call count.
type PendingCall = core.PendingCall

// FieldChange continues a Changed chain, pinning a double field's before/after.
type FieldChange = core.FieldChange

// ShowSource toggles printing the failing assertion's source line with a caret.
func ShowSource(on bool) {
	core.ShowSource = on
}

// Expect starts a verification chain with an explicit test handle.
func Expect(t testing.TB) *Verification {
	return core.Expect(t)
}

// That starts a verification chain naming the expected caller.
func That(caller any) *Verification {
	return core.That(caller)
}


// Equal fails the test when got and want are not deeply equal. On failure it
// names what was compared (the source expression of got) and, for composite
// values, points at the exact fields that differ.
func Equal[T any](t testing.TB, got, want T) {
	t.Helper()
	core.EqualAt(t, 1, got, want)
}

// NotEqual fails the test when got and want are deeply equal. Like Equal, it
// names the compared expression found in the test's source.
func NotEqual[T any](t testing.TB, got, want T) {
	t.Helper()
	core.NotEqualAt(t, 1, got, want)
}

// SoftEqual fails the test when got and want are not equal, allowing type conversion.
func SoftEqual(t testing.TB, got, want any) {
	t.Helper()
	core.SoftEqualAt(t, 1, got, want)
}

// Zero fails the test when value is not the zero value of its type.
func Zero(t testing.TB, value any) {
	t.Helper()
	core.ZeroAt(t, 1, value)
}

// NotZero fails the test when value is the zero value of its type.
func NotZero(t testing.TB, value any) {
	t.Helper()
	core.NotZeroAt(t, 1, value)
}

// Regexp fails the test when value does not match pattern. pattern may be a
// *regexp.Regexp or a string; value may be a string, []byte or fmt.Stringer.
func Regexp(t testing.TB, pattern, value any) {
	t.Helper()
	core.RegexpAt(t, 1, pattern, value)
}

// NotRegexp fails the test when value matches pattern.
func NotRegexp(t testing.TB, pattern, value any) {
	t.Helper()
	core.NotRegexpAt(t, 1, pattern, value)
}

// Subset fails the test when list does not contain every element (slices and
// arrays) or every key/value pair (maps) of subset.
func Subset(t testing.TB, list, subset any) {
	t.Helper()
	core.SubsetAt(t, 1, list, subset)
}

// NotSubset fails the test when list contains the whole subset.
func NotSubset(t testing.TB, list, subset any) {
	t.Helper()
	core.NotSubsetAt(t, 1, list, subset)
}

// IsType fails the test when object's dynamic type differs from expectedType's.
func IsType(t testing.TB, expectedType, object any) {
	t.Helper()
	core.IsTypeAt(t, 1, expectedType, object)
}

// Nil fails the test when value is not nil, naming the source expression.
func Nil(t testing.TB, value any) {
	t.Helper()
	core.NilAt(t, 1, value)
}

// NotNil fails the test when value is nil.
func NotNil(t testing.TB, value any) {
	t.Helper()
	core.NotNilAt(t, 1, value)
}

// Empty fails the test when value is not its zero value — for strings, slices,
// maps, arrays and channels that means a non-zero length.
func Empty(t testing.TB, value any) {
	t.Helper()
	core.EmptyAt(t, 1, value)
}

// NotEmpty fails the test when value is its zero value.
func NotEmpty(t testing.TB, value any) {
	t.Helper()
	core.NotEmptyAt(t, 1, value)
}

// Len fails the test when object's length is not want. object must be a
// string, slice, array, map or channel.
func Len(t testing.TB, object any, want int) {
	t.Helper()
	core.LenAt(t, 1, object, want)
}

// Contains fails the test when container does not hold element. container may
// be a string (substring), a slice or array (member) or a map (key).
func Contains(t testing.TB, container, element any) {
	t.Helper()
	core.ContainsAt(t, 1, container, element)
}

// NotContains fails the test when container holds element.
func NotContains(t testing.TB, container, element any) {
	t.Helper()
	core.NotContainsAt(t, 1, container, element)
}

// NoError fails the test immediately when err is not nil. The failure names
// the call that produced the error, found in the test's source.
func NoError(t testing.TB, err error) {
	t.Helper()
	core.NoErrorAt(t, 1, err)
}

// ErrorIs fails the test when err does not match target in its chain
// (errors.Is). The failure names the call that produced the error.
func ErrorIs(t testing.TB, err, target error) {
	t.Helper()
	core.ErrorIsAt(t, 1, err, target)
}

// ErrorAs fails the test when no error in err's chain matches the type of
// target, a non-nil pointer to an error type (errors.As).
func ErrorAs(t testing.TB, err error, target any) {
	t.Helper()
	core.ErrorAsAt(t, 1, err, target)
}

// ErrorContains fails the test when err is nil or its message does not contain
// substr.
func ErrorContains(t testing.TB, err error, substr string) {
	t.Helper()
	core.ErrorContainsAt(t, 1, err, substr)
}

// EqualError fails the test when err is nil or its message is not exactly want.
func EqualError(t testing.TB, err error, want string) {
	t.Helper()
	core.EqualErrorAt(t, 1, err, want)
}

// NotErrorIs fails the test when err matches target in its chain (errors.Is).
func NotErrorIs(t testing.TB, err, target error) {
	t.Helper()
	core.NotErrorIsAt(t, 1, err, target)
}

// NotErrorAs fails the test when some error in err's chain matches the type of
// target, a non-nil pointer to an error type (errors.As).
func NotErrorAs(t testing.TB, err error, target any) {
	t.Helper()
	core.NotErrorAsAt(t, 1, err, target)
}

// Error fails the test immediately when err is nil. The failure names the
// call that should have produced the error, found in the test's source.
func Error(t testing.TB, err error) {
	t.Helper()
	core.ErrorAt(t, 1, err)
}

// Greater fails the test when got is not strictly greater than want. The type
// parameter makes comparing different types a compile error.
func Greater[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	core.GreaterAt(t, 1, got, want)
}

// GreaterOrEqual fails the test when got is less than want.
func GreaterOrEqual[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	core.GreaterOrEqualAt(t, 1, got, want)
}

// Less fails the test when got is not strictly less than want.
func Less[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	core.LessAt(t, 1, got, want)
}

// LessOrEqual fails the test when got is greater than want.
func LessOrEqual[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	core.LessOrEqualAt(t, 1, got, want)
}

// Positive fails the test when got is not greater than its zero value.
func Positive[T cmp.Ordered](t testing.TB, got T) {
	t.Helper()
	core.PositiveAt(t, 1, got)
}

// Negative fails the test when got is not less than its zero value.
func Negative[T cmp.Ordered](t testing.TB, got T) {
	t.Helper()
	core.NegativeAt(t, 1, got)
}

// InDelta fails the test when got and want differ by more than delta.
func InDelta(t testing.TB, got, want, delta float64) {
	t.Helper()
	core.InDeltaAt(t, 1, got, want, delta)
}

// Same fails the test when expected and actual are not the same pointer.
func Same(t testing.TB, expected, actual any) {
	t.Helper()
	core.SameAt(t, 1, expected, actual)
}

// NotSame fails the test when expected and actual are the same pointer.
func NotSame(t testing.TB, expected, actual any) {
	t.Helper()
	core.NotSameAt(t, 1, expected, actual)
}

// ElementsMatch fails the test when listA and listB do not hold the same
// elements, ignoring order.
func ElementsMatch(t testing.TB, listA, listB any) {
	t.Helper()
	core.ElementsMatchAt(t, 1, listA, listB)
}

// Panics fails the test when fn does not panic.
func Panics(t testing.TB, fn func()) {
	t.Helper()
	core.PanicsAt(t, 1, fn)
}

// NotPanics fails the test when fn panics, reporting the recovered value.
func NotPanics(t testing.TB, fn func()) {
	t.Helper()
	core.NotPanicsAt(t, 1, fn)
}

// PanicsWith fails the test when fn does not panic, or panics with a value not
// deeply equal to want.
func PanicsWith(t testing.TB, want any, fn func()) {
	t.Helper()
	core.PanicsWithAt(t, 1, want, fn)
}

// PanicsWithError fails the test when fn does not panic, panics with a
// non-error value, or panics with an error whose message is not want.
func PanicsWithError(t testing.TB, want string, fn func()) {
	t.Helper()
	core.PanicsWithErrorAt(t, 1, want, fn)
}

// Eventually fails the test when condition does not return true within
// waitFor, polling every tick.
func Eventually(t testing.TB, condition func() bool, waitFor, tick time.Duration) {
	t.Helper()
	core.Eventually(t, condition, waitFor, tick)
}

// Never fails the test when condition returns true at any point within
// waitFor, polling every tick.
func Never(t testing.TB, condition func() bool, waitFor, tick time.Duration) {
	t.Helper()
	core.Never(t, condition, waitFor, tick)
}

// True fails the test when the condition is false.
func True(t testing.TB, condition bool, format string, args ...any) {
	t.Helper()
	core.True(t, condition, format, args...)
}

// False fails the test when the condition is true.
func False(t testing.TB, condition bool, format string, args ...any) {
	t.Helper()
	core.False(t, condition, format, args...)
}

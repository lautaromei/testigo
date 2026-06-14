package core

// These are test-only convenience helpers that mirror the public API of the
// `assert` package (assert.Equal, assert.NoError, ...). They forward to the
// real *At engine with extraSkip=1. They used to live in core's production
// files as exported duplicates of what `assert` already exposes; since core's
// own tests need them but cannot import `assert` (that would be an import
// cycle), they now live here as test-only forwarders.

import (
	"cmp"
	"testing"
)

// Equal fails the test when got and want are not deeply equal.
func Equal[T any](t testing.TB, got, want T) {
	t.Helper()
	EqualAt(t, 1, got, want)
}

// NotEqual fails the test when got and want are deeply equal.
func NotEqual[T any](t testing.TB, got, want T) {
	t.Helper()
	NotEqualAt(t, 1, got, want)
}

// Nil fails the test when value is not nil.
func Nil(t testing.TB, value any) {
	t.Helper()
	NilAt(t, 1, value)
}

// NotNil fails the test when value is nil.
func NotNil(t testing.TB, value any) {
	t.Helper()
	NotNilAt(t, 1, value)
}

// Empty fails the test when value is not its zero value.
func Empty(t testing.TB, value any) {
	t.Helper()
	EmptyAt(t, 1, value)
}

// NotEmpty fails the test when value is its zero value.
func NotEmpty(t testing.TB, value any) {
	t.Helper()
	NotEmptyAt(t, 1, value)
}

// Len fails the test when object's length is not want.
func Len(t testing.TB, object any, want int) {
	t.Helper()
	LenAt(t, 1, object, want)
}

// Contains fails the test when container does not hold element.
func Contains(t testing.TB, container, element any) {
	t.Helper()
	ContainsAt(t, 1, container, element)
}

// NotContains fails the test when container holds element.
func NotContains(t testing.TB, container, element any) {
	t.Helper()
	NotContainsAt(t, 1, container, element)
}

// ErrorIs fails the test when err does not match target in its chain.
func ErrorIs(t testing.TB, err, target error) {
	t.Helper()
	ErrorIsAt(t, 1, err, target)
}

// ErrorAs fails the test when no error in err's chain matches target's type.
func ErrorAs(t testing.TB, err error, target any) {
	t.Helper()
	ErrorAsAt(t, 1, err, target)
}

// ErrorContains fails the test when err is nil or its message lacks substr.
func ErrorContains(t testing.TB, err error, substr string) {
	t.Helper()
	ErrorContainsAt(t, 1, err, substr)
}

// NoError fails the test immediately when err is not nil.
func NoError(t testing.TB, err error) {
	t.Helper()
	NoErrorAt(t, 1, err)
}

// Error fails the test immediately when err is nil.
func Error(t testing.TB, err error) {
	t.Helper()
	ErrorAt(t, 1, err)
}

// Greater fails the test when got is not strictly greater than want.
func Greater[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	GreaterAt(t, 1, got, want)
}

// GreaterOrEqual fails the test when got is less than want.
func GreaterOrEqual[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	GreaterOrEqualAt(t, 1, got, want)
}

// Less fails the test when got is not strictly less than want.
func Less[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	LessAt(t, 1, got, want)
}

// LessOrEqual fails the test when got is greater than want.
func LessOrEqual[T cmp.Ordered](t testing.TB, got, want T) {
	t.Helper()
	LessOrEqualAt(t, 1, got, want)
}

// Positive fails the test when got is not greater than its zero value.
func Positive[T cmp.Ordered](t testing.TB, got T) {
	t.Helper()
	PositiveAt(t, 1, got)
}

// Negative fails the test when got is not less than its zero value.
func Negative[T cmp.Ordered](t testing.TB, got T) {
	t.Helper()
	NegativeAt(t, 1, got)
}

// InDelta fails the test when got and want differ by more than delta.
func InDelta(t testing.TB, got, want, delta float64) {
	t.Helper()
	InDeltaAt(t, 1, got, want, delta)
}

// Panics fails the test when fn does not panic.
func Panics(t testing.TB, fn func()) {
	t.Helper()
	PanicsAt(t, 1, fn)
}

// NotPanics fails the test when fn panics.
func NotPanics(t testing.TB, fn func()) {
	t.Helper()
	NotPanicsAt(t, 1, fn)
}

// PanicsWith fails the test when fn does not panic with a value equal to want.
func PanicsWith(t testing.TB, want any, fn func()) {
	t.Helper()
	PanicsWithAt(t, 1, want, fn)
}

// Same fails the test when expected and actual are not the same pointer.
func Same(t testing.TB, expected, actual any) {
	t.Helper()
	SameAt(t, 1, expected, actual)
}

// NotSame fails the test when expected and actual are the same pointer.
func NotSame(t testing.TB, expected, actual any) {
	t.Helper()
	NotSameAt(t, 1, expected, actual)
}

// ElementsMatch fails the test when listA and listB do not hold the same
// elements, ignoring order.
func ElementsMatch(t testing.TB, listA, listB any) {
	t.Helper()
	ElementsMatchAt(t, 1, listA, listB)
}

// SoftEqual fails the test when got and want are not equal, allowing a type
// conversion between convertible types.
func SoftEqual(t testing.TB, got, want any) {
	t.Helper()
	SoftEqualAt(t, 1, got, want)
}

// Zero fails the test when value is not the zero value of its type.
func Zero(t testing.TB, value any) {
	t.Helper()
	ZeroAt(t, 1, value)
}

// NotZero fails the test when value is the zero value of its type.
func NotZero(t testing.TB, value any) {
	t.Helper()
	NotZeroAt(t, 1, value)
}

// Regexp fails the test when value does not match pattern.
func Regexp(t testing.TB, pattern, value any) {
	t.Helper()
	RegexpAt(t, 1, pattern, value)
}

// NotRegexp fails the test when value matches pattern.
func NotRegexp(t testing.TB, pattern, value any) {
	t.Helper()
	NotRegexpAt(t, 1, pattern, value)
}

// Subset fails the test when list does not contain every element of subset.
func Subset(t testing.TB, list, subset any) {
	t.Helper()
	SubsetAt(t, 1, list, subset)
}

// NotSubset fails the test when list contains the whole subset.
func NotSubset(t testing.TB, list, subset any) {
	t.Helper()
	NotSubsetAt(t, 1, list, subset)
}

// IsType fails the test when object's dynamic type differs from expectedType's.
func IsType(t testing.TB, expectedType, object any) {
	t.Helper()
	IsTypeAt(t, 1, expectedType, object)
}

// EqualError fails the test when err is nil or its message is not exactly want.
func EqualError(t testing.TB, err error, want string) {
	t.Helper()
	EqualErrorAt(t, 1, err, want)
}

// PanicsWithError fails the test when fn does not panic with an error message
// equal to want.
func PanicsWithError(t testing.TB, want string, fn func()) {
	t.Helper()
	PanicsWithErrorAt(t, 1, want, fn)
}

// NotErrorIs fails the test when err matches target in its chain.
func NotErrorIs(t testing.TB, err, target error) {
	t.Helper()
	NotErrorIsAt(t, 1, err, target)
}

// NotErrorAs fails the test when some error in err's chain matches target's type.
func NotErrorAs(t testing.TB, err error, target any) {
	t.Helper()
	NotErrorAsAt(t, 1, err, target)
}

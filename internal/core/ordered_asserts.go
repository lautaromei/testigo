package core

import (
	"cmp"
	"fmt"
	"math"
	"testing"
)

// GreaterAt is the explicit-skip form for wrapping packages; see EqualAt.
func GreaterAt[T cmp.Ordered](t testing.TB, extraSkip int, got, want T) {
	t.Helper()
	orderedAt(t, extraSkip, "Greater", got > want, got, ">", want)
}

// GreaterOrEqualAt is the explicit-skip form for wrapping packages; see EqualAt.
func GreaterOrEqualAt[T cmp.Ordered](t testing.TB, extraSkip int, got, want T) {
	t.Helper()
	orderedAt(t, extraSkip, "GreaterOrEqual", got >= want, got, ">=", want)
}

// LessAt is the explicit-skip form for wrapping packages; see EqualAt.
func LessAt[T cmp.Ordered](t testing.TB, extraSkip int, got, want T) {
	t.Helper()
	orderedAt(t, extraSkip, "Less", got < want, got, "<", want)
}

// LessOrEqualAt is the explicit-skip form for wrapping packages; see EqualAt.
func LessOrEqualAt[T cmp.Ordered](t testing.TB, extraSkip int, got, want T) {
	t.Helper()
	orderedAt(t, extraSkip, "LessOrEqual", got <= want, got, "<=", want)
}

// PositiveAt is the explicit-skip form for wrapping packages; see EqualAt.
func PositiveAt[T cmp.Ordered](t testing.TB, extraSkip int, got T) {
	t.Helper()
	var zero T
	orderedAt(t, extraSkip, "Positive", got > zero, got, ">", zero)
}

// NegativeAt is the explicit-skip form for wrapping packages; see EqualAt.
func NegativeAt[T cmp.Ordered](t testing.TB, extraSkip int, got T) {
	t.Helper()
	var zero T
	orderedAt(t, extraSkip, "Negative", got < zero, got, "<", zero)
}

func orderedAt[T any](t testing.TB, extraSkip int, fnName string, ok bool, got T, op string, want T) {
	t.Helper()
	noteValueAssertion(t)
	if ok {
		return
	}
	label := argExpr(2+extraSkip, fnName, 1)
	msg := fmt.Sprintf("got %s%v%s, want %s%s %v%s", red, got, reset, green, op, want, reset)
	t.Error(labeled(label, msg))
}

// InDeltaAt is the explicit-skip form for wrapping packages; see EqualAt.
func InDeltaAt(t testing.TB, extraSkip int, got, want, delta float64) {
	t.Helper()
	noteValueAssertion(t)
	if math.Abs(got-want) <= delta {
		return
	}
	label := argExpr(1+extraSkip, "InDelta", 1)
	msg := fmt.Sprintf("got %s%v%s, want %s%v ± %v%s", red, got, reset, green, want, delta, reset)
	t.Error(labeled(label, msg))
}

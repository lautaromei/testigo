package core

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// NotEqualAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotEqualAt[T any](t testing.TB, extraSkip int, got, want T) {
	t.Helper()
	noteValueAssertion()
	if !reflect.DeepEqual(got, want) {
		return
	}
	label := argExpr(1+extraSkip, "NotEqual", 1)
	t.Error(labeled(label, fmt.Sprintf("got %s%s%s, want a different value", red, fmtAny(got), reset)) + caretBlock(1+extraSkip, "NotEqual", 1))
}

// NilAt is the explicit-skip form for wrapping packages; see EqualAt.
func NilAt(t testing.TB, extraSkip int, value any) {
	t.Helper()
	noteValueAssertion()
	if isNil(value) {
		return
	}
	label := argExpr(1+extraSkip, "Nil", 1)
	t.Error(labeled(label, gotWant(fmtAny(value), "nil")) + caretBlock(1+extraSkip, "Nil", 1))
}

// NotNilAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotNilAt(t testing.TB, extraSkip int, value any) {
	t.Helper()
	noteValueAssertion()
	if !isNil(value) {
		return
	}
	label := argExpr(1+extraSkip, "NotNil", 1)
	t.Error(labeled(label, gotWant("nil", "non-nil")) + caretBlock(1+extraSkip, "NotNil", 1))
}

// EmptyAt is the explicit-skip form for wrapping packages; see EqualAt.
func EmptyAt(t testing.TB, extraSkip int, value any) {
	t.Helper()
	noteValueAssertion()
	if isEmpty(value) {
		return
	}
	label := argExpr(1+extraSkip, "Empty", 1)
	t.Error(labeled(label, gotWant(fmtAny(value), "empty")) + caretBlock(1+extraSkip, "Empty", 1))
}

// NotEmptyAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotEmptyAt(t testing.TB, extraSkip int, value any) {
	t.Helper()
	noteValueAssertion()
	if !isEmpty(value) {
		return
	}
	label := argExpr(1+extraSkip, "NotEmpty", 1)
	t.Error(labeled(label, gotWant("empty", "non-empty")) + caretBlock(1+extraSkip, "NotEmpty", 1))
}

// LenAt is the explicit-skip form for wrapping packages; see EqualAt.
func LenAt(t testing.TB, extraSkip int, object any, want int) {
	t.Helper()
	noteValueAssertion()
	label := argExpr(1+extraSkip, "Len", 1)
	got, ok := length(object)
	caret := caretBlock(1+extraSkip, "Len", 1)
	if !ok {
		t.Error(labeled(label, fmt.Sprintf("got %s%T%s, want a value with a length", red, object, reset)) + caret)
		return
	}
	if got == want {
		return
	}
	t.Error(labeled(label, fmt.Sprintf("got len %s%d%s, want %s%d%s", red, got, reset, green, want, reset)) + caret)
}

// ContainsAt is the explicit-skip form for wrapping packages; see EqualAt.
func ContainsAt(t testing.TB, extraSkip int, container, element any) {
	t.Helper()
	noteValueAssertion()
	found, ok := contains(container, element)
	if ok && found {
		return
	}
	label := argExpr(1+extraSkip, "Contains", 1)
	caret := caretBlock(1+extraSkip, "Contains", 1)
	if !ok {
		t.Error(labeled(label, fmt.Sprintf("got %s%T%s, want a container (string, slice, array or map)", red, container, reset)) + caret)
		return
	}
	t.Error(labeled(label, fmt.Sprintf("does not contain %s%s%s", green, fmtAny(element), reset)) + caret)
}

// NotContainsAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotContainsAt(t testing.TB, extraSkip int, container, element any) {
	t.Helper()
	noteValueAssertion()
	found, ok := contains(container, element)
	if ok && !found {
		return
	}
	label := argExpr(1+extraSkip, "NotContains", 1)
	caret := caretBlock(1+extraSkip, "NotContains", 1)
	if !ok {
		t.Error(labeled(label, fmt.Sprintf("got %s%T%s, want a container (string, slice, array or map)", red, container, reset)) + caret)
		return
	}
	t.Error(labeled(label, fmt.Sprintf("contains %s%s%s, want it absent", red, fmtAny(element), reset)) + caret)
}

// ErrorIsAt is the explicit-skip form for wrapping packages; see EqualAt.
func ErrorIsAt(t testing.TB, extraSkip int, err, target error) {
	t.Helper()
	noteValueAssertion()
	if errors.Is(err, target) {
		return
	}
	origin := errorOrigin(1+extraSkip, "ErrorIs")
	msg := fmt.Sprintf("got %s%v%s, want it to match %s%v%s", red, errText(err), reset, green, errText(target), reset)
	t.Error(labeled(origin, msg) + caretBlock(1+extraSkip, "ErrorIs", 1))
}

// ErrorAsAt is the explicit-skip form for wrapping packages; see EqualAt.
func ErrorAsAt(t testing.TB, extraSkip int, err error, target any) {
	t.Helper()
	noteValueAssertion()
	if errors.As(err, target) {
		return
	}
	origin := errorOrigin(1+extraSkip, "ErrorAs")
	want := reflect.TypeOf(target)
	if want != nil && want.Kind() == reflect.Ptr {
		want = want.Elem()
	}
	msg := fmt.Sprintf("got %s%v%s, want an error of type %s%v%s", red, errText(err), reset, green, want, reset)
	t.Error(labeled(origin, msg) + caretBlock(1+extraSkip, "ErrorAs", 1))
}

// ErrorContainsAt is the explicit-skip form for wrapping packages; see EqualAt.
func ErrorContainsAt(t testing.TB, extraSkip int, err error, substr string) {
	t.Helper()
	noteValueAssertion()
	if err != nil && strings.Contains(err.Error(), substr) {
		return
	}
	origin := errorOrigin(1+extraSkip, "ErrorContains")
	msg := fmt.Sprintf("got %s%v%s, want it to contain %s%q%s", red, errText(err), reset, green, substr, reset)
	t.Error(labeled(origin, msg) + caretBlock(1+extraSkip, "ErrorContains", 1))
}

func argExpr(skip int, fnName string, argIndex int) string {
	expr, _, _ := callArgExpression(skip+1, fnName, argIndex)
	if expr == "" || isLiteral(expr) {
		return ""
	}
	return expr
}

func labeled(label, msg string) string {
	if label == "" {
		return msg
	}
	return fmt.Sprintf("%s%s%s: %s", bold, label, reset, msg)
}

func gotWant(got, want string) string {
	return fmt.Sprintf("got %s%s%s, want %s%s%s", red, got, reset, green, want, reset)
}

func fmtAny(v any) string {
	return formatValue(reflect.ValueOf(v))
}

func errText(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() == 0
	case reflect.Ptr:
		return rv.IsNil()
	}
	return rv.IsZero()
}

func length(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len(), true
	}
	return 0, false
}

func contains(container, element any) (found, searchable bool) {
	if container == nil {
		return false, false
	}
	cv := reflect.ValueOf(container)
	switch cv.Kind() {
	case reflect.String:
		sub, ok := element.(string)
		if !ok {
			return false, false
		}
		return strings.Contains(cv.String(), sub), true
	case reflect.Slice, reflect.Array:
		for i := 0; i < cv.Len(); i++ {
			if reflect.DeepEqual(cv.Index(i).Interface(), element) {
				return true, true
			}
		}
		return false, true
	case reflect.Map:
		ev := reflect.ValueOf(element)
		if !ev.IsValid() || !ev.Type().AssignableTo(cv.Type().Key()) {
			return false, true
		}
		return cv.MapIndex(ev).IsValid(), true
	}
	return false, false
}

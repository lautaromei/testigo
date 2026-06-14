package core

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"testing"
)

// SoftEqualAt is the explicit-skip form for wrapping packages; see EqualAt.
//
// Unlike EqualAt it is lenient about type: values of convertible types that
// hold the same underlying value are considered equal (e.g. int32(5) and
// int64(5), or 5 and 5.0).
func SoftEqualAt(t testing.TB, extraSkip int, got, want any) {
	t.Helper()
	noteValueAssertion()
	if equalValues(got, want) {
		return
	}
	label := argExpr(1+extraSkip, "SoftEqual", 1)
	t.Error(labeled(label, gotWant(fmtAny(got), fmtAny(want))) + caretBlock(1+extraSkip, "SoftEqual", 1))
}

// equalValues reports whether got and want are equal after allowing a type
// conversion between convertible types. Numeric values are widened before
// comparison to avoid overflow false positives.
func equalValues(got, want any) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	if !gv.IsValid() || !wv.IsValid() {
		return false
	}
	gt, wt := gv.Type(), wv.Type()
	if !gt.ConvertibleTo(wt) {
		return false
	}
	if isNumericType(gt) != isNumericType(wt) {
		return false
	}
	if isNumericType(gt) && isNumericType(wt) {
		if gt.Size() >= wt.Size() {
			return wv.Convert(gt).Interface() == got
		}
		return gv.Convert(wt).Interface() == want
	}
	return reflect.DeepEqual(gv.Convert(wt).Interface(), want)
}

func isNumericType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128:
		return true
	}
	return false
}

// ZeroAt is the explicit-skip form for wrapping packages; see EqualAt.
func ZeroAt(t testing.TB, extraSkip int, value any) {
	t.Helper()
	noteValueAssertion()
	if isZero(value) {
		return
	}
	label := argExpr(1+extraSkip, "Zero", 1)
	t.Error(labeled(label, gotWant(fmtAny(value), "the zero value")) + caretBlock(1+extraSkip, "Zero", 1))
}

// NotZeroAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotZeroAt(t testing.TB, extraSkip int, value any) {
	t.Helper()
	noteValueAssertion()
	if !isZero(value) {
		return
	}
	label := argExpr(1+extraSkip, "NotZero", 1)
	t.Error(labeled(label, gotWant("the zero value", "a non-zero value")) + caretBlock(1+extraSkip, "NotZero", 1))
}

func isZero(v any) bool {
	if v == nil {
		return true
	}
	return reflect.ValueOf(v).IsZero()
}

// RegexpAt is the explicit-skip form for wrapping packages; see EqualAt.
//
// pattern may be a *regexp.Regexp or a string (compiled on the fly); value may
// be a string, []byte or fmt.Stringer.
func RegexpAt(t testing.TB, extraSkip int, pattern, value any) {
	t.Helper()
	noteValueAssertion()
	label := argExpr(1+extraSkip, "Regexp", 1)
	caret := caretBlock(1+extraSkip, "Regexp", 1)
	re, err := compileRegexp(pattern)
	if err != nil {
		t.Error(labeled(label, fmt.Sprintf("invalid pattern %s%v%s: %v", red, pattern, reset, err)) + caret)
		return
	}
	if re.MatchString(regexpSubject(value)) {
		return
	}
	t.Error(labeled(label, fmt.Sprintf("%s%q%s does not match %s%s%s", red, regexpSubject(value), reset, green, re.String(), reset)) + caret)
}

// NotRegexpAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotRegexpAt(t testing.TB, extraSkip int, pattern, value any) {
	t.Helper()
	noteValueAssertion()
	label := argExpr(1+extraSkip, "NotRegexp", 1)
	caret := caretBlock(1+extraSkip, "NotRegexp", 1)
	re, err := compileRegexp(pattern)
	if err != nil {
		t.Error(labeled(label, fmt.Sprintf("invalid pattern %s%v%s: %v", red, pattern, reset, err)) + caret)
		return
	}
	if !re.MatchString(regexpSubject(value)) {
		return
	}
	t.Error(labeled(label, fmt.Sprintf("%s%q%s matches %s%s%s, want no match", red, regexpSubject(value), reset, green, re.String(), reset)) + caret)
}

func compileRegexp(pattern any) (*regexp.Regexp, error) {
	switch p := pattern.(type) {
	case *regexp.Regexp:
		return p, nil
	case string:
		return regexp.Compile(p)
	case fmt.Stringer:
		return regexp.Compile(p.String())
	default:
		return regexp.Compile(fmt.Sprint(pattern))
	}
}

func regexpSubject(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(value)
	}
}

// SubsetAt is the explicit-skip form for wrapping packages; see EqualAt.
//
// It fails when list does not contain every element (for slices/arrays) or
// every key/value pair (for maps) of subset.
func SubsetAt(t testing.TB, extraSkip int, list, subset any) {
	t.Helper()
	noteValueAssertion()
	label := argExpr(1+extraSkip, "Subset", 1)
	caret := caretBlock(1+extraSkip, "Subset", 1)
	missing, ok := subsetMissing(list, subset)
	if !ok {
		t.Error(labeled(label, "got incompatible kinds, want two slices/arrays or two maps") + caret)
		return
	}
	if len(missing) == 0 {
		return
	}
	t.Error(labeled(label, fmt.Sprintf("missing %s%v%s", green, missing, reset)) + caret)
}

// NotSubsetAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotSubsetAt(t testing.TB, extraSkip int, list, subset any) {
	t.Helper()
	noteValueAssertion()
	label := argExpr(1+extraSkip, "NotSubset", 1)
	caret := caretBlock(1+extraSkip, "NotSubset", 1)
	missing, ok := subsetMissing(list, subset)
	if !ok {
		t.Error(labeled(label, "got incompatible kinds, want two slices/arrays or two maps") + caret)
		return
	}
	if len(missing) > 0 {
		return
	}
	t.Error(labeled(label, "contains the whole subset, want at least one element absent") + caret)
}

// subsetMissing returns the elements of subset not found in list. The second
// result is false when the kinds are incompatible.
func subsetMissing(list, subset any) (missing []any, ok bool) {
	lv, sv := reflect.ValueOf(list), reflect.ValueOf(subset)
	if !lv.IsValid() || !sv.IsValid() {
		return nil, false
	}

	if lv.Kind() == reflect.Map || sv.Kind() == reflect.Map {
		if lv.Kind() != reflect.Map || sv.Kind() != reflect.Map {
			return nil, false
		}
		for _, key := range sv.MapKeys() {
			got := lv.MapIndex(key)
			if !got.IsValid() || !reflect.DeepEqual(got.Interface(), sv.MapIndex(key).Interface()) {
				missing = append(missing, key.Interface())
			}
		}
		return missing, true
	}

	elems, okList := toSlice(list)
	want, okSub := toSlice(subset)
	if !okList || !okSub {
		return nil, false
	}
	for _, item := range want {
		if !containsElem(elems, item) {
			missing = append(missing, item)
		}
	}
	return missing, true
}

func containsElem(list []any, element any) bool {
	for _, item := range list {
		if reflect.DeepEqual(item, element) {
			return true
		}
	}
	return false
}

// IsTypeAt is the explicit-skip form for wrapping packages; see EqualAt.
//
// It fails when object's dynamic type differs from expectedType's.
func IsTypeAt(t testing.TB, extraSkip int, expectedType, object any) {
	t.Helper()
	noteValueAssertion()
	if reflect.TypeOf(object) == reflect.TypeOf(expectedType) {
		return
	}
	label := argExpr(1+extraSkip, "IsType", 2)
	msg := fmt.Sprintf("got type %s%s%s, want %s%s%s", red, typeName(reflect.TypeOf(object)), reset, green, typeName(reflect.TypeOf(expectedType)), reset)
	t.Error(labeled(label, msg) + caretBlock(1+extraSkip, "IsType", 2))
}

// EqualErrorAt is the explicit-skip form for wrapping packages; see EqualAt.
//
// It fails when err is nil or its message is not exactly want.
func EqualErrorAt(t testing.TB, extraSkip int, err error, want string) {
	t.Helper()
	noteValueAssertion()
	if err != nil && err.Error() == want {
		return
	}
	origin := errorOrigin(1+extraSkip, "EqualError")
	msg := fmt.Sprintf("got %s%v%s, want %s%q%s", red, errText(err), reset, green, want, reset)
	t.Error(labeled(origin, msg) + caretBlock(1+extraSkip, "EqualError", 1))
}

// PanicsWithErrorAt is the explicit-skip form for wrapping packages; see EqualAt.
//
// It fails when fn does not panic, panics with a non-error value, or panics
// with an error whose message is not want.
func PanicsWithErrorAt(t testing.TB, extraSkip int, want string, fn func()) {
	t.Helper()
	noteValueAssertion()
	value, panicked := recovered(fn)
	label := argExpr(1+extraSkip, "PanicsWithError", 2)
	if !panicked {
		t.Error(labeled(label, fmt.Sprintf("did not panic, want panic with error %s%q%s", green, want, reset)))
		return
	}
	err, ok := value.(error)
	if !ok {
		t.Error(labeled(label, fmt.Sprintf("panicked with %s%v%s (%T), want an error", red, value, reset, value)))
		return
	}
	if err.Error() == want {
		return
	}
	t.Error(labeled(label, gotWant(err.Error(), want)))
}

// NotErrorIsAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotErrorIsAt(t testing.TB, extraSkip int, err, target error) {
	t.Helper()
	noteValueAssertion()
	if !errors.Is(err, target) {
		return
	}
	origin := errorOrigin(1+extraSkip, "NotErrorIs")
	msg := fmt.Sprintf("got %s%v%s, want it not to match %s%v%s", red, errText(err), reset, green, errText(target), reset)
	t.Error(labeled(origin, msg) + caretBlock(1+extraSkip, "NotErrorIs", 1))
}

// NotErrorAsAt is the explicit-skip form for wrapping packages; see EqualAt.
func NotErrorAsAt(t testing.TB, extraSkip int, err error, target any) {
	t.Helper()
	noteValueAssertion()
	if !errors.As(err, target) {
		return
	}
	origin := errorOrigin(1+extraSkip, "NotErrorAs")
	want := reflect.TypeOf(target)
	if want != nil && want.Kind() == reflect.Ptr {
		want = want.Elem()
	}
	msg := fmt.Sprintf("got %s%v%s, want no error of type %s%s%s", red, errText(err), reset, green, typeName(want), reset)
	t.Error(labeled(origin, msg) + caretBlock(1+extraSkip, "NotErrorAs", 1))
}

package core

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
)

// EqualAt is the explicit-skip form of Equal for wrapping packages.
func EqualAt[T any](t testing.TB, extraSkip int, got, want T) {
	t.Helper()
	noteValueAssertion()
	if reflect.DeepEqual(got, want) {
		return
	}

	label := gotExpression(1 + extraSkip)

	// Equal is strict on dynamic type: when T is an interface, two values that
	// look alike but carry different concrete types must not match. SoftEqual
	// is the lenient counterpart that allows convertible types.
	if gt, wt := reflect.TypeOf(got), reflect.TypeOf(want); gt != wt {
		msg := fmt.Sprintf("got type %s%s%s, want %s%s%s", red, typeName(gt), reset, green, typeName(wt), reset)
		t.Error(labeled(label, msg) + caretBlock(1+extraSkip, "Equal", 1))
		return
	}

	diffs := make([]valueDiff, 0, maxDiffs)
	diffValues(reflect.ValueOf(got), reflect.ValueOf(want), "", &diffs)
	if len(diffs) == 0 {
		diffs = append(diffs, valueDiff{got: formatValue(reflect.ValueOf(got)), want: formatValue(reflect.ValueOf(want))})
	}

	t.Error(diffMessage(label, diffs) + caretBlock(1+extraSkip, "Equal", 1))
}

func typeName(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

// NoErrorAt is the explicit-skip form for wrapping packages; see EqualAt.
func NoErrorAt(t testing.TB, extraSkip int, err error) {
	t.Helper()
	noteValueAssertion()
	if err == nil {
		return
	}
	caret := caretBlock(1+extraSkip, "NoError", 1)
	if origin := errorOrigin(1+extraSkip, "NoError"); origin != "" {
		t.Fatalf("%s%s%s: expected no error, got %s%v%s%s", bold, origin, reset, red, err, reset, caret)
		return
	}
	t.Fatalf("expected no error, got %v%s", err, caret)
}

// ErrorAt is the explicit-skip form for wrapping packages; see EqualAt.
func ErrorAt(t testing.TB, extraSkip int, err error) {
	t.Helper()
	noteValueAssertion()
	if err != nil {
		return
	}
	caret := caretBlock(1+extraSkip, "Error", 1)
	if origin := errorOrigin(1+extraSkip, "Error"); origin != "" {
		t.Fatalf("%s%s%s: expected an error, got nil%s", bold, origin, reset, caret)
		return
	}
	t.Fatalf("expected an error, got nil%s", caret)
}

// True fails the test when the condition is false.
func True(t testing.TB, condition bool, format string, args ...any) {
	t.Helper()
	noteValueAssertion()
	if !condition {
		t.Errorf(format, args...)
	}
}

// False fails the test when the condition is true.
func False(t testing.TB, condition bool, format string, args ...any) {
	t.Helper()
	noteValueAssertion()
	if condition {
		t.Errorf(format, args...)
	}
}

const maxDiffs = 8

type valueDiff struct {
	path string
	got  string
	want string
	block string
}

func diffMessage(label string, diffs []valueDiff) string {
	line := func(d valueDiff) string {
		if d.block != "" {
			return "\n" + indent(d.block, "  ")
		}
		return fmt.Sprintf("got %s%s%s, want %s%s%s", red, d.got, reset, green, d.want, reset)
	}

	if len(diffs) == 1 {
		prefix := label + diffs[0].path
		if prefix == "" {
			return strings.TrimPrefix(line(diffs[0]), "\n")
		}
		return fmt.Sprintf("%s%s%s: %s", bold, prefix, reset, line(diffs[0]))
	}

	header := label
	if header == "" {
		header = "values"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s%s differs:", bold, header, reset)
	for _, d := range diffs {
		fmt.Fprintf(&b, "\n  %s: %s", d.path, line(d))
	}
	return b.String()
}

func indent(s, pad string) string {
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}

func diffValues(got, want reflect.Value, path string, diffs *[]valueDiff) {
	if len(*diffs) >= maxDiffs {
		return
	}

	if !got.IsValid() || !want.IsValid() || got.Type() != want.Type() {
		appendDiff(diffs, path, got, want)
		return
	}

	switch got.Kind() {
	case reflect.Struct:
		for i := 0; i < got.NumField(); i++ {
			diffValues(got.Field(i), want.Field(i), path+"."+got.Type().Field(i).Name, diffs)
		}
	case reflect.Slice, reflect.Array:
		if got.Type().Elem().Kind() == reflect.Uint8 {
			if d, ok := byteDiff(path, got, want); ok {
				*diffs = append(*diffs, d)
			}
			return
		}
		if got.Len() != want.Len() {
			*diffs = append(*diffs, valueDiff{path: path + ".len", got: fmt.Sprint(got.Len()), want: fmt.Sprint(want.Len())})
			return
		}
		for i := 0; i < got.Len(); i++ {
			diffValues(got.Index(i), want.Index(i), fmt.Sprintf("%s[%d]", path, i), diffs)
		}
	case reflect.Map:
		for _, key := range want.MapKeys() {
			gotVal := got.MapIndex(key)
			keyPath := fmt.Sprintf("%s[%v]", path, key)
			if !gotVal.IsValid() {
				*diffs = append(*diffs, valueDiff{path: keyPath, got: "<missing>", want: formatValue(want.MapIndex(key))})
				continue
			}
			diffValues(gotVal, want.MapIndex(key), keyPath, diffs)
		}
		for _, key := range got.MapKeys() {
			if !want.MapIndex(key).IsValid() {
				*diffs = append(*diffs, valueDiff{path: fmt.Sprintf("%s[%v]", path, key), got: formatValue(got.MapIndex(key)), want: "<missing>"})
			}
		}
	case reflect.Ptr, reflect.Interface:
		if got.IsNil() || want.IsNil() {
			if got.IsNil() != want.IsNil() {
				appendDiff(diffs, path, got, want)
			}
			return
		}
		diffValues(got.Elem(), want.Elem(), path, diffs)
	default:
		if got.Kind() == reflect.String && isMultiline(got.String(), want.String()) {
			*diffs = append(*diffs, valueDiff{path: path, block: lineDiff(got.String(), want.String())})
			return
		}
		if !equalLeaf(got, want) {
			appendDiff(diffs, path, got, want)
		}
	}
}

func appendDiff(diffs *[]valueDiff, path string, got, want reflect.Value) {
	g, w := formatValue(got), formatValue(want)
	if g == w {
		g, w = withType(g, got), withType(w, want)
	}
	*diffs = append(*diffs, valueDiff{path: path, got: g, want: w})
}

func withType(s string, v reflect.Value) string {
	if !v.IsValid() {
		return s
	}
	return fmt.Sprintf("%s (%s)", s, v.Type())
}

func equalLeaf(got, want reflect.Value) bool {
	if got.CanInterface() && want.CanInterface() {
		return reflect.DeepEqual(got.Interface(), want.Interface())
	}
	return fmt.Sprintf("%#v", got) == fmt.Sprintf("%#v", want)
}

const maxValueLen = 120

func formatValue(v reflect.Value) string {
	return truncateMiddle(formatValueFull(v), maxValueLen)
}

func formatValueFull(v reflect.Value) string {
	if !v.IsValid() {
		return "<nil>"
	}

	if v.CanInterface() && !isNilValue(v) {
		switch x := v.Interface().(type) {
		case error:
			return enrichScalar(x.Error(), v)
		case fmt.Stringer:
			return enrichScalar(x.String(), v)
		}
	}

	switch v.Kind() {
	case reflect.String:
		return fmt.Sprintf("%q", v)
	case reflect.Ptr:
		if v.IsNil() {
			return "<nil>"
		}
		return "&" + formatValueFull(v.Elem())
	}
	return fmt.Sprintf("%v", v)
}

func enrichScalar(name string, v reflect.Value) string {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.Bool:
		return fmt.Sprintf("%s(%s)", name, rawScalar(v))
	}
	return name
}

func isNilValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func rawScalar(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	}
	return fmt.Sprintf("%v", v)
}

func truncateMiddle(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	keep := max / 2
	omitted := len(r) - 2*keep
	return fmt.Sprintf("%s … %d more … %s", string(r[:keep]), omitted, string(r[len(r)-keep:]))
}

func byteDiff(path string, gotV, wantV reflect.Value) (valueDiff, bool) {
	g, w := toBytes(gotV), toBytes(wantV)
	off := firstByteDiff(g, w)
	if off < 0 {
		return valueDiff{}, false
	}
	return valueDiff{
		path: fmt.Sprintf("%s (byte %d, len %d vs %d)", path, off, len(g), len(w)),
		got:  hexWindow(g, off),
		want: hexWindow(w, off),
	}, true
}

func toBytes(v reflect.Value) []byte {
	b := make([]byte, v.Len())
	for i := range b {
		b[i] = byte(v.Index(i).Uint())
	}
	return b
}

func firstByteDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func hexWindow(b []byte, off int) string {
	const ctx = 4
	start := off - ctx
	if start < 0 {
		start = 0
	}
	end := off + ctx + 1
	if end > len(b) {
		end = len(b)
	}
	var parts []string
	if start > 0 {
		parts = append(parts, "…")
	}
	for i := start; i < end; i++ {
		cell := fmt.Sprintf("%02x", b[i])
		if i == off {
			cell = "[" + cell + "]"
		}
		parts = append(parts, cell)
	}
	if off >= len(b) {
		parts = append(parts, "[--]")
	}
	if end < len(b) {
		parts = append(parts, "…")
	}
	return strings.Join(parts, " ")
}

func isMultiline(a, b string) bool {
	return strings.Contains(a, "\n") || strings.Contains(b, "\n")
}

func lineDiff(got, want string) string {
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	ops := diffLines(gl, wl)
	return renderLineOps(ops)
}

type lineOp struct {
	kind byte
	text string
}

func diffLines(a, b []string) []lineOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var ops []lineOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, lineOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, lineOp{'-', a[i]})
			i++
		default:
			ops = append(ops, lineOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, lineOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, lineOp{'+', b[j]})
	}
	return ops
}

func renderLineOps(ops []lineOp) string {
	const ctx = 2
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		for k := i - ctx; k <= i+ctx; k++ {
			if k >= 0 && k < len(ops) {
				keep[k] = true
			}
		}
	}

	var b strings.Builder
	collapsed := false
	for i, op := range ops {
		if !keep[i] {
			if !collapsed {
				b.WriteString("   ⋮\n")
				collapsed = true
			}
			continue
		}
		collapsed = false
		switch op.kind {
		case '-':
			fmt.Fprintf(&b, "%s- %s%s\n", red, op.text, reset)
		case '+':
			fmt.Fprintf(&b, "%s+ %s%s\n", green, op.text, reset)
		default:
			fmt.Fprintf(&b, "  %s\n", op.text)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ShowSource makes failures append the assertion's source line with a caret under the failing argument; off by default.
var ShowSource bool

var sourceCache sync.Map

func caretBlock(skip int, fnName string, argIndex int) string {
	if !ShowSource {
		return ""
	}
	expr, file, line := callArgExpression(skip+1, fnName, argIndex)
	if expr == "" {
		return ""
	}
	lines := sourceLines(file)
	if line-1 < 0 || line-1 >= len(lines) {
		return ""
	}
	src := lines[line-1]
	call := strings.Index(src, fnName+"(")
	if call < 0 {
		return ""
	}
	rel := strings.Index(src[call:], expr)
	if rel < 0 {
		return ""
	}
	col := call + rel

	trimmed := strings.TrimLeft(src, " \t")
	col -= len(src) - len(trimmed)
	if col < 0 || col > len(trimmed) {
		return ""
	}
	pad := strings.Repeat(" ", len([]rune(trimmed[:col])))
	carets := strings.Repeat("^", len([]rune(expr)))
	return fmt.Sprintf("\n    %s\n    %s%s%s%s", trimmed, pad, yellow, carets, reset)
}

func gotExpression(skip int) string {
	expr, _, _ := callArgExpression(skip+1, "Equal", 1)
	if expr == "" || isLiteral(expr) {
		return ""
	}
	return expr
}

func errorOrigin(skip int, fnName string) string {
	expr, file, line := callArgExpression(skip+1, fnName, 1)
	if expr == "" {
		return ""
	}
	if strings.Contains(expr, "(") {
		return expr
	}

	lines := sourceLines(file)
	for i := line - 2; i >= 0 && line-2-i < 30; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "func ") {
			break
		}
		if rhs := assignmentRHS(trimmed, expr); rhs != "" {
			return rhs
		}
	}
	return ""
}

func assignmentRHS(line, name string) string {
	op := ":="
	idx := strings.Index(line, op)
	if idx < 0 {
		op = " = "
		idx = strings.Index(line, op)
	}
	if idx < 0 {
		return ""
	}

	for _, lhs := range strings.Split(line[:idx], ",") {
		if strings.TrimSpace(lhs) == name {
			return strings.TrimSpace(line[idx+len(op):])
		}
	}
	return ""
}

func callArgExpression(skip int, fnName string, argIndex int) (expr, file string, line int) {
	_, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return "", "", 0
	}

	lines := sourceLines(file)
	if line-1 < 0 || line-1 >= len(lines) {
		return "", file, line
	}

	end := line + 4
	if end > len(lines) {
		end = len(lines)
	}
	snippet := strings.Join(lines[line-1:end], "\n")

	idx := strings.Index(snippet, fnName+"(")
	if idx < 0 {
		return "", file, line
	}
	args := splitArgs(snippet[idx+len(fnName)+1:])
	if len(args) <= argIndex {
		return "", file, line
	}
	return strings.TrimSpace(args[argIndex]), file, line
}

func sourceLines(file string) []string {
	if cached, ok := sourceCache.Load(file); ok {
		return cached.([]string)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		sourceCache.Store(file, []string(nil))
		return nil
	}
	lines := strings.Split(string(content), "\n")
	sourceCache.Store(file, lines)
	return lines
}

func splitArgs(s string) []string {
	var args []string
	depth := 0
	start := 0
	var quote rune

	for i, r := range s {
		if quote != 0 {
			if r == quote && (quote == '`' || i == 0 || s[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			quote = r
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if r == ')' && depth == 0 {
				args = append(args, s[start:i])
				return args
			}
			depth--
		case ',':
			if depth == 0 {
				args = append(args, s[start:i])
				start = i + 1
			}
		}
	}
	return nil
}

func isLiteral(expr string) bool {
	r := rune(expr[0])
	return unicode.IsDigit(r) || r == '"' || r == '\'' || r == '`' || r == '-' ||
		expr == "true" || expr == "false" || expr == "nil" ||
		strings.Contains(expr, "{")
}

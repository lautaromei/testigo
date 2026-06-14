package core

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// --- fixtures ---

type status int

const (
	statusPending status = iota
	statusPaid
)

func (s status) String() string {
	switch s {
	case statusPending:
		return "Pending"
	case statusPaid:
		return "Paid"
	}
	return "Unknown"
}

// errCode is an error whose underlying value is a scalar, so the raw form adds
// information beyond the message.
type errCode int

func (e errCode) Error() string { return fmt.Sprintf("code %d", int(e)) }

// point is a non-scalar Stringer: its String() already shows everything useful.
type point struct{ x, y int }

func (p point) String() string { return fmt.Sprintf("(%d,%d)", p.x, p.y) }

// failEqual runs Equal against a captured handle and returns the ANSI-stripped
// message.
func failEqual[T any](got, want T) string {
	ft := &fakeT{}
	Equal(ft, got, want)
	return stripANSI(ft.message)
}

// --- Stringer / enum / error aware ---

func TestStringerAware(t *testing.T) {
	t.Run("enum shows name and raw value", func(t *testing.T) {
		msg := failEqual(statusPending, statusPaid)
		if !strings.Contains(msg, "Pending(0)") || !strings.Contains(msg, "Paid(1)") {
			t.Errorf("got: %s", msg)
		}
	})

	t.Run("error with scalar underlying shows both", func(t *testing.T) {
		msg := failEqual(errCode(404), errCode(500))
		if !strings.Contains(msg, "code 404(404)") || !strings.Contains(msg, "code 500(500)") {
			t.Errorf("got: %s", msg)
		}
	})

	t.Run("non-scalar Stringer shows only String()", func(t *testing.T) {
		// A struct is walked field-by-field by the diff, so exercise formatValue
		// directly: it must use String() and not append a raw struct dump.
		got := formatValue(reflect.ValueOf(point{1, 2}))
		if got != "(1,2)" {
			t.Errorf("expected (1,2), got: %s", got)
		}
	})

	t.Run("nil error does not panic", func(t *testing.T) {
		var e *errStruct // implements error, nil pointer
		got := formatValue(reflect.ValueOf(e))
		if got != "<nil>" {
			t.Errorf("expected <nil>, got: %s", got)
		}
	})
}

type errStruct struct{ msg string }

func (e *errStruct) Error() string { return e.msg }

// --- pointer dereference ---

func TestPointerDereferenced(t *testing.T) {
	t.Run("pointer to scalar shows value", func(t *testing.T) {
		n := 5
		if got := formatValue(reflect.ValueOf(&n)); got != "&5" {
			t.Errorf("expected &5, got: %s", got)
		}
	})

	t.Run("pointer to struct shows value", func(t *testing.T) {
		v := struct{ a int }{7}
		got := formatValue(reflect.ValueOf(&v))
		if !strings.Contains(got, "&") || !strings.Contains(got, "7") {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("nil pointer shows nil not address", func(t *testing.T) {
		var p *int
		if got := formatValue(reflect.ValueOf(p)); got != "<nil>" {
			t.Errorf("expected <nil>, got: %s", got)
		}
	})

	t.Run("no address leaks", func(t *testing.T) {
		n := 1
		if got := formatValue(reflect.ValueOf(&n)); strings.Contains(got, "0x") {
			t.Errorf("address leaked: %s", got)
		}
	})
}

// --- type mismatch annotation ---

func TestTypeMismatchAnnotated(t *testing.T) {
	type box struct{ v any }

	t.Run("int32 vs int64 with equal text", func(t *testing.T) {
		msg := failEqual(box{int32(1)}, box{int64(1)})
		if !strings.Contains(msg, "(int32)") || !strings.Contains(msg, "(int64)") {
			t.Errorf("got: %s", msg)
		}
	})

	t.Run("float32 vs float64 with equal text", func(t *testing.T) {
		msg := failEqual(box{float32(1.5)}, box{float64(1.5)})
		if !strings.Contains(msg, "(float32)") || !strings.Contains(msg, "(float64)") {
			t.Errorf("got: %s", msg)
		}
	})

	t.Run("different values are not annotated", func(t *testing.T) {
		msg := failEqual(1, 2)
		if strings.Contains(msg, "(int)") {
			t.Errorf("type noise on a plain value diff: %s", msg)
		}
	})
}

// --- []byte hexdump ---

func TestByteHexdump(t *testing.T) {
	t.Run("diff in the middle", func(t *testing.T) {
		msg := failEqual([]byte("ok!\x00more"), []byte("ok!\x21more"))
		if !strings.Contains(msg, "byte 3") {
			t.Errorf("missing offset: %s", msg)
		}
		if !strings.Contains(msg, "[00]") || !strings.Contains(msg, "[21]") {
			t.Errorf("missing bracketed bytes: %s", msg)
		}
	})

	t.Run("diff at offset zero", func(t *testing.T) {
		msg := failEqual([]byte("abc"), []byte("xbc"))
		if !strings.Contains(msg, "byte 0") || !strings.Contains(msg, "[61]") || !strings.Contains(msg, "[78]") {
			t.Errorf("got: %s", msg)
		}
	})

	t.Run("length mismatch marks missing byte", func(t *testing.T) {
		msg := failEqual([]byte("abc"), []byte("abcd"))
		if !strings.Contains(msg, "len 3 vs 4") {
			t.Errorf("missing length info: %s", msg)
		}
		if !strings.Contains(msg, "[--]") {
			t.Errorf("expected missing-byte marker: %s", msg)
		}
	})

	t.Run("leading ellipsis when window is offset", func(t *testing.T) {
		got := append([]byte("0123456789"), 'A')
		want := append([]byte("0123456789"), 'B')
		msg := failEqual(got, want)
		if !strings.Contains(msg, "…") {
			t.Errorf("expected ellipsis for distant window: %s", msg)
		}
	})

	t.Run("equal byte slices pass", func(t *testing.T) {
		ft := &fakeT{}
		Equal(ft, []byte("same"), []byte("same"))
		if ft.failed {
			t.Errorf("unexpected failure: %s", ft.message)
		}
	})
}

// --- multiline git-style diff ---

func TestMultilineLineDiff(t *testing.T) {
	t.Run("changed line shows -/+", func(t *testing.T) {
		msg := failEqual("a\nb\nc", "a\nX\nc")
		if !strings.Contains(msg, "- b") || !strings.Contains(msg, "+ X") {
			t.Errorf("got:\n%s", msg)
		}
		if !strings.Contains(msg, "  a") || !strings.Contains(msg, "  c") {
			t.Errorf("missing context:\n%s", msg)
		}
	})

	t.Run("added line", func(t *testing.T) {
		msg := failEqual("a\nb", "a\nb\nc")
		if !strings.Contains(msg, "+ c") {
			t.Errorf("got:\n%s", msg)
		}
	})

	t.Run("removed line", func(t *testing.T) {
		msg := failEqual("a\nb\nc", "a\nc")
		if !strings.Contains(msg, "- b") {
			t.Errorf("got:\n%s", msg)
		}
	})

	t.Run("collapses distant context", func(t *testing.T) {
		var lines []string
		for i := 0; i < 30; i++ {
			lines = append(lines, fmt.Sprintf("line%d", i))
		}
		got := strings.Join(lines, "\n")
		lines[15] = "CHANGED"
		want := strings.Join(lines, "\n")
		msg := failEqual(got, want)
		if !strings.Contains(msg, "⋮") {
			t.Errorf("expected collapse marker:\n%s", msg)
		}
		if strings.Contains(msg, "line0\n") {
			t.Errorf("distant line not collapsed:\n%s", msg)
		}
	})

	t.Run("single-line strings keep inline form", func(t *testing.T) {
		msg := failEqual("foo", "bar")
		if strings.Contains(msg, "\n") {
			t.Errorf("single-line diff should stay inline: %q", msg)
		}
	})
}

// --- smart truncation ---

func TestSmartTruncation(t *testing.T) {
	t.Run("long value truncated with marker", func(t *testing.T) {
		out := formatValue(reflect.ValueOf(strings.Repeat("x", 500)))
		if len([]rune(out)) > maxValueLen+40 {
			t.Errorf("not truncated: len %d", len([]rune(out)))
		}
		if !strings.Contains(out, "more") {
			t.Errorf("missing omission marker: %s", out)
		}
	})

	t.Run("short value untouched", func(t *testing.T) {
		out := formatValue(reflect.ValueOf("short"))
		if out != `"short"` {
			t.Errorf("short value altered: %s", out)
		}
	})

	t.Run("keeps head and tail", func(t *testing.T) {
		out := truncateMiddle("HEAD"+strings.Repeat("-", 300)+"TAIL", maxValueLen)
		if !strings.HasPrefix(out, "HEAD") || !strings.HasSuffix(out, "TAIL") {
			t.Errorf("ends not preserved: %s", out)
		}
	})
}

// --- source-line caret ---

func TestSourceCaret(t *testing.T) {
	t.Run("renders and aligns under the argument", func(t *testing.T) {
		ShowSource = true
		defer func() { ShowSource = false }()

		ft := &fakeT{}
		payment := struct{ id int }{id: 1}
		Equal(ft, payment.id, 2)

		msg := stripANSI(ft.message)
		if !strings.Contains(msg, "Equal(ft, payment.id, 2)") {
			t.Errorf("missing source line:\n%s", msg)
		}
		lines := strings.Split(msg, "\n")
		last := lines[len(lines)-1]
		src := lines[len(lines)-2]
		if strings.Index(src, "payment.id") != strings.Index(last, "^") {
			t.Errorf("caret misaligned:\n%s\n%s", src, last)
		}
		if strings.Count(last, "^") != len("payment.id") {
			t.Errorf("caret length mismatch:\n%s", last)
		}
	})

	t.Run("off by default", func(t *testing.T) {
		ft := &fakeT{}
		x := 1
		Equal(ft, x, 2)
		if strings.Contains(ft.message, "^") {
			t.Errorf("caret shown while ShowSource is off: %s", ft.message)
		}
	})

	t.Run("works for value asserts", func(t *testing.T) {
		ShowSource = true
		defer func() { ShowSource = false }()

		ft := &fakeT{}
		var p *int
		NotNil(ft, p)
		if !strings.Contains(stripANSI(ft.message), "^") {
			t.Errorf("expected caret for NotNil:\n%s", ft.message)
		}
	})

	t.Run("works for error asserts", func(t *testing.T) {
		ShowSource = true
		defer func() { ShowSource = false }()

		ft := &fakeT{}
		ErrorIs(ft, errors.New("boom"), errCode(1))
		if !strings.Contains(stripANSI(ft.message), "^") {
			t.Errorf("expected caret for ErrorIs:\n%s", ft.message)
		}
	})
}

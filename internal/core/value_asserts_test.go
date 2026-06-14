package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestNotEqual(t *testing.T) {
	t.Run("passes when values differ", func(t *testing.T) {
		ft := &fakeT{}
		NotEqual(ft, 1, 2)
		if ft.failed {
			t.Errorf("Expected NotEqual to pass, got: %s", ft.message)
		}
	})

	t.Run("names the expression when equal", func(t *testing.T) {
		ft := &fakeT{}
		token := struct{ id int }{id: 7}
		NotEqual(ft, token.id, 7)
		if got := stripANSI(ft.message); got != "token.id: got 7, want a different value" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestNilAndNotNil(t *testing.T) {
	t.Run("Nil passes on nil pointer", func(t *testing.T) {
		ft := &fakeT{}
		var p *int
		Nil(ft, p)
		if ft.failed {
			t.Errorf("Expected Nil to pass, got: %s", ft.message)
		}
	})

	t.Run("Nil names the expression", func(t *testing.T) {
		ft := &fakeT{}
		provider := struct{ err error }{err: errors.New("boom")}
		Nil(ft, provider.err)
		if got := stripANSI(ft.message); got != "provider.err: got boom, want nil" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("NotNil fails on nil", func(t *testing.T) {
		ft := &fakeT{}
		var p *int
		NotNil(ft, p)
		if got := stripANSI(ft.message); got != "p: got nil, want non-nil" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestEmptyAndNotEmpty(t *testing.T) {
	t.Run("Empty passes on zero values", func(t *testing.T) {
		ft := &fakeT{}
		Empty(ft, "")
		Empty(ft, []int(nil))
		Empty(ft, 0)
		if ft.failed {
			t.Errorf("Expected Empty to pass, got: %s", ft.message)
		}
	})

	t.Run("Empty names the expression", func(t *testing.T) {
		ft := &fakeT{}
		rooms := []string{"deluxe"}
		Empty(ft, rooms)
		if got := stripANSI(ft.message); got != "rooms: got [deluxe], want empty" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("NotEmpty fails on zero value", func(t *testing.T) {
		ft := &fakeT{}
		name := ""
		NotEmpty(ft, name)
		if got := stripANSI(ft.message); got != "name: got empty, want non-empty" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestLen(t *testing.T) {
	t.Run("passes on matching length", func(t *testing.T) {
		ft := &fakeT{}
		Len(ft, []int{1, 2}, 2)
		if ft.failed {
			t.Errorf("Expected Len to pass, got: %s", ft.message)
		}
	})

	t.Run("names the expression on mismatch", func(t *testing.T) {
		ft := &fakeT{}
		rooms := []string{"a", "b", "c"}
		Len(ft, rooms, 2)
		if got := stripANSI(ft.message); got != "rooms: got len 3, want 2" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("reports a non-measurable type", func(t *testing.T) {
		ft := &fakeT{}
		Len(ft, 42, 1)
		if got := stripANSI(ft.message); got != "got int, want a value with a length" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestContainsAndNotContains(t *testing.T) {
	t.Run("string substring", func(t *testing.T) {
		ft := &fakeT{}
		Contains(ft, "hello world", "world")
		if ft.failed {
			t.Errorf("Expected Contains to pass, got: %s", ft.message)
		}
	})

	t.Run("slice member missing names expression", func(t *testing.T) {
		ft := &fakeT{}
		rooms := []string{"deluxe", "queen"}
		Contains(ft, rooms, "suite")
		if got := stripANSI(ft.message); got != `rooms: does not contain "suite"` {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("map key present", func(t *testing.T) {
		ft := &fakeT{}
		prices := map[string]int{"deluxe": 150}
		Contains(ft, prices, "deluxe")
		if ft.failed {
			t.Errorf("Expected Contains to pass, got: %s", ft.message)
		}
	})

	t.Run("NotContains fails when present", func(t *testing.T) {
		ft := &fakeT{}
		rooms := []string{"deluxe", "suite"}
		NotContains(ft, rooms, "suite")
		if got := stripANSI(ft.message); got != `rooms: contains "suite", want it absent` {
			t.Errorf("got: %s", got)
		}
	})
}

var errInsufficientFunds = errors.New("insufficient funds")

func chargeFailing() error { return fmt.Errorf("charge: %w", errInsufficientFunds) }

func TestErrorIs(t *testing.T) {
	t.Run("passes when error matches", func(t *testing.T) {
		ft := &fakeT{}
		ErrorIs(ft, chargeFailing(), errInsufficientFunds)
		if ft.failed {
			t.Errorf("Expected ErrorIs to pass, got: %s", ft.message)
		}
	})

	t.Run("names the producing call on mismatch", func(t *testing.T) {
		ft := &fakeT{}
		err := chargeFailing()
		ErrorIs(ft, err, errors.New("other"))
		got := stripANSI(ft.message)
		if got != "chargeFailing(): got charge: insufficient funds, want it to match other" {
			t.Errorf("got: %s", got)
		}
	})
}

type declinedError struct{ code int }

func (e *declinedError) Error() string { return fmt.Sprintf("declined %d", e.code) }

func TestErrorAs(t *testing.T) {
	t.Run("passes when type matches", func(t *testing.T) {
		ft := &fakeT{}
		var target *declinedError
		ErrorAs(ft, &declinedError{code: 51}, &target)
		if ft.failed {
			t.Errorf("Expected ErrorAs to pass, got: %s", ft.message)
		}
	})

	t.Run("fails on type mismatch", func(t *testing.T) {
		ft := &fakeT{}
		var target *declinedError
		ErrorAs(ft, errors.New("plain"), &target)
		if !ft.failed {
			t.Error("Expected ErrorAs to fail")
		}
	})
}

func TestErrorContains(t *testing.T) {
	t.Run("passes when message contains substr", func(t *testing.T) {
		ft := &fakeT{}
		ErrorContains(ft, errors.New("charge declined"), "declined")
		if ft.failed {
			t.Errorf("Expected ErrorContains to pass, got: %s", ft.message)
		}
	})

	t.Run("fails when missing", func(t *testing.T) {
		ft := &fakeT{}
		ErrorContains(ft, errors.New("charge declined"), "timeout")
		if !ft.failed {
			t.Error("Expected ErrorContains to fail")
		}
	})
}

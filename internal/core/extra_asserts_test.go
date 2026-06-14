package core

import (
	"errors"
	"fmt"
	"regexp"
	"testing"
)

func TestEqualStrictType(t *testing.T) {
	t.Run("fails when dynamic types differ", func(t *testing.T) {
		ft := &fakeT{}
		var amount any = int32(5)
		Equal(ft, amount, any(int64(5)))
		if !ft.failed {
			t.Fatal("Expected Equal to fail on differing types")
		}
		if msg := stripANSI(ft.message); msg != "amount: got type int32, want int64" {
			t.Errorf("got: %s", msg)
		}
	})

	t.Run("passes when types and values match", func(t *testing.T) {
		ft := &fakeT{}
		var amount any = int64(5)
		Equal(ft, amount, any(int64(5)))
		if ft.failed {
			t.Errorf("Expected Equal to pass, got: %s", ft.message)
		}
	})
}

func TestSoftEqual(t *testing.T) {
	t.Run("passes across convertible numeric types", func(t *testing.T) {
		ft := &fakeT{}
		SoftEqual(ft, int32(5), int64(5))
		SoftEqual(ft, 5, 5.0)
		if ft.failed {
			t.Errorf("Expected SoftEqual to pass, got: %s", ft.message)
		}
	})

	t.Run("fails and names the expression on different values", func(t *testing.T) {
		ft := &fakeT{}
		count := int32(5)
		SoftEqual(ft, count, int64(6))
		if got := stripANSI(ft.message); got != "count: got 5, want 6" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestZeroAndNotZero(t *testing.T) {
	t.Run("Zero passes on zero values", func(t *testing.T) {
		ft := &fakeT{}
		Zero(ft, 0)
		Zero(ft, "")
		Zero(ft, (*int)(nil))
		if ft.failed {
			t.Errorf("Expected Zero to pass, got: %s", ft.message)
		}
	})

	t.Run("Zero names the expression on non-zero", func(t *testing.T) {
		ft := &fakeT{}
		pax := 3
		Zero(ft, pax)
		if got := stripANSI(ft.message); got != "pax: got 3, want the zero value" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("NotZero fails on zero", func(t *testing.T) {
		ft := &fakeT{}
		name := ""
		NotZero(ft, name)
		if got := stripANSI(ft.message); got != "name: got the zero value, want a non-zero value" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestRegexpAndNotRegexp(t *testing.T) {
	t.Run("Regexp passes with string pattern", func(t *testing.T) {
		ft := &fakeT{}
		Regexp(ft, "^del", "deluxe")
		if ft.failed {
			t.Errorf("Expected Regexp to pass, got: %s", ft.message)
		}
	})

	t.Run("Regexp passes with compiled pattern", func(t *testing.T) {
		ft := &fakeT{}
		Regexp(ft, regexp.MustCompile(`\d+`), "room 42")
		if ft.failed {
			t.Errorf("Expected Regexp to pass, got: %s", ft.message)
		}
	})

	t.Run("Regexp fails on no match", func(t *testing.T) {
		ft := &fakeT{}
		Regexp(ft, "^suite", "deluxe")
		if !ft.failed {
			t.Error("Expected Regexp to fail")
		}
	})

	t.Run("NotRegexp fails on match", func(t *testing.T) {
		ft := &fakeT{}
		NotRegexp(ft, "^del", "deluxe")
		if !ft.failed {
			t.Error("Expected NotRegexp to fail")
		}
	})
}

func TestSubsetAndNotSubset(t *testing.T) {
	t.Run("Subset passes on slice subset", func(t *testing.T) {
		ft := &fakeT{}
		Subset(ft, []string{"deluxe", "suite", "queen"}, []string{"suite", "queen"})
		if ft.failed {
			t.Errorf("Expected Subset to pass, got: %s", ft.message)
		}
	})

	t.Run("Subset passes on map subset", func(t *testing.T) {
		ft := &fakeT{}
		Subset(ft, map[string]int{"deluxe": 150, "suite": 300}, map[string]int{"suite": 300})
		if ft.failed {
			t.Errorf("Expected Subset to pass, got: %s", ft.message)
		}
	})

	t.Run("Subset reports missing elements", func(t *testing.T) {
		ft := &fakeT{}
		rooms := []string{"deluxe"}
		Subset(ft, rooms, []string{"deluxe", "suite"})
		if got := stripANSI(ft.message); got != "rooms: missing [suite]" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("NotSubset fails when subset is contained", func(t *testing.T) {
		ft := &fakeT{}
		NotSubset(ft, []int{1, 2, 3}, []int{1, 2})
		if !ft.failed {
			t.Error("Expected NotSubset to fail")
		}
	})
}

func TestIsType(t *testing.T) {
	t.Run("passes when types match", func(t *testing.T) {
		ft := &fakeT{}
		IsType(ft, 0, 42)
		if ft.failed {
			t.Errorf("Expected IsType to pass, got: %s", ft.message)
		}
	})

	t.Run("fails when types differ", func(t *testing.T) {
		ft := &fakeT{}
		IsType(ft, "", 42)
		if got := stripANSI(ft.message); got != "got type int, want string" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestEqualError(t *testing.T) {
	t.Run("passes on exact message", func(t *testing.T) {
		ft := &fakeT{}
		EqualError(ft, errors.New("boom"), "boom")
		if ft.failed {
			t.Errorf("Expected EqualError to pass, got: %s", ft.message)
		}
	})

	t.Run("fails on different message", func(t *testing.T) {
		ft := &fakeT{}
		EqualError(ft, errors.New("boom"), "bang")
		if !ft.failed {
			t.Error("Expected EqualError to fail")
		}
	})

	t.Run("fails on nil error", func(t *testing.T) {
		ft := &fakeT{}
		EqualError(ft, nil, "boom")
		if !ft.failed {
			t.Error("Expected EqualError to fail on nil")
		}
	})
}

func TestPanicsWithError(t *testing.T) {
	t.Run("passes when panicking with matching error", func(t *testing.T) {
		ft := &fakeT{}
		PanicsWithError(ft, "boom", func() { panic(errors.New("boom")) })
		if ft.failed {
			t.Errorf("Expected PanicsWithError to pass, got: %s", ft.message)
		}
	})

	t.Run("fails on non-error panic", func(t *testing.T) {
		ft := &fakeT{}
		PanicsWithError(ft, "boom", func() { panic("boom") })
		if !ft.failed {
			t.Error("Expected PanicsWithError to fail on non-error panic")
		}
	})

	t.Run("fails when not panicking", func(t *testing.T) {
		ft := &fakeT{}
		PanicsWithError(ft, "boom", func() {})
		if !ft.failed {
			t.Error("Expected PanicsWithError to fail when not panicking")
		}
	})
}

func TestNotErrorIsAndNotErrorAs(t *testing.T) {
	sentinel := errors.New("sentinel")

	t.Run("NotErrorIs passes when error does not match", func(t *testing.T) {
		ft := &fakeT{}
		NotErrorIs(ft, errors.New("other"), sentinel)
		if ft.failed {
			t.Errorf("Expected NotErrorIs to pass, got: %s", ft.message)
		}
	})

	t.Run("NotErrorIs fails when error matches", func(t *testing.T) {
		ft := &fakeT{}
		NotErrorIs(ft, fmt.Errorf("wrap: %w", sentinel), sentinel)
		if !ft.failed {
			t.Error("Expected NotErrorIs to fail")
		}
	})

	t.Run("NotErrorAs fails when type matches", func(t *testing.T) {
		ft := &fakeT{}
		var target *declinedError
		NotErrorAs(ft, &declinedError{code: 51}, &target)
		if !ft.failed {
			t.Error("Expected NotErrorAs to fail")
		}
	})
}

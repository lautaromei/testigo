package core

import "testing"

func TestGreaterAndLess(t *testing.T) {
	t.Run("Greater passes", func(t *testing.T) {
		ft := &fakeT{}
		Greater(ft, 5, 3)
		if ft.failed {
			t.Errorf("Expected Greater to pass, got: %s", ft.message)
		}
	})

	t.Run("Greater names the expression", func(t *testing.T) {
		ft := &fakeT{}
		balance := 30
		Greater(ft, balance, 50)
		if got := stripANSI(ft.message); got != "balance: got 30, want > 50" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("GreaterOrEqual boundary", func(t *testing.T) {
		ft := &fakeT{}
		GreaterOrEqual(ft, 5, 5)
		if ft.failed {
			t.Errorf("Expected GreaterOrEqual to pass at boundary, got: %s", ft.message)
		}
	})

	t.Run("Less names the expression", func(t *testing.T) {
		ft := &fakeT{}
		age := 40
		Less(ft, age, 18)
		if got := stripANSI(ft.message); got != "age: got 40, want < 18" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("LessOrEqual fails", func(t *testing.T) {
		ft := &fakeT{}
		LessOrEqual(ft, 9, 8)
		if got := stripANSI(ft.message); got != "got 9, want <= 8" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestPositiveAndNegative(t *testing.T) {
	t.Run("Positive passes", func(t *testing.T) {
		ft := &fakeT{}
		Positive(ft, 3)
		if ft.failed {
			t.Errorf("Expected Positive to pass, got: %s", ft.message)
		}
	})

	t.Run("Positive names the expression", func(t *testing.T) {
		ft := &fakeT{}
		delta := -2
		Positive(ft, delta)
		if got := stripANSI(ft.message); got != "delta: got -2, want > 0" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("Negative fails on positive", func(t *testing.T) {
		ft := &fakeT{}
		Negative(ft, 5)
		if got := stripANSI(ft.message); got != "got 5, want < 0" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestInDelta(t *testing.T) {
	t.Run("within tolerance passes", func(t *testing.T) {
		ft := &fakeT{}
		InDelta(ft, 1.02, 1.0, 0.05)
		if ft.failed {
			t.Errorf("Expected InDelta to pass, got: %s", ft.message)
		}
	})

	t.Run("outside tolerance names the expression", func(t *testing.T) {
		ft := &fakeT{}
		rate := 1.2
		InDelta(ft, rate, 1.0, 0.05)
		if got := stripANSI(ft.message); got != "rate: got 1.2, want 1 ± 0.05" {
			t.Errorf("got: %s", got)
		}
	})
}

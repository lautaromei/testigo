package core

import (
	"testing"
	"time"
)

func TestPanics(t *testing.T) {
	t.Run("passes when fn panics", func(t *testing.T) {
		ft := &fakeT{}
		Panics(ft, func() { panic("boom") })
		if ft.failed {
			t.Errorf("Expected Panics to pass, got: %s", ft.message)
		}
	})

	t.Run("fails when fn does not panic", func(t *testing.T) {
		ft := &fakeT{}
		Panics(ft, func() {})
		if got := stripANSI(ft.message); got != "did not panic" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("NotPanics reports the recovered value", func(t *testing.T) {
		ft := &fakeT{}
		NotPanics(ft, func() { panic("boom") })
		if got := stripANSI(ft.message); got != "panicked with boom" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("PanicsWith matches the value", func(t *testing.T) {
		ft := &fakeT{}
		PanicsWith(ft, "boom", func() { panic("boom") })
		if ft.failed {
			t.Errorf("Expected PanicsWith to pass, got: %s", ft.message)
		}
	})

	t.Run("PanicsWith reports a mismatch", func(t *testing.T) {
		ft := &fakeT{}
		PanicsWith(ft, "boom", func() { panic("bang") })
		if got := stripANSI(ft.message); got != "got bang, want boom" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestSameAndNotSame(t *testing.T) {
	t.Run("Same passes for same pointer", func(t *testing.T) {
		ft := &fakeT{}
		p := &struct{ x int }{}
		Same(ft, p, p)
		if ft.failed {
			t.Errorf("Expected Same to pass, got: %s", ft.message)
		}
	})

	t.Run("Same fails for distinct pointers", func(t *testing.T) {
		ft := &fakeT{}
		a := &struct{ x int }{}
		b := &struct{ x int }{}
		Same(ft, a, b)
		if got := stripANSI(ft.message); got != "b: points to a different object than expected" {
			t.Errorf("got: %s", got)
		}
	})

	t.Run("NotSame fails for same pointer", func(t *testing.T) {
		ft := &fakeT{}
		p := &struct{ x int }{}
		NotSame(ft, p, p)
		if got := stripANSI(ft.message); got != "p: points to the same object, want a different one" {
			t.Errorf("got: %s", got)
		}
	})
}

func TestElementsMatch(t *testing.T) {
	t.Run("passes regardless of order", func(t *testing.T) {
		ft := &fakeT{}
		ElementsMatch(ft, []int{1, 2, 3}, []int{3, 1, 2})
		if ft.failed {
			t.Errorf("Expected ElementsMatch to pass, got: %s", ft.message)
		}
	})

	t.Run("reports missing and extra", func(t *testing.T) {
		ft := &fakeT{}
		got := []string{"deluxe", "queen"}
		ElementsMatch(ft, got, []string{"deluxe", "suite"})
		if msg := stripANSI(ft.message); msg != "got: missing [suite], extra [queen]" {
			t.Errorf("got: %s", msg)
		}
	})
}

func TestEventuallyAndNever(t *testing.T) {
	t.Run("Eventually passes when condition turns true", func(t *testing.T) {
		ft := &fakeT{}
		calls := 0
		Eventually(ft, func() bool { calls++; return calls >= 3 }, 200*time.Millisecond, time.Millisecond)
		if ft.failed {
			t.Errorf("Expected Eventually to pass, got: %s", ft.message)
		}
	})

	t.Run("Eventually fails when condition stays false", func(t *testing.T) {
		ft := &fakeT{}
		Eventually(ft, func() bool { return false }, 20*time.Millisecond, time.Millisecond)
		if !ft.failed {
			t.Error("Expected Eventually to fail")
		}
	})

	t.Run("Never fails when condition turns true", func(t *testing.T) {
		ft := &fakeT{}
		Never(ft, func() bool { return true }, 20*time.Millisecond, time.Millisecond)
		if !ft.failed {
			t.Error("Expected Never to fail")
		}
	})
}

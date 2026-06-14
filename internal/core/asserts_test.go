package core

import (
	"errors"
	"strings"
	"testing"
)

func TestEqual(t *testing.T) {
	t.Run("passes on equal values", func(t *testing.T) {
		ft := &fakeT{}
		Equal(ft, 42, 42)
		Equal(ft, "hello", "hello")
		Equal(ft, []int{1, 2}, []int{1, 2})

		if ft.failed {
			t.Errorf("Expected Equal to pass, but it failed with: %s", ft.message)
		}
	})

	t.Run("fails with got/want on different values", func(t *testing.T) {
		ft := &fakeT{}
		Equal(ft, 1, 2)

		if !ft.failed {
			t.Error("Expected Equal to fail, but it passed")
		}
		if got := stripANSI(ft.message); got != "got 1, want 2" {
			t.Errorf("Expected 'got 1, want 2', got: %s", got)
		}
	})

	t.Run("names the compared expression", func(t *testing.T) {
		ft := &fakeT{}
		payment := struct{ id int }{id: 1}
		Equal(ft, payment.id, 2)

		message := stripANSI(ft.message)
		if message != "payment.id: got 1, want 2" {
			t.Errorf("Expected the expression as label, got: %s", message)
		}
	})

	t.Run("points at the differing field of a struct", func(t *testing.T) {
		type room struct{ name string }
		type booking struct {
			pax  int
			room room
		}

		ft := &fakeT{}
		got := booking{pax: 1, room: room{name: "Suite"}}
		want := booking{pax: 1, room: room{name: "Deluxe"}}
		Equal(ft, got, want)

		message := stripANSI(ft.message)
		if message != `got.room.name: got "Suite", want "Deluxe"` {
			t.Errorf("Expected field path in failure, got: %s", message)
		}
	})

	t.Run("lists every differing field", func(t *testing.T) {
		type booking struct {
			pax     int
			checkin string
		}

		ft := &fakeT{}
		Equal(ft, booking{1, "2026-03-14"}, booking{2, "2026-03-15"})

		message := stripANSI(ft.message)
		if !strings.Contains(message, ".pax: got 1, want 2") {
			t.Errorf("Expected .pax diff, got: %s", message)
		}
		if !strings.Contains(message, `.checkin: got "2026-03-14", want "2026-03-15"`) {
			t.Errorf("Expected .checkin diff, got: %s", message)
		}
	})

	t.Run("reports slice length mismatches", func(t *testing.T) {
		ft := &fakeT{}
		rooms := []string{"deluxe"}
		Equal(ft, rooms, []string{"deluxe", "suite"})

		message := stripANSI(ft.message)
		if message != "rooms.len: got 1, want 2" {
			t.Errorf("Expected length diff, got: %s", message)
		}
	})

	t.Run("points at the differing slice element", func(t *testing.T) {
		ft := &fakeT{}
		rooms := []string{"deluxe", "queen"}
		Equal(ft, rooms, []string{"deluxe", "suite"})

		message := stripANSI(ft.message)
		if message != `rooms[1]: got "queen", want "suite"` {
			t.Errorf("Expected element diff, got: %s", message)
		}
	})

	t.Run("reports missing map keys", func(t *testing.T) {
		ft := &fakeT{}
		prices := map[string]int{"deluxe": 150}
		Equal(ft, prices, map[string]int{"deluxe": 150, "suite": 300})

		message := stripANSI(ft.message)
		if message != "prices[suite]: got <missing>, want 300" {
			t.Errorf("Expected missing key diff, got: %s", message)
		}
	})
}

func failingOp() (int, error) { return 0, errors.New("boom") }

func workingOp() (int, error) { return 7, nil }

func TestNoError(t *testing.T) {
	t.Run("passes on nil", func(t *testing.T) {
		ft := &fakeT{}
		NoError(ft, nil)
		if ft.failed {
			t.Errorf("Expected NoError to pass on nil, but it failed with: %s", ft.message)
		}
	})

	t.Run("names the call that produced the error", func(t *testing.T) {
		ft := &fakeT{}
		_, err := failingOp()
		NoError(ft, err)

		message := stripANSI(ft.message)
		if message != "failingOp(): expected no error, got boom" {
			t.Errorf("Expected the failing call in the message, got: %s", message)
		}
	})

	t.Run("names inline calls directly", func(t *testing.T) {
		ft := &fakeT{}
		NoError(ft, errors.New("boom"))

		message := stripANSI(ft.message)
		if !strings.Contains(message, `errors.New("boom"): expected no error, got boom`) {
			t.Errorf("Expected the inline call in the message, got: %s", message)
		}
	})
}

func TestError(t *testing.T) {
	t.Run("passes on non-nil error", func(t *testing.T) {
		ft := &fakeT{}
		Error(ft, errors.New("boom"))
		if ft.failed {
			t.Errorf("Expected Error to pass on non-nil error, but it failed with: %s", ft.message)
		}
	})

	t.Run("names the call that should have failed", func(t *testing.T) {
		ft := &fakeT{}
		_, err := workingOp()
		Error(ft, err)

		message := stripANSI(ft.message)
		if message != "workingOp(): expected an error, got nil" {
			t.Errorf("Expected the call in the message, got: %s", message)
		}
	})
}

func TestTrueAndFalse(t *testing.T) {
	ft := &fakeT{}
	True(ft, true, "should not fail")
	False(ft, false, "should not fail")
	if ft.failed {
		t.Errorf("Expected True/False to pass, but failed with: %s", ft.message)
	}

	True(ft, false, "room %s should be available", "deluxe")
	if !ft.failed || ft.message != "room deluxe should be available" {
		t.Errorf("Expected formatted failure message, got: %s", ft.message)
	}

	ft = &fakeT{}
	False(ft, true, "room should not be available")
	if !ft.failed {
		t.Error("Expected False to fail on true condition")
	}
}

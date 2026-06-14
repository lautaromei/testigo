package core

import (
	"strings"
	"testing"
)

// hotelStub exercises change detection across an embedded Spy, a nested struct
// by value and a plain unexported field.
type hotelStub struct {
	Spy
	room struct {
		name string
		beds int
	}
	balance int
}

func (h *hotelStub) charge() { h.Call() }

// failingChange binds ft to the goroutine, runs the chain and returns the
// reported failure, mimicking a chain inside a testigo.Run subtest.
func failingChange(ft *fakeT, chain func()) string {
	defer bindGoroutine(ft)()
	chain()
	return ft.message
}

func TestDidChange(t *testing.T) {
	t.Run("passes when a field changed", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{balance: 100})
		stub.balance = 150

		That(stub).DidChange()
	})

	t.Run("passes when a nested field changed", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{})
		stub.room.name = "Suite"

		That(stub).DidChange()
	})

	t.Run("fails when nothing changed", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{balance: 100})

		msg := failingChange(&fakeT{}, func() {
			That(stub).DidChange()
		})

		if !strings.Contains(msg, "to change") || !strings.Contains(msg, "hotelStub") {
			t.Errorf("Expected a 'to change' failure naming hotelStub, got: %v", msg)
		}
	})

	t.Run("ignores recorded calls — a call is not a state change", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{balance: 100})
		stub.charge() // records a call on the embedded Spy, but mutates no field

		msg := failingChange(&fakeT{}, func() {
			That(stub).DidChange()
		})

		if !strings.Contains(msg, "to change") {
			t.Errorf("Expected DidChange to fail when only a call was recorded, got: %v", msg)
		}
	})

	t.Run("reports on the subtest bound by Run", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{balance: 100})

		ft := &fakeT{}
		failingChange(ft, func() {
			That(stub).DidChange()
		})

		if !ft.failed {
			t.Error("Expected the failure on the bound test")
		}
	})
}

func TestDidntChange(t *testing.T) {
	t.Run("passes when no field changed", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{balance: 100})

		That(stub).DidNotChange()
	})

	t.Run("passes when only a call was recorded", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{balance: 100})
		stub.charge()

		That(stub).DidNotChange()
	})

	t.Run("fails when a field changed", func(t *testing.T) {
		stub := NewDouble(t, &hotelStub{balance: 100})
		stub.balance = 150

		msg := failingChange(&fakeT{}, func() {
			That(stub).DidNotChange()
		})

		if !strings.Contains(msg, "stay unchanged") {
			t.Errorf("Expected a 'stay unchanged' failure, got: %v", msg)
		}
	})

	t.Run("panics when That did not name a registered double", func(t *testing.T) {
		orphan := &hotelStub{}

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("Expected DidNotChange to panic for an unregistered double")
			}
			if !strings.Contains(r.(string), "NewDouble") {
				t.Errorf("Expected a registration panic, got: %v", r)
			}
		}()
		That(orphan).DidNotChange()
	})
}

func TestDidChange_PlainVariables(t *testing.T) {
	t.Run("tracks a registered int variable", func(t *testing.T) {
		total := 5
		NewDouble(t, &total)
		total = 9

		That(&total).DidChange()
	})

	t.Run("tracks strings and slices", func(t *testing.T) {
		status := "pending"
		rooms := []string{"single"}
		NewDouble(t, &status)
		NewDouble(t, &rooms)

		status = "confirmed"
		rooms = append(rooms, "suite")

		That(&status).DidChange()
		That(&rooms).DidChange()
	})

	t.Run("restores plain variables between subtests", func(t *testing.T) {
		total := 5
		NewDouble(t, &total)

		Run(t, "mutates the variable", func(t *testing.T) {
			total = 9
			That(&total).DidChange()
		})

		Run(t, "sees it restored", func(t *testing.T) {
			if total != 5 {
				t.Errorf("Expected total restored to 5, got %d", total)
			}
			That(&total).DidNotChange()
		})
	})
}

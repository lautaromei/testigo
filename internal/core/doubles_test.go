package core

import (
	"strings"
	"testing"
)

// valueSpyDouble embeds Spy by value, like a typical test double.
type valueSpyDouble struct {
	Spy
	balance int
}

func (v *valueSpyDouble) Charge(amount int) {
	v.Call(amount)
	v.balance += amount
}

func TestDoubles_RestoresStateBetweenSubtests(t *testing.T) {
	double := NewDouble(t, &valueSpyDouble{balance: 100})

	Run(t, "first subtest mutates the double", func(t *testing.T) {
		double.Charge(50)

		if double.balance != 150 {
			t.Errorf("Expected balance 150, got %d", double.balance)
		}
		Expect(t).Called(double.Charge).WithParams(50).Once()
		Equal(t, double.balance, 150)
	})

	Run(t, "second subtest sees the initial state", func(t *testing.T) {
		if double.balance != 100 {
			t.Errorf("Expected balance restored to 100, got %d", double.balance)
		}
		if double.TotalCalls() != 0 {
			t.Errorf("Expected 0 recorded calls after reset, got %d", double.TotalCalls())
		}
	})
}

func TestDoubles_ClearsPointerSpies(t *testing.T) {
	subject := NewDouble(t, &TestSubject{spy: NewSpy()})

	Run(t, "records calls in the first subtest", func(t *testing.T) {
		subject.DoSomething("a", 1)
		Expect(t).Called(subject.DoSomething).WithParams("a", 1).Once()
		Equal(t, subject.spy.TotalCalls(), 1)
	})

	Run(t, "the pointer spy starts clean in the next subtest", func(t *testing.T) {
		if subject.spy.TotalCalls() != 0 {
			t.Errorf("Expected pointer spy cleared between subtests, got %d calls", subject.spy.TotalCalls())
		}
	})
}

func TestDoubles_Reset(t *testing.T) {
	double := NewDouble(t, &valueSpyDouble{balance: 10})

	double.Charge(5)
	Reset(t)

	if double.balance != 10 {
		t.Errorf("Expected balance restored to 10, got %d", double.balance)
	}
	if double.TotalCalls() != 0 {
		t.Errorf("Expected 0 recorded calls after Reset, got %d", double.TotalCalls())
	}
}

func TestDoubles_RegisterSeveralAtOnce(t *testing.T) {
	first := NewDouble(t, &valueSpyDouble{balance: 1})
	second := NewDouble(t, &TestSubject{spy: NewSpy()})

	Run(t, "mutates both doubles", func(t *testing.T) {
		first.Charge(99)
		second.DoSomething("x", 1)
		Expect(t).Called(first.Charge).WithParams(99).Once()
		Expect(t).Called(second.DoSomething).WithParams("x", 1).Once()
		Equal(t, first.balance, 100)
	})

	Run(t, "both doubles are restored", func(t *testing.T) {
		if first.balance != 1 {
			t.Errorf("Expected balance restored to 1, got %d", first.balance)
		}
		if first.TotalCalls() != 0 || second.spy.TotalCalls() != 0 {
			t.Errorf("Expected both spies cleared, got %d and %d calls", first.TotalCalls(), second.spy.TotalCalls())
		}
	})
}

// externalDouble stands in for a double backed by state a struct copy cannot
// reach (a database, a temp dir). It owns no Spy and resets itself through
// Reset, which is what marks it external to NewDouble.
type externalDouble struct {
	state  int
	resets int
}

func (e *externalDouble) Reset() {
	e.resets++
	e.state = 7 // re-seed the baseline, the way a DB reset truncates and re-seeds
}

func TestResetter_RunsResetBeforeEverySubtest(t *testing.T) {
	double := NewDouble(t, &externalDouble{})

	Run(t, "first subtest gets a reset before it runs", func(t *testing.T) {
		if double.resets != 1 {
			t.Errorf("Expected Reset to run once before the first subtest, got %d", double.resets)
		}
		if double.state != 7 {
			t.Errorf("Expected Reset to re-seed state to 7, got %d", double.state)
		}
		double.state = 99
	})

	Run(t, "second subtest gets another reset", func(t *testing.T) {
		if double.resets != 2 {
			t.Errorf("Expected Reset to run again before the second subtest, got %d", double.resets)
		}
		if double.state != 7 {
			t.Errorf("Expected state re-seeded to 7, got %d", double.state)
		}
	})
}

func TestResetter_RunsOnExplicitReset(t *testing.T) {
	double := NewDouble(t, &externalDouble{})

	Reset(t)
	Reset(t)

	if double.resets != 2 {
		t.Errorf("Expected Reset to run on every Reset, got %d", double.resets)
	}
}

// wrapperResettable is an external double (custom Reset, no Spy) holding a
// pointer that stands in for a live handle which must survive resets unchanged.
type wrapperResettable struct {
	handle *externalDouble
}

func (w *wrapperResettable) Reset() {}

// TestResetter_ExternalFieldsAreNotCopied checks that an external double's
// fields are left to Reset alone — they are never deep copied, so a double
// wrapping a live handle keeps that exact handle across resets.
func TestResetter_ExternalFieldsAreNotCopied(t *testing.T) {
	handle := &externalDouble{}
	w := NewDouble(t, &wrapperResettable{handle: handle})

	Reset(t)

	if w.handle != handle {
		t.Error("Expected the external double's handle to be kept by identity, not copied")
	}
}

func TestDouble_ReturnsTheSamePointer(t *testing.T) {
	original := &valueSpyDouble{}

	if NewDouble(t, original) != original {
		t.Error("Expected NewDouble to return the same pointer it was given")
	}
}

// forgetfulSpy embeds Spy but its charge method forgets to call Call.
type forgetfulSpy struct {
	Spy
}

func (f *forgetfulSpy) charge(amount int) {} // Missing Call on purpose.

func (f *forgetfulSpy) refund(amount int) {
	f.Call(amount)
}

// unwatchedSpy also forgets Call, but is never registered with NewDouble.
type unwatchedSpy struct {
	Spy
}

func (u *unwatchedSpy) charge(amount int) {}

func TestDoubles_HintsAboutMissingSpyCall(t *testing.T) {
	t.Run("hints when a registered spy recorded nothing", func(t *testing.T) {
		spy := NewDouble(t, &forgetfulSpy{})
		spy.charge(10)

		_, err := assertCalls(false, expectTimes(1, spy.charge, 10))

		if err == nil {
			t.Fatal("Expected the charge expectation to fail")
		}
		if !strings.Contains(err.Error(), "hint:") {
			t.Errorf("Expected a Call hint, got: %v", err)
		}
	})

	t.Run("no hint when the spy recorded calls from other methods", func(t *testing.T) {
		spy := NewDouble(t, &forgetfulSpy{})
		spy.charge(10)
		spy.refund(5)

		_, err := assertCalls(false, expectTimes(1, spy.charge, 10), expectTimes(1, spy.refund, 5))

		if err == nil {
			t.Fatal("Expected the charge expectation to fail")
		}
		if strings.Contains(err.Error(), "hint:") {
			t.Errorf("Expected no Call hint, got: %v", err)
		}
	})

	t.Run("hints to register a double when the component is unknown", func(t *testing.T) {
		spy := &unwatchedSpy{}
		spy.charge(10)

		_, err := assertCalls(false, expectTimes(1, spy.charge, 10))

		if err == nil {
			t.Fatal("Expected the charge expectation to fail")
		}
		if !strings.Contains(err.Error(), "is not a registered testigo double") {
			t.Errorf("Expected a hint about registering the double, got: %v", err)
		}
	})
}

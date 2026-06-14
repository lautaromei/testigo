package core

import (
	"strings"
	"testing"
	"time"
)

// TestOrdering exercises Before/After/Within against calls recorded across the
// global sequence and wall-clock stamps, in both the passing and failing paths.
func TestOrdering(t *testing.T) {
	t.Run("Before passes when a precedes b", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.DoSomething("x", 1) // a
		s.AnotherMethod()     // b

		ok, err := assertOrder(newExpectation(s.DoSomething), s.AnotherMethod, orderBefore, 0)
		if !ok {
			t.Fatalf("expected Before to pass, got: %v", err)
		}
	})

	t.Run("Before fails when a follows b", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.AnotherMethod()     // b first
		s.DoSomething("x", 1) // a after

		ok, err := assertOrder(newExpectation(s.DoSomething), s.AnotherMethod, orderBefore, 0)
		if ok || err == nil || !strings.Contains(err.Error(), "to be called before") {
			t.Fatalf("expected an out-of-order failure, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("Before fails when the other call never happened", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.DoSomething("x", 1)

		ok, err := assertOrder(newExpectation(s.DoSomething), s.AnotherMethod, orderBefore, 0)
		if ok || err == nil || !strings.Contains(err.Error(), "never called") {
			t.Fatalf("expected a never-called failure, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("After passes when a follows b", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.AnotherMethod()     // b
		s.DoSomething("x", 1) // a

		ok, err := assertOrder(newExpectation(s.DoSomething), s.AnotherMethod, orderAfter, 0)
		if !ok {
			t.Fatalf("expected After to pass, got: %v", err)
		}
	})

	t.Run("Within passes inside a generous window", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.DoSomething("x", 1)
		s.AnotherMethod()

		ok, err := assertOrder(newExpectation(s.DoSomething), s.AnotherMethod, orderWithin, time.Hour)
		if !ok {
			t.Fatalf("expected Within to pass, got: %v", err)
		}
	})

	t.Run("Within fails when the gap exceeds the window", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.DoSomething("x", 1)
		s.AnotherMethod()

		// A negative window can never contain the (non-negative) gap, so this
		// fails deterministically without depending on real elapsed time.
		ok, err := assertOrder(newExpectation(s.DoSomething), s.AnotherMethod, orderWithin, -1)
		if ok || err == nil || !strings.Contains(err.Error(), "within") {
			t.Fatalf("expected a window failure, got ok=%v err=%v", ok, err)
		}
	})
}

// TestChanged exercises Changed(&field).From(...).To(...), naming the field by
// reference, in both the passing and failing paths.
func TestChanged(t *testing.T) {
	t.Run("passes and pins From/To when the field moved", func(t *testing.T) {
		double := NewDouble(t, &valueSpyDouble{balance: 100})
		double.Charge(50) // balance 100 -> 150

		ft := &fakeT{}
		Expect(ft).That(double).Changed(&double.balance).From(100).To(150)
		if ft.failed {
			t.Fatalf("expected the Changed chain to pass, got: %s", ft.message)
		}
	})

	t.Run("fails when To does not match the current value", func(t *testing.T) {
		double := NewDouble(t, &valueSpyDouble{balance: 100})
		double.Charge(50)

		ft := &fakeT{}
		Expect(ft).That(double).Changed(&double.balance).To(999)
		if !ft.failed || !strings.Contains(ft.message, "to change to") {
			t.Fatalf("expected a To mismatch, got failed=%v msg=%s", ft.failed, ft.message)
		}
	})

	t.Run("fails when From does not match the initial value", func(t *testing.T) {
		double := NewDouble(t, &valueSpyDouble{balance: 100})
		double.Charge(50)

		ft := &fakeT{}
		Expect(ft).That(double).Changed(&double.balance).From(0)
		if !ft.failed || !strings.Contains(ft.message, "to change from") {
			t.Fatalf("expected a From mismatch, got failed=%v msg=%s", ft.failed, ft.message)
		}
	})

	t.Run("fails when the field never moved", func(t *testing.T) {
		double := NewDouble(t, &valueSpyDouble{balance: 100})
		double.Charge(0) // recorded, but balance unchanged

		ft := &fakeT{}
		Expect(ft).That(double).Changed(&double.balance)
		if !ft.failed || !strings.Contains(ft.message, "to change") {
			t.Fatalf("expected an unchanged failure, got failed=%v msg=%s", ft.failed, ft.message)
		}
	})

	t.Run("fails when the pointer is not a field of the double", func(t *testing.T) {
		double := NewDouble(t, &valueSpyDouble{balance: 100})

		var stray int
		ft := &fakeT{}
		Expect(ft).That(double).Changed(&stray)
		if !ft.failed || !strings.Contains(ft.message, "pointer to a field") {
			t.Fatalf("expected a bad-pointer failure, got failed=%v msg=%s", ft.failed, ft.message)
		}
	})
}

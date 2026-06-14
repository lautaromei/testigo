package core

import (
	"strings"
	"testing"
	"time"
)

// TestChainOrdering exercises the PendingCall.Before/After/Within wrappers
// through a full Expect chain, where TestOrdering only drives assertOrder
// directly.
func TestChainOrdering(t *testing.T) {
	t.Run("Before passes through the chain", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.DoSomething("x", 1)
		s.AnotherMethod()

		ft := &fakeT{}
		Expect(ft).Called(s.DoSomething).Before(s.AnotherMethod)
		if ft.failed {
			t.Fatalf("expected chain Before to pass, got: %s", ft.message)
		}
	})

	t.Run("After passes through the chain", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.AnotherMethod()
		s.DoSomething("x", 1)

		ft := &fakeT{}
		Expect(ft).Called(s.DoSomething).After(s.AnotherMethod)
		if ft.failed {
			t.Fatalf("expected chain After to pass, got: %s", ft.message)
		}
	})

	t.Run("Within passes through the chain", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.DoSomething("x", 1)
		s.AnotherMethod()

		ft := &fakeT{}
		Expect(ft).Called(s.DoSomething).Within(time.Hour, s.AnotherMethod)
		if ft.failed {
			t.Fatalf("expected chain Within to pass, got: %s", ft.message)
		}
	})

	t.Run("Before fails through the chain when out of order", func(t *testing.T) {
		defer isolateCurrentTestRegistries()()
		s := &TestSubject{spy: &Spy{}}
		s.AnotherMethod()
		s.DoSomething("x", 1)

		ft := &fakeT{}
		Expect(ft).Called(s.DoSomething).Before(s.AnotherMethod)
		if !ft.failed || !strings.Contains(ft.message, "to be called before") {
			t.Fatalf("expected chain Before to fail, got failed=%v msg=%s", ft.failed, ft.message)
		}
	})
}

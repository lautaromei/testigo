package core

import (
	"strings"
	"testing"
)

func TestShortChains_InsideRun(t *testing.T) {
	double := NewDouble(t, &valueSpyDouble{})

	Run(t, "Called resolves the subtest's t", func(t *testing.T) {
		double.Charge(7)

		Called(double.Charge).WithParams(7).Once()
		Equal(t, true, true)
	})

	Run(t, "That resolves it too", func(t *testing.T) {
		delegate := chargeDelegate{double: double}
		delegate.run(9)

		That(delegate).Called(double.Charge).WithParams(9).Once()
		Equal(t, true, true)
	})
}

// chargeDelegate stands in for a subject under test calling the double.
type chargeDelegate struct{ double *valueSpyDouble }

func (d chargeDelegate) run(n int) { d.double.Charge(n) }

func TestCalls_GroupsExpectationsUnderOneSubject(t *testing.T) {
	double := NewDouble(t, &valueSpyDouble{})
	delegate := chargeDelegate{double: double}

	Run(t, "groups calls with a bound Called", func(t *testing.T) {
		delegate.run(7)
		delegate.run(9)

		That(delegate).Calls(func(c *Verification) {
			c.Called(double.Charge).WithParams(7).Once()
			c.Called(double.Charge).WithParams(9).Once()
		})
		Equal(t, true, true)
	})
}

type orderedDouble struct {
	Spy
}

func (d *orderedDouble) A() { d.Call() }
func (d *orderedDouble) B() { d.Call() }
func (d *orderedDouble) C() { d.Call() }

func TestCallsOrdered_PassesWhenCallsHappenInOrder(t *testing.T) {
	d := NewDouble(t, &orderedDouble{})

	Run(t, "in order", func(t *testing.T) {
		d.A()
		d.B()
		d.C()

		Expect(t).CallsOrdered(func(c *Verification) {
			c.Called(d.A).Once()
			c.Called(d.B).Once()
			c.Called(d.C).Once()
		})
		Equal(t, true, true)
	})
}

func TestCallsOrdered_FailsWhenOutOfOrder(t *testing.T) {
	defer isolateCurrentTestRegistries()()
	d := NewDouble(t, &orderedDouble{})

	d.B()
	d.A()

	ft := &fakeT{}
	Expect(ft).CallsOrdered(func(c *Verification) {
		c.Called(d.A)
		c.Called(d.B)
	})
	if !ft.failed {
		t.Fatal("expected CallsOrdered to fail when B ran before A")
	}
}

func TestShortChains_AfterDoubleWithoutRun(t *testing.T) {
	double := NewDouble(t, &valueSpyDouble{})
	double.Charge(3)

	Called(double.Charge).WithParams(3).Once()
	Equal(t, true, true)
}

func TestShortChains_PanicWithoutBoundTest(t *testing.T) {
	recovered := make(chan any, 1)
	go func() {
		defer func() { recovered <- recover() }()
		Called(func() {})
	}()

	r := <-recovered
	if r == nil {
		t.Fatal("Expected Called to panic on a goroutine with no bound test")
	}
	if !strings.Contains(r.(string), "testigo.Expect(t)") {
		t.Errorf("Expected the panic to point at the Expect(t) fallback, got: %v", r)
	}
}

func TestFinalCheck_FailsWhenCallsVerifiedWithoutOutcomeAssertion(t *testing.T) {
	defer isolateCurrentTestRegistries()()

	spy := &Spy{}
	subject := &TestSubject{spy: spy}
	subject.DoSomething("hello", 123)

	ft := &fakeT{}
	Expect(ft).Called(subject.DoSomething).WithParams("hello", 123).Once()
	ft.runCleanups()

	if !ft.failed {
		t.Fatal("Expected the call-only test to fail")
	}
	if !strings.Contains(ft.message, "calls were verified") || !strings.Contains(ft.message, "1 required") {
		t.Fatalf("Expected a missing-outcome failure, got: %s", ft.message)
	}
}

func TestFinalCheck_PassesWhenOutcomeAsserted(t *testing.T) {
	defer isolateCurrentTestRegistries()()

	spy := &Spy{}
	subject := &TestSubject{spy: spy}
	subject.DoSomething("hello", 123)

	ft := &fakeT{}
	Expect(ft).Called(subject.DoSomething).WithParams("hello", 123).Once()
	Equal(ft, 1, 1)
	ft.runCleanups()

	if ft.failed {
		t.Fatalf("Expected the verified call with an outcome assertion to pass, got failure: %s", ft.message)
	}
	if strings.Contains(ft.message, "calls were verified") {
		t.Fatalf("Expected no missing-outcome failure, got: %s", ft.message)
	}
}

func TestFinalCheck_RequiresOneOutcomeAssertionPerReturnValue(t *testing.T) {
	defer isolateCurrentTestRegistries()()

	spy := &Spy{}
	subject := &TestSubject{spy: spy}
	_, _ = subject.DoSomethingReturning("hello")

	ft := &fakeT{}
	Expect(ft).Called(subject.DoSomethingReturning).WithParams("hello").Once()
	Equal(ft, 1, 1)
	ft.runCleanups()

	if !ft.failed {
		t.Fatal("Expected one assertion to be insufficient for a two-result function")
	}
	if !strings.Contains(ft.message, "only 1 result/state assertion") || !strings.Contains(ft.message, "2 required") {
		t.Fatalf("Expected the failure to require two outcome assertions, got: %s", ft.message)
	}
}

func isolateCurrentTestRegistries() func() {
	testID := getTestID()
	testSpies.Delete(testID)
	testVerifiers.Delete(testID)
	return func() {
		testSpies.Delete(testID)
		testVerifiers.Delete(testID)
	}
}

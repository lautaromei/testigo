package core

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestSubject is a simple struct to test spying on its methods.
type TestSubject struct {
	spy *Spy
}

func (ts *TestSubject) DoSomething(arg1 string, arg2 int) {
	ts.spy.Call(arg1, arg2)
}

func (ts *TestSubject) DoSomethingElse() {
	ts.spy.Call()
}

func (ts *TestSubject) DoSomethingWith(v any) {
	ts.spy.Call(v)
}

func (ts *TestSubject) DoSomethingReturning(arg string) (int, error) {
	ts.spy.Call(arg)
	return 1, nil
}

func (ts *TestSubject) AnotherMethod() {
	ts.spy.Call()
}

// expectTimes builds an expectation directly, to exercise the internal
// verification without going through a Expect chain.
func expectTimes(n int, fn any, args ...any) *CalledFunc {
	e := newExpectation(fn)
	e.times = n
	e.expectedArgs = args
	return e
}

func TestAssertCalls(t *testing.T) {
	t.Run("succeeds when all expectations match", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)
		subject.DoSomethingElse()

		ok, err := assertCalls(true,
			expectTimes(1, subject.DoSomething, "hello", 123),
			expectTimes(1, subject.DoSomethingElse),
		)

		if !ok {
			t.Errorf("Expected assertCalls to succeed, but it failed: %v", err)
		}
		if err != nil {
			t.Errorf("Expected error to be nil, but got: %v", err)
		}
	})

	t.Run("fails on unexpected call", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)
		subject.DoSomethingElse() // This one is unexpected

		ok, err := assertCalls(true,
			expectTimes(1, subject.DoSomething, "hello", 123),
		)

		if ok {
			t.Error("Expected assertCalls to fail, but it succeeded")
		}
		if err == nil {
			t.Error("Expected an error, but got nil")
		} else if !strings.Contains(err.Error(), "found 1 unexpected call(s)") {
			t.Errorf("Expected error to mention 'unexpected call', but got: %v", err)
		}
	})

	t.Run("ignores extra calls without the unexpected check", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)
		subject.DoSomethingElse() // Not verified, but tolerated without the check.

		ok, err := assertCalls(false,
			expectTimes(1, subject.DoSomething, "hello", 123),
		)

		if !ok {
			t.Errorf("Expected assertCalls without unexpected check to succeed, but it failed: %v", err)
		}
	})

	t.Run("fails on missing call", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)

		ok, err := assertCalls(true,
			expectTimes(1, subject.DoSomething, "hello", 123),
			expectTimes(1, subject.DoSomethingElse), // This one is missing
		)

		if ok {
			t.Error("Expected assertCalls to fail, but it succeeded")
		}
		if err == nil {
			t.Error("Expected an error, but got nil")
		} else if !strings.Contains(err.Error(), "but it was not called") {
			t.Errorf("Expected error to mention 'not called', but got: %v", err)
		}
	})

	t.Run("fails on mismatched parameters", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("world", 456)

		ok, err := assertCalls(true,
			expectTimes(1, subject.DoSomething, "hello", 123),
		)

		if ok {
			t.Error("Expected assertCalls to fail, but it succeeded")
		}
		if err == nil {
			t.Error("Expected an error, but got nil")
		} else if !strings.Contains(err.Error(), "params differ") {
			t.Errorf("Expected the graph to annotate the params diff, but got: %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "unexpected call") {
			t.Errorf("Error should not report an 'unexpected call' for a parameter mismatch: %v", err)
		}
	})

	t.Run("fails on wrong number of calls", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)
		subject.DoSomething("hello", 123)

		ok, err := assertCalls(true,
			expectTimes(1, subject.DoSomething, "hello", 123),
		)

		if ok {
			t.Error("Expected assertCalls to fail, but it succeeded")
		}
		if err == nil {
			t.Error("Expected an error, but got nil")
		} else if !strings.Contains(stripANSI(err.Error()), "✘ expected x1, got x2") {
			t.Errorf("Expected error to mention call count mismatch, but got: %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "unexpected call") {
			t.Errorf("Error should not report an 'unexpected call' for a call count mismatch: %v", err)
		}
	})

	t.Run("succeeds with Anything matcher", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("any string", 999)

		ok, err := assertCalls(true,
			expectTimes(1, subject.DoSomething, Anything, 999),
		)

		if !ok {
			t.Errorf("Expected assertCalls to succeed with Anything matcher, but it failed: %v", err)
		}
	})
}

// fakeT captures failures and cleanups so we can test Expect chains
// without failing the real test.
type fakeT struct {
	testing.TB
	failed   bool
	message  string
	cleanups []func()
}

func (f *fakeT) Helper() {}

func (f *fakeT) Failed() bool { return f.failed }

func (f *fakeT) Fail() { f.failed = true }

// Output captures what reportFinal writes (via t.Output) into message, so the
// final-check tests can read it the same way they read Error/Fatal messages.
func (f *fakeT) Output() io.Writer { return fakeWriter{f} }

type fakeWriter struct{ f *fakeT }

func (w fakeWriter) Write(p []byte) (int, error) {
	w.f.message += string(p)
	return len(p), nil
}

func (f *fakeT) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

func (f *fakeT) runCleanups() {
	for _, fn := range f.cleanups {
		fn()
	}
}

func (f *fakeT) runCleanupsLIFO() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func (f *fakeT) Error(args ...any) {
	f.failed = true
	f.message = fmt.Sprint(args...)
}

func (f *fakeT) Fatal(args ...any) {
	f.failed = true
	f.message = fmt.Sprint(args...)
}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.failed = true
	f.message = fmt.Sprintf(format, args...)
}

func (f *fakeT) Errorf(format string, args ...any) {
	f.failed = true
	f.message = fmt.Sprintf(format, args...)
}

func TestHappened(t *testing.T) {
	t.Run("passes when the expectation happened", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).WithParams("hello", 123).Once()
		Equal(ft, true, true)
		ft.runCleanups()

		if ft.failed {
			t.Errorf("Expected the chain to pass, but it failed with: %s", ft.message)
		}
	})

	t.Run("fails with the call graph on mismatch", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("world", 456)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).WithParams("hello", 123).Once()

		if !ft.failed {
			t.Error("Expected the chain to fail, but it passed")
		}
		if !strings.Contains(ft.message, "Call Graph") {
			t.Errorf("Expected failure message to include the call graph, but got: %s", ft.message)
		}
	})

	t.Run("matches any arguments without WithParams", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("whatever", 777)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).Once()
		Equal(ft, true, true)
		ft.runCleanups()

		if ft.failed {
			t.Errorf("Expected the chain without WithParams to pass, but it failed with: %s", ft.message)
		}
	})

	t.Run("verifies the caller with That", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}
		outer.PerformAction()

		ft := &fakeT{}
		Expect(ft).That(outer).Called(inner.ProcessData).WithParams(Anything).Once()
		Expect(ft).Called(outer.PerformAction).Once()
		Equal(ft, true, true)
		ft.runCleanups()

		if ft.failed {
			t.Errorf("Expected That(outer) chain to pass, but it failed with: %s", ft.message)
		}
	})

	t.Run("fails when the caller does not match", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}
		outer.PerformAction()

		ft := &fakeT{}
		Expect(ft).That("core.SomeoneElse").Called(inner.ProcessData).WithParams(Anything).Once()

		if !ft.failed {
			t.Error("Expected the chain to fail on caller mismatch, but it passed")
		}
		if !strings.Contains(stripANSI(ft.message), "✘ expected caller: SomeoneElse") {
			t.Errorf("Expected failure message to mention the caller, but got: %s", ft.message)
		}
	})

	t.Run("supports Times, Never and AtLeastOnce", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("repeat", 0)
		subject.DoSomething("repeat", 0)
		subject.DoSomething("repeat", 0)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).Times(3)
		Expect(ft).Called(subject.DoSomethingElse).Never()
		Expect(ft).Called(subject.DoSomething).AtLeastOnce()
		Equal(ft, true, true)
		ft.runCleanups()

		if ft.failed {
			t.Errorf("Expected all chains to pass, but failed with: %s", ft.message)
		}
	})

	t.Run("fails with AtLeastOnce when never called", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		_ = subject

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).AtLeastOnce()

		if !ft.failed {
			t.Error("Expected AtLeastOnce to fail, but it passed")
		}
		if !strings.Contains(ft.message, "expected 'DoSomething' to be called at least 1 time(s), but it was not called") {
			t.Errorf("Expected error to mention 'at least 1 time(s)', but got: %s", ft.message)
		}
	})

	t.Run("flags unverified calls when the test finishes", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)
		subject.DoSomethingElse() // Recorded, but never verified by a chain.

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).WithParams("hello", 123).Once()
		ft.runCleanups()

		if !ft.failed {
			t.Error("Expected the final check to fail on the unverified call, but it passed")
		}
		if !strings.Contains(ft.message, "unexpected call") || !strings.Contains(ft.message, "DoSomethingElse") {
			t.Errorf("Expected final check to flag DoSomethingElse as unexpected, but got: %s", ft.message)
		}
	})

	t.Run("defaults to once when the chain has no count", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).WithParams("hello", 123) // No terminal: once by default
		Equal(ft, true, true)
		ft.runCleanups()

		if ft.failed {
			t.Errorf("Expected the default-once chain to pass, but it failed with: %s", ft.message)
		}
	})

	t.Run("fails the default-once chain when called twice", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)
		subject.DoSomething("hello", 123)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething) // Once by default, but called twice
		ft.runCleanups()

		if !ft.failed {
			t.Error("Expected the default-once chain to fail, but it passed")
		}
		if !strings.Contains(stripANSI(ft.message), "✘ expected x1, got x2") {
			t.Errorf("Expected error to mention the count mismatch, but got: %s", ft.message)
		}
	})

	t.Run("supports Twice and ThreeTimes", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("a", 1)
		subject.DoSomething("a", 1)
		subject.DoSomethingElse()
		subject.DoSomethingElse()
		subject.DoSomethingElse()

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).Twice()
		Expect(ft).Called(subject.DoSomethingElse).ThreeTimes()
		Equal(ft, true, true)
		ft.runCleanups()

		if ft.failed {
			t.Errorf("Expected Twice and ThreeTimes to pass, but failed with: %s", ft.message)
		}
	})

	t.Run("fails with a clear error when given a non-function", func(t *testing.T) {
		ft := &fakeT{}
		Expect(ft).Called("not a function").Once()

		if !ft.failed {
			t.Error("Expected the chain to fail, but it passed")
		}
		if !strings.Contains(ft.message, "expected a function") {
			t.Errorf("Expected error to mention 'expected a function', but got: %s", ft.message)
		}
	})
}

func TestSpy_ClearAndTotalCalls(t *testing.T) {
	spy := NewSpy()
	subject := &TestSubject{spy: spy}

	if spy.TotalCalls() != 0 {
		t.Errorf("Expected 0 total calls for a new spy, but got %d", spy.TotalCalls())
	}

	subject.DoSomething("a", 1)
	subject.DoSomethingElse()

	if spy.TotalCalls() != 2 {
		t.Errorf("Expected 2 total calls, but got %d", spy.TotalCalls())
	}

	spy.Clear()

	if spy.TotalCalls() != 0 {
		t.Errorf("Expected 0 total calls after Clear(), but got %d", spy.TotalCalls())
	}
}

// --- Structs for nested call with struct param test ---

type CustomData struct {
	ID   int
	Name string
}

type InnerService struct {
	spy *Spy
}

func (is *InnerService) ProcessData(data CustomData) {
	is.spy.Call(data)
}

type OuterService struct {
	spy   *Spy
	inner *InnerService
}

func (os *OuterService) PerformAction() {
	os.spy.Call() // Watch the outer call
	data := CustomData{ID: 1, Name: "Test Data"}
	os.inner.ProcessData(data)
}

func TestSpy_NestedCallWithStructParam(t *testing.T) {
	t.Run("succeeds when nested call with struct param is expected correctly", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}

		outer.PerformAction()

		expectedData := CustomData{ID: 1, Name: "Test Data"}
		Expect(t).Called(outer.PerformAction).Once()
		Expect(t).Called(inner.ProcessData).WithParams(expectedData).Once()
		Equal(t, expectedData.ID, 1)
	})

	t.Run("fails when struct param does not match", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}
		outer.PerformAction()

		wrongData := CustomData{ID: 99, Name: "Wrong Data"}
		ft := &fakeT{}
		Expect(ft).Called(inner.ProcessData).WithParams(wrongData).Once()

		if !ft.failed {
			t.Error("Expected the chain to fail due to struct mismatch, but it passed")
		}
	})
}

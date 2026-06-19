package core

import (
	"sync"
	"testing"
)

// These tests document call attribution across goroutines: Call registers the
// spy in testSpies under the goroutine ID it runs on (getTestID), and
// assertCalls collects spies under the goroutine ID of the asserting test. A
// bare spy called only from worker goroutines is registered under the worker's
// ID, so the test's assertions never see it — even though the calls themselves
// are recorded on the spy.
//
// A spy registered with NewDouble is the supported exception: the double is
// bound to the test that owns it, so its calls are visible to that test's (and
// its subtests') assertions regardless of which goroutine ran Call — an HTTP
// handler under httptest, for instance (see TestNewDoubleSpyVisible* below).

func TestGoroutineAttribution(t *testing.T) {
	t.Run("baseline: calls on the test goroutine are visible", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}

		subject.DoSomething("hello", 123)

		ok, err := assertCalls(false, expectTimes(1, subject.DoSomething, "hello", 123))
		if !ok {
			t.Errorf("expected the call on the test goroutine to be visible, got: %v", err)
		}
	})

	t.Run("limitation: calls only from a worker goroutine are invisible to assertions", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			subject.DoSomething("hello", 123)
		}()
		wg.Wait()

		// The call IS recorded on the spy itself...
		if got := len(spy.calls); got != 1 {
			t.Fatalf("expected the spy to record 1 call regardless of goroutine, got %d", got)
		}

		// ...but the spy was registered under the worker's goroutine ID, so
		// the test goroutine's assertion cannot find it.
		ok, err := assertCalls(false, expectTimes(1, subject.DoSomething, "hello", 123))
		if ok {
			t.Error("expected the assertion to miss the worker-only call; the limitation seems fixed — update these tests")
		}
		if err == nil {
			t.Error("expected a 'never called' style error, got nil")
		}
	})

	t.Run("workaround: one call on the test goroutine makes worker calls visible too", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}

		// This call registers the spy under the test goroutine's ID.
		subject.DoSomethingElse()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			subject.DoSomething("hello", 123)
		}()
		wg.Wait()

		// collectTestCalls reads every call of each registered spy, so once
		// the spy is registered for this test, worker calls count as well.
		ok, err := assertCalls(false,
			expectTimes(1, subject.DoSomething, "hello", 123),
			expectTimes(1, subject.DoSomethingElse),
		)
		if !ok {
			t.Errorf("expected worker calls to be visible once the spy is registered for the test, got: %v", err)
		}
	})
}

// TestNewDoubleSpyVisibleFromWorkerGoroutine is the supported counterpart to the
// limitation above: a NewDouble-registered spy is owned by the test, so a call
// made only from a worker goroutine — never on the test goroutine — is still
// visible to the test's assertions. This is what lets an end-to-end test verify
// interactions an HTTP handler triggered on the server's own goroutine.
func TestNewDoubleSpyVisibleFromWorkerGoroutine(t *testing.T) {
	subject := NewDouble(t, &TestSubject{spy: &Spy{}})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		subject.DoSomething("hello", 123)
	}()
	wg.Wait()

	ok, err := assertCalls(false, expectTimes(1, subject.DoSomething, "hello", 123))
	if !ok {
		t.Errorf("expected a NewDouble-registered spy's worker-goroutine call to be visible, got: %v", err)
	}
}

func TestCallRecordStoresCallingGoroutineID(t *testing.T) {
	subject := NewDouble(t, &TestSubject{spy: &Spy{}})
	testGoroutineID := currentGoroutineID()

	var wg sync.WaitGroup
	workerGoroutineID := make(chan uint64, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		workerGoroutineID <- currentGoroutineID()
		subject.DoSomething("hello", 123)
	}()
	wg.Wait()

	if got := len(subject.spy.calls); got != 1 {
		t.Fatalf("expected one recorded call, got %d", got)
	}
	want := <-workerGoroutineID
	got := subject.spy.calls[0].GoroutineID
	if got == 0 {
		t.Fatal("expected call record to include a goroutine id")
	}
	if got != want {
		t.Fatalf("expected call goroutine id %d, got %d", want, got)
	}
	if got == testGoroutineID {
		t.Fatalf("expected worker call to keep worker goroutine id, got test goroutine id %d", got)
	}
}

// TestNewDoubleSpyVisibleFromSubtest checks the ownership walks up: the double
// is registered on the parent test, the worker call happens during a
// testigo.Run subtest, and the subtest's assertion still sees it.
func TestNewDoubleSpyVisibleFromSubtest(t *testing.T) {
	subject := NewDouble(t, &TestSubject{spy: &Spy{}})

	Run(t, "worker call inside a subtest is visible", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			subject.DoSomething("hello", 123)
		}()
		wg.Wait()

		ok, err := assertCalls(false, expectTimes(1, subject.DoSomething, "hello", 123))
		if !ok {
			t.Errorf("expected the worker call to be visible from the subtest, got: %v", err)
		}
	})
}

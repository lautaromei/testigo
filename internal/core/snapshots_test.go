package core

import (
	"strings"
	"testing"
)

type parcel struct {
	Weight int
	tags   []string
}

type courierSpy struct {
	Spy
}

func (c *courierSpy) ship(p *parcel) {
	c.Call(p)
}

func (c *courierSpy) shipMany(codes []string) {
	c.Call(codes)
}

type node struct {
	id   int
	next *node
}

func (c *courierSpy) visit(n *node) {
	c.Call(n)
}

func TestSpyCall_SnapshotsParams(t *testing.T) {
	t.Run("matches the state at call time, not the mutated one", func(t *testing.T) {
		spy := &courierSpy{}
		p := &parcel{Weight: 2}

		spy.ship(p)
		p.Weight = 9 // The SUT keeps mutating its own object after the call.

		ok, err := assertCalls(false, expectTimes(1, spy.ship, &parcel{Weight: 2}))
		if !ok {
			t.Errorf("Expected the snapshot to match the call-time state, got: %v", err)
		}
	})

	t.Run("explains aliasing when the expectation matches the mutated state", func(t *testing.T) {
		spy := &courierSpy{}
		p := &parcel{Weight: 2}

		spy.ship(p)
		p.Weight = 9

		ok, err := assertCalls(false, expectTimes(1, spy.ship, &parcel{Weight: 9}))
		if ok {
			t.Fatal("Expected the assertion against the mutated state to fail")
		}
		if !strings.Contains(err.Error(), "mutated after the call (aliasing)") {
			t.Errorf("Expected an aliasing note, got: %v", err)
		}
	})

	t.Run("snapshots slices", func(t *testing.T) {
		spy := &courierSpy{}
		codes := []string{"A1"}

		spy.shipMany(codes)
		codes[0] = "Z9"

		ok, err := assertCalls(false, expectTimes(1, spy.shipMany, []string{"A1"}))
		if !ok {
			t.Errorf("Expected the slice snapshot to match, got: %v", err)
		}
	})

	t.Run("snapshots unexported fields", func(t *testing.T) {
		spy := &courierSpy{}
		p := &parcel{Weight: 1, tags: []string{"fragile"}}

		spy.ship(p)
		p.tags[0] = "heavy"

		ok, err := assertCalls(false, expectTimes(1, spy.ship, &parcel{Weight: 1, tags: []string{"fragile"}}))
		if !ok {
			t.Errorf("Expected unexported slice fields to be snapshotted, got: %v", err)
		}
	})

	t.Run("survives cyclic structures", func(t *testing.T) {
		spy := &courierSpy{}
		n := &node{id: 1}
		n.next = n // Cycle.

		spy.visit(n) // Must not hang nor overflow.

		if spy.TotalCalls() != 1 {
			t.Errorf("Expected the cyclic param to be recorded, got %d calls", spy.TotalCalls())
		}
		snapshot := spy.calls[0].Snapshots[0].(*node)
		if snapshot == n {
			t.Error("Expected the snapshot to be a different object than the original")
		}
		if snapshot.next != snapshot {
			t.Error("Expected the snapshot to preserve the cycle onto itself")
		}
	})

	t.Run("plain values skip the deep copy", func(t *testing.T) {
		spy := &TestSubject{spy: NewSpy()}
		spy.DoSomething("hello", 42)

		ok, err := assertCalls(false, expectTimes(1, spy.DoSomething, "hello", 42))
		if !ok {
			t.Errorf("Expected plain params to keep matching, got: %v", err)
		}
	})
}

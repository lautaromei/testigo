package core

import (
	"strings"
	"testing"
)

func stripANSI(s string) string {
	for _, code := range []string{bold, red, green, yellow, reset} {
		s = strings.ReplaceAll(s, code, "")
	}
	return s
}

func TestDrawGraph(t *testing.T) {
	t.Run("returns a message when no calls were recorded", func(t *testing.T) {
		spy := &Spy{}
		Equal(t, spy.DrawGraph(""), "Call Graph: No calls recorded.")
	})

	t.Run("renders nested calls as an indented tree", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}
		outer.PerformAction()

		graph := stripANSI(spy.DrawGraph("testigo"))
		lines := strings.Split(graph, "\n")

		var actionLine, dataLine string
		for _, line := range lines {
			if strings.Contains(line, "PerformAction") {
				actionLine = line
			}
			if strings.Contains(line, "ProcessData") {
				dataLine = line
			}
		}

		True(t, strings.Contains(actionLine, "└─▶ OuterService.PerformAction"), "expected PerformAction node, got: %q", actionLine)
		True(t, strings.Contains(dataLine, "└─▶ InnerService.ProcessData({1 Test Data})"), "expected ProcessData node with params, got: %q", dataLine)
		True(t, strings.Index(graph, "PerformAction") < strings.Index(graph, "ProcessData"), "expected ProcessData nested under PerformAction:\n%s", graph)
		True(t, len(dataLine)-len(strings.TrimLeft(dataLine, " ")) > len(actionLine)-len(strings.TrimLeft(actionLine, " ")), "expected ProcessData indented deeper:\n%s", graph)
	})

	t.Run("keeps package names when packages are mixed", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomethingElse()

		graph := stripANSI(spy.DrawGraph(""))
		True(t, strings.Contains(graph, "core.TestSubject.DoSomethingElse"), "expected full component name, got:\n%s", graph)
	})

	t.Run("collapses identical sibling calls with a counter", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("a", 1)
		subject.DoSomething("a", 1)
		subject.DoSomething("a", 1)
		subject.DoSomethingElse()

		graph := stripANSI(spy.DrawGraph("testigo"))

		True(t, strings.Contains(graph, "DoSomething(a, 1) (x3)"), "expected collapsed node with (x3), got:\n%s", graph)
		Equal(t, strings.Count(graph, "DoSomething(a, 1)"), 1)
	})

	t.Run("does not collapse calls with different params", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("a", 1)
		subject.DoSomething("b", 2)

		graph := stripANSI(spy.DrawGraph("testigo"))

		True(t, strings.Contains(graph, "DoSomething(a, 1)"), "expected first call, got:\n%s", graph)
		True(t, strings.Contains(graph, "DoSomething(b, 2)"), "expected second call, got:\n%s", graph)
		False(t, strings.Contains(graph, "(x2)"), "expected no collapsing, got:\n%s", graph)
	})

	t.Run("uses branch connectors for siblings and a corner for the last one", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("a", 1)
		subject.DoSomethingElse()

		graph := stripANSI(spy.DrawGraph("testigo"))

		True(t, strings.Contains(graph, "├─▶ TestSubject.DoSomething"), "expected branch connector, got:\n%s", graph)
		True(t, strings.Contains(graph, "└─▶ TestSubject.DoSomethingElse"), "expected corner connector on last sibling, got:\n%s", graph)
	})

	t.Run("survives call cycles", func(t *testing.T) {
		spy := &Spy{
			calls: []*CallRecord{
				{CallerComponent: "p.A", CallerMethod: "Ping", CalleeComponent: "p.B", CalleeMethod: "Pong"},
				{CallerComponent: "p.B", CallerMethod: "Pong", CalleeComponent: "p.A", CalleeMethod: "Ping"},
			},
		}

		graph := stripANSI(spy.DrawGraph(""))
		True(t, strings.Contains(graph, "Pong"), "expected cycle members rendered, got:\n%s", graph)
		True(t, strings.Contains(graph, "Ping"), "expected cycle members rendered, got:\n%s", graph)
	})
}

func TestDrawGraph_FailureAnnotations(t *testing.T) {
	t.Run("marks unexpected calls", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("hello", 123)
		subject.DoSomethingElse() // Never verified.

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).Once()
		ft.runCleanups()

		message := stripANSI(ft.message)
		True(t, strings.Contains(message, "DoSomethingElse   ✘ unexpected call"), "expected unexpected-call marker, got:\n%s", message)
	})

	t.Run("annotates count mismatches in place", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("a", 1)
		subject.DoSomething("a", 1)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).Once()

		message := stripANSI(ft.message)
		True(t, strings.Contains(message, "✘ expected x1, got x2"), "expected count annotation, got:\n%s", message)
	})

	t.Run("annotates param mismatches with a per-field diff", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.DoSomething("world", 456)

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).WithParams("hello", 123).Once()

		message := stripANSI(ft.message)
		True(t, strings.Contains(message, "✘ params differ"), "expected params diff annotation, got:\n%s", message)
		True(t, strings.Contains(message, `arg0: got "world", want "hello"`), "expected the first arg diff, got:\n%s", message)
		True(t, strings.Contains(message, "arg1: got 456, want 123"), "expected the second arg diff, got:\n%s", message)
		True(t, strings.Contains(message, "DoSomething(world, 456)"), "expected actual call in the tree, got:\n%s", message)
	})

	t.Run("param diffs point at the differing field inside a struct", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		type reservation struct {
			pax    int
			guests []string
		}
		subject.DoSomethingWith(reservation{pax: 2, guests: []string{"Jesse", "Jane"}})

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomethingWith).WithParams(reservation{pax: 2, guests: []string{"Walter", "Jane"}}).Once()

		message := stripANSI(ft.message)
		True(t, strings.Contains(message, `.guests[0]: got "Jesse", want "Walter"`), "expected the nested field diff, got:\n%s", message)
		False(t, strings.Contains(message, ".guests[1]"), "unchanged fields should not be reported:\n%s", message)
	})

	t.Run("annotates caller mismatches with both callers, correctly labeled", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}
		outer.PerformAction()

		ft := &fakeT{}
		Expect(ft).That("core.SomeoneElse").Called(inner.ProcessData).Once()

		message := stripANSI(ft.message)
		True(t, strings.Contains(message, "✘ expected caller: SomeoneElse"), "expected caller annotation, got:\n%s", message)
		True(t, strings.Contains(message, "✓ actual caller:   OuterService"), "expected actual caller annotation, got:\n%s", message)
	})

	t.Run("reports caller mismatch even without WithParams", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}
		outer.PerformAction()

		ft := &fakeT{}
		Expect(ft).That("core.SomeoneElse").Called(inner.ProcessData).Once()

		True(t, strings.Contains(stripANSI(ft.message), "✓ actual caller:   OuterService"), "expected a caller-mismatch error, got:\n%s", ft.message)
		False(t, strings.Contains(ft.message, "different arguments"), "caller mismatch misreported as argument mismatch:\n%s", ft.message)
	})

	t.Run("error locations point at the real call site, not at Call", func(t *testing.T) {
		spy := &Spy{}
		inner := &InnerService{spy: spy}
		outer := &OuterService{spy: spy, inner: inner}
		outer.PerformAction() // ProcessData is invoked inside PerformAction, in this file.

		ft := &fakeT{}
		Expect(ft).Called(inner.ProcessData).Twice()

		True(t, strings.Contains(ft.message, "spy_test.go:"), "expected location of the caller's file, got:\n%s", ft.message)
	})

	t.Run("annotates at-least expectations", func(t *testing.T) {
		spy := &Spy{}
		subject := &TestSubject{spy: spy}
		subject.AnotherMethod()

		ft := &fakeT{}
		Expect(ft).Called(subject.DoSomething).AtLeastOnce()

		message := stripANSI(ft.message)
		True(t, strings.Contains(message, "at least"), "expected at-least annotation in the report, got:\n%s", message)
	})
}

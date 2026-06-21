package boundarymut

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const dropEventSrc = `package p

func build() string { return "x" }
func writeJSON(w, status, v any) {}

func handle(w any) {
	status := 200
	v := build()
	writeJSON(w, status, v)
}
`

// findSite enumerates src and returns the site whose callee matches method.
func findSite(t *testing.T, src string, method string) site {
	t.Helper()
	sites, err := enumerate("p.go", []byte(src))
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	for _, s := range sites {
		if s.method == method {
			return s
		}
	}
	t.Fatalf("no site for method %q", method)
	return site{}
}

// TestDropEventBlanksArgs verifies DROP_EVENT drops the call but keeps the
// produced value consumed, so the writeJSON-style site stays compilable where a
// plain DROP_CALL would orphan `v`.
func TestDropEventBlanksArgs(t *testing.T) {
	s := findSite(t, dropEventSrc, "writeJSON")
	if !s.bareCall {
		t.Fatal("writeJSON site should be a bare call (DROP_EVENT-eligible)")
	}

	out, ok := apply(s, DropEvent, []byte(dropEventSrc))
	if !ok {
		t.Fatal("apply DropEvent returned not-ok")
	}
	got := string(out)

	if strings.Contains(got, "writeJSON(w, status, v)") {
		t.Errorf("call not dropped:\n%s", got)
	}
	if !strings.Contains(got, "_, _, _ = w, status, v") {
		t.Errorf("args not blanked:\n%s", got)
	}
	// Must still parse (compilable shape — every operand consumed).
	if _, err := parser.ParseFile(token.NewFileSet(), "p.go", out, 0); err != nil {
		t.Errorf("mutated source does not parse: %v\n%s", err, got)
	}
}

// TestDropEventEligibility checks only bare ExprStmt calls get DROP_EVENT; the
// error-checked if-form does not (DROP_CALL already compiles there).
func TestDropEventEligibility(t *testing.T) {
	const src = `package p

func emit() error { return nil }

func run() {
	emit()
	if err := emit(); err != nil {
		_ = err
	}
}
`
	sites, err := enumerate("p.go", []byte(src))
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	var bare, checked int
	for _, s := range sites {
		if s.bareCall {
			bare++
		} else {
			checked++
		}
	}
	if bare != 1 {
		t.Errorf("expected 1 bare-call site, got %d", bare)
	}
	if checked != 1 {
		t.Errorf("expected 1 non-bare (if-checked) site, got %d", checked)
	}
}

// TestRewireCalleeSwapsFun verifies REWIRE_CALLEE swaps the callees of two
// adjacent calls while leaving each call's arguments in place — redirecting
// edge A→foo to A→bar (the wrong-target fault edge-not-observed predicts).
func TestRewireCalleeSwapsFun(t *testing.T) {
	const src = `package p

func foo(x int) {}
func bar(y int) {}

func run() {
	foo(1)
	bar(2)
}
`
	s := findSite(t, src, "foo")
	if !s.hasNext {
		t.Fatal("foo site should have a next call (REWIRE-eligible)")
	}
	out, ok := apply(s, RewireCallee, []byte(src))
	if !ok {
		t.Fatal("apply RewireCallee returned not-ok")
	}
	got := string(out)
	// Args stay fixed (1 then 2); callees swap.
	if !strings.Contains(got, "bar(1)") || !strings.Contains(got, "foo(2)") {
		t.Errorf("callees not rewired (want bar(1)/foo(2)):\n%s", got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "p.go", out, 0); err != nil {
		t.Errorf("mutated source does not parse: %v\n%s", err, got)
	}
}

// TestRewireCalleeNoNext rejects a site whose next statement is not a call.
func TestRewireCalleeNoNext(t *testing.T) {
	const src = `package p

func foo(x int) {}

func run() {
	foo(1)
	x := 2
	_ = x
}
`
	s := findSite(t, src, "foo")
	if s.hasNext {
		t.Fatal("foo site should have no next call")
	}
	if _, ok := apply(s, RewireCallee, []byte(src)); ok {
		t.Error("apply RewireCallee should fail with no adjacent call")
	}
}

// TestDropEventNoArgs falls back to a plain delete when the call has no args.
func TestDropEventNoArgs(t *testing.T) {
	const src = `package p

func ping() {}

func run() {
	ping()
}
`
	s := findSite(t, src, "ping")
	out, ok := apply(s, DropEvent, []byte(src))
	if !ok {
		t.Fatal("apply DropEvent (no args) returned not-ok")
	}
	// One "ping()" remains (the func decl); the call statement is gone.
	if n := strings.Count(string(out), "ping()"); n != 1 {
		t.Errorf("expected no-arg call dropped (1 ping() left, the decl), got %d:\n%s", n, out)
	}
}

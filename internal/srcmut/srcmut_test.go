package srcmut

import (
	"strings"
	"testing"
)

func TestApplyArgSwap(t *testing.T) {
	src := []byte(`package p

func run(b Bank) {
	b.Transfer("acct-a", "acct-b")
}
`)
	// callOrd 0 is b.Transfer(...); swap its two string args.
	s := site{absFile: "p.go", operator: ArgSwap, callOrd: 0, argIdx: 0, argIdx2: 1}
	out, ok := apply(s, src)
	if !ok {
		t.Fatal("apply ArgSwap returned ok=false")
	}
	got := string(out)
	if !strings.Contains(got, `b.Transfer("acct-b", "acct-a")`) {
		t.Fatalf("arguments were not swapped:\n%s", got)
	}
}

func TestApplyArgSwapOutOfRange(t *testing.T) {
	src := []byte(`package p

func run(b Bank) {
	b.Ping("x")
}
`)
	s := site{absFile: "p.go", operator: ArgSwap, callOrd: 0, argIdx: 0, argIdx2: 1}
	if _, ok := apply(s, src); ok {
		t.Fatal("expected ok=false swapping a non-existent second argument")
	}
}

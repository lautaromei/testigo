package edgecovssa

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeMemDBReportsCheckedEffects(t *testing.T) {
	dir, err := filepath.Abs("../../memdb")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Package != "github.com/lautaromei/testigo/memdb" {
		t.Fatalf("package = %q", rep.Package)
	}
	if rep.Summary.Effects == 0 {
		t.Fatalf("expected effects, got summary %+v", rep.Summary)
	}
	if rep.Summary.EffectsReachedUnchecked == 0 {
		t.Fatalf("expected reached-unchecked effects, got summary %+v", rep.Summary)
	}
	if len(rep.Findings) == 0 {
		t.Fatalf("expected findings, got summary %+v", rep.Summary)
	}
	if got := rep.Findings[0].Kind; got != "effect-reached-unchecked" {
		t.Fatalf("first finding kind = %q", got)
	}
	if _, err := rep.JSON(); err != nil {
		t.Fatal(err)
	}
	if dot := rep.DOT(); dot == "" {
		t.Fatal("empty DOT")
	}
}

func TestAnalyzeProjectUsesProjectCoverage(t *testing.T) {
	dir, err := filepath.Abs("../../memdb")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := AnalyzeProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summary.Edges == 0 {
		t.Fatalf("expected project edges, got summary %+v", rep.Summary)
	}
	if rep.Summary.Branches == 0 {
		t.Fatalf("expected project branches, got summary %+v", rep.Summary)
	}
}

func TestAnalyzeCountsConcreteInterfaceDispatchEdges(t *testing.T) {
	dir := t.TempDir()
	writeEdgeFile(t, dir, "go.mod", "module fixture.test/edgeiface\n\ngo 1.24\n")
	writeEdgeFile(t, dir, "sample.go", `package edgeiface

type sink interface {
	Put(string)
}

type memorySink struct {
	values []string
}

func (m *memorySink) Put(v string) {
	m.values = append(m.values, v)
}

func send(s sink) {
	s.Put("ok")
}
`)
	writeEdgeFile(t, dir, "sample_test.go", `package edgeiface

import "testing"

func TestSend(t *testing.T) {
	var got memorySink
	send(&got)
	if len(got.values) != 1 {
		t.Fatalf("values = %d", len(got.values))
	}
}
`)

	rep, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summary.InterfaceEdges == 0 {
		t.Fatalf("expected concrete interface edges, got summary %+v", rep.Summary)
	}
	for _, f := range rep.Findings {
		if f.Kind == "edge-not-observed" {
			t.Fatalf("edge-not-observed should be diagnostic-only, got finding %+v", f)
		}
	}
	if txt := rep.Text(); !strings.Contains(txt, "interface") {
		t.Fatalf("text summary did not mention interface edges:\n%s", txt)
	}
}

func TestAnalyzeDoesNotReportLocalSliceStoresAsUncheckedEffects(t *testing.T) {
	dir := t.TempDir()
	writeEdgeFile(t, dir, "go.mod", "module fixture.test/edgelocal\n\ngo 1.24\n")
	writeEdgeFile(t, dir, "sample.go", `package edgelocal

func parts(s string) []string {
	out := make([]string, 2)
	out[0] = s[:1]
	out[1] = s[1:]
	return out
}
`)
	writeEdgeFile(t, dir, "sample_test.go", `package edgelocal

import "testing"

func TestParts(t *testing.T) {
	got := parts("ab")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("parts = %#v", got)
	}
}
`)

	rep, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Kind == "effect-reached-unchecked" && strings.Contains(f.File, "sample.go") {
			t.Fatalf("local slice store should not be an unchecked effect: %+v", f)
		}
	}
}

func TestAnalyzeDoesNotReportIteratorYieldAsUncheckedEffect(t *testing.T) {
	dir := t.TempDir()
	writeEdgeFile(t, dir, "go.mod", "module fixture.test/edgeyield\n\ngo 1.24\n")
	writeEdgeFile(t, dir, "sample.go", `package edgeyield

func seq(s string) func(func(string) bool) {
	return func(yield func(string) bool) {
		yield(s)
	}
}
`)
	writeEdgeFile(t, dir, "sample_test.go", `package edgeyield

import "testing"

func TestSeq(t *testing.T) {
	var got []string
	for v := range seq("ok") {
		got = append(got, v)
	}
	if len(got) != 1 || got[0] != "ok" {
		t.Fatalf("seq = %#v", got)
	}
}
`)

	rep, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Kind == "effect-reached-unchecked" && strings.Contains(f.File, "sample.go") {
			t.Fatalf("yield should be treated as API-observed: %+v", f)
		}
	}
}

func writeEdgeFile(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

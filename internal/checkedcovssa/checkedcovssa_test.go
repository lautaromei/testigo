package checkedcovssa

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeTracksHeapWritesByField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module fixture.test/checked\n\ngo 1.24\n")
	src := `package checked

type recorder struct {
	Code   int
	Header string
}

func writeCode(r *recorder) {
	r.Code = 201
}

func writeHeader(r *recorder) {
	r.Header = "application/json"
}

func handle(r *recorder) {
	writeCode(r)
	writeHeader(r)
}
`
	writeFile(t, dir, "sample.go", src)
	writeFile(t, dir, "sample_test.go", `package checked

import "testing"

func TestHandleChecksCodeOnly(t *testing.T) {
	var got recorder
	handle(&got)
	if got.Code != 201 {
		t.Fatalf("code = %d", got.Code)
	}
}
`)

	rep, err := Analyze(dir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	codeStore := lineOf(t, src, "r.Code = 201")
	codeCall := lineOf(t, src, "writeCode(r)")
	headerStore := lineOf(t, src, `r.Header = "application/json"`)
	headerCall := lineOf(t, src, "writeHeader(r)")

	if !rep.IsChecked("sample.go", codeStore) {
		t.Fatalf("Code store line %d was not checked", codeStore)
	}
	if !rep.IsChecked("sample.go", codeCall) {
		t.Fatalf("writeCode call line %d was not checked", codeCall)
	}
	if rep.IsChecked("sample.go", headerStore) {
		t.Fatalf("Header store line %d was checked by Code assertion", headerStore)
	}
	if rep.IsChecked("sample.go", headerCall) {
		t.Fatalf("writeHeader call line %d was checked by Code assertion", headerCall)
	}
}

func TestAnalyzeTracksHeapWritesThroughInterfaceDispatch(t *testing.T) {
	t.Skip("checkedcovssa field-sensitive heap summaries are static-call only; dynamic dispatch is a future iteration")
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module fixture.test/iface\n\ngo 1.24\n")
	src := `package iface

type Writer interface {
	SetCode(int)
	SetHeader(string)
}

type recorder struct {
	Code   int
	Header string
}

func (r *recorder) SetCode(c int)      { r.Code = c }
func (r *recorder) SetHeader(h string) { r.Header = h }

func write(w Writer) {
	w.SetCode(201)
	w.SetHeader("application/json")
}

func handle(w Writer) {
	write(w)
}
`
	writeFile(t, dir, "sample.go", src)
	writeFile(t, dir, "sample_test.go", `package iface

import "testing"

func TestHandleChecksCodeOnly(t *testing.T) {
	got := &recorder{}
	handle(got)
	if got.Code != 201 {
		t.Fatalf("code = %d", got.Code)
	}
}
`)

	rep, err := Analyze(dir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	codeStore := lineOf(t, src, "r.Code = c")
	codeCall := lineOf(t, src, "w.SetCode(201)")
	headerStore := lineOf(t, src, "r.Header = h")
	headerCall := lineOf(t, src, `w.SetHeader("application/json")`)

	if !rep.IsChecked("sample.go", codeStore) {
		t.Fatalf("Code store line %d not checked through interface dispatch", codeStore)
	}
	if !rep.IsChecked("sample.go", codeCall) {
		t.Fatalf("SetCode call line %d not checked through interface dispatch", codeCall)
	}
	if rep.IsChecked("sample.go", headerStore) {
		t.Fatalf("Header store line %d wrongly checked by Code assertion", headerStore)
	}
	if rep.IsChecked("sample.go", headerCall) {
		t.Fatalf("SetHeader call line %d wrongly checked by Code assertion", headerCall)
	}
}

func writeFile(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func lineOf(t *testing.T, src, needle string) int {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	t.Fatalf("line containing %q not found", needle)
	return 0
}

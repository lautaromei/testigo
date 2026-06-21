// Package boundarymut is the offline "teacher" mutation oracle for testigo's
// boundary detectors (AUDIT_PLAN §9, §3.1). Stock mutators (Gremlins,
// go-mutesting) only generate relational/arithmetic operators, which validate
// the relational detectors but never the interaction detectors testigo targets.
// This package adds the missing operator classes:
//
//	DROP_CALL     — delete a call statement      (validates drop-call / drop-emit: detectors 6, 9)
//	DROP_EVENT    — drop a call but blank its args (validates effect-reached-unchecked:
//	                edgecov; viable where DROP_CALL leaves the produced value orphaned)
//	DUP_CALL      — duplicate a call statement    (validates dup-call: detector 4)
//	SWAP_CALL     — swap two adjacent call stmts  (validates reorder: detector 7)
//	REWIRE_CALLEE — swap the callee of two adjacent calls, args fixed (validates
//	                edge-not-observed: edgecov; redirects edge A→t1 to A→t2 — the
//	                wrong-target fault an unobserved edge predicts no test catches)
//
// It mutates one covered call statement at a time, runs the package's test
// suite, and records whether the mutant was KILLED (a test failed) or LIVED (a
// surviving mutant — a real test gap). It never runs in the audit runtime path.
package boundarymut

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Operator names the mutation class applied at a site.
type Operator string

const (
	DropCall     Operator = "DROP_CALL"
	DropEvent    Operator = "DROP_EVENT"
	DupCall      Operator = "DUP_CALL"
	SwapCall     Operator = "SWAP_CALL"
	RewireCallee Operator = "REWIRE_CALLEE"
)

// Status is the oracle label for a mutant.
type Status string

const (
	Killed     Status = "KILLED"      // an assertion failed → the mutant was caught
	Crashed    Status = "CRASHED"     // killed only by a panic/timeout, not an assertion
	Lived      Status = "LIVED"       // tests passed → surviving mutant (a gap)
	NotViable  Status = "NOT_VIABLE"  // mutated source failed to compile
	NotCovered Status = "NOT_COVERED" // site is not executed by any test
)

// Mutant is one enumerated-and-evaluated mutation site.
type Mutant struct {
	File     string   `json:"file"`     // base filename
	Line     int      `json:"line"`     // 1-based line of the call statement
	Column   int      `json:"column"`   // 1-based column
	Operator Operator `json:"operator"` // mutation class
	Method   string   `json:"method"`   // callee name — aligns to audit finding.site
	Status   Status   `json:"status"`
}

// Result is the full per-package oracle output.
type Result struct {
	Package string   `json:"package"`
	Mutants []Mutant `json:"mutants"`
}

// Summary counts mutants by status.
func (r Result) Summary() map[Status]int {
	out := map[Status]int{}
	for _, m := range r.Mutants {
		out[m.Status]++
	}
	return out
}

// site is an internal enumeration record locating a call statement by its
// deterministic position so it can be re-found in a freshly parsed file.
type site struct {
	absFile  string
	base     string
	blockOrd int // index of the enclosing block in deterministic walk order
	stmtIdx  int // index of the statement within that block
	line     int
	column   int
	method   string
	hasNext  bool // next sibling is also a call statement (enables SWAP)
	bareCall bool // statement is a bare ExprStmt call (enables DROP_EVENT)
}

// Options configures a run.
type Options struct {
	Dir     string        // package directory to mutate
	Timeout time.Duration // per-mutant test timeout (default 60s)

	// ProjectRoot, when set, judges each mutant against the whole project test
	// suite (`go test ./...` from the module root with project-wide coverage)
	// instead of only Dir's own package. This catches orchestration mutants that
	// a cross-package integration test kills — the per-package suite would miss
	// them and mislabel the mutant LIVED.
	ProjectRoot string
}

// Run enumerates and evaluates boundary mutants for the package in opts.Dir.
//
// It mutates source files in place, one mutant at a time, and always restores
// the original bytes before returning (including on panic). Only mutants on
// covered lines are executed; others are reported as NOT_COVERED.
func Run(opts Options) (Result, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}

	// Resolve the test scope: per-package (`.` in Dir) or project-wide
	// (`./...` from the module root).
	testDir, pattern, coverpkg := dir, ".", ""
	if opts.ProjectRoot != "" {
		root, err := filepath.Abs(opts.ProjectRoot)
		if err != nil {
			return Result{}, err
		}
		testDir, pattern, coverpkg = root, "./...", "./..."
	}

	files, err := goSourceFiles(dir)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, fmt.Errorf("no non-test .go files in %s", dir)
	}

	covered, err := coveredLines(testDir, pattern, coverpkg, opts.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("coverage: %w", err)
	}

	// Snapshot originals; guarantee restore.
	originals := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return Result{}, err
		}
		originals[f] = b
	}
	restoreAll := func() {
		for f, b := range originals {
			_ = os.WriteFile(f, b, 0o644)
		}
	}
	defer restoreAll()

	var sites []site
	for _, f := range files {
		fs, err := enumerate(f, originals[f])
		if err != nil {
			return Result{}, err
		}
		sites = append(sites, fs...)
	}

	res := Result{Package: dir}
	for _, s := range sites {
		ops := []Operator{DropCall, DupCall}
		if s.bareCall {
			ops = append(ops, DropEvent)
		}
		if s.hasNext {
			ops = append(ops, SwapCall, RewireCallee)
		}
		for _, op := range ops {
			m := Mutant{File: s.base, Line: s.line, Column: s.column, Operator: op, Method: s.method}
			if cov := covered[s.base]; cov == nil || !cov[s.line] {
				m.Status = NotCovered
				res.Mutants = append(res.Mutants, m)
				continue
			}
			mutated, ok := apply(s, op, originals[s.absFile])
			if !ok {
				m.Status = NotViable
				res.Mutants = append(res.Mutants, m)
				continue
			}
			if err := os.WriteFile(s.absFile, mutated, 0o644); err != nil {
				return res, err
			}
			m.Status = runSuite(testDir, pattern, opts.Timeout)
			_ = os.WriteFile(s.absFile, originals[s.absFile], 0o644) // restore between mutants
			res.Mutants = append(res.Mutants, m)
		}
	}
	sort.Slice(res.Mutants, func(i, j int) bool {
		a, b := res.Mutants[i], res.Mutants[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Operator < b.Operator
	})
	return res, nil
}

// WriteJSON serializes a Result to w-friendly bytes.
func (r Result) WriteJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func goSourceFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, n))
	}
	sort.Strings(out)
	return out, nil
}

// collectBlocks returns every block statement in deterministic source order so
// a site located during enumeration can be re-found in a fresh parse.
func collectBlocks(file *ast.File) []*ast.BlockStmt {
	var blocks []*ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		if b, ok := n.(*ast.BlockStmt); ok {
			blocks = append(blocks, b)
		}
		return true
	})
	return blocks
}

// stmtCallee reports the primary effectful callee of a statement that can be
// dropped/duplicated/swapped as a whole unit, and whether the statement is such
// a site. It recognizes the idioms that carry a boundary interaction:
//
//	foo(...)                         bare call
//	if err := foo(...); err != nil   the canonical error-checked emit
//	go foo(...) / defer foo(...)
func stmtCallee(s ast.Stmt) (string, bool) {
	switch x := s.(type) {
	case *ast.ExprStmt:
		if call, ok := x.X.(*ast.CallExpr); ok {
			return calleeName(call), true
		}
	case *ast.GoStmt:
		return calleeName(x.Call), true
	case *ast.DeferStmt:
		return calleeName(x.Call), true
	case *ast.IfStmt:
		if as, ok := x.Init.(*ast.AssignStmt); ok && len(as.Rhs) == 1 {
			if call, ok := as.Rhs[0].(*ast.CallExpr); ok {
				return calleeName(call), true
			}
		}
	}
	return "", false
}

// exprStmtCall returns the CallExpr of a bare expression-statement call, or nil
// if the statement is not of that form. These are the sites DROP_EVENT targets.
func exprStmtCall(s ast.Stmt) *ast.CallExpr {
	if es, ok := s.(*ast.ExprStmt); ok {
		if call, ok := es.X.(*ast.CallExpr); ok {
			return call
		}
	}
	return nil
}

// stmtCall returns the primary CallExpr carried by a statement in the same idioms
// stmtCallee recognizes (bare call, go/defer, error-checked if-init), or nil. It
// gives REWIRE_CALLEE the handle to swap a call's Fun expression in place.
func stmtCall(s ast.Stmt) *ast.CallExpr {
	switch x := s.(type) {
	case *ast.ExprStmt:
		if call, ok := x.X.(*ast.CallExpr); ok {
			return call
		}
	case *ast.GoStmt:
		return x.Call
	case *ast.DeferStmt:
		return x.Call
	case *ast.IfStmt:
		if as, ok := x.Init.(*ast.AssignStmt); ok && len(as.Rhs) == 1 {
			if call, ok := as.Rhs[0].(*ast.CallExpr); ok {
				return call
			}
		}
	}
	return nil
}

func calleeName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}

func enumerate(absFile string, src []byte) ([]site, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absFile, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(absFile)
	var out []site
	for bo, b := range collectBlocks(file) {
		for i, stmt := range b.List {
			method, ok := stmtCallee(stmt)
			if !ok {
				continue
			}
			pos := fset.Position(stmt.Pos())
			_, hasNext := stmtCallee(nextStmt(b.List, i))
			out = append(out, site{
				absFile:  absFile,
				base:     base,
				blockOrd: bo,
				stmtIdx:  i,
				line:     pos.Line,
				column:   pos.Column,
				method:   method,
				hasNext:  hasNext,
				bareCall: exprStmtCall(stmt) != nil,
			})
		}
	}
	return out, nil
}

func nextStmt(list []ast.Stmt, i int) ast.Stmt {
	if i+1 < len(list) {
		return list[i+1]
	}
	return nil
}

// apply re-parses src, locates the site by (blockOrd, stmtIdx), applies op, and
// returns the printed mutated source. ok is false if the site cannot be located
// or the operator is inapplicable.
func apply(s site, op Operator, src []byte) ([]byte, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, s.absFile, src, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	blocks := collectBlocks(file)
	if s.blockOrd >= len(blocks) {
		return nil, false
	}
	b := blocks[s.blockOrd]
	if s.stmtIdx >= len(b.List) {
		return nil, false
	}
	switch op {
	case DropCall:
		b.List = append(b.List[:s.stmtIdx:s.stmtIdx], b.List[s.stmtIdx+1:]...)
	case DropEvent:
		// Drop the call's effect but keep the source compilable by consuming the
		// argument expressions through blanks. This reaches writeJSON-style sites
		// whose produced value would be left orphaned by a plain DROP_CALL.
		call := exprStmtCall(b.List[s.stmtIdx])
		if call == nil {
			return nil, false
		}
		if len(call.Args) == 0 {
			b.List = append(b.List[:s.stmtIdx:s.stmtIdx], b.List[s.stmtIdx+1:]...)
			break
		}
		lhs := make([]ast.Expr, len(call.Args))
		for i := range call.Args {
			lhs[i] = ast.NewIdent("_")
		}
		b.List[s.stmtIdx] = &ast.AssignStmt{Lhs: lhs, Tok: token.ASSIGN, Rhs: call.Args}
	case DupCall:
		dup := b.List[s.stmtIdx]
		nl := make([]ast.Stmt, 0, len(b.List)+1)
		nl = append(nl, b.List[:s.stmtIdx+1]...)
		nl = append(nl, dup)
		nl = append(nl, b.List[s.stmtIdx+1:]...)
		b.List = nl
	case SwapCall:
		if s.stmtIdx+1 >= len(b.List) {
			return nil, false
		}
		b.List[s.stmtIdx], b.List[s.stmtIdx+1] = b.List[s.stmtIdx+1], b.List[s.stmtIdx]
	case RewireCallee:
		// Redirect each call's target without moving its statement or arguments:
		// swap the Fun expressions of the two adjacent calls. A→t1 becomes A→t2 and
		// vice versa. Compiles only when the signatures line up; a mismatch fails to
		// build and is reported NOT_VIABLE — same graceful path as DROP_CALL.
		if s.stmtIdx+1 >= len(b.List) {
			return nil, false
		}
		c1, c2 := stmtCall(b.List[s.stmtIdx]), stmtCall(b.List[s.stmtIdx+1])
		if c1 == nil || c2 == nil {
			return nil, false
		}
		if c1.Fun == c2.Fun {
			return nil, false // same callee — rewire is a no-op
		}
		c1.Fun, c2.Fun = c2.Fun, c1.Fun
	default:
		return nil, false
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// runSuite runs the package tests once and classifies the outcome. A build
// failure is NotViable; a test failure is Killed; a pass is Lived.
func runSuite(dir, pattern string, timeout time.Duration) Status {
	cmd := exec.Command("go", "test", "-mod=mod", "-count=1",
		"-timeout", timeout.String(), pattern)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return Lived
	}
	if bytes.Contains(out, []byte("[build failed]")) ||
		bytes.Contains(out, []byte("# ")) && bytes.Contains(out, []byte(".go:")) &&
			!bytes.Contains(out, []byte("--- FAIL")) {
		return NotViable
	}
	// A panic/timeout/deadlock caught the mutant by crashing execution, not by an
	// assertion reading the mutated value — note go test still co-prints
	// "--- FAIL" for the panicking test, so we key off the crash markers, not its
	// absence. The checked-coverage signal models asserted-value dependence only,
	// so these are tracked apart to keep the eval comparing like with like.
	if bytes.Contains(out, []byte("panic:")) ||
		bytes.Contains(out, []byte("test timed out")) ||
		bytes.Contains(out, []byte("fatal error:")) {
		return Crashed
	}
	return Killed
}

var coverRe = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)

// coveredLines runs the suite once with coverage and returns covered lines
// keyed by base filename, mirroring checkedcovssa's profile reader.
func coveredLines(dir, pattern, coverpkg string, timeout time.Duration) (map[string]map[int]bool, error) {
	tmp := filepath.Join(os.TempDir(), "boundarymut-cover.out")
	args := []string{"test", "-mod=mod", "-covermode=set",
		"-timeout", timeout.String(), "-coverprofile=" + tmp}
	if coverpkg != "" {
		args = append(args, "-coverpkg="+coverpkg)
	}
	args = append(args, pattern)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: coverage `go test` non-zero (may be partial):\n%s\n",
			strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return nil, fmt.Errorf("no cover profile: %v", err)
	}
	covered := map[string]map[int]bool{}
	for _, ln := range strings.Split(string(data), "\n") {
		m := coverRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		base := filepath.Base(m[1])
		start, _ := strconv.Atoi(m[2])
		end, _ := strconv.Atoi(m[3])
		count, _ := strconv.Atoi(m[4])
		if count == 0 {
			continue
		}
		if covered[base] == nil {
			covered[base] = map[int]bool{}
		}
		for i := start; i <= end; i++ {
			covered[base][i] = true
		}
	}
	return covered, nil
}

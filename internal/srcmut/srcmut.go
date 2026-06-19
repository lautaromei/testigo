// Package srcmut is the offline expression-level mutation oracle for testigo's
// Variation detectors (AUDIT_PLAN §9). The boundary oracle (internal/boundarymut)
// only generates whole-statement drop/dup/reorder mutants, which validate the
// interaction detectors; this package adds the argument-level operators those
// detectors' Variation siblings target:
//
//	VALUE_MUT    — perturb a literal call argument   (validates literal-pinned-once: detector 17)
//	ARG_CORRUPT  — replace a call argument with its   (validates unpinned-arg: detector 2)
//	               type's zero value
//
// A mutant LIVES when the suite still passes after the argument is corrupted —
// i.e. no test pins that argument, exactly the gap the Variation detectors
// predict. Mutants are located by (callee method, argument index), aligning to
// the audit finding sites of the form "Type.Method arg#N".
package srcmut

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

// Operator names the mutation class.
type Operator string

const (
	ValueMut      Operator = "VALUE_MUT"
	ArgCorrupt    Operator = "ARG_CORRUPT"
	ReturnCorrupt Operator = "RETURN_CORRUPT"
	ArgSwap       Operator = "ARG_SWAP" // swap two same-typed args (validates argument-swap-blind)
)

// Status is the oracle label for a mutant.
type Status string

const (
	Killed     Status = "KILLED"
	Lived      Status = "LIVED"
	NotViable  Status = "NOT_VIABLE"
	NotCovered Status = "NOT_COVERED"
)

// Mutant is one enumerated-and-evaluated argument mutation.
type Mutant struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Column   int      `json:"column"`
	Operator Operator `json:"operator"`
	Method   string   `json:"method"`    // callee — aligns to finding site
	ArgIndex int      `json:"arg_index"` // 0-based argument index
	Status   Status   `json:"status"`
}

// Result is the per-package oracle output.
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

// Options configures a run.
type Options struct {
	Dir     string
	Timeout time.Duration
}

type site struct {
	absFile  string
	base     string
	line     int
	column   int
	method   string
	operator Operator
	argIdx   int // ARG_CORRUPT/VALUE_MUT: argument index

	// ARG_CORRUPT / VALUE_MUT locate by call ordinal + replace an argument.
	// ARG_SWAP locates by call ordinal + swaps argIdx with argIdx2.
	callOrd     int
	argIdx2     int    // ARG_SWAP: second argument index to swap with argIdx
	replacement string // source text to substitute for the argument

	// RETURN_CORRUPT locates by block + statement index and inserts a statement.
	blockOrd int
	stmtIdx  int
	insert   string // source statement inserted after the assignment
}

// Run enumerates and evaluates argument mutants for the package in opts.Dir.
// Source files are mutated one at a time and always restored.
func Run(opts Options) (Result, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}

	sites, files, err := enumerate(dir)
	if err != nil {
		return Result{}, err
	}
	covered, err := coveredLines(dir, opts.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("coverage: %w", err)
	}

	originals := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return Result{}, err
		}
		originals[f] = b
	}
	defer func() {
		for f, b := range originals {
			_ = os.WriteFile(f, b, 0o644)
		}
	}()

	res := Result{Package: dir}
	for _, s := range sites {
		m := Mutant{File: s.base, Line: s.line, Column: s.column, Operator: s.operator, Method: s.method, ArgIndex: s.argIdx}
		if cov := covered[s.base]; cov == nil || !cov[s.line] {
			m.Status = NotCovered
			res.Mutants = append(res.Mutants, m)
			continue
		}
		mutated, ok := apply(s, originals[s.absFile])
		if !ok {
			m.Status = NotViable
			res.Mutants = append(res.Mutants, m)
			continue
		}
		if err := os.WriteFile(s.absFile, mutated, 0o644); err != nil {
			return res, err
		}
		m.Status = runSuite(dir, opts.Timeout)
		_ = os.WriteFile(s.absFile, originals[s.absFile], 0o644)
		res.Mutants = append(res.Mutants, m)
	}
	sort.Slice(res.Mutants, func(i, j int) bool {
		a, b := res.Mutants[i], res.Mutants[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.ArgIndex != b.ArgIndex {
			return a.ArgIndex < b.ArgIndex
		}
		return a.Operator < b.Operator
	})
	return res, nil
}

// WriteJSON serializes a Result.
func (r Result) WriteJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// enumerate type-checks the package and collects argument mutation sites for
// every call to a selector method (x.Method(args...)) in non-test source.
func enumerate(dir string) ([]site, []string, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:        dir,
		BuildFlags: []string{"-mod=mod"},
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, nil, err
	}
	var sites []site
	fileSet := map[string]bool{}
	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		for i, f := range p.Syntax {
			abs := p.CompiledGoFiles[i]
			if strings.HasSuffix(abs, "_test.go") {
				continue
			}
			base := filepath.Base(abs)
			qual := types.RelativeTo(p.Types)

			// Argument-level mutants (VALUE_MUT / ARG_CORRUPT) on selector calls.
			for ord, call := range collectCalls(f) {
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				method := sel.Sel.Name
				for ai, arg := range call.Args {
					pos := p.Fset.Position(arg.Pos())
					if v, ok := valueMutation(arg); ok {
						sites = append(sites, site{absFile: abs, base: base, line: pos.Line, column: pos.Column, method: method, operator: ValueMut, argIdx: ai, callOrd: ord, replacement: v})
						fileSet[abs] = true
					}
					if z, ok := zeroValue(p.TypesInfo.TypeOf(arg), qual); ok {
						sites = append(sites, site{absFile: abs, base: base, line: pos.Line, column: pos.Column, method: method, operator: ArgCorrupt, argIdx: ai, callOrd: ord, replacement: z})
						fileSet[abs] = true
					}
				}

				// ARG_SWAP: swap two arguments of the same static type. Models
				// the "wrong variable / swapped argument" fault (transfer(to,
				// from)); it survives unless a test pins the two positions to
				// distinct values.
				for ai := 0; ai < len(call.Args); ai++ {
					for aj := ai + 1; aj < len(call.Args); aj++ {
						ti, tj := p.TypesInfo.TypeOf(call.Args[ai]), p.TypesInfo.TypeOf(call.Args[aj])
						if ti == nil || tj == nil || !types.Identical(ti, tj) {
							continue
						}
						if renderExpr(p.Fset, call.Args[ai]) == renderExpr(p.Fset, call.Args[aj]) {
							continue // swap would be a no-op
						}
						pos := p.Fset.Position(call.Args[ai].Pos())
						sites = append(sites, site{absFile: abs, base: base, line: pos.Line, column: pos.Column, method: method, operator: ArgSwap, argIdx: ai, argIdx2: aj, callOrd: ord})
						fileSet[abs] = true
					}
				}
			}

			// Return-corruption mutants: after `x[, err] := obj.M(...)`, overwrite
			// a non-error result with its zero value.
			for bo, b := range collectBlocks(f) {
				for si, stmt := range b.List {
					method, lhs, ok := returnAssign(stmt)
					if !ok {
						continue
					}
					for _, name := range lhs {
						if name.id == "_" {
							continue
						}
						t := p.TypesInfo.TypeOf(name.expr)
						if isErrorType(t) {
							continue // force-error's job, not return-corruption
						}
						z, ok := zeroValue(t, qual)
						if !ok {
							continue
						}
						pos := p.Fset.Position(name.expr.Pos())
						sites = append(sites, site{absFile: abs, base: base, line: pos.Line, column: pos.Column, method: method, operator: ReturnCorrupt, blockOrd: bo, stmtIdx: si, insert: name.id + " = " + z})
						fileSet[abs] = true
					}
				}
			}
		}
	}
	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	sort.Strings(files)
	return sites, files, nil
}

// collectCalls returns every call expression in deterministic source order.
func collectCalls(file *ast.File) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			calls = append(calls, c)
		}
		return true
	})
	return calls
}

// collectBlocks returns every block statement in deterministic source order.
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

type lhsName struct {
	id   string
	expr ast.Expr
}

// returnAssign reports whether stmt is `lhs... := obj.Method(args)` (single
// selector-call RHS) and returns the callee method name and the LHS names.
func returnAssign(stmt ast.Stmt) (string, []lhsName, bool) {
	as, ok := stmt.(*ast.AssignStmt)
	if !ok || len(as.Rhs) != 1 {
		return "", nil, false
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil, false
	}
	var names []lhsName
	for _, l := range as.Lhs {
		id, ok := l.(*ast.Ident)
		if !ok {
			return "", nil, false
		}
		names = append(names, lhsName{id: id.Name, expr: l})
	}
	return sel.Sel.Name, names, true
}

func isErrorType(t types.Type) bool {
	if t == nil {
		return false
	}
	return t.String() == "error"
}

// valueMutation returns a perturbed literal for a BasicLit argument.
func valueMutation(arg ast.Expr) (string, bool) {
	lit, ok := arg.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	switch lit.Kind {
	case token.STRING:
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return "", false
		}
		return strconv.Quote(s + "_mut"), true
	case token.INT:
		n, err := strconv.ParseInt(lit.Value, 0, 64)
		if err != nil {
			return "", false
		}
		return strconv.FormatInt(n+1, 10), true
	case token.FLOAT:
		f, err := strconv.ParseFloat(lit.Value, 64)
		if err != nil {
			return "", false
		}
		return strconv.FormatFloat(f+1, 'g', -1, 64), true
	}
	return "", false
}

// zeroValue renders the zero value of t as source, when it can be expressed
// without risk. qual qualifies named types relative to the package being
// mutated (so same-package types are unqualified). Returns ok=false for types
// it cannot safely render.
func zeroValue(t types.Type, qual types.Qualifier) (string, bool) {
	if t == nil {
		return "", false
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return "false", true
		case u.Info()&types.IsString != 0:
			return `""`, true
		case u.Info()&types.IsNumeric != 0:
			return "0", true
		}
	case *types.Pointer, *types.Interface, *types.Slice, *types.Map,
		*types.Chan, *types.Signature:
		return "nil", true
	case *types.Struct:
		// Render the named type as T{} (correctly qualified for the file's
		// package); bare anonymous struct{} is rare and skipped.
		if _, ok := t.(*types.Named); ok {
			return types.TypeString(t, qual) + "{}", true
		}
	}
	return "", false
}

// apply re-parses src, finds the call by ordinal, replaces argument argIdx with
// the site's replacement text, and prints the mutated file.
func apply(s site, src []byte) ([]byte, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, s.absFile, src, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	if s.operator == ReturnCorrupt {
		blocks := collectBlocks(file)
		if s.blockOrd >= len(blocks) {
			return nil, false
		}
		b := blocks[s.blockOrd]
		if s.stmtIdx >= len(b.List) {
			return nil, false
		}
		stmt, err := parseStmt(s.insert)
		if err != nil {
			return nil, false
		}
		nl := make([]ast.Stmt, 0, len(b.List)+1)
		nl = append(nl, b.List[:s.stmtIdx+1]...)
		nl = append(nl, stmt)
		nl = append(nl, b.List[s.stmtIdx+1:]...)
		b.List = nl
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, file); err != nil {
			return nil, false
		}
		return buf.Bytes(), true
	}

	calls := collectCalls(file)
	if s.callOrd >= len(calls) {
		return nil, false
	}
	call := calls[s.callOrd]
	if s.argIdx >= len(call.Args) {
		return nil, false
	}
	if s.operator == ArgSwap {
		if s.argIdx2 >= len(call.Args) {
			return nil, false
		}
		call.Args[s.argIdx], call.Args[s.argIdx2] = call.Args[s.argIdx2], call.Args[s.argIdx]
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, file); err != nil {
			return nil, false
		}
		return buf.Bytes(), true
	}
	repl, err := parser.ParseExpr(s.replacement)
	if err != nil {
		return nil, false
	}
	call.Args[s.argIdx] = repl
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// renderExpr prints an expression to source text for no-op swap detection.
func renderExpr(fset *token.FileSet, e ast.Expr) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, e); err != nil {
		return ""
	}
	return buf.String()
}

// parseStmt parses a single Go statement by wrapping it in a throwaway func.
func parseStmt(src string) (ast.Stmt, error) {
	wrapped := "package p\nfunc _(){\n" + src + "\n}\n"
	f, err := parser.ParseFile(token.NewFileSet(), "", wrapped, 0)
	if err != nil {
		return nil, err
	}
	body := f.Decls[0].(*ast.FuncDecl).Body
	if len(body.List) != 1 {
		return nil, fmt.Errorf("expected 1 statement, got %d", len(body.List))
	}
	return body.List[0], nil
}

func runSuite(dir string, timeout time.Duration) Status {
	cmd := exec.Command("go", "test", "-mod=mod", "-count=1", "-timeout", timeout.String(), ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return Lived
	}
	if bytes.Contains(out, []byte("[build failed]")) ||
		(bytes.Contains(out, []byte(".go:")) && !bytes.Contains(out, []byte("--- FAIL"))) {
		return NotViable
	}
	return Killed
}

var coverRe = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)

func coveredLines(dir string, timeout time.Duration) (map[string]map[int]bool, error) {
	tmp := filepath.Join(os.TempDir(), "srcmut-cover.out")
	cmd := exec.Command("go", "test", "-mod=mod", "-covermode=set", "-timeout", timeout.String(), "-coverprofile="+tmp, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: coverage `go test` non-zero:\n%s\n", strings.TrimSpace(string(out)))
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

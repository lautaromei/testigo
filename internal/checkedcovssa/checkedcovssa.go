// Package checkedcovssa implements the SSA-based checked-coverage detector
// used by the testigo CLI.
//
// It answers: which statements does the suite execute but never let influence
// an asserted return value?
package checkedcovssa

import (
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Finding is one covered-but-unchecked statement-line.
type Finding struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Func      string `json:"func"`
	Statement string `json:"statement"`
	Reason    string `json:"reason"`
}

// Report is the structured result of a checked-coverage analysis.
type Report struct {
	Package        string    `json:"package"`
	OracleCalls    int       `json:"oracle_calls"`
	SeedValues     int       `json:"seed_values"`
	CoveredLines   int       `json:"covered_lines"`
	UncheckedLines int       `json:"unchecked_lines"`
	UncheckedPct   int       `json:"unchecked_pct"`
	Findings       []Finding `json:"findings"`

	// covered/checked per base file, after the structural filter. Exposed so
	// callers (the mutation eval harness) can label an arbitrary line.
	covered    map[string]map[int]bool
	checked    map[string]map[int]bool
	checkedAbs map[string]map[int]bool
}

// IsCovered reports whether base:line is a covered, non-structural statement.
func (r Report) IsCovered(base string, line int) bool {
	return r.covered[base] != nil && r.covered[base][line]
}

// IsChecked reports whether base:line feeds an asserted value.
func (r Report) IsChecked(base string, line int) bool {
	return r.checked[base] != nil && r.checked[base][line]
}

// IsCheckedFile reports whether abs:line feeds an asserted value. Unlike
// IsChecked, it keys by normalized path and is safe for whole-project analyses
// where many packages contain files with the same basename.
func (r Report) IsCheckedFile(abs string, line int) bool {
	key, err := filepath.Abs(abs)
	if err != nil {
		key = filepath.Clean(abs)
	} else {
		key = filepath.Clean(key)
	}
	return r.checkedAbs[key] != nil && r.checkedAbs[key][line]
}

// Run executes the checked-coverage analysis for dir and prints a text report.
func Run(dir string) error {
	rep, err := Analyze(dir)
	if err != nil {
		return err
	}
	fmt.Print(rep.Text())
	return nil
}

// Analyze runs the checked-coverage analysis for the package directory dir and
// returns the structured report.
func Analyze(dir string) (Report, error) {
	if strings.TrimSpace(dir) == "" {
		return Report{}, fmt.Errorf("usage: checkedcov <package-dir>")
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Tests:      true,
		Dir:        dir,
		BuildFlags: []string{"-mod=mod"},
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return Report{}, fmt.Errorf("load: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		fmt.Fprintln(os.Stderr, "warning: type errors above; analysis may be partial")
	}
	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()
	fset := prog.Fset

	allFns := ssautil.AllFunctions(prog)

	// Resolve dynamic dispatch (interface invokes + func-value calls) once, via
	// RTA over the test entry points. Without this the backward slice dies at
	// every `StaticCallee()==nil` call — the dominant blind spot for code reached
	// only through interfaces (e.g. an http.ResponseWriter threaded through
	// ServeHTTP, whose .Code/.Body writes the test asserts). dynCallees maps each
	// non-static call to the concrete callees actually instantiated by the suite.
	inScope := map[*types.Package]bool{}
	for _, p := range pkgs {
		if p.Types != nil {
			inScope[p.Types] = true
		}
	}
	dynCallees := resolveDynamicCallees(allFns, prog, inScope)

	callSites := map[*ssa.Function][]*ssa.Call{}
	returns := map[*ssa.Function][]*ssa.Return{}
	storesByLoc := map[memKey][]storeRef{}
	storeKeysByRoot := map[ssa.Value][]memKey{}
	paramStores := map[*ssa.Function]map[int]map[string]bool{}
	closureBinds := map[*ssa.FreeVar][]ssa.Value{}
	controlDeps := map[*ssa.BasicBlock][]ssa.Value{}
	// argMutators maps a value to the positions of calls that receive it as a
	// mutable argument (slice/map). Such a call can write through the
	// argument, so if the value reaches an asserted value the call influenced it
	// — e.g. slices.SortStableFunc(out, cmp) shapes the asserted `out`.
	argMutators := map[ssa.Value][]token.Pos{}
	for fn := range allFns {
		for b, deps := range fnControlDeps(fn) {
			controlDeps[b] = append(controlDeps[b], deps...)
		}
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				switch x := instr.(type) {
				case *ssa.Call:
					if callee := x.Call.StaticCallee(); callee != nil {
						callSites[callee] = append(callSites[callee], x)
					}
					// delete(m, k) is a builtin store: it mutates m's root.
					if bi, ok := x.Call.Value.(*ssa.Builtin); ok && bi.Name() == "delete" && len(x.Call.Args) > 0 {
						k := addrKey(x.Call.Args[0])
						storesByLoc[k] = append(storesByLoc[k], storeRef{nil, x.Pos()})
						storeKeysByRoot[k.root] = append(storeKeysByRoot[k.root], k)
					}
					// Record every mutable argument as a potential in-place writer.
					for _, a := range x.Call.Args {
						if a != nil && isMutableArg(a.Type()) {
							argMutators[a] = append(argMutators[a], x.Pos())
						}
					}
				case *ssa.Return:
					returns[fn] = append(returns[fn], x)
				case *ssa.Store:
					k := addrKey(x.Addr)
					storesByLoc[k] = append(storesByLoc[k], storeRef{x.Val, x.Pos()})
					storeKeysByRoot[k.root] = append(storeKeysByRoot[k.root], k)
					recordParamStore(paramStores, fn, k)
				case *ssa.MapUpdate:
					k := addrKey(x.Map)
					storesByLoc[k] = append(storesByLoc[k], storeRef{x.Value, x.Pos()})
					storeKeysByRoot[k.root] = append(storeKeysByRoot[k.root], k)
					recordParamStore(paramStores, fn, k)
				case *ssa.MakeClosure:
					if callee, ok := x.Fn.(*ssa.Function); ok {
						for i, fv := range callee.FreeVars {
							if i < len(x.Bindings) {
								closureBinds[fv] = append(closureBinds[fv], x.Bindings[i])
							}
						}
					}
				}
			}
		}
	}

	// Register dynamically-resolved call sites too, so args flow back into the
	// concrete callees' parameters. For an interface invoke the receiver is the
	// concrete method's parameter 0 (see callArg), so e.g. `w.WriteHeader(s)`
	// resolves to (*ResponseRecorder).WriteHeader and lets the .Code write flow.
	for call, callees := range dynCallees {
		for _, callee := range callees {
			callSites[callee] = append(callSites[callee], call)
		}
	}

	// Summarize field writes through function parameters and propagate them
	// through direct calls. This keeps heap-boundary checks field-sensitive:
	// asserting rec.Code marks calls that write Code, but not calls that only
	// write rec.HeaderMap.
	for changed := true; changed; {
		changed = false
		for fn := range allFns {
			for _, b := range fn.Blocks {
				for _, instr := range b.Instrs {
					c, ok := instr.(*ssa.Call)
					if !ok {
						continue
					}
					for _, callee := range calleesOf(c, dynCallees) {
						for argIdx, paths := range paramStores[callee] {
							arg, ok := callArg(c, argIdx)
							if !ok {
								continue
							}
							argKey, ok := valueKey(arg)
							if !ok {
								continue
							}
							param, ok := argKey.root.(*ssa.Parameter)
							if !ok || param.Parent() != fn {
								continue
							}
							callerIdx := paramIndex(fn, param)
							if callerIdx < 0 {
								continue
							}
							for path := range paths {
								if addParamStore(paramStores, fn, callerIdx, joinPath(argKey.path, path)) {
									changed = true
								}
							}
						}
					}
				}
			}
		}
	}

	callMutators := map[memKey][]callMutation{}
	for fn := range allFns {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				c, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				for _, callee := range calleesOf(c, dynCallees) {
					for argIdx, paths := range paramStores[callee] {
						if argIdx >= len(callee.Params) {
							continue
						}
						arg, ok := callArg(c, argIdx)
						if !ok {
							continue
						}
						argKey, ok := valueKey(arg)
						if !ok {
							continue
						}
						for path := range paths {
							k := memKey{root: argKey.root, path: joinPath(argKey.path, path)}
							callMutators[k] = append(callMutators[k], callMutation{
								pos:   c.Pos(),
								param: callee.Params[argIdx],
								path:  path,
							})
						}
					}
				}
			}
		}
	}

	// A captured variable is one alloc in the enclosing function but a distinct
	// FreeVar root inside each closure, so stores through the FreeVar are keyed
	// apart from loads of the alloc. Mirror every FreeVar's store keys onto the
	// alloc(s) it binds, so a parent load of the captured value sees the closure's
	// writes (e.g. `s += p` inside the closure feeds the asserted `s`).
	for fv, bindings := range closureBinds {
		keys := storeKeysByRoot[fv]
		if len(keys) == 0 {
			continue
		}
		for _, b := range bindings {
			storeKeysByRoot[b] = append(storeKeysByRoot[b], keys...)
		}
	}

	// Resolve indirect calls through a func-typed parameter to the closures /
	// functions bound at the enclosing function's static call sites, and register
	// the indirect call as a call site of each target. This lets the backward
	// slice flow the call's arguments into the closure body — so e.g. a test's
	// `r.Do(func(v){ got = append(got, v) })` connects the asserted `got` back to
	// the `f(p.Value)` call inside Do, marking it checked.
	for fn := range allFns {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				ic, ok := instr.(*ssa.Call)
				if !ok || ic.Call.StaticCallee() != nil {
					continue
				}
				// In NaiveForm a parameter is spilled to a local, so the callee
				// value is usually a load of that local rather than the bare
				// *ssa.Parameter. Resolve through the spill to the parameter.
				p := resolveFuncParam(ic.Call.Value, storesByLoc)
				if p == nil {
					continue
				}
				parent := p.Parent()
				idx := -1
				for i, pp := range parent.Params {
					if pp == p {
						idx = i
						break
					}
				}
				if idx < 0 {
					continue
				}
				for _, caller := range callSites[parent] {
					arg, ok := callArg(caller, idx)
					if !ok {
						continue
					}
					var target *ssa.Function
					switch a := arg.(type) {
					case *ssa.MakeClosure:
						target, _ = a.Fn.(*ssa.Function)
					case *ssa.Function:
						target = a
					}
					if target != nil {
						callSites[target] = append(callSites[target], ic)
					}
				}
			}
		}
	}

	var seeds []workItem
	oracleCalls := 0
	seedControl := func(b *ssa.BasicBlock) {
		for _, dep := range controlDeps[b] {
			seeds = append(seeds, workItem{v: dep})
		}
		for d := b.Idom(); d != nil; d = d.Idom() {
			if n := len(d.Instrs); n > 0 {
				if ifi, ok := d.Instrs[n-1].(*ssa.If); ok {
					seeds = append(seeds, workItem{v: ifi.Cond})
				}
			}
		}
	}
	for fn := range allFns {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				c, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				hit, ok := recognize(c)
				if !ok {
					continue
				}
				oracleCalls++
				for _, a := range hit.args {
					seeds = append(seeds, workItem{v: a})
				}
				if hit.seedControl {
					seedControl(b)
				}
			}
		}
	}

	checked := map[string]map[int]bool{}
	checkedAbs := map[string]map[int]bool{}
	record := func(pos token.Pos) {
		if !pos.IsValid() {
			return
		}
		p := fset.Position(pos)
		if p.Filename == "" {
			return
		}
		base := filepath.Base(p.Filename)
		if checked[base] == nil {
			checked[base] = map[int]bool{}
		}
		checked[base][p.Line] = true
		abs, err := filepath.Abs(p.Filename)
		if err != nil {
			abs = filepath.Clean(p.Filename)
		} else {
			abs = filepath.Clean(abs)
		}
		if checkedAbs[abs] == nil {
			checkedAbs[abs] = map[int]bool{}
		}
		checkedAbs[abs][p.Line] = true
	}
	seen := map[ssa.Value]map[string]bool{}
	blockSeen := map[*ssa.BasicBlock]bool{}
	work := append([]workItem{}, seeds...)
	push := func(v ssa.Value, path string) {
		if v != nil {
			work = append(work, workItem{v: v, path: path})
		}
	}
	for len(work) > 0 {
		item := work[len(work)-1]
		work = work[:len(work)-1]
		v := item.v
		if v == nil {
			continue
		}
		if seen[v] == nil {
			seen[v] = map[string]bool{}
		}
		if seen[v][item.path] {
			continue
		}
		seen[v][item.path] = true
		record(v.Pos())
		for _, pos := range argMutators[v] {
			record(pos)
		}

		if instr, ok := v.(ssa.Instruction); ok {
			if b := instr.Block(); b != nil && !blockSeen[b] {
				blockSeen[b] = true
				for _, dep := range controlDeps[b] {
					push(dep, "")
				}
				for d := b.Idom(); d != nil; d = d.Idom() {
					if n := len(d.Instrs); n > 0 {
						if ifi, ok := d.Instrs[n-1].(*ssa.If); ok {
							push(ifi.Cond, "")
						}
					}
				}
			}
		}

		if param, ok := v.(*ssa.Parameter); ok {
			parent := param.Parent()
			idx := -1
			for i, p := range parent.Params {
				if p == param {
					idx = i
					break
				}
			}
			if idx >= 0 {
				for _, c := range callSites[parent] {
					if arg, ok := callArg(c, idx); ok {
						push(arg, item.path)
					}
				}
			}
		}
		if fv, ok := v.(*ssa.FreeVar); ok {
			for _, bind := range closureBinds[fv] {
				push(bind, item.path)
			}
		}
		if instr, ok := v.(ssa.Instruction); ok {
			record(instr.Pos())
			fieldMemoryDemand := false
			if k, ok := loadKey(v); ok && joinPath(k.path, item.path) != "" {
				fieldMemoryDemand = true
			}
			if extract, ok := instr.(*ssa.Extract); ok {
				if call, ok := extract.Tuple.(*ssa.Call); ok {
					if callee := call.Call.StaticCallee(); callee != nil {
						for _, r := range returns[callee] {
							record(r.Pos())
							if extract.Index < len(r.Results) {
								push(r.Results[extract.Index], item.path)
							}
						}
					}
				} else {
					push(extract.Tuple, item.path)
				}
			} else if fieldMemoryDemand {
			} else {
				for _, op := range instr.Operands(nil) {
					if op != nil && *op != nil {
						push(*op, "")
					}
				}
			}
		}
		if c, ok := v.(*ssa.Call); ok {
			if callee := c.Call.StaticCallee(); callee != nil {
				for _, r := range returns[callee] {
					record(r.Pos())
					for _, res := range r.Results {
						push(res, item.path)
					}
				}
			}
		}
		if k, ok := demandKey(v, item.path); ok {
			demandPath := k.path
			for _, mut := range mutatorsForDemand(callMutators, k.root, demandPath) {
				record(mut.pos)
				push(mut.param, mut.path)
			}
			nextPaths := map[memKey]string{}
			if demandPath == "" {
				for _, key := range storeKeysByRoot[k.root] {
					nextPaths[key] = ""
				}
			} else {
				for _, key := range storeKeysByRoot[k.root] {
					if key.path == "" {
						nextPaths[key] = demandPath
						continue
					}
					if key.path == demandPath {
						nextPaths[key] = ""
						continue
					}
					if strings.HasPrefix(demandPath, key.path+".") {
						nextPaths[key] = strings.TrimPrefix(demandPath, key.path+".")
					}
				}
			}
			for key, nextPath := range nextPaths {
				for _, s := range storesByLoc[key] {
					record(s.pos)
					push(s.val, nextPath)
				}
			}
		}
	}

	covered, err := coveredLines(dir)
	if err != nil {
		return Report{}, fmt.Errorf("coverage: %w", err)
	}

	targetPath := ""
	srcFiles := map[string]string{}
	for _, p := range pkgs {
		if strings.HasSuffix(p.ID, ".test") || strings.Contains(p.ID, "[") {
			continue
		}
		if targetPath == "" {
			targetPath = p.PkgPath
		}
		for _, f := range p.GoFiles {
			srcFiles[filepath.Base(f)] = f
		}
	}
	markCheckedLocalAggregateStores(allFns, fset, targetPath, checked, checkedAbs, paramStores, dynCallees, callSites)

	type fnRange struct {
		name       string
		base       string
		start, end int
	}
	var fns []fnRange
	fnSeen := map[string]bool{}
	for fn := range allFns {
		if fn.Pkg == nil || fn.Pkg.Pkg.Path() != targetPath || fn.Syntax() == nil {
			continue
		}
		s := fset.Position(fn.Syntax().Pos())
		e := fset.Position(fn.Syntax().End())
		if s.Filename == "" {
			continue
		}
		base := filepath.Base(s.Filename)
		k := fmt.Sprintf("%s:%s:%d", base, fn.Name(), s.Line)
		if fnSeen[k] {
			continue
		}
		fnSeen[k] = true
		fns = append(fns, fnRange{fn.Name(), base, s.Line, e.Line})
	}
	sort.Slice(fns, func(i, j int) bool {
		if fns[i].base != fns[j].base {
			return fns[i].base < fns[j].base
		}
		return fns[i].start < fns[j].start
	})

	srcLines := map[string][]string{}
	for base, abs := range srcFiles {
		if b, e := os.ReadFile(abs); e == nil {
			srcLines[base] = strings.Split(string(b), "\n")
		}
	}

	rep := Report{
		Package:     targetPath,
		OracleCalls: oracleCalls,
		SeedValues:  len(seeds),
		covered:     map[string]map[int]bool{},
		checked:     checked,
		checkedAbs:  checkedAbs,
	}
	totalCov, totalGap := 0, 0
	for _, fr := range fns {
		cov := covered[fr.base]
		if cov == nil {
			continue
		}
		src := srcLines[fr.base]
		if rep.covered[fr.base] == nil {
			rep.covered[fr.base] = map[int]bool{}
		}
		for ln := fr.start + 1; ln <= fr.end; ln++ {
			if !cov[ln] || isStructural(src, ln) {
				continue
			}
			rep.covered[fr.base][ln] = true
			totalCov++
			if checked[fr.base][ln] {
				continue
			}
			totalGap++
			rep.Findings = append(rep.Findings, Finding{
				File:      fr.base,
				Line:      ln,
				Func:      fr.name,
				Statement: strings.TrimSpace(lineAt(src, ln)),
				Reason:    "covered but no asserted value depends on it",
			})
		}
	}
	rep.CoveredLines = totalCov
	rep.UncheckedLines = totalGap
	rep.UncheckedPct = int(pct(totalGap, totalCov) + 0.5)
	return rep, nil
}

// Text renders the report in the human-readable CLI format.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "checkedcov — package %s  (%d oracle calls, %d seed values)\n\n", r.Package, r.OracleCalls, r.SeedValues)
	lastFn := ""
	for _, f := range r.Findings {
		fn := f.File + "." + f.Func
		if fn != lastFn {
			if lastFn != "" {
				fmt.Fprintln(&b)
			}
			fmt.Fprintf(&b, "  %s\n", fn)
			lastFn = fn
		}
		fmt.Fprintf(&b, "    %s:%d  covered, unchecked:  %s\n", f.File, f.Line, f.Statement)
	}
	if len(r.Findings) > 0 {
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "summary: %d covered statement-lines, %d unchecked (%d%% run without feeding any asserted value)\n",
		r.CoveredLines, r.UncheckedLines, r.UncheckedPct)
	fmt.Fprintln(&b, "slice: interprocedural data + closure captures + field-sensitive parameter heap writes + coarse memory (store/load by root) + control dependence (post-dominators + dominator fallback).")
	return b.String()
}

// JSON renders the machine-readable report contract.
func (r Report) JSON() ([]byte, error) {
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	return json.MarshalIndent(r, "", "  ")
}

// resolveDynamicCallees resolves interface-invoke call sites to the concrete
// methods the suite can actually dispatch to, scoped to the SUT and test
// packages — NOT the transitive dependency closure. It collects the concrete
// types the suite boxes into interfaces (every *ssa.MakeInterface in scope) and,
// for each in-scope interface invoke, binds the method of each such type that
// satisfies the call. This is the bounded, reflection-free equivalent of RTA's
// instantiated-type set: cost is |scoped invokes| x |scoped concrete types|, so
// it stays cheap even when the test binary imports huge dependencies (pgx,
// testcontainers) that whole-program RTA would choke on.
func resolveDynamicCallees(allFns map[*ssa.Function]bool, prog *ssa.Program, inScope map[*types.Package]bool) map[*ssa.Call][]*ssa.Function {
	dyn := map[*ssa.Call][]*ssa.Function{}
	scoped := func(fn *ssa.Function) bool {
		return fn != nil && fn.Pkg != nil && inScope[fn.Pkg.Pkg]
	}
	var concrete []types.Type
	seenT := map[string]bool{}
	for fn := range allFns {
		if !scoped(fn) {
			continue
		}
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				if mi, ok := instr.(*ssa.MakeInterface); ok {
					t := mi.X.Type()
					if k := t.String(); !seenT[k] {
						seenT[k] = true
						concrete = append(concrete, t)
					}
				}
			}
		}
	}
	for fn := range allFns {
		if !scoped(fn) {
			continue
		}
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				c, ok := instr.(*ssa.Call)
				if !ok || !c.Call.IsInvoke() {
					continue
				}
				m := c.Call.Method
				if m == nil {
					continue
				}
				for _, t := range concrete {
					sel := prog.MethodSets.MethodSet(t).Lookup(m.Pkg(), m.Name())
					if sel == nil {
						continue
					}
					if impl := prog.MethodValue(sel); impl != nil {
						dyn[c] = append(dyn[c], impl)
					}
				}
			}
		}
	}
	return dyn
}

// calleesOf returns the callees of a call: the single static callee, or the
// scoped concrete callees for a dynamic (interface) call.
func calleesOf(c *ssa.Call, dyn map[*ssa.Call][]*ssa.Function) []*ssa.Function {
	if s := c.Call.StaticCallee(); s != nil {
		return []*ssa.Function{s}
	}
	return dyn[c]
}

// callArg returns the value passed for the callee's parameter idx. For an
// interface invoke the concrete method's parameter 0 is the receiver, which SSA
// keeps in Call.Value rather than Args, so we shift accordingly. A MakeInterface
// boxing is unwrapped to its concrete operand so heap roots line up across the
// interface boundary (e.g. handle(got) where got is boxed into an interface).
func callArg(c *ssa.Call, idx int) (ssa.Value, bool) {
	var v ssa.Value
	if c.Call.IsInvoke() {
		if idx == 0 {
			v = c.Call.Value
		} else {
			idx--
			if idx < 0 || idx >= len(c.Call.Args) {
				return nil, false
			}
			v = c.Call.Args[idx]
		}
	} else {
		if idx < 0 || idx >= len(c.Call.Args) {
			return nil, false
		}
		v = c.Call.Args[idx]
	}
	if v == nil {
		return nil, false
	}
	if mi, ok := v.(*ssa.MakeInterface); ok {
		v = mi.X
	}
	return v, true
}

type storeRef struct {
	val ssa.Value
	pos token.Pos
}

type callMutation struct {
	pos   token.Pos
	param ssa.Value
	path  string
}

// isMutableArg reports whether a value of type t, passed as a call argument,
// lets the callee write back through it in place: slices and maps. The slice
// header / map ref is shared, so a mutation (sort, fill, delete) is visible in
// the caller's same SSA value. Pointers are excluded: a pointer receiver is
// passed to *every* method call, so flagging them all would mark mutating calls
// whose effect no asserted value actually depends on (needs heap aliasing).
func isMutableArg(t types.Type) bool {
	switch types.Unalias(t).Underlying().(type) {
	case *types.Slice, *types.Map:
		return true
	}
	return false
}

type workItem struct {
	v    ssa.Value
	path string
}

type memKey struct {
	root ssa.Value
	path string
}

const maxFieldPathDepth = 4

func fnControlDeps(fn *ssa.Function) map[*ssa.BasicBlock][]ssa.Value {
	out := map[*ssa.BasicBlock][]ssa.Value{}
	blocks := fn.Blocks
	if len(blocks) == 0 {
		return out
	}

	all := map[*ssa.BasicBlock]bool{}
	for _, b := range blocks {
		all[b] = true
	}
	postdom := map[*ssa.BasicBlock]map[*ssa.BasicBlock]bool{}
	for _, b := range blocks {
		postdom[b] = map[*ssa.BasicBlock]bool{}
		if len(b.Succs) == 0 {
			postdom[b][b] = true
			continue
		}
		for x := range all {
			postdom[b][x] = true
		}
	}

	changed := true
	for changed {
		changed = false
		for _, b := range blocks {
			next := map[*ssa.BasicBlock]bool{b: true}
			if len(b.Succs) > 0 {
				inter := cloneBlockSet(postdom[b.Succs[0]])
				for _, succ := range b.Succs[1:] {
					for x := range inter {
						if !postdom[succ][x] {
							delete(inter, x)
						}
					}
				}
				for x := range inter {
					next[x] = true
				}
			}
			if !sameBlockSet(postdom[b], next) {
				postdom[b] = next
				changed = true
			}
		}
	}

	for _, branch := range blocks {
		if len(branch.Succs) < 2 || len(branch.Instrs) == 0 {
			continue
		}
		ifi, ok := branch.Instrs[len(branch.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		for _, controlled := range blocks {
			if postdom[controlled][branch] {
				continue
			}
			for _, succ := range branch.Succs {
				if postdom[succ][controlled] {
					out[controlled] = append(out[controlled], ifi.Cond)
					break
				}
			}
		}
	}
	return out
}

func cloneBlockSet(in map[*ssa.BasicBlock]bool) map[*ssa.BasicBlock]bool {
	out := map[*ssa.BasicBlock]bool{}
	for b := range in {
		out[b] = true
	}
	return out
}

func sameBlockSet(a, b map[*ssa.BasicBlock]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for x := range a {
		if !b[x] {
			return false
		}
	}
	return true
}

// resolveFuncParam follows a callee value back to the *ssa.Parameter it loads.
// NaiveForm spills parameters to a local, so an indirect call's callee is
// typically a load of that spill slot; we look through it to the parameter that
// was stored there. Returns nil if the value is not a (spilled) parameter.
func resolveFuncParam(v ssa.Value, storesByLoc map[memKey][]storeRef) *ssa.Parameter {
	switch x := v.(type) {
	case *ssa.Parameter:
		return x
	case *ssa.UnOp:
		if x.Op == token.MUL {
			for _, s := range storesByLoc[addrKey(x.X)] {
				if p, ok := s.val.(*ssa.Parameter); ok {
					return p
				}
			}
		}
	}
	return nil
}

func recordParamStore(out map[*ssa.Function]map[int]map[string]bool, fn *ssa.Function, k memKey) {
	param, ok := k.root.(*ssa.Parameter)
	if !ok || param.Parent() != fn {
		return
	}
	idx := paramIndex(fn, param)
	if idx < 0 {
		return
	}
	addParamStore(out, fn, idx, k.path)
}

func addParamStore(out map[*ssa.Function]map[int]map[string]bool, fn *ssa.Function, idx int, path string) bool {
	if fieldPathDepth(path) > maxFieldPathDepth {
		return false
	}
	if out[fn] == nil {
		out[fn] = map[int]map[string]bool{}
	}
	if out[fn][idx] == nil {
		out[fn][idx] = map[string]bool{}
	}
	if out[fn][idx][path] {
		return false
	}
	out[fn][idx][path] = true
	return true
}

func paramIndex(fn *ssa.Function, param *ssa.Parameter) int {
	for i, p := range fn.Params {
		if p == param {
			return i
		}
	}
	return -1
}

func markCheckedLocalAggregateStores(allFns map[*ssa.Function]bool, fset *token.FileSet, targetPath string, checked map[string]map[int]bool, checkedAbs map[string]map[int]bool, paramStores map[*ssa.Function]map[int]map[string]bool, dyn map[*ssa.Call][]*ssa.Function, callSites map[*ssa.Function][]*ssa.Call) {
	if targetPath == "" {
		return
	}
	record := func(pos token.Pos) {
		if !pos.IsValid() {
			return
		}
		p := fset.Position(pos)
		if p.Filename == "" {
			return
		}
		base := filepath.Base(p.Filename)
		if checked[base] == nil {
			checked[base] = map[int]bool{}
		}
		checked[base][p.Line] = true
		abs, err := filepath.Abs(p.Filename)
		if err != nil {
			abs = filepath.Clean(p.Filename)
		} else {
			abs = filepath.Clean(abs)
		}
		if checkedAbs[abs] == nil {
			checkedAbs[abs] = map[int]bool{}
		}
		checkedAbs[abs][p.Line] = true
	}
	for fn := range allFns {
		if fn.Pkg == nil || fn.Pkg.Pkg.Path() != targetPath {
			continue
		}
		returnChecked := functionReturnOrCallSiteChecked(fn, fset, checked, callSites)
		if !returnChecked {
			continue
		}
		if functionOnlyBuildsCheckedReturn(fn, paramStores, dyn) {
			for _, b := range fn.Blocks {
				for _, instr := range b.Instrs {
					record(instr.Pos())
				}
			}
			continue
		}
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				switch x := instr.(type) {
				case *ssa.Store:
					if isLocalAggregateStore(fn, x.Addr) {
						record(x.Pos())
					}
				case *ssa.MapUpdate:
					if isLocalAggregateStore(fn, x.Map) {
						record(x.Pos())
					}
				}
			}
		}
	}
}

func functionReturnOrCallSiteChecked(fn *ssa.Function, fset *token.FileSet, checked map[string]map[int]bool, callSites map[*ssa.Function][]*ssa.Call) bool {
	for _, c := range callSites[fn] {
		p := fset.Position(c.Pos())
		if p.Filename != "" && checked[filepath.Base(p.Filename)][p.Line] {
			return true
		}
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			ret, ok := instr.(*ssa.Return)
			if !ok {
				continue
			}
			p := fset.Position(ret.Pos())
			if p.Filename != "" && checked[filepath.Base(p.Filename)][p.Line] {
				return true
			}
		}
	}
	return false
}

func functionOnlyBuildsCheckedReturn(fn *ssa.Function, paramStores map[*ssa.Function]map[int]map[string]bool, dyn map[*ssa.Call][]*ssa.Function) bool {
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			switch x := instr.(type) {
			case *ssa.Store:
				if !isLocalAggregateStore(fn, x.Addr) {
					return false
				}
			case *ssa.MapUpdate:
				if !isLocalAggregateStore(fn, x.Map) {
					return false
				}
			case *ssa.Send:
				return false
			case *ssa.Call:
				if callMayMutateExternalState(fn, x, paramStores, dyn) {
					return false
				}
			}
		}
	}
	return true
}

func callMayMutateExternalState(fn *ssa.Function, c *ssa.Call, paramStores map[*ssa.Function]map[int]map[string]bool, dyn map[*ssa.Call][]*ssa.Function) bool {
	if c == nil {
		return false
	}
	if bi, ok := c.Call.Value.(*ssa.Builtin); ok {
		return bi.Name() == "delete"
	}
	callees := calleesOf(c, dyn)
	for _, callee := range callees {
		for idx := range paramStores[callee] {
			arg, ok := callArg(c, idx)
			if ok && !isLocalAggregateStore(fn, arg) {
				return true
			}
		}
	}
	sig := c.Call.Signature()
	if sig == nil || sig.Results().Len() == 0 {
		callee := c.Call.StaticCallee()
		return callee == nil || !samePackageUnexported(callee, fn)
	}
	return false
}

func samePackageUnexported(callee, caller *ssa.Function) bool {
	if callee == nil || caller == nil || callee.Pkg == nil || caller.Pkg == nil || callee.Pkg.Pkg != caller.Pkg.Pkg {
		return false
	}
	name := callee.Name()
	return name == "" || strings.Contains(name, "$") || ('a' <= name[0] && name[0] <= 'z')
}

func isLocalAggregateStore(fn *ssa.Function, v ssa.Value) bool {
	if v == nil {
		return false
	}
	root := addrKey(v).root
	switch root.(type) {
	case *ssa.Alloc, *ssa.MakeSlice, *ssa.MakeMap, *ssa.MakeChan:
		return true
	}
	instr, ok := root.(ssa.Instruction)
	if !ok {
		return false
	}
	if fn != nil && instr.Parent() != fn {
		return false
	}
	switch instr.(type) {
	case *ssa.Call, *ssa.MakeInterface:
		return false
	default:
		return true
	}
}

func fieldPathDepth(path string) int {
	if path == "" {
		return 0
	}
	return strings.Count(path, ".") + 1
}

func addrKey(v ssa.Value) memKey {
	var fields []string
	for {
		switch x := v.(type) {
		case *ssa.FieldAddr:
			fields = append([]string{strconv.Itoa(x.Field)}, fields...)
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		default:
			return memKey{root: v, path: strings.Join(fields, ".")}
		}
	}
}

func valueKey(v ssa.Value) (memKey, bool) {
	if k, ok := loadKey(v); ok {
		return k, true
	}
	if _, ok := v.(*ssa.Parameter); ok {
		return memKey{root: v}, true
	}
	return memKey{}, false
}

func loadKey(v ssa.Value) (memKey, bool) {
	switch x := v.(type) {
	case *ssa.UnOp:
		if x.Op == token.MUL {
			return addrKey(x.X), true
		}
	case *ssa.FieldAddr:
		return addrKey(x), true
	case *ssa.IndexAddr:
		return addrKey(x), true
	case *ssa.Field:
		k := addrKey(x.X)
		k.path = joinPath(k.path, strconv.Itoa(x.Field))
		return k, true
	case *ssa.Index:
		return addrKey(x.X), true
	case *ssa.Lookup:
		return addrKey(x.X), true
	case *ssa.Alloc:
		return memKey{root: x}, true
	}
	return memKey{}, false
}

func demandKey(v ssa.Value, path string) (memKey, bool) {
	if k, ok := loadKey(v); ok {
		k.path = joinPath(k.path, path)
		return k, true
	}
	if path == "" {
		return memKey{}, false
	}
	switch v.(type) {
	case *ssa.Parameter, *ssa.FreeVar, *ssa.Alloc:
		return memKey{root: v, path: path}, true
	}
	return memKey{}, false
}

func mutatorsForDemand(callMutators map[memKey][]callMutation, root ssa.Value, demandPath string) []callMutation {
	var out []callMutation
	for key, positions := range callMutators {
		if key.root != root {
			continue
		}
		if demandPath == "" {
			out = append(out, positions...)
			continue
		}
		if key.path == demandPath || strings.HasPrefix(demandPath, key.path+".") {
			out = append(out, positions...)
		}
	}
	return out
}

func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "." + b
}

// oracleHit describes how a recognized verification call seeds the slice.
type oracleHit struct {
	args        []ssa.Value // values passed to the oracle (testify/testigo)
	seedControl bool        // also seed the if-conditions guarding the call's block
}

// nativeFailMethods are the *testing.T/B methods that fail or fail-mark a test.
var nativeFailMethods = map[string]bool{
	"Error": true, "Errorf": true, "Errorln": true,
	"Fatal": true, "Fatalf": true, "Fatalln": true,
	"Fail": true, "FailNow": true,
}

// recognize maps a call to its oracle seeds. Returns (hit, false) if the call
// is not a verification point.
func recognize(c *ssa.Call) (oracleHit, bool) {
	callee := c.Call.StaticCallee()
	if callee == nil || callee.Pkg == nil {
		// Interface dispatch (e.g. via testing.TB): match by method name + sig.
		if m := c.Call.Method; m != nil && m.Pkg() != nil &&
			m.Pkg().Path() == "testing" && nativeFailMethods[m.Name()] {
			return oracleHit{args: c.Call.Args, seedControl: true}, true
		}
		return oracleHit{}, false
	}
	path := callee.Pkg.Pkg.Path()
	if isAssertOracle(path) {
		return oracleHit{args: c.Call.Args}, true
	}
	// Native testing.T/B fail-method called on a concrete receiver.
	if path == "testing" && nativeFailMethods[callee.Name()] {
		return oracleHit{args: c.Call.Args, seedControl: true}, true
	}
	return oracleHit{}, false
}

func isAssertOracle(pkgPath string) bool {
	return strings.Contains(pkgPath, "/testigo/assert") ||
		strings.Contains(pkgPath, "testify/assert") ||
		strings.Contains(pkgPath, "testify/require")
}

var coverRe = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)

func coveredLines(dir string) (map[string]map[int]bool, error) {
	tmp := filepath.Join(os.TempDir(), "checkedcov-ssa.out")
	cmd := exec.Command("go", "test", "-mod=mod", "-covermode=set", "-coverprofile="+tmp, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: `go test` non-zero (coverage may be partial):\n%s\n", strings.TrimSpace(string(out)))
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

func lineAt(src []string, n int) string {
	if n-1 >= 0 && n-1 < len(src) {
		return src[n-1]
	}
	return ""
}

func isStructural(src []string, n int) bool {
	t := strings.TrimSpace(lineAt(src, n))
	return t == "" ||
		t == "{" ||
		t == "}" ||
		t == ")" ||
		t == "}, nil" ||
		(strings.HasPrefix(t, "func ") && strings.HasSuffix(t, "{")) ||
		(strings.HasPrefix(t, ") ") && strings.HasSuffix(t, "{")) ||
		strings.HasPrefix(t, "//")
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

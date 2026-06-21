// Package edgecovssa implements a structural checked-edge coverage report.
//
// It is intentionally conservative in v1: SSA concrete call edges (static +
// scope-bounded interface dispatch), branch
// sides, and side-effecting SSA instructions are classified with ordinary Go
// coverage plus checkedcov's existing asserted-value slice.
package edgecovssa

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

	"github.com/lautaromei/testigo/internal/checkedcovssa"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Summary is the compact machine-readable count block for an edgecov report.
type Summary struct {
	Edges                   int `json:"edges"`
	EdgesUnobserved         int `json:"edges_unobserved"`
	InterfaceEdges          int `json:"interface_edges"`
	Branches                int `json:"branches"`
	BranchesUntaken         int `json:"branches_untaken"`
	Effects                 int `json:"effects"`
	EffectsReachedUnchecked int `json:"effects_reached_unchecked"`
	EffectsUnreached        int `json:"effects_unreached"`
}

// Finding is one actionable checked-edge coverage gap.
type Finding struct {
	Rank      int    `json:"rank"`
	Kind      string `json:"kind"`
	Func      string `json:"func"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Statement string `json:"statement,omitempty"`
	Target    string `json:"target,omitempty"`
	Effect    string `json:"effect,omitempty"`
	GuardedBy string `json:"guarded_by,omitempty"`
	Predicts  string `json:"predicts"`
	Reason    string `json:"reason"`
}

// Report is the structured result of an edgecov analysis.
type Report struct {
	Package  string    `json:"package"`
	Summary  Summary   `json:"summary"`
	Findings []Finding `json:"findings"`
}

// Options configures an edgecov run.
type Options struct {
	Dir          string
	Project      bool
	CoverProfile string
}

// Analyze runs edgecov for the package directory dir.
func Analyze(dir string) (Report, error) {
	return AnalyzeWithOptions(Options{Dir: dir})
}

// AnalyzeProject runs edgecov for every package under dir, using a single
// project-wide coverage profile so integration tests in sibling packages count.
func AnalyzeProject(dir string) (Report, error) {
	return AnalyzeWithOptions(Options{Dir: dir, Project: true})
}

// AnalyzeWithOptions runs edgecov with explicit options.
func AnalyzeWithOptions(opts Options) (Report, error) {
	dir := opts.Dir
	if strings.TrimSpace(dir) == "" {
		return Report{}, fmt.Errorf("usage: edgecov <package-dir>")
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Tests:      true,
		Dir:        dir,
		BuildFlags: []string{"-mod=mod"},
	}
	pattern := "."
	if opts.Project {
		pattern = "./..."
	}
	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return Report{}, fmt.Errorf("load: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		fmt.Fprintln(os.Stderr, "warning: type errors above; analysis may be partial")
	}
	prog, _ := ssautil.AllPackages(pkgs, ssa.NaiveForm|ssa.InstantiateGenerics)
	prog.Build()
	fset := prog.Fset
	allFns := ssautil.AllFunctions(prog)

	targets := targetPackages(pkgs, opts.Project)
	if len(targets) == 0 {
		return Report{}, fmt.Errorf("could not identify target package")
	}
	srcFiles := mergeSourceFiles(targets)
	sourceIndex := coverSourceIndex(pkgs)
	coverage, err := coveredLines(dir, opts.Project, opts.CoverProfile, sourceIndex)
	if err != nil {
		return Report{}, fmt.Errorf("coverage: %w", err)
	}
	checked := checkedLinesForTargets(targets, srcFiles)
	srcLines := readSourceLines(srcFiles)
	rtaResult := analyzeReachability(allFns, prog, pkgs)
	targetSet := map[string]bool{}
	for _, target := range targets {
		targetSet[target.path] = true
	}

	var edgeItems []edgeItem
	var branchItems []branchItem
	var effectItems []effectItem
	for fn := range allFns {
		if !isTargetFunc(fn, targetSet) || fn.Syntax() == nil || isTestFile(fset.Position(fn.Syntax().Pos()).Filename) {
			continue
		}
		collectFunc(fn, fset, coverage, checked, rtaResult, &edgeItems, &branchItems, &effectItems)
	}
	edgeItems = dedupEdges(edgeItems)
	branchItems = dedupBranches(branchItems)
	effectItems = dedupEffects(effectItems)

	rep := Report{Package: reportPackage(dir, targets, opts.Project)}
	var findings []Finding
	for _, e := range edgeItems {
		if !e.reachable {
			continue
		}
		rep.Summary.Edges++
		if e.interfaceDispatch {
			rep.Summary.InterfaceEdges++
		}
		if e.observed {
			continue
		}
		rep.Summary.EdgesUnobserved++
	}
	for _, b := range branchItems {
		rep.Summary.Branches++
		if b.taken {
			continue
		}
		rep.Summary.BranchesUntaken++
		guardedBy := b.guard
		if len(b.guardedEffects) > 0 {
			guardedBy = b.guard + " guards " + strings.Join(b.guardedEffects, "; ")
		}
		findings = append(findings, Finding{
			Rank:      2,
			Kind:      "branch-not-taken",
			Func:      b.fn,
			File:      b.file,
			Line:      b.line,
			Statement: lineAt(srcLines[b.file], b.line),
			GuardedBy: guardedBy,
			Predicts:  "guard mutation survives",
			Reason:    "one branch successor did not execute under the test coverage profile",
		})
	}
	for _, e := range effectItems {
		rep.Summary.Effects++
		switch {
		case e.reached && !e.checked:
			rep.Summary.EffectsReachedUnchecked++
			findings = append(findings, Finding{
				Rank:      1,
				Kind:      "effect-reached-unchecked",
				Func:      e.fn,
				File:      e.file,
				Line:      e.line,
				Statement: lineAt(srcLines[e.file], e.line),
				Effect:    e.effect,
				Predicts:  "DROP_CALL survives",
				Reason:    "effect executed but no asserted value depends on its source line",
			})
		case !e.reached:
			rep.Summary.EffectsUnreached++
			findings = append(findings, Finding{
				Rank:      3,
				Kind:      "effect-not-reached",
				Func:      e.fn,
				File:      e.file,
				Line:      e.line,
				Statement: lineAt(srcLines[e.file], e.line),
				Effect:    e.effect,
				Predicts:  "DROP_CALL survives",
				Reason:    "side effect did not execute under the test coverage profile",
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Rank != findings[j].Rank {
			return findings[i].Rank < findings[j].Rank
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	rep.Findings = findings
	return rep, nil
}

// Run executes the edgecov analysis for dir and prints a text report.
func Run(dir string) error {
	rep, err := Analyze(dir)
	if err != nil {
		return err
	}
	fmt.Print(rep.Text())
	return nil
}

// Text renders the report in a concise human-readable format.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "edgecov — package %s\n\n", r.Package)
	for _, f := range r.Findings {
		subject := f.Effect
		if subject == "" {
			subject = f.Target
		}
		if subject == "" {
			subject = f.GuardedBy
		}
		fmt.Fprintf(&b, "  [%d] %s  %s:%d  %s\n", f.Rank, f.Kind, f.File, f.Line, subject)
		if f.Statement != "" {
			fmt.Fprintf(&b, "      %s\n", strings.TrimSpace(f.Statement))
		}
	}
	if len(r.Findings) > 0 {
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "summary: %d edges (%d interface, %d unobserved), %d branches (%d untaken), %d effects (%d reached-unchecked, %d unreached)\n",
		r.Summary.Edges, r.Summary.InterfaceEdges, r.Summary.EdgesUnobserved,
		r.Summary.Branches, r.Summary.BranchesUntaken,
		r.Summary.Effects, r.Summary.EffectsReachedUnchecked, r.Summary.EffectsUnreached)
	fmt.Fprintln(&b, "model: SSA concrete call edges (static + scope-bounded interface dispatch), branch successors, structural effects; checked state reuses checkedcov's asserted-value slice.")
	return b.String()
}

// JSON renders the machine-readable report contract.
func (r Report) JSON() ([]byte, error) {
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	return json.MarshalIndent(r, "", "  ")
}

// DOT renders a compact graph-oriented view of the findings.
func (r Report) DOT() string {
	var b strings.Builder
	fmt.Fprintln(&b, "digraph edgecov {")
	fmt.Fprintln(&b, `  rankdir=LR;`)
	fmt.Fprintln(&b, `  node [shape=box,fontname="Menlo"];`)
	fmt.Fprintf(&b, "  package [label=%q,shape=oval];\n", r.Package)
	for i, f := range r.Findings {
		id := fmt.Sprintf("finding_%d", i)
		label := fmt.Sprintf("[%d] %s\\n%s:%d", f.Rank, f.Kind, f.File, f.Line)
		if f.Effect != "" {
			label += "\\n" + f.Effect
		}
		if f.Target != "" {
			label += "\\n" + f.Target
		}
		if f.GuardedBy != "" {
			label += "\\n" + f.GuardedBy
		}
		fmt.Fprintf(&b, "  %s [label=%q];\n", id, label)
		fmt.Fprintf(&b, "  package -> %s;\n", id)
	}
	fmt.Fprintln(&b, "}")
	return b.String()
}

type edgeItem struct {
	fn                string
	file              string
	line              int
	target            string
	observed          bool
	reachable         bool
	interfaceDispatch bool
}

type branchItem struct {
	fn             string
	file           string
	line           int
	guard          string
	guardedEffects []string
	taken          bool
}

type effectItem struct {
	fn      string
	file    string
	line    int
	effect  string
	reached bool
	checked bool
}

type checkedLines map[string]map[int]bool

func (c checkedLines) checked(file string, line int) bool {
	return c[file] != nil && c[file][line]
}

func collectFunc(fn *ssa.Function, fset *token.FileSet, cov coverageMap, checked checkedLines, reach reachability, edges *[]edgeItem, branches *[]branchItem, effects *[]effectItem) {
	for _, b := range fn.Blocks {
		if len(b.Instrs) > 0 {
			if ifi, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If); ok {
				for _, succ := range b.Succs {
					file, line := instrPos(fset, ifi)
					if file == "" || !cov.covered(file, line) {
						continue
					}
					*branches = append(*branches, branchItem{
						fn:             fn.Name(),
						file:           file,
						line:           line,
						guard:          ifi.Cond.String(),
						guardedEffects: controlledEffects(fn, b, succ),
						taken:          blockCovered(fset, cov, succ),
					})
				}
			}
		}
		for _, instr := range b.Instrs {
			switch x := instr.(type) {
			case *ssa.Call:
				addCallEdges(fn, fset, cov, reach, x, edges)
				if effect, ok := callEffect(x); ok {
					file, line := pos(fset, x.Pos())
					if file == "" {
						continue
					}
					*effects = append(*effects, effectItem{
						fn:      fn.Name(),
						file:    file,
						line:    line,
						effect:  effect,
						reached: cov.covered(file, line),
						checked: checked.checked(file, line),
					})
				}
			case *ssa.Go:
				addCallEdges(fn, fset, cov, reach, x, edges)
			case *ssa.Defer:
				addCallEdges(fn, fset, cov, reach, x, edges)
			case *ssa.Store:
				if effect, ok := storeEffect(x); ok {
					addEffect(fset, cov, checked, fn, x.Pos(), effect, effects)
				}
			case *ssa.MapUpdate:
				addEffect(fset, cov, checked, fn, x.Pos(), "map update "+x.Map.String(), effects)
			case *ssa.Send:
				addEffect(fset, cov, checked, fn, x.Pos(), "send "+x.Chan.String(), effects)
			}
		}
	}
}

func addCallEdges(fn *ssa.Function, fset *token.FileSet, cov coverageMap, reach reachability, site ssa.CallInstruction, edges *[]edgeItem) {
	file, line := pos(fset, site.Common().Pos())
	if file == "" {
		file, line = instrPos(fset, site)
	}
	if file == "" {
		return
	}
	for _, callee := range reach.targets(site) {
		if callee.Pkg != nil && callee.Pkg.Pkg != nil && isTestSupportPackage(callee.Pkg.Pkg.Path()) {
			continue
		}
		*edges = append(*edges, edgeItem{
			fn:                fn.Name(),
			file:              file,
			line:              line,
			target:            callee.String(),
			observed:          cov.covered(file, line),
			reachable:         reach.reachable[fn] || funcCovered(fset, cov, fn),
			interfaceDispatch: site.Common().IsInvoke(),
		})
	}
}

func addEffect(fset *token.FileSet, cov coverageMap, checked checkedLines, fn *ssa.Function, p token.Pos, label string, effects *[]effectItem) {
	file, line := pos(fset, p)
	if file == "" {
		return
	}
	*effects = append(*effects, effectItem{
		fn:      fn.Name(),
		file:    file,
		line:    line,
		effect:  label,
		reached: cov.covered(file, line),
		checked: checked.checked(file, line),
	})
}

func callEffect(c *ssa.Call) (string, bool) {
	if bi, ok := c.Call.Value.(*ssa.Builtin); ok && bi.Name() == "delete" {
		return "delete", true
	} else if ok {
		return "", false
	}
	callee := c.Call.StaticCallee()
	name := c.Call.String()
	if callee != nil {
		name = callee.String()
	}
	for _, a := range c.Call.Args {
		if a != nil && isMutableArg(a.Type()) {
			return "call " + name, true
		}
	}
	sig := c.Call.Signature()
	if sig == nil || sig.Results().Len() == 0 {
		return "call " + name, true
	}
	refs := c.Referrers()
	if refs == nil || len(*refs) == 0 {
		return "call " + name, true
	}
	return "", false
}

func storeEffect(s *ssa.Store) (string, bool) {
	if isLocalStore(s.Addr) {
		return "", false
	}
	return "store " + s.Addr.String(), true
}

func instructionEffect(instr ssa.Instruction) (string, bool) {
	switch x := instr.(type) {
	case *ssa.Call:
		return callEffect(x)
	case *ssa.Store:
		return storeEffect(x)
	case *ssa.MapUpdate:
		return "map update " + x.Map.String(), true
	case *ssa.Send:
		return "send " + x.Chan.String(), true
	}
	return "", false
}

func controlledEffects(fn *ssa.Function, branch, succ *ssa.BasicBlock) []string {
	if branch == nil || succ == nil {
		return nil
	}
	succReach := reachableBlocks(succ, nil)
	otherReach := map[*ssa.BasicBlock]bool{}
	for _, other := range branch.Succs {
		if other == succ {
			continue
		}
		for b := range reachableBlocks(other, nil) {
			otherReach[b] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, b := range fn.Blocks {
		if !succReach[b] || otherReach[b] {
			continue
		}
		for _, instr := range b.Instrs {
			effect, ok := instructionEffect(instr)
			if !ok || seen[effect] {
				continue
			}
			seen[effect] = true
			out = append(out, effect)
		}
	}
	if len(out) == 0 {
		for _, instr := range succ.Instrs {
			effect, ok := instructionEffect(instr)
			if ok && !seen[effect] {
				out = append(out, effect)
			}
		}
	}
	sort.Strings(out)
	return out
}

func reachableBlocks(root *ssa.BasicBlock, stop map[*ssa.BasicBlock]bool) map[*ssa.BasicBlock]bool {
	seen := map[*ssa.BasicBlock]bool{}
	work := []*ssa.BasicBlock{root}
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		if b == nil || seen[b] || stop[b] {
			continue
		}
		seen[b] = true
		work = append(work, b.Succs...)
	}
	return seen
}

func isLocalStore(v ssa.Value) bool {
	root := addrRoot(v)
	_, ok := root.(*ssa.Alloc)
	return ok
}

func addrRoot(v ssa.Value) ssa.Value {
	for {
		switch x := v.(type) {
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		default:
			return v
		}
	}
}

type reachability struct {
	reachable map[*ssa.Function]bool
	edges     map[ssa.CallInstruction][]*ssa.Function
}

func (r reachability) targets(site ssa.CallInstruction) []*ssa.Function {
	if site == nil {
		return nil
	}
	if targets := r.edges[site]; len(targets) > 0 {
		return targets
	}
	if callee := site.Common().StaticCallee(); callee != nil {
		return []*ssa.Function{callee}
	}
	return nil
}

func analyzeReachability(allFns map[*ssa.Function]bool, prog *ssa.Program, pkgs []*packages.Package) reachability {
	inScope := map[*types.Package]bool{}
	for _, p := range pkgs {
		if p.Types != nil {
			inScope[p.Types] = true
		}
	}
	// Resolve interface-invoke edges bounded to the SUT+test scope (the types the
	// suite actually instantiates), instead of whole-program RTA. RTA reaches the
	// testing/reflect/runtime closure and blows up on test binaries that import
	// heavy deps (pgx, testcontainers); this scoped resolution stays cheap.
	dyn := resolveDynamicCallees(allFns, prog, inScope)

	outgoing := map[*ssa.Function][]*ssa.Function{}
	var roots []*ssa.Function
	for fn := range allFns {
		if isTestRoot(fn) {
			roots = append(roots, fn)
		}
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				c, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				if callee := c.Call.StaticCallee(); callee != nil {
					outgoing[fn] = append(outgoing[fn], callee)
				} else {
					outgoing[fn] = append(outgoing[fn], dyn[c]...)
				}
			}
		}
	}
	seen := map[*ssa.Function]bool{}
	work := append([]*ssa.Function{}, roots...)
	for len(work) > 0 {
		fn := work[len(work)-1]
		work = work[:len(work)-1]
		if fn == nil || seen[fn] {
			continue
		}
		seen[fn] = true
		work = append(work, outgoing[fn]...)
	}
	edges := map[ssa.CallInstruction][]*ssa.Function{}
	for c, targets := range dyn {
		sort.Slice(targets, func(i, j int) bool { return targets[i].String() < targets[j].String() })
		edges[c] = dedupFuncs(targets)
	}
	return reachability{reachable: seen, edges: edges}
}

func dedupFuncs(in []*ssa.Function) []*ssa.Function {
	seen := map[*ssa.Function]bool{}
	var out []*ssa.Function
	for _, f := range in {
		if f == nil || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// resolveDynamicCallees resolves interface-invoke call sites to the concrete
// methods the suite can dispatch to, scoped to the SUT and test packages (the
// types the suite boxes into interfaces), not the transitive dependency closure.
// Bounded, reflection-free equivalent of RTA's instantiated-type set.
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

func isTestRoot(fn *ssa.Function) bool {
	if fn == nil || fn.Syntax() == nil {
		return false
	}
	if !isTestFile(fn.Prog.Fset.Position(fn.Syntax().Pos()).Filename) {
		return false
	}
	name := fn.Name()
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Fuzz")
}

func isTestFile(name string) bool {
	return strings.HasSuffix(filepath.Base(name), "_test.go")
}

type packageTarget struct {
	path  string
	dir   string
	files map[string]string
}

func targetPackages(pkgs []*packages.Package, project bool) []packageTarget {
	seen := map[string]bool{}
	var out []packageTarget
	for _, p := range pkgs {
		if p.PkgPath == "" || strings.HasSuffix(p.ID, ".test") || strings.Contains(p.ID, "[") {
			continue
		}
		if project && isTestSupportPackage(p.PkgPath) {
			continue
		}
		if seen[p.PkgPath] {
			continue
		}
		files := map[string]string{}
		dir := ""
		for _, f := range p.GoFiles {
			key := fileKey(f)
			files[key] = f
			if dir == "" {
				dir = filepath.Dir(f)
			}
		}
		if len(files) == 0 {
			continue
		}
		seen[p.PkgPath] = true
		out = append(out, packageTarget{path: p.PkgPath, dir: dir, files: files})
		if !project {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func mergeSourceFiles(targets []packageTarget) map[string]string {
	out := map[string]string{}
	for _, target := range targets {
		for key, abs := range target.files {
			out[key] = abs
		}
	}
	return out
}

func checkedLinesForTargets(targets []packageTarget, srcFiles map[string]string) checkedLines {
	out := checkedLines{}
	for _, target := range targets {
		rep, err := checkedcovssa.Analyze(target.dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: checked slice for %s failed: %v\n", target.path, err)
			continue
		}
		for key, abs := range srcFiles {
			data, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			lines := strings.Count(string(data), "\n") + 1
			for line := 1; line <= lines; line++ {
				if !rep.IsCheckedFile(abs, line) {
					continue
				}
				if out[key] == nil {
					out[key] = map[int]bool{}
				}
				out[key][line] = true
			}
		}
	}
	return out
}

func reportPackage(dir string, targets []packageTarget, project bool) string {
	if !project && len(targets) == 1 {
		return targets[0].path
	}
	if len(targets) > 0 {
		return targets[0].path + "/..."
	}
	return filepath.Clean(dir) + "/..."
}

func isTargetFunc(fn *ssa.Function, targetPaths map[string]bool) bool {
	return fn != nil && fn.Pkg != nil && fn.Pkg.Pkg != nil && targetPaths[fn.Pkg.Pkg.Path()]
}

func isTestSupportPackage(pkgPath string) bool {
	base := filepath.Base(pkgPath)
	return base == "testdata" || strings.HasSuffix(base, "test")
}

func blockCovered(fset *token.FileSet, cov coverageMap, b *ssa.BasicBlock) bool {
	if b == nil {
		return false
	}
	for _, instr := range b.Instrs {
		file, line := pos(fset, instr.Pos())
		if file != "" && cov.covered(file, line) {
			return true
		}
	}
	return false
}

func funcCovered(fset *token.FileSet, cov coverageMap, fn *ssa.Function) bool {
	for _, b := range fn.Blocks {
		if blockCovered(fset, cov, b) {
			return true
		}
	}
	return false
}

func dedupEdges(in []edgeItem) []edgeItem {
	seen := map[string]int{}
	var out []edgeItem
	for _, x := range in {
		key := fmt.Sprintf("%s:%s:%d:%s", x.fn, x.file, x.line, x.target)
		if i, ok := seen[key]; ok {
			out[i].observed = out[i].observed || x.observed
			out[i].reachable = out[i].reachable || x.reachable
			out[i].interfaceDispatch = out[i].interfaceDispatch || x.interfaceDispatch
			continue
		}
		seen[key] = len(out)
		out = append(out, x)
	}
	return out
}

func dedupBranches(in []branchItem) []branchItem {
	seen := map[string]int{}
	var out []branchItem
	for _, x := range in {
		key := fmt.Sprintf("%s:%s:%d:%s:%s", x.fn, x.file, x.line, x.guard, strings.Join(x.guardedEffects, "\x00"))
		if i, ok := seen[key]; ok {
			out[i].taken = out[i].taken || x.taken
			continue
		}
		seen[key] = len(out)
		out = append(out, x)
	}
	return out
}

func dedupEffects(in []effectItem) []effectItem {
	seen := map[string]int{}
	var out []effectItem
	for _, x := range in {
		key := fmt.Sprintf("%s:%s:%d:%s", x.fn, x.file, x.line, x.effect)
		if i, ok := seen[key]; ok {
			out[i].reached = out[i].reached || x.reached
			out[i].checked = out[i].checked || x.checked
			continue
		}
		seen[key] = len(out)
		out = append(out, x)
	}
	return out
}

func pos(fset *token.FileSet, p token.Pos) (string, int) {
	if !p.IsValid() {
		return "", 0
	}
	pp := fset.Position(p)
	if pp.Filename == "" {
		return "", 0
	}
	return fileKey(pp.Filename), pp.Line
}

func instrPos(fset *token.FileSet, instr ssa.Instruction) (string, int) {
	file, line := pos(fset, instr.Pos())
	if file != "" {
		return file, line
	}
	for _, op := range instr.Operands(nil) {
		if op == nil || *op == nil {
			continue
		}
		file, line = pos(fset, (*op).Pos())
		if file != "" {
			return file, line
		}
	}
	return "", 0
}

func isMutableArg(t types.Type) bool {
	switch types.Unalias(t).Underlying().(type) {
	case *types.Slice, *types.Map:
		return true
	}
	return false
}

type coverageMap map[string]map[int]bool

func (c coverageMap) covered(file string, line int) bool {
	return c[file] != nil && c[file][line]
}

var coverRe = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)

func coveredLines(dir string, project bool, coverProfile string, sourceIndex map[string]string) (coverageMap, error) {
	profile := coverProfile
	if profile == "" {
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("edgecov-ssa-%d.out", os.Getpid()))
		args := []string{"test", "-mod=mod", "-covermode=set", "-coverprofile=" + tmp}
		if project {
			args = append(args, "-coverpkg=./...", "./...")
		} else {
			args = append(args, ".")
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: `go test` non-zero (coverage may be partial):\n%s\n", strings.TrimSpace(string(out)))
		}
		profile = tmp
	} else if !filepath.IsAbs(profile) {
		profile = filepath.Join(dir, profile)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		return nil, fmt.Errorf("no cover profile: %v", err)
	}
	covered := coverageMap{}
	for _, ln := range strings.Split(string(data), "\n") {
		m := coverRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		file := coverFileKey(m[1], dir, sourceIndex)
		start, _ := strconv.Atoi(m[2])
		end, _ := strconv.Atoi(m[3])
		count, _ := strconv.Atoi(m[4])
		if count == 0 {
			continue
		}
		if covered[file] == nil {
			covered[file] = map[int]bool{}
		}
		for i := start; i <= end; i++ {
			covered[file][i] = true
		}
	}
	return covered, nil
}

func coverSourceIndex(pkgs []*packages.Package) map[string]string {
	out := map[string]string{}
	for _, p := range pkgs {
		if p.PkgPath == "" {
			continue
		}
		for _, f := range p.GoFiles {
			key := fileKey(f)
			out[p.PkgPath+"/"+filepath.Base(f)] = key
			out[filepath.ToSlash(f)] = key
			out[fileKey(f)] = key
		}
	}
	return out
}

func coverFileKey(name, dir string, sourceIndex map[string]string) string {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if key := sourceIndex[name]; key != "" {
		return key
	}
	if filepath.IsAbs(name) {
		return fileKey(name)
	}
	if key := sourceIndex[filepath.ToSlash(filepath.Clean(name))]; key != "" {
		return key
	}
	return fileKey(filepath.Join(dir, filepath.FromSlash(name)))
}

func readSourceLines(files map[string]string) map[string][]string {
	out := map[string][]string{}
	for key, abs := range files {
		if b, err := os.ReadFile(abs); err == nil {
			out[key] = strings.Split(string(b), "\n")
		}
	}
	return out
}

func fileKey(name string) string {
	if name == "" {
		return ""
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return filepath.Clean(name)
	}
	return filepath.Clean(abs)
}

func lineAt(src []string, n int) string {
	if n-1 >= 0 && n-1 < len(src) {
		return strings.TrimSpace(src[n-1])
	}
	return ""
}

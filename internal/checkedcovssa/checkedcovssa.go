// Package checkedcovssa implements the SSA-based checked-coverage detector
// used by the testigo CLI.
//
// It answers: which statements does the suite execute but never let influence
// an asserted return value?
package checkedcovssa

import (
	"fmt"
	"go/token"
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

// Run executes the checked-coverage analysis for the package directory dir.
func Run(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("usage: checkedcov-ssa <package-dir>")
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
		return fmt.Errorf("load: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		fmt.Fprintln(os.Stderr, "warning: type errors above; analysis may be partial")
	}
	prog, _ := ssautil.AllPackages(pkgs, ssa.NaiveForm)
	prog.Build()
	fset := prog.Fset

	allFns := ssautil.AllFunctions(prog)
	callSites := map[*ssa.Function][]*ssa.Call{}
	returns := map[*ssa.Function][]*ssa.Return{}
	storesByLoc := map[memKey][]storeRef{}
	storeKeysByRoot := map[ssa.Value][]memKey{}
	closureBinds := map[*ssa.FreeVar][]ssa.Value{}
	controlDeps := map[*ssa.BasicBlock][]ssa.Value{}
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
				case *ssa.Return:
					returns[fn] = append(returns[fn], x)
				case *ssa.Store:
					k := addrKey(x.Addr)
					storesByLoc[k] = append(storesByLoc[k], storeRef{x.Val, x.Pos()})
					storeKeysByRoot[k.root] = append(storeKeysByRoot[k.root], k)
				case *ssa.MapUpdate:
					k := addrKey(x.Map)
					storesByLoc[k] = append(storesByLoc[k], storeRef{x.Value, x.Pos()})
					storeKeysByRoot[k.root] = append(storeKeysByRoot[k.root], k)
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

	var seeds []workItem
	oracleCalls := 0
	for fn := range allFns {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				c, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				callee := c.Call.StaticCallee()
				if callee == nil || callee.Pkg == nil {
					continue
				}
				if !isOracle(callee.Pkg.Pkg.Path()) {
					continue
				}
				oracleCalls++
				for _, a := range c.Call.Args {
					seeds = append(seeds, workItem{v: a})
				}
			}
		}
	}

	checked := map[string]map[int]bool{}
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
					if idx < len(c.Call.Args) {
						push(c.Call.Args[idx], item.path)
					}
				}
			}
			continue
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
		if k, ok := loadKey(v); ok {
			demandPath := joinPath(k.path, item.path)
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
		return fmt.Errorf("coverage: %w", err)
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
	type fnRange struct {
		name       string
		base       string
		start, end int
	}
	var fns []fnRange
	for fn := range allFns {
		if fn.Pkg == nil || fn.Pkg.Pkg.Path() != targetPath || fn.Syntax() == nil {
			continue
		}
		s := fset.Position(fn.Syntax().Pos())
		e := fset.Position(fn.Syntax().End())
		if s.Filename == "" {
			continue
		}
		fns = append(fns, fnRange{fn.Name(), filepath.Base(s.Filename), s.Line, e.Line})
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

	fmt.Printf("checkedcov-ssa — package %s  (%d oracle calls, %d seed values)\n\n", targetPath, oracleCalls, len(seeds))
	totalCov, totalGap := 0, 0
	for _, fr := range fns {
		cov := covered[fr.base]
		if cov == nil {
			continue
		}
		src := srcLines[fr.base]
		var gap []int
		for ln := fr.start + 1; ln <= fr.end; ln++ {
			if !cov[ln] || isStructural(src, ln) {
				continue
			}
			totalCov++
			if checked[fr.base][ln] {
				continue
			}
			gap = append(gap, ln)
			totalGap++
		}
		if len(gap) == 0 {
			continue
		}
		fmt.Printf("  %s.%s\n", fr.base, fr.name)
		for _, ln := range gap {
			fmt.Printf("    %s:%d  covered, unchecked:  %s\n", fr.base, ln, strings.TrimSpace(lineAt(src, ln)))
		}
		fmt.Println()
	}
	fmt.Printf("summary: %d covered statement-lines, %d unchecked (%.0f%% run without feeding any asserted value)\n",
		totalCov, totalGap, pct(totalGap, totalCov))
	fmt.Println("slice: interprocedural data + closure captures + coarse memory (store/load by root) + control dependence (post-dominators + dominator fallback).")
	fmt.Println("remaining: full heap aliasing needs a pointer analysis outside current x/tools.")
	return nil
}

type storeRef struct {
	val ssa.Value
	pos token.Pos
}

type workItem struct {
	v    ssa.Value
	path string
}

type memKey struct {
	root ssa.Value
	path string
}

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

func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "." + b
}

func isOracle(pkgPath string) bool {
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

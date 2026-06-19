// Command audit-eval is the offline calibration harness for testigo's audit
// detectors (AUDIT_PLAN §9). For each package in a corpus it:
//
//  1. runs the suite with TESTIGO_AUDIT_JSON to capture the scored findings
//     (each detector's predicted P(survive) for a boundary mutant);
//  2. runs the boundary mutation oracle (internal/boundarymut) to label each
//     interaction site KILLED (caught) or LIVED (a surviving mutant — a gap);
//  3. aligns findings to labels by callee method name and emits
//     (score, survived) samples;
//  4. reports MAE / R² / Brier / ROC-AUC and a per-detector precision ranking
//     that surfaces the low-signal ("mierda") detectors first.
//
// Usage:
//
//	audit-eval <module-dir> <pkg> [pkg...]
//	  e.g. audit-eval ../testigo-usage ./internal/auctions ./internal/products
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lautaromei/testigo/internal/boundarymut"
	"github.com/lautaromei/testigo/internal/eval"
	"github.com/lautaromei/testigo/internal/srcmut"
)

// experimentalRules mirror internal/core's off-by-default detectors; they are
// excluded from the precision-floor gate (they ship off and are only measured).
var experimentalRules = map[string]bool{
	"unpinned-arg": true,
}

// argOperator maps argument-level detectors to the srcmut operator that
// validates them (AUDIT_PLAN §9). Both predict that changing an argument value
// survives — unpinned-arg because no value is pinned, literal-pinned-once
// because the single observed value is never varied — so both are validated by
// ARG_CORRUPT (replacing the argument with its zero value).
var argOperator = map[string]srcmut.Operator{
	"unpinned-arg":        srcmut.ArgCorrupt, // detector 2 — value unconstrained (experimental: noisy)
	"argument-swap-blind": srcmut.ArgSwap,    // wrong-variable / swapped-arg (keyed by method)
}

// boundaryOperator maps each detector rule to the boundary mutation operator
// that validates it (AUDIT_PLAN §5/§9 "Validated by" column). Only these four
// scored detectors target a mutant class this oracle generates; every other
// detector needs a different oracle (gremlins relational-flip, custom
// arg/return/value/branch/force-error mutators, or the checked-coverage layer)
// and must NOT be scored against the boundary oracle — doing so would mislabel
// it as a false positive against the wrong mutant class.
var boundaryOperator = map[string]boundarymut.Operator{
	"loose-count":       boundarymut.DupCall,  // detector 4, dup-call
	"order-insensitive": boundarymut.SwapCall, // detector 7, reorder
}

// boundaryRuleFor is the reverse: which active detector each boundary operator
// validates. DropCall is absent — its detectors (never-asserted-method,
// event-drop) were removed as redundant under testigo's strict runtime.
var boundaryRuleFor = map[boundarymut.Operator]string{
	boundarymut.DupCall:  "loose-count",
	boundarymut.SwapCall: "order-insensitive",
}

type exportedFinding struct {
	Rule       string  `json:"rule"`
	Kind       string  `json:"kind"`
	Score      float64 `json:"score"`
	Observable bool    `json:"observable"`
	Site       string  `json:"site"`
	Type       string  `json:"odc_type"`
	Trigger    string  `json:"odc_trigger"`
	Impact     string  `json:"odc_impact"`
}

func main() {
	calibrateOut := flag.String("calibrate", "", "write generated per-detector calibration constants to this Go file")
	maxMAE := flag.Float64("max-mae", -1, "CI gate: exit non-zero if MAE exceeds this budget (disabled if < 0)")
	minPrec := flag.Float64("min-prec", -1, "CI gate: exit non-zero if any shipped detector's precision is below this floor (disabled if < 0)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: audit-eval [-calibrate file] [-max-mae m] [-min-prec p] <module-dir> <pkg> [pkg...]")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		flag.Usage()
		os.Exit(2)
	}
	moduleDir := args[0]
	pkgs := args[1:]

	var samples []eval.Sample
	var hazards, firedNoMutant, uncoveredSurvivors int
	firedUnmapped := map[string]int{} // scored rules fired but with no oracle we validate against

	for _, pkg := range pkgs {
		dir := filepath.Join(moduleDir, pkg)

		bMuts, err := boundaryMutants(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: boundary oracle %s: %v\n", pkg, err)
		}
		aMuts, err := argMutants(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: arg oracle %s: %v\n", pkg, err)
		}
		findings, inScope, err := auditFindings(moduleDir, pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: audit %s: %v\n", pkg, err)
			continue
		}

		pi := newPredictionIndex(findings)
		covered := map[string]bool{} // prediction keys a mutant aligned to

		pkgSamples := 0
		// Boundary mutants → samples. Each covered mutant on an in-scope (doubled)
		// callee becomes a sample whether it LIVED (positive) or was KILLED
		// (negative), so the metrics see both classes.
		for _, m := range bMuts {
			if m.Status != boundarymut.Lived && m.Status != boundarymut.Killed {
				continue
			}
			rule, ok := boundaryRuleFor[m.Operator]
			if !ok || !inScope[m.Method] {
				if m.Status == boundarymut.Lived && inScope[m.Method] {
					uncoveredSurvivors++ // a real gap no shipped detector covers
				}
				continue
			}
			samples = append(samples, eval.Sample{Rule: rule, Score: pi.boundaryScore(rule, m.Method), Survived: m.Status == boundarymut.Lived, Observable: true})
			covered[predKey(rule, m.Method, -1)] = true
			pkgSamples++
		}
		// Argument mutants → samples (ARG_SWAP by method, ARG_CORRUPT by arg index).
		for _, m := range aMuts {
			if m.Status != srcmut.Lived && m.Status != srcmut.Killed {
				continue
			}
			if !inScope[m.Method] {
				continue
			}
			switch m.Operator {
			case srcmut.ArgSwap:
				samples = append(samples, eval.Sample{Rule: "argument-swap-blind", Score: pi.swapScore(m.Method), Survived: m.Status == srcmut.Lived, Observable: true})
				covered[predKey("argument-swap-blind", m.Method, -1)] = true
				pkgSamples++
			case srcmut.ArgCorrupt:
				samples = append(samples, eval.Sample{Rule: "unpinned-arg", Score: pi.argScore(m.Method, m.ArgIndex), Survived: m.Status == srcmut.Lived, Observable: true})
				covered[predKey("unpinned-arg", m.Method, m.ArgIndex)] = true
				pkgSamples++
			default:
				if m.Status == srcmut.Lived {
					uncoveredSurvivors++
				}
			}
		}
		// Findings that fired but never aligned to a mutant: either no oracle we
		// validate against (firedUnmapped) or no covered mutant at their site.
		for _, f := range findings {
			if f.Kind != "scored" {
				hazards++
				continue
			}
			if !isValidatedRule(f.Rule) {
				firedUnmapped[f.Rule]++
				continue
			}
			idx := -1
			if f.Rule == "unpinned-arg" {
				if i, ok := siteArgIndex(f.Site); ok {
					idx = i
				}
			}
			if !covered[predKey(f.Rule, siteMethod(f.Site), idx)] {
				firedNoMutant++
			}
		}
		fmt.Printf("%-28s mutants=%d  samples=%d  findings=%d  in-scope=%d\n",
			pkg, len(bMuts)+len(aMuts), pkgSamples, len(findings), len(inScope))
	}

	report(samples, firedNoMutant, hazards, uncoveredSurvivors, firedUnmapped)

	if *calibrateOut != "" {
		if err := writeCalibration(*calibrateOut, samples); err != nil {
			fmt.Fprintf(os.Stderr, "calibrate: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nwrote calibration to %s\n", *calibrateOut)
	}

	if code := gate(samples, *maxMAE, *minPrec); code != 0 {
		os.Exit(code)
	}
}

// writeCalibration emits the generated calibration map: each detector with
// oracle samples gets its empirical precision as its fitted P(survive|fired)
// (AUDIT_PLAN §9.3). Detectors without samples are omitted and keep their prior.
func writeCalibration(path string, samples []eval.Sample) error {
	stats := eval.ByDetector(samples)
	sort.Slice(stats, func(i, j int) bool { return stats[i].Rule < stats[j].Rule })

	var b strings.Builder
	b.WriteString("// Code generated by audit-eval -calibrate; DO NOT EDIT.\n\n")
	b.WriteString("package core\n\n")
	b.WriteString("// auditCalibratedScores maps each scored detector that has offline oracle\n")
	b.WriteString("// samples to its fitted P(survive|fired) = empirical precision over the\n")
	b.WriteString("// benchmark corpus (AUDIT_PLAN §9.3). Detectors absent here keep the prior\n")
	b.WriteString("// score hard-coded in their inspect().\n")
	b.WriteString("var auditCalibratedScores = map[string]float64{\n")
	for _, d := range stats {
		if d.N == 0 {
			continue
		}
		fmt.Fprintf(&b, "\t%q: %.2f, // n=%d\n", d.Rule, d.Precision, d.N)
	}
	b.WriteString("}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("gofmt generated source: %w", err)
	}
	return os.WriteFile(path, src, 0o644)
}

// gate enforces the CI budget: MAE must stay under maxMAE and every shipped
// detector's precision must clear minPrec. Returns a non-zero exit code on
// violation. Either check is disabled when its threshold is negative.
func gate(samples []eval.Sample, maxMAE, minPrec float64) int {
	if len(samples) == 0 || (maxMAE < 0 && minPrec < 0) {
		return 0
	}
	code := 0
	if maxMAE >= 0 {
		if mae := eval.MAE(samples); mae > maxMAE {
			fmt.Fprintf(os.Stderr, "\nGATE FAIL: MAE=%.3f exceeds budget %.3f\n", mae, maxMAE)
			code = 1
		}
	}
	if minPrec >= 0 {
		for _, d := range eval.ByDetector(samples) {
			if experimentalRules[d.Rule] {
				continue // off-by-default detectors are not gated
			}
			if d.Precision < minPrec {
				fmt.Fprintf(os.Stderr, "GATE FAIL: detector %s precision %.2f below floor %.2f\n", d.Rule, d.Precision, minPrec)
				code = 1
			}
		}
	}
	return code
}

// isValidatedRule reports whether a detector has an oracle this harness scores
// it against (an active boundary operator, or one of the arg detectors).
func isValidatedRule(rule string) bool {
	if rule == "argument-swap-blind" || rule == "unpinned-arg" {
		return true
	}
	for _, r := range boundaryRuleFor {
		if r == rule {
			return true
		}
	}
	return false
}

func predKey(rule, method string, idx int) string {
	return fmt.Sprintf("%s\x00%s\x00%d", rule, method, idx)
}

// predictionIndex holds, per detector and site, the score the detector
// predicted (0 when it did not fire there). Built from the suite's findings.
type predictionIndex struct {
	boundary map[string]map[string]float64 // rule -> method -> score
	arg      map[string]float64            // "method\x00idx" -> score (unpinned-arg)
	swap     map[string]float64            // method -> score (argument-swap-blind)
}

func newPredictionIndex(findings []exportedFinding) predictionIndex {
	pi := predictionIndex{
		boundary: map[string]map[string]float64{},
		arg:      map[string]float64{},
		swap:     map[string]float64{},
	}
	for _, f := range findings {
		if f.Kind != "scored" {
			continue
		}
		method := siteMethod(f.Site)
		switch {
		case f.Rule == "argument-swap-blind":
			if f.Score > pi.swap[method] {
				pi.swap[method] = f.Score
			}
		case f.Rule == "unpinned-arg":
			idx, ok := siteArgIndex(f.Site)
			if !ok {
				continue
			}
			k := fmt.Sprintf("%s\x00%d", method, idx)
			if f.Score > pi.arg[k] {
				pi.arg[k] = f.Score
			}
		default:
			if _, ok := boundaryOperator[f.Rule]; ok {
				if pi.boundary[f.Rule] == nil {
					pi.boundary[f.Rule] = map[string]float64{}
				}
				if f.Score > pi.boundary[f.Rule][method] {
					pi.boundary[f.Rule][method] = f.Score
				}
			}
		}
	}
	return pi
}

func (pi predictionIndex) boundaryScore(rule, method string) float64 {
	return pi.boundary[rule][method]
}
func (pi predictionIndex) swapScore(method string) float64 { return pi.swap[method] }
func (pi predictionIndex) argScore(method string, idx int) float64 {
	return pi.arg[fmt.Sprintf("%s\x00%d", method, idx)]
}

// boundaryMutants / argMutants run the oracles and return the raw covered
// mutants. Bidirectional alignment (mutant -> prediction) happens in main.
func boundaryMutants(dir string) ([]boundarymut.Mutant, error) {
	res, err := boundarymut.Run(boundarymut.Options{Dir: dir})
	if err != nil {
		return nil, err
	}
	return res.Mutants, nil
}

func argMutants(dir string) ([]srcmut.Mutant, error) {
	res, err := srcmut.Run(srcmut.Options{Dir: dir})
	if err != nil {
		return nil, err
	}
	return res.Mutants, nil
}

func auditFindings(moduleDir, pkg string) ([]exportedFinding, map[string]bool, error) {
	out := filepath.Join(os.TempDir(), "audit-eval-findings.jsonl")
	_ = os.Remove(out)
	_ = os.Remove(out + ".methods")
	cmd := exec.Command("go", "test", "-count=1", pkg)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "TESTIGO_AUDIT=warn", "TESTIGO_AUDIT_JSON="+out, "TESTIGO_AUDIT_EXPERIMENTAL=on")
	_, _ = cmd.CombinedOutput() // test failures don't block finding capture

	f, err := os.Open(out)
	if err != nil {
		return nil, nil, fmt.Errorf("no findings file: %w", err)
	}
	defer f.Close()
	var findings []exportedFinding
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ef exportedFinding
		if err := json.Unmarshal([]byte(line), &ef); err != nil {
			continue
		}
		findings = append(findings, ef)
	}
	return findings, readObservedMethods(out + ".methods"), sc.Err()
}

// readObservedMethods loads the boundary-observable callee universe the suite
// exported (the in-scope set for sampling). Missing/empty → empty set.
func readObservedMethods(path string) map[string]bool {
	set := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	var names []string
	if json.Unmarshal(data, &names) != nil {
		return set
	}
	for _, n := range names {
		set[n] = true
	}
	return set
}

// siteMethod extracts the callee method name from a finding site such as
// "auctiontest.NotifierSpy.AuctionClosed arg#1" -> "AuctionClosed".
func siteMethod(site string) string {
	s := site
	if i := strings.Index(s, " "); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// siteArgIndex extracts the argument index from a site like
// "shippingtest.NotifierSpy.Delivered arg#1" -> 1.
func siteArgIndex(site string) (int, bool) {
	i := strings.Index(site, "arg#")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(site[i+len("arg#"):]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// neededOracle names the oracle each unvalidated scored detector still needs,
// so the coverage gap is explicit rather than silent (AUDIT_PLAN §9 operator
// table). Detectors absent here are either boundary-validated or hazards.
var neededOracle = map[string]string{
	"boundary-blind":         "relational-flip (gremlins)",
	"unpinned-arg":           "arg-corruption mutator",
	"outcome-unpinned":       "return-corruption mutator",
	"discarded-return":       "return-corruption mutator",
	"error-path-unexercised": "force-error mutator",
	"outcome-under-cover":    "branch-removal mutator",
	"overwrite-dead-store":   "dead-store-delete mutator",
	"unchecked-statement":    "checked-coverage oracle",
}

func report(samples []eval.Sample, firedNoMutant, hazards, uncoveredSurvivors int, firedUnmapped map[string]int) {
	survived := 0
	for _, s := range samples {
		if s.Survived {
			survived++
		}
	}
	fmt.Println()
	fmt.Println("=== eval (bidirectional: every in-scope mutant is a sample) ===")
	fmt.Printf("samples: %d  (%d LIVED / %d KILLED)\n", len(samples), survived, len(samples)-survived)
	fmt.Printf("uncovered survivors (LIVED, no shipped detector): %d\n", uncoveredSurvivors)
	fmt.Printf("findings fired but no aligned mutant: %d\n", firedNoMutant)
	fmt.Printf("hazards (excluded from metrics): %d\n", hazards)
	if len(samples) == 0 {
		fmt.Println("\nno aligned samples — cannot compute metrics.")
		return
	}

	ps := eval.PerSuite(samples)
	fmt.Printf("\nMAE=%.3f  R2=%.3f  Brier=%.3f  AUC=%.3f\n",
		eval.MAE(samples), eval.R2(samples), eval.Brier(samples), eval.AUC(samples))
	fmt.Printf("predicted-survival=%.2f  actual-survival=%.2f  observable-fraction=%.2f\n",
		ps.PredictedSurvivalRate, float64(survived)/float64(len(samples)), ps.ObservableFraction)

	fmt.Println("\nper-detector (worst precision first — these fire where the suite already kills the mutant):")
	fmt.Printf("  %-26s %5s %6s %6s %6s %9s\n", "detector", "n", "prec", "rec", "f1", "meanScore")
	for _, d := range eval.ByDetector(samples) {
		flag := ""
		if d.Precision == 0 {
			flag = "  ← mierda (all false positives)"
		}
		fmt.Printf("  %-26s %5d %6.2f %6.2f %6.2f %9.2f%s\n",
			d.Rule, d.N, d.Precision, d.Recall, d.F1, d.MeanScore, flag)
	}

	if len(firedUnmapped) > 0 {
		fmt.Println("\nfired but NOT validated by any oracle (need their own oracle):")
		rules := make([]string, 0, len(firedUnmapped))
		for r := range firedUnmapped {
			rules = append(rules, r)
		}
		sort.Strings(rules)
		for _, r := range rules {
			need := neededOracle[r]
			if need == "" {
				need = "?"
			}
			fmt.Printf("  %-26s n=%-3d needs %s\n", r, firedUnmapped[r], need)
		}
	}
}

// Command checkedcov-eval validates the checkedcov signal by mutation.
//
// For each package it runs the checked-coverage analysis and a statement-level
// mutation oracle (boundarymut: drop/dup/swap call statements), then joins them
// per line:
//
//	predicted unchecked + mutant LIVED  → TP (real gap, no assert caught it)
//	predicted checked    + mutant KILLED → TN (assert caught the mutation)
//	predicted unchecked  + mutant KILLED → FP (signal cried gap, but caught)
//	predicted checked    + mutant LIVED  → FN (signal said safe, but survived)
//
//	precision = TP/(TP+FP)   recall = TP/(TP+FN)
//
// GOROOT is read-only, so the mutation oracle needs writable package source;
// run this on a checked-out repo (testigo's own packages, or any native/testify
// suite). The plain `checkedcov` analysis still runs on stdlib directly.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lautaromei/testigo/internal/boundarymut"
	"github.com/lautaromei/testigo/internal/checkedcovssa"
)

type cell struct {
	TP, FP, TN, FN int
}

func (c cell) precision() float64 { return ratio(c.TP, c.TP+c.FP) }
func (c cell) recall() float64    { return ratio(c.TP, c.TP+c.FN) }
func (c cell) accuracy() float64  { return ratio(c.TP+c.TN, c.TP+c.TN+c.FP+c.FN) }

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

type pkgReport struct {
	Package        string  `json:"package"`
	UncheckedPct   int     `json:"unchecked_pct"`
	TP             int     `json:"tp"`
	FP             int     `json:"fp"`
	TN             int     `json:"tn"`
	FN             int     `json:"fn"`
	Precision      float64 `json:"precision"`
	Recall         float64 `json:"recall"`
	Accuracy       float64 `json:"accuracy"`
	EvaluatedSites int     `json:"evaluated_sites"`
	CrashKills     int     `json:"crash_kills"`
}

func main() {
	timeout := flag.Duration("timeout", 60*time.Second, "per-mutant test timeout")
	format := flag.String("format", "text", "output format: text|json")
	debug := flag.Bool("debug", false, "print per-line TP/FP/TN/FN classifications")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: checkedcov-eval [flags] <package-dir>...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	var reports []pkgReport
	var total cell
	for _, dir := range flag.Args() {
		rep, err := checkedcovssa.Analyze(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: analyze: %v\n", dir, err)
			continue
		}
		mut, err := boundarymut.Run(boundarymut.Options{Dir: dir, Timeout: *timeout})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: mutate: %v\n", dir, err)
			continue
		}

		var c cell
		crashed := 0
		seen := map[string]bool{} // dedup operators on the same line: first wins
		for _, m := range mut.Mutants {
			// Crash-kills (panic/timeout) caught the mutant without any assertion
			// reading the mutated value. checkedcov models asserted-value
			// dependence, so comparing against them is apples-to-oranges; skip.
			if m.Status == boundarymut.Crashed {
				if rep.IsCovered(m.File, m.Line) {
					crashed++
				}
				continue
			}
			if m.Status != boundarymut.Killed && m.Status != boundarymut.Lived {
				continue
			}
			if !rep.IsCovered(m.File, m.Line) {
				continue // not a non-structural covered statement in checkedcov's view
			}
			key := fmt.Sprintf("%s:%d", m.File, m.Line)
			if seen[key] {
				continue
			}
			seen[key] = true

			checked := rep.IsChecked(m.File, m.Line)
			killed := m.Status == boundarymut.Killed
			var label string
			switch {
			case !checked && !killed:
				c.TP++
				label = "TP"
			case checked && killed:
				c.TN++
				label = "TN"
			case !checked && killed:
				c.FP++
				label = "FP"
			case checked && !killed:
				c.FN++
				label = "FN"
			}
			if *debug {
				fmt.Fprintf(os.Stderr, "[%s] %s:%d  checked=%v killed=%v\n", label, m.File, m.Line, checked, killed)
			}
		}
		total.TP += c.TP
		total.FP += c.FP
		total.TN += c.TN
		total.FN += c.FN
		reports = append(reports, pkgReport{
			Package: rep.Package, UncheckedPct: rep.UncheckedPct,
			TP: c.TP, FP: c.FP, TN: c.TN, FN: c.FN,
			Precision: c.precision(), Recall: c.recall(), Accuracy: c.accuracy(),
			EvaluatedSites: c.TP + c.FP + c.TN + c.FN,
			CrashKills:     crashed,
		})
	}

	if *format == "json" {
		out := map[string]any{
			"packages":  reports,
			"precision": total.precision(),
			"recall":    total.recall(),
			"accuracy":  total.accuracy(),
			"tp":        total.TP, "fp": total.FP, "tn": total.TN, "fn": total.FN,
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("%-48s %5s %4s %4s %4s %4s  %5s %5s %5s %4s\n", "package", "sites", "TP", "FP", "TN", "FN", "prec", "rec", "acc", "cr")
	for _, r := range reports {
		fmt.Printf("%-48s %5d %4d %4d %4d %4d  %.2f  %.2f  %.2f %4d\n",
			trunc(r.Package, 48), r.EvaluatedSites, r.TP, r.FP, r.TN, r.FN, r.Precision, r.Recall, r.Accuracy, r.CrashKills)
	}
	fmt.Printf("\nCORPUS: %d sites — precision %.2f  recall %.2f  accuracy %.2f  (TP=%d FP=%d TN=%d FN=%d)\n",
		total.TP+total.FP+total.TN+total.FN, total.precision(), total.recall(), total.accuracy(),
		total.TP, total.FP, total.TN, total.FN)
	fmt.Println("precision = fraction of 'unchecked' calls that are real gaps (mutant survived).")
	fmt.Println("recall    = fraction of real gaps the signal flagged as unchecked.")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

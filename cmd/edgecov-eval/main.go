// Command edgecov-eval validates edgecov's reached-unchecked effect signal
// against real DROP_CALL / DROP_EVENT mutants from boundarymut.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lautaromei/testigo/internal/boundarymut"
	"github.com/lautaromei/testigo/internal/edgecovssa"
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
	Predictions    int     `json:"predictions"`
	JoinedPositive int     `json:"joined_positive"`
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
	project := flag.Bool("project", false, "project mode: predictions from edgecov -project and mutants judged against the whole module suite (`go test ./...`)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: edgecov-eval [flags] <package-dir>...   (with -project, a single module root)")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	// jobs pairs a package dir to mutate with the prediction set scoped to that
	// package and the project root to judge mutants against (empty = per-package).
	type job struct {
		dir         string
		predicted   map[string]bool
		projectRoot string
	}
	var jobs []job

	if *project {
		root := flag.Arg(0)
		rep, err := edgecovssa.AnalyzeProject(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: analyze -project: %v\n", root, err)
			os.Exit(1)
		}
		predicted := predictionSet(rep) // keyed by absolute path:line
		dirs, err := packageDirs(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: list packages: %v\n", root, err)
			os.Exit(1)
		}
		for _, dir := range dirs {
			jobs = append(jobs, job{dir: dir, predicted: predicted, projectRoot: root})
		}
	} else {
		for _, dir := range flag.Args() {
			rep, err := edgecovssa.Analyze(dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: analyze: %v\n", dir, err)
				continue
			}
			jobs = append(jobs, job{dir: dir, predicted: predictionSet(rep)})
		}
	}

	var reports []pkgReport
	var total cell
	for _, j := range jobs {
		absDir, err := filepath.Abs(j.dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: abs: %v\n", j.dir, err)
			continue
		}
		mut, err := boundarymut.Run(boundarymut.Options{Dir: j.dir, ProjectRoot: j.projectRoot, Timeout: *timeout})
		if err != nil {
			if strings.Contains(err.Error(), "no non-test") {
				continue // package has nothing to mutate
			}
			fmt.Fprintf(os.Stderr, "%s: mutate: %v\n", j.dir, err)
			continue
		}

		var c cell
		crashed := 0
		joinedPositive := 0
		seen := map[string]bool{}
		for _, m := range mut.Mutants {
			// DROP_CALL is the primary drop oracle; DROP_EVENT covers the
			// writeJSON-style sites where DROP_CALL is NOT_VIABLE (orphaned arg).
			// Per-site dedup below keeps the viable operator: a NOT_VIABLE mutant
			// is skipped before marking the site seen, so the DROP_EVENT variant
			// of the same site still gets evaluated.
			if m.Operator != boundarymut.DropCall && m.Operator != boundarymut.DropEvent {
				continue
			}
			if m.Status == boundarymut.Crashed {
				crashed++
				continue
			}
			if m.Status != boundarymut.Killed && m.Status != boundarymut.Lived {
				continue
			}
			// Key by absolute path so predictions (abs) and mutants (base, made
			// abs here) match, and same-base files across packages never collide.
			key := siteKey(filepath.Join(absDir, m.File), m.Line)
			if seen[key] {
				continue
			}
			seen[key] = true

			p := j.predicted[key]
			if p {
				joinedPositive++
			}
			lived := m.Status == boundarymut.Lived
			var label string
			switch {
			case p && lived:
				c.TP++
				label = "TP"
			case p && !lived:
				c.FP++
				label = "FP"
			case !p && !lived:
				c.TN++
				label = "TN"
			case !p && lived:
				c.FN++
				label = "FN"
			}
			if *debug {
				fmt.Fprintf(os.Stderr, "[%s] %s:%d predicted=%v status=%s method=%s\n", label, m.File, m.Line, p, m.Status, m.Method)
			}
		}
		// In project mode, a package with no evaluated sites adds only noise.
		if *project && c.TP+c.FP+c.TN+c.FN == 0 && crashed == 0 {
			continue
		}
		total.TP += c.TP
		total.FP += c.FP
		total.TN += c.TN
		total.FN += c.FN
		reports = append(reports, pkgReport{
			Package: mut.Package, Predictions: scopedPredictions(j.predicted, absDir), JoinedPositive: joinedPositive,
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

	fmt.Printf("%-56s %5s %5s %5s %4s %4s %4s %4s  %5s %5s %5s %4s\n", "package", "pred", "join", "sites", "TP", "FP", "TN", "FN", "prec", "rec", "acc", "cr")
	for _, r := range reports {
		fmt.Printf("%-56s %5d %5d %5d %4d %4d %4d %4d  %.2f  %.2f  %.2f %4d\n",
			trunc(r.Package, 56), r.Predictions, r.JoinedPositive, r.EvaluatedSites, r.TP, r.FP, r.TN, r.FN, r.Precision, r.Recall, r.Accuracy, r.CrashKills)
	}
	fmt.Printf("\nCORPUS: %d sites — precision %.2f  recall %.2f  accuracy %.2f  (TP=%d FP=%d TN=%d FN=%d)\n",
		total.TP+total.FP+total.TN+total.FN, total.precision(), total.recall(), total.accuracy(),
		total.TP, total.FP, total.TN, total.FN)
	fmt.Println("positive = edgecov effect-reached-unchecked; oracle = DROP_CALL/DROP_EVENT mutant lived.")
}

func siteKey(file string, line int) string {
	return fmt.Sprintf("%s:%d", file, line)
}

// predictionSet collects effect-reached-unchecked findings keyed by absolute
// path:line (Report.Finding.File is already an absolute cleaned path).
func predictionSet(rep edgecovssa.Report) map[string]bool {
	out := map[string]bool{}
	for _, f := range rep.Findings {
		if f.Kind != "effect-reached-unchecked" {
			continue
		}
		out[siteKey(f.File, f.Line)] = true
	}
	return out
}

// scopedPredictions counts how many predictions fall inside absDir, so a
// per-package row reports its own prediction count rather than the project total.
func scopedPredictions(predicted map[string]bool, absDir string) int {
	n := 0
	prefix := absDir + string(filepath.Separator)
	for k := range predicted {
		if strings.HasPrefix(k, prefix) {
			n++
		}
	}
	return n
}

// packageDirs lists the directories of every package under root via `go list`.
func packageDirs(root string) ([]string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			dirs = append(dirs, ln)
		}
	}
	return dirs, nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

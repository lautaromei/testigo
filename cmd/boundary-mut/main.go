// Command boundary-mut runs the boundary mutation oracle over a package and
// prints the per-mutant labels (KILLED/LIVED/...) plus a summary.
//
//	boundary-mut [-json] <package-dir>
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lautaromei/testigo/internal/boundarymut"
)

func main() {
	jsonOut := flag.Bool("json", false, "emit machine-readable JSON")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: boundary-mut [-json] <package-dir>")
		os.Exit(2)
	}

	res, err := boundarymut.Run(boundarymut.Options{Dir: flag.Arg(0)})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *jsonOut {
		b, err := res.WriteJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(b))
		return
	}

	for _, m := range res.Mutants {
		fmt.Printf("  %-11s %-10s %s:%d:%d  %s\n", m.Status, m.Operator, m.File, m.Line, m.Column, m.Method)
	}
	s := res.Summary()
	fmt.Printf("\nsummary: %d KILLED, %d LIVED, %d NOT_COVERED, %d NOT_VIABLE\n",
		s[boundarymut.Killed], s[boundarymut.Lived], s[boundarymut.NotCovered], s[boundarymut.NotViable])
	if s[boundarymut.Lived] > 0 {
		fmt.Printf("%d surviving boundary mutant(s) — real test gaps.\n", s[boundarymut.Lived])
	}
}

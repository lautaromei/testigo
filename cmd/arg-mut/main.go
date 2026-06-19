// Command arg-mut runs the argument mutation oracle (internal/srcmut) over a
// package and prints per-mutant labels plus a summary.
//
//	arg-mut [-json] <package-dir>
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lautaromei/testigo/internal/srcmut"
)

func main() {
	jsonOut := flag.Bool("json", false, "emit machine-readable JSON")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: arg-mut [-json] <package-dir>")
		os.Exit(2)
	}
	res, err := srcmut.Run(srcmut.Options{Dir: flag.Arg(0)})
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
		fmt.Printf("  %-11s %-11s %s:%d  %s arg#%d\n", m.Status, m.Operator, m.File, m.Line, m.Method, m.ArgIndex)
	}
	s := res.Summary()
	fmt.Printf("\nsummary: %d KILLED, %d LIVED, %d NOT_COVERED, %d NOT_VIABLE\n",
		s[srcmut.Killed], s[srcmut.Lived], s[srcmut.NotCovered], s[srcmut.NotViable])
}

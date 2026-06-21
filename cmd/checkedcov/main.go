// Command checkedcov reports covered-but-unchecked statement-lines: code the
// test suite executes but never lets influence an asserted value. It recognizes
// native testing.T/B asserts, testify, and testigo oracles out of the box.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lautaromei/testigo/internal/checkedcovssa"
)

func main() {
	format := flag.String("format", "text", "output format: text|json")
	jsonOut := flag.String("json-out", "", "write JSON report to PATH (implies json content)")
	minUnchecked := flag.Int("min-unchecked", -1, "exit non-zero if unchecked%% exceeds N (CI gate; -1 disables)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: checkedcov [flags] <package-dir>   e.g. checkedcov ./memdb")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	rep, err := checkedcovssa.Analyze(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *jsonOut != "" {
		data, err := rep.JSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(*jsonOut, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	switch {
	case *format == "json":
		data, err := rep.JSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	case *jsonOut == "":
		fmt.Print(rep.Text())
	}

	if *minUnchecked >= 0 && rep.UncheckedPct > *minUnchecked {
		fmt.Fprintf(os.Stderr, "gate: unchecked %d%% exceeds threshold %d%%\n", rep.UncheckedPct, *minUnchecked)
		os.Exit(3)
	}
}

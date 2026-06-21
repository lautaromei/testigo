// Command edgecov reports checked-edge coverage gaps: reachable call edges,
// branch sides, and side effects that tests did not execute or did execute
// without any asserted value depending on them.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lautaromei/testigo/internal/edgecovssa"
)

func main() {
	format := flag.String("format", "text", "output format: text|json|dot")
	jsonOut := flag.String("json-out", "", "write JSON report to PATH")
	dotOut := flag.String("dot-out", "", "write DOT graph to PATH")
	project := flag.Bool("project", false, "analyze all packages under DIR with project-wide coverage")
	coverProfile := flag.String("coverprofile", "", "read an existing Go coverage profile instead of running tests")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: edgecov [flags] <package-dir>   e.g. edgecov ./memdb")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	rep, err := edgecovssa.AnalyzeWithOptions(edgecovssa.Options{
		Dir:          flag.Arg(0),
		Project:      *project,
		CoverProfile: *coverProfile,
	})
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
	if *dotOut != "" {
		if err := os.WriteFile(*dotOut, []byte(rep.DOT()), 0o644); err != nil {
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
	case *format == "dot":
		fmt.Print(rep.DOT())
	case *jsonOut == "":
		fmt.Print(rep.Text())
	}
}

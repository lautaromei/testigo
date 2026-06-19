package main

import (
	"fmt"
	"os"

	"github.com/lautaromei/testigo/internal/checkedcovssa"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: checkedcov <package-dir>   e.g. checkedcov ./memdb")
		os.Exit(2)
	}
	if err := checkedcovssa.Run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

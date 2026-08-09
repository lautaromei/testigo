// Command testigo-gen generates optional test fixtures, spies, doubles groups,
// seeders, random object factories, and memdb helpers from a tagged spec struct.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lautaromei/testigo/internal/testigogen"
)

func main() {
	var opts testigogen.Options
	var output string
	var check bool
	flag.StringVar(&opts.Dir, "dir", ".", "package directory containing the specification")
	flag.StringVar(&opts.SpecType, "type", "testigoSpec", "tagged specification struct type")
	flag.StringVar(&output, "output", "zz_testigo_gen.go", "generated file, or - for stdout")
	flag.BoolVar(&check, "check", false, "fail when output is missing or stale")
	flag.Parse()

	source, err := testigogen.Generate(opts)
	if err != nil {
		fatal(err)
	}
	if output == "-" {
		if check {
			fatal(fmt.Errorf("testigo-gen: -check requires a file output"))
		}
		_, _ = os.Stdout.Write(source)
		return
	}

	path := output
	if !filepath.IsAbs(path) {
		path = filepath.Join(opts.Dir, path)
	}
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			fatal(fmt.Errorf("testigo-gen: generated file is missing: %w", err))
		}
		if !bytes.Equal(current, source) {
			fatal(fmt.Errorf("testigo-gen: %s is stale; run testigo-gen without -check", path))
		}
		return
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, source) {
		return
	}
	if err := writeAtomic(path, source); err != nil {
		fatal(err)
	}
}

func writeAtomic(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("testigo-gen: create output directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".testigo-gen-*")
	if err != nil {
		return fmt.Errorf("testigo-gen: create temporary output: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("testigo-gen: write output: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("testigo-gen: set output permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("testigo-gen: close output: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("testigo-gen: replace output: %w", err)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

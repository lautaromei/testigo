package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/lautaromei/testigo/internal/checkedcovssa"
)

func main() {
	if len(os.Args) < 3 {
		usage()
	}

	cmd := os.Args[1]
	target := os.Args[2]

	var exitCode int
	switch cmd {
	case "audit":
		if err := runRuntimeAudit(target); err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
		}
		if err := checkedcovssa.Run(target); err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
		}
	case "checkedcov":
		if err := checkedcovssa.Run(target); err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
		}
	default:
		usage()
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: testigo <audit|checkedcov> <package-dir>")
	os.Exit(2)
}

func runRuntimeAudit(dir string) error {
	cmd := exec.Command("go", "test", "-mod=mod", "-count=1", "-v", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), ensureAuditEnv()...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	if err != nil {
		return fmt.Errorf("runtime audit: %w", err)
	}
	return nil
}

func ensureAuditEnv() []string {
	if os.Getenv("TESTIGO_AUDIT") != "" {
		return nil
	}
	return []string{"TESTIGO_AUDIT=warn"}
}

// Package audit exposes testigo's suite-level audit layer.
package audit

import (
	"os"
	"testing"

	"github.com/lautaromei/testigo/internal/core"
)

// Report runs suite-level audit detectors and prints findings according to
// TESTIGO_AUDIT. It returns true when the configured severity should fail the
// process.
func Report() bool {
	return core.AuditReport()
}

// Main wraps testing.M with a suite-end audit report.
func Main(m *testing.M) {
	code := m.Run()
	if Report() && code == 0 {
		code = 1
	}
	os.Exit(code)
}

// AsErrors makes audit findings fail the process when Main is used.
func AsErrors() {
	os.Setenv("TESTIGO_AUDIT", "error")
}

// Disable turns off one audit rule by name.
func Disable(rule string) {
	core.AuditDisable(rule)
}

// Ignore suppresses one finding site for a rule. Site is usually file:line.
func Ignore(rule, site string) {
	core.AuditIgnore(rule, site)
}

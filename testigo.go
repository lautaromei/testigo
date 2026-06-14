// Package testigo provides hand-written test doubles for Go.
package testigo

import (
	"testing"

	"github.com/lautaromei/testigo/internal/core"
	"github.com/lautaromei/testigo/random"
)

// Spy records the calls a test double receives.
type Spy = core.Spy

// Matcher matches a call argument when an exact expected value is too rigid.
type Matcher = core.Matcher

// NewSpy builds a standalone Spy.
func NewSpy() *Spy {
	return core.NewSpy()
}

// Seed fixes the run's random seed from a readable word phrase.
func Seed(phrase string) {
	if err := random.SetSeedPhrase(phrase); err != nil {
		panic(err)
	}
}

// Copy returns a deep copy of v that shares no memory with the original.
func Copy[T any](v T) T {
	return core.Clone(v)
}

// NewDouble registers a test double for the given test and captures its current
// state as the baseline restored before each Run. Returns the same pointer.
func NewDouble[T any](t *testing.T, double *T) *T {
	return core.NewDouble(t, double)
}

// MethodCoverage is the coverage of a single spied method.
type MethodCoverage = core.MethodCoverage

// Coverage returns the interaction coverage of every spied method observed so far.
func Coverage() []MethodCoverage {
	return core.Coverage()
}

// CoverageReport renders the interaction coverage as a human-readable summary.
func CoverageReport() string {
	return core.CoverageReport()
}

// Run runs a subtest with every double registered for t restored to its
// initial state beforehand. It also binds the subtest to its goroutine, so
// assert chains inside it need no testing.T.
func Run(t *testing.T, name string, testFunc func(t *testing.T)) {
	t.Helper()
	core.Run(t, name, testFunc)
}

// Reset restores all doubles registered for t immediately, without running a
// subtest.
func Reset(t *testing.T) {
	core.Reset(t)
}

// Resetter is a double that restores its own state. Any double embedding Spy
// satisfies it for free (Spy.Reset clears recorded calls); a double backed by
// external state a struct copy cannot reach — a database, a temp dir —
// implements Reset to wipe and re-seed that state, and NewDouble runs it at
// every reset point with no separate hook.
type Resetter = core.Resetter

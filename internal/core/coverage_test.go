package core

import (
	"strings"
	"testing"
)

func TestInteractionCoverage(t *testing.T) {
	ResetCoverage()
	defer ResetCoverage()

	if got := Coverage(); len(got) != 0 {
		t.Fatalf("Coverage after reset should be empty, got %d entries", len(got))
	}
	if got := CoverageReport(); !strings.Contains(got, "no spied calls") {
		t.Fatalf("empty CoverageReport should report no calls, got: %q", got)
	}

	noteObserved("pkg.Cashier", "charge")
	noteObserved("pkg.Cashier", "charge")
	noteObserved("pkg.Room", "book")
	noteAsserted("pkg.Cashier", "charge")

	noteObserved("", "ignored")
	noteObserved("Unknown", "ignored")
	noteAsserted("", "ignored")

	cov := Coverage()
	if len(cov) != 2 {
		t.Fatalf("expected 2 observed methods, got %d: %+v", len(cov), cov)
	}

	charge := cov[0]
	if charge.Method != "pkg.Cashier.charge" || charge.Calls != 2 || !charge.Asserted {
		t.Errorf("unexpected charge coverage: %+v", charge)
	}

	book := cov[1]
	if book.Method != "pkg.Room.book" || book.Calls != 1 || book.Asserted {
		t.Errorf("unexpected book coverage: %+v", book)
	}

	report := CoverageReport()
	if !strings.Contains(report, "1/2 spied methods verified") {
		t.Errorf("report missing summary line: %q", report)
	}
	if !strings.Contains(report, "pkg.Room.book") || !strings.Contains(report, "called but never asserted") {
		t.Errorf("report missing unverified method detail: %q", report)
	}
}

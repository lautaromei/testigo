package core

import (
	"fmt"
	"sort"
	"strings"
)

type duplicateTestCaseDetector struct{}

func (duplicateTestCaseDetector) name() string { return "duplicate-test-case" }

func (duplicateTestCaseDetector) odc() ODC {
	return ODC{
		Type:      "Checking",
		Trigger:   "Coverage",
		Qualifier: "Extraneous",
		Impact:    "Maintainability",
	}
}

func (duplicateTestCaseDetector) kind() findingKind { return scored }

func (d duplicateTestCaseDetector) inspect(a *acc) []scoredFinding {
	a.mu.Lock()
	defer a.mu.Unlock()

	bySignature := map[string][]testCaseDigest{}
	for _, testCase := range a.testCases {
		if testCase.signature == "" {
			continue
		}
		bySignature[testCase.signature] = append(bySignature[testCase.signature], testCase)
	}

	signatures := make([]string, 0, len(bySignature))
	for signature, group := range bySignature {
		if len(group) < 2 {
			continue
		}
		signatures = append(signatures, signature)
	}
	sort.Slice(signatures, func(i, j int) bool {
		gi, gj := bySignature[signatures[i]], bySignature[signatures[j]]
		if len(gi) != len(gj) {
			return len(gi) > len(gj)
		}
		return duplicateTestCaseSite(gi) < duplicateTestCaseSite(gj)
	})

	findings := make([]scoredFinding, 0, len(signatures))
	for _, signature := range signatures {
		group := bySignature[signature]
		sort.Slice(group, func(i, j int) bool { return group[i].name < group[j].name })
		first := group[0]
		findings = append(findings, scoredFinding{
			rule:       d.name(),
			kind:       d.kind(),
			odc:        d.odc(),
			score:      0.61,
			observable: true,
			site:       duplicateTestCaseSite(group),
			message: fmt.Sprintf(
				"%d tests have the same interaction/assertion signature (%d call(s), %d expectation(s), %d value assertion(s)): %s",
				len(group),
				first.callCount,
				first.expectationCount,
				first.valueAsserts,
				strings.Join(duplicateTestCaseNames(group), ", "),
			),
			fix: aiFix{
				Problem:      "duplicate_test_case_signature",
				SuggestedFix: "Merge the duplicate cases, or change one case to use different inputs, branches, or assertions so it protects a distinct behavior.",
			},
		})
	}
	return findings
}

func duplicateTestCaseNames(group []testCaseDigest) []string {
	names := make([]string, 0, len(group))
	for _, testCase := range group {
		names = append(names, testCase.name)
	}
	sort.Strings(names)
	return names
}

func duplicateTestCaseSite(group []testCaseDigest) string {
	names := duplicateTestCaseNames(group)
	if len(names) == 0 {
		return "duplicate tests"
	}
	if len(names) == 1 {
		return names[0]
	}
	return names[0] + " <=> " + names[1]
}

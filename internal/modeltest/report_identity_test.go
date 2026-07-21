package modeltest

import "testing"

func TestJUnitSuiteUsesFullRelativeFileIdentity(t *testing.T) {
	res := &Results{Tests: []TestResult{{
		Name: "teams/acme/access/group/can-view",
		File: "tests/teams/acme/access.test.yaml",
		Assertions: []AssertionResult{{
			Kind: kindCheck, Subject: "check user:anne viewer document:1", Passed: true,
		}},
	}}}
	suites, _ := buildJUnitSuites(res)
	if len(suites) != 1 || suites[0].Name != "teams/acme/access" {
		t.Fatalf("suite names = %+v, want teams/acme/access", suites)
	}
}

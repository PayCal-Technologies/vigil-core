package cli

import "testing"

func TestExitTaxonomyIsCompleteAndStable(t *testing.T) {
	tests := []struct {
		code   int
		name   string
		status string
	}{
		{ExitSuccess, "success", "ok"},
		{ExitCheckFailed, "check_failed", "failed"},
		{ExitUsage, "usage_or_config", "invalid"},
		{ExitPolicyBlocked, "policy_blocked", "blocked"},
		{ExitDependencyMissing, "dependency_missing", "dependency_missing"},
		{ExitInterrupted, "timeout_or_cancelled", "interrupted"},
		{ExitMutationViolation, "mutation_violation", "mutation_violation"},
		{ExitInternal, "internal_error", "internal_error"},
	}
	for _, test := range tests {
		class := ClassifyExit(test.code)
		if class.Code != test.code || class.Name != test.name || class.Status != test.status {
			t.Fatalf("ClassifyExit(%d) = %#v", test.code, class)
		}
		if err := ValidateExit(test.code); err != nil {
			t.Fatalf("ValidateExit(%d): %v", test.code, err)
		}
	}
	if err := ValidateExit(8); err == nil {
		t.Fatal("ValidateExit accepted an unknown code")
	}
	if class := ClassifyExit(99); class.Code != ExitInternal {
		t.Fatalf("unknown exit classified as %#v", class)
	}
}

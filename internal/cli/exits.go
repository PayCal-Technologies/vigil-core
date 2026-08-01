package cli

import "fmt"

const (
	ExitSuccess           = 0
	ExitCheckFailed       = 1
	ExitUsage             = 2
	ExitPolicyBlocked     = 3
	ExitDependencyMissing = 4
	ExitInterrupted       = 5
	ExitMutationViolation = 6
	ExitInternal          = 7
)

type ExitClass struct {
	Code   int    `json:"code"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

var exitClasses = map[int]ExitClass{
	ExitSuccess:           {Code: ExitSuccess, Name: "success", Status: "ok"},
	ExitCheckFailed:       {Code: ExitCheckFailed, Name: "check_failed", Status: "failed"},
	ExitUsage:             {Code: ExitUsage, Name: "usage_or_config", Status: "invalid"},
	ExitPolicyBlocked:     {Code: ExitPolicyBlocked, Name: "policy_blocked", Status: "blocked"},
	ExitDependencyMissing: {Code: ExitDependencyMissing, Name: "dependency_missing", Status: "dependency_missing"},
	ExitInterrupted:       {Code: ExitInterrupted, Name: "timeout_or_cancelled", Status: "interrupted"},
	ExitMutationViolation: {Code: ExitMutationViolation, Name: "mutation_violation", Status: "mutation_violation"},
	ExitInternal:          {Code: ExitInternal, Name: "internal_error", Status: "internal_error"},
}

func ClassifyExit(code int) ExitClass {
	if class, ok := exitClasses[code]; ok {
		return class
	}
	return ExitClass{
		Code:   ExitInternal,
		Name:   "internal_error",
		Status: "internal_error",
	}
}

func ValidateExit(code int) error {
	if _, ok := exitClasses[code]; !ok {
		return fmt.Errorf("unsupported Vigil exit code %d", code)
	}
	return nil
}

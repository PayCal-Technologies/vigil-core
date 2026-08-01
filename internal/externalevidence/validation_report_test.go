package externalevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildValidationReportAcceptsCriterionProof(t *testing.T) {
	data := marshalReport(t, validReport())
	result := BuildValidationReport("docs/reviews/external.json", "VIGIL-AC-16", data)
	if err := ValidateValidationReport(result); err != nil {
		t.Fatal(err)
	}
	if result.Status != ValidationStatusValid || result.Criterion != "VIGIL-AC-16" {
		t.Fatalf("validation report = %#v", result)
	}
	if len(result.VerifiedCriteria) != 1 || result.VerifiedCriteria[0] != "VIGIL-AC-16" {
		t.Fatalf("verified criteria = %#v", result.VerifiedCriteria)
	}
}

func TestBuildValidationReportRejectsMissingCriterionProof(t *testing.T) {
	data := marshalReport(t, validReport())
	result := BuildValidationReport("docs/reviews/external.json", "VIGIL-AC-20", data)
	if err := ValidateValidationReport(result); err != nil {
		t.Fatal(err)
	}
	if result.Status != ValidationStatusInvalid || !strings.Contains(strings.Join(result.Errors, "\n"), "does not verify VIGIL-AC-20") {
		t.Fatalf("validation report = %#v", result)
	}
}

func TestBuildValidationReportRejectsInvalidJSON(t *testing.T) {
	result := BuildValidationReport("docs/reviews/external.json", "", []byte(`{"schema_version":"1"`))
	if err := ValidateValidationReport(result); err != nil {
		t.Fatal(err)
	}
	if result.Status != ValidationStatusInvalid || len(result.Errors) == 0 {
		t.Fatalf("validation report = %#v", result)
	}
}

func TestInvalidValidationReportRequiresErrors(t *testing.T) {
	report := InvalidValidationReport("docs/reviews/external.json", "VIGIL-AC-16", nil)
	if err := ValidateValidationReport(report); err != nil {
		t.Fatal(err)
	}
	report.Errors = nil
	if err := ValidateValidationReport(report); err == nil || !strings.Contains(err.Error(), "errors") {
		t.Fatalf("ValidateValidationReport error = %v", err)
	}
}

func TestValidateValidationReportRejectsWhitespaceOnlyFields(t *testing.T) {
	valid := BuildValidationReport("docs/reviews/external.json", "VIGIL-AC-16", marshalReport(t, validReport()))
	if err := ValidateValidationReport(valid); err != nil {
		t.Fatal(err)
	}
	valid.Report = " "
	if err := ValidateValidationReport(valid); err == nil || !strings.Contains(err.Error(), "report path") {
		t.Fatalf("blank report error = %v", err)
	}

	invalid := InvalidValidationReport("docs/reviews/external.json", "VIGIL-AC-16", nil)
	invalid.Errors[0] = " "
	if err := ValidateValidationReport(invalid); err == nil || !strings.Contains(err.Error(), "error 0") {
		t.Fatalf("blank error error = %v", err)
	}
}

func marshalReport(t *testing.T, report Report) []byte {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

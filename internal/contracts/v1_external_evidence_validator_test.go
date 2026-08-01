package contracts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/externalevidence"
)

func TestV1ExternalEvidenceValidatorJSONContract(t *testing.T) {
	root := filepath.Join("..", "..")
	binary := filepath.Join(t.TempDir(), "validate-v1-external-evidence")
	build := exec.Command("go", "build", "-mod=readonly", "-buildvcs=false", "-trimpath", "-o", binary, "./scripts/validate-v1-external-evidence.go")
	build.Dir = root
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build external evidence validator: %v\n%s", err, output)
	}

	reportPath := filepath.Join(t.TempDir(), "external-evidence.json")
	writeValidatorExternalReport(t, reportPath)

	valid, code := runExternalEvidenceValidator(t, root, binary, "--report", reportPath, "--criterion", "VIGIL-AC-16", "--json")
	if code != 0 || valid.Status != externalevidence.ValidationStatusValid || valid.Criterion != "VIGIL-AC-16" {
		t.Fatalf("valid report code=%d report=%#v", code, valid)
	}

	missingCriterion, code := runExternalEvidenceValidator(t, root, binary, "--report", reportPath, "--criterion", "VIGIL-AC-20", "--json")
	if code != 1 || missingCriterion.Status != externalevidence.ValidationStatusInvalid ||
		!strings.Contains(strings.Join(missingCriterion.Errors, "\n"), "does not verify VIGIL-AC-20") {
		t.Fatalf("missing-criterion report code=%d report=%#v", code, missingCriterion)
	}

	missingReport, code := runExternalEvidenceValidator(t, root, binary, "--json")
	if code != 2 || missingReport.Status != externalevidence.ValidationStatusInvalid ||
		!strings.Contains(strings.Join(missingReport.Errors, "\n"), "usage:") {
		t.Fatalf("missing-report report code=%d report=%#v", code, missingReport)
	}

	badFlag, code := runExternalEvidenceValidator(t, root, binary, "--report", reportPath, "--json", "--bogus")
	if code != 2 || badFlag.Status != externalevidence.ValidationStatusInvalid || badFlag.Report != reportPath ||
		!strings.Contains(strings.Join(badFlag.Errors, "\n"), "flag provided but not defined") {
		t.Fatalf("bad-flag report code=%d report=%#v", code, badFlag)
	}
}

func runExternalEvidenceValidator(t *testing.T, root, binary string, args ...string) (externalevidence.ValidationReport, int) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = root
	output, err := command.Output()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run validator %v: %v", args, err)
		}
		code = exitErr.ExitCode()
		output = append(output, exitErr.Stderr...)
	}
	var report externalevidence.ValidationReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("validator output is not JSON for %v: %v\n%s", args, err, output)
	}
	if err := externalevidence.ValidateValidationReport(report); err != nil {
		t.Fatalf("validator report is invalid for %v: %v\n%s", args, err, output)
	}
	return report, code
}

func writeValidatorExternalReport(t *testing.T, path string) {
	t.Helper()
	report := externalevidence.Report{
		SchemaVersion:    externalevidence.SchemaVersion,
		Target:           "v1.0",
		GeneratedAt:      time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		CandidateCommit:  "0123456789abcdef0123456789abcdef01234567",
		CandidateVersion: "1.0.0-rc.1",
		PublicURL:        "https://github.com/PayCal-Technologies/vigil-public/issues/16",
		Reviewer: externalevidence.Reviewer{
			Name:         "Independent Reviewer",
			Organization: "External Lab",
			Relationship: "No employment, authorship, or financial relationship with the implementation.",
			Independent:  true,
		},
		Criteria: []externalevidence.CriterionResult{{
			ID:       "VIGIL-AC-16",
			Status:   "verified",
			Detail:   "external integration consumed public contracts",
			Evidence: []string{"https://github.com/PayCal-Technologies/vigil-public/issues/17"},
		}},
		Findings: []externalevidence.Finding{},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

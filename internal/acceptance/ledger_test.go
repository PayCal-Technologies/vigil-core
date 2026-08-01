package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/externalevidence"
	"github.com/PayCal-Technologies/vigil-public/internal/operationalevidence"
)

func TestGateStableV1RequiresEveryCriterionVerified(t *testing.T) {
	ledger := validLedger()
	for index := range ledger.Criteria {
		if ledger.Criteria[index].ID == "VIGIL-AC-16" {
			ledger.Criteria[index].Status = StatusExternalPending
			ledger.Criteria[index].Blocker = "Independent review has not completed."
		}
	}

	required, pending, err := Gate("1.0.0", ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !required || len(pending) != 1 || pending[0].ID != "VIGIL-AC-16" {
		t.Fatalf("stable v1 gate = required:%v pending:%#v", required, pending)
	}

	for _, version := range []string{"0.9.0", "1.0.0-rc.1", "v2.0.0-beta.2"} {
		required, pending, err := Gate(version, ledger)
		if err != nil {
			t.Fatalf("%s: %v", version, err)
		}
		if required || pending == nil || len(pending) != 0 {
			t.Fatalf("%s unexpectedly requires complete acceptance: required=%v pending=%#v", version, required, pending)
		}
	}

	for index := range ledger.Criteria {
		if ledger.Criteria[index].ID == "VIGIL-AC-16" {
			ledger.Criteria[index].Status = StatusVerified
			ledger.Criteria[index].Blocker = ""
		}
	}
	required, pending, err = Gate("v1.0.0+build.7", ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !required || len(pending) != 0 {
		t.Fatalf("complete stable v1 gate = required:%v pending:%#v", required, pending)
	}
}

func TestBuildGateReportProducesMachineReadableStatuses(t *testing.T) {
	ledger := validLedger()
	for index := range ledger.Criteria {
		if ledger.Criteria[index].ID == "VIGIL-AC-16" {
			ledger.Criteria[index].Status = StatusExternalPending
			ledger.Criteria[index].Blocker = "Independent review has not completed."
		}
	}

	report, err := BuildGateReport("1.0.0", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != GateStatusBlocked || !report.GateRequired || report.PendingCount != 1 || report.Pending[0].ID != "VIGIL-AC-16" {
		t.Fatalf("blocked report = %#v", report)
	}

	report, err = BuildGateReport("0.9.0", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != GateStatusNotRequired || report.GateRequired || report.PendingCount != 0 {
		t.Fatalf("not-required report = %#v", report)
	}

	for index := range ledger.Criteria {
		if ledger.Criteria[index].ID == "VIGIL-AC-16" {
			ledger.Criteria[index].Status = StatusVerified
			ledger.Criteria[index].Blocker = ""
		}
	}
	report, err = BuildGateReport("1.0.0", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != GateStatusSatisfied || !report.GateRequired || report.PendingCount != 0 {
		t.Fatalf("satisfied report = %#v", report)
	}

	invalid := InvalidGateReport("", CanonicalLedgerPath, fmt.Errorf("invalid semantic version"))
	if err := ValidateGateReport(invalid); err != nil {
		t.Fatalf("invalid error report should still validate: %v", err)
	}
	invalid.Errors = nil
	if err := ValidateGateReport(invalid); err == nil || !strings.Contains(err.Error(), "errors") {
		t.Fatalf("empty invalid report error = %v", err)
	}
}

func TestValidateGateReportRejectsWhitespaceOnlyFieldsAndCountDrift(t *testing.T) {
	ledger := validLedger()
	for index := range ledger.Criteria {
		if ledger.Criteria[index].ID == "VIGIL-AC-16" {
			ledger.Criteria[index].Status = StatusExternalPending
			ledger.Criteria[index].Blocker = "Independent review has not completed."
		}
	}
	blocked, err := BuildGateReport("1.0.0", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		mutate  func(*GateReport)
		wantErr string
	}{
		{"version", func(report *GateReport) { report.Version = " " }, "version"},
		{"ledger", func(report *GateReport) { report.AcceptanceLedger = " " }, "ledger path"},
		{"error", func(report *GateReport) {
			*report = InvalidGateReport("", CanonicalLedgerPath, fmt.Errorf("fixture"))
			report.Errors[0] = " "
		}, "error 0"},
		{"pending count", func(report *GateReport) { report.PendingCount = 2 }, "pending_count"},
		{"pending domain", func(report *GateReport) { report.Pending[0].Domain = " " }, "domain and statement"},
		{"pending statement", func(report *GateReport) { report.Pending[0].Statement = " " }, "domain and statement"},
		{"pending status", func(report *GateReport) { report.Pending[0].Status = "pending" }, "unsupported status"},
		{"pending blocker", func(report *GateReport) { report.Pending[0].Blocker = " " }, "blocker"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := blocked
			report.Pending = append([]GatePendingCriterion(nil), blocked.Pending...)
			report.Errors = append([]string{}, blocked.Errors...)
			test.mutate(&report)
			if err := ValidateGateReport(report); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateGateReport error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestGateRejectsMissingRequiredV1Criterion(t *testing.T) {
	ledger := validLedger()
	ledger.Criteria = ledger.Criteria[:len(ledger.Criteria)-1]
	if _, _, err := Gate("0.9.0", ledger); err == nil || !strings.Contains(err.Error(), "VIGIL-AC-22") {
		t.Fatalf("missing-baseline error = %v", err)
	}
}

func TestReadIsStrictBoundedAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	validPath := filepath.Join(root, "acceptance.json")
	writeLedger(t, validPath, `{
	  "schema_version": "1",
	  "target": "v1.0",
	  "criteria": [{
	    "id": "VIGIL-AC-01",
	    "domain": "contract",
	    "statement": "Contract is verified.",
	    "status": "verified",
	    "evidence": [{"kind": "go_test", "path": "contract_test.go", "symbol": "TestContract"}]
	  }]
	}`)
	if _, err := Read(validPath); err != nil {
		t.Fatal(err)
	}

	unknownPath := filepath.Join(root, "unknown.json")
	writeLedger(t, unknownPath, `{
	  "schema_version": "1",
	  "target": "v1.0",
	  "criteria": [],
	  "unknown": true
	}`)
	if _, err := Read(unknownPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	linkPath := filepath.Join(root, "link.json")
	if err := os.Symlink(validPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(linkPath); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("symlink error = %v", err)
	}

	oversizedPath := filepath.Join(root, "oversized.json")
	if err := os.WriteFile(oversizedPath, make([]byte, MaxLedgerBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(oversizedPath); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestValidateRejectsIncompleteOrContradictoryCriteria(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Ledger)
	}{
		{"duplicate", func(ledger *Ledger) { ledger.Criteria = append(ledger.Criteria, ledger.Criteria[0]) }},
		{"unknown status", func(ledger *Ledger) { ledger.Criteria[0].Status = "passed" }},
		{"verified blocker", func(ledger *Ledger) { ledger.Criteria[0].Blocker = "stale" }},
		{"missing evidence", func(ledger *Ledger) { ledger.Criteria[0].Evidence = nil }},
		{"missing statement", func(ledger *Ledger) { ledger.Criteria[0].Statement = "" }},
		{"operational pending on external criterion", func(ledger *Ledger) {
			for index := range ledger.Criteria {
				if ledger.Criteria[index].ID == "VIGIL-AC-16" {
					ledger.Criteria[index].Status = StatusOperationalPending
					ledger.Criteria[index].Blocker = "wrong pending class"
				}
			}
		}},
		{"external pending on operational criterion", func(ledger *Ledger) {
			for index := range ledger.Criteria {
				if ledger.Criteria[index].ID == "VIGIL-AC-09" {
					ledger.Criteria[index].Status = StatusExternalPending
					ledger.Criteria[index].Blocker = "wrong pending class"
				}
			}
		}},
		{"external pending on local criterion", func(ledger *Ledger) {
			ledger.Criteria[0].Status = StatusExternalPending
			ledger.Criteria[0].Blocker = "wrong pending class"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ledger := validLedger()
			test.mutate(&ledger)
			if err := Validate(ledger); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestGateRejectsInvalidSemanticVersion(t *testing.T) {
	for _, version := range []string{"1", "1.0", "01.0.0", "v1.0.0-", "latest"} {
		if _, _, err := Gate(version, validLedger()); err == nil {
			t.Fatalf("expected %q to fail", version)
		}
	}
}

func TestValidateReleaseGateReportMatchesVersionSemantics(t *testing.T) {
	ledger := validLedger()
	notRequired, err := BuildGateReport("0.9.0", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseGateReport(notRequired, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseGateReport(notRequired, "0.9.1"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("version mismatch error = %v", err)
	}
	notRequired.AcceptanceLedger = "tmp/acceptance.json"
	if err := ValidateReleaseGateReport(notRequired, "0.9.0"); err == nil || !strings.Contains(err.Error(), "canonical ledger") {
		t.Fatalf("custom ledger error = %v", err)
	}

	prerelease, err := BuildGateReport("1.0.0-rc.1", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseGateReport(prerelease, "1.0.0-rc.1"); err != nil {
		t.Fatal(err)
	}

	satisfied, err := BuildGateReport("1.0.0", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseGateReport(satisfied, "1.0.0"); err != nil {
		t.Fatal(err)
	}

	for index := range ledger.Criteria {
		if ledger.Criteria[index].ID == "VIGIL-AC-16" {
			ledger.Criteria[index].Status = StatusExternalPending
			ledger.Criteria[index].Blocker = "Independent review has not completed."
		}
	}
	blocked, err := BuildGateReport("1.0.0", CanonicalLedgerPath, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseGateReport(blocked, "1.0.0"); err == nil || !strings.Contains(err.Error(), "requires satisfied") {
		t.Fatalf("blocked stable release gate error = %v", err)
	}
}

func TestValidateRepositoryEvidenceRejectsEscapeAndStaleTestSymbol(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "contract_test.go")
	if err := os.WriteFile(testPath, []byte("package fixture\nimport \"testing\"\nfunc TestContract(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := localEvidenceLedger()
	if err := ValidateRepositoryEvidence(root, ledger); err != nil {
		t.Fatal(err)
	}

	ledger.Criteria[0].Evidence[0].Path = "../outside.go"
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "unconfined") {
		t.Fatalf("escape error = %v", err)
	}
	ledger = localEvidenceLedger()
	ledger.Criteria[0].Evidence[0].Symbol = "TestMissing"
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "missing Go test") {
		t.Fatalf("stale-symbol error = %v", err)
	}
	ledger = localEvidenceLedger()
	ledger.Criteria[0].Evidence[0].Path = "contract.go"
	if err := os.WriteFile(filepath.Join(root, "contract.go"), []byte("package fixture\nimport \"testing\"\nfunc TestContract(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "*_test.go") {
		t.Fatalf("non-test path error = %v", err)
	}
	ledger = localEvidenceLedger()
	if err := os.WriteFile(testPath, []byte("package fixture\nfunc TestContract() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "missing Go test") {
		t.Fatalf("bad test signature error = %v", err)
	}
	ledger = localEvidenceLedger()
	ledger.Criteria[0].Evidence[0].Symbol = "Testcontract"
	if err := os.WriteFile(testPath, []byte("package fixture\nimport \"testing\"\nfunc Testcontract(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "missing Go test") {
		t.Fatalf("lowercase test suffix error = %v", err)
	}
	ledger = localEvidenceLedger()
	if err := os.WriteFile(testPath, []byte("package fixture\nimport \"testing\"\nfunc TestContract[T any](t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "missing Go test") {
		t.Fatalf("generic test function error = %v", err)
	}
}

func TestValidateRepositoryEvidenceAcceptsVerifiedOperationalReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "operational-evidence.json")
	writeOperationalReport(t, reportPath, "VIGIL-AC-09", "verified")
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-09",
			Domain:    "platform",
			Statement: "Native downloaded archives passed.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "operational_report", Path: "operational-evidence.json"}},
		}},
	}
	if err := ValidateRepositoryEvidence(root, ledger); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRepositoryEvidenceRejectsOperationalCriterionWithoutOperationalReport(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "contract_test.go")
	if err := os.WriteFile(testPath, []byte("package fixture\nimport \"testing\"\nfunc TestContract(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-09",
			Domain:    "platform",
			Statement: "Native downloaded archives passed.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "go_test", Path: "contract_test.go", Symbol: "TestContract"}},
		}},
	}
	err := ValidateRepositoryEvidence(root, ledger)
	if err == nil || !strings.Contains(err.Error(), "verified operational_report evidence") {
		t.Fatalf("missing operational report error = %v", err)
	}
}

func TestValidateRepositoryEvidenceRejectsOperationalReportForNonOperationalCriterion(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "operational-evidence.json")
	writeOperationalReport(t, reportPath, "VIGIL-AC-16", "verified")
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-16",
			Domain:    "review",
			Statement: "External integration validates public contracts.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "operational_report", Path: "operational-evidence.json"}},
		}},
	}
	err := ValidateRepositoryEvidence(root, ledger)
	if err == nil || !strings.Contains(err.Error(), "not accepted as verified operational evidence") {
		t.Fatalf("non-operational report error = %v", err)
	}
}

func TestValidateRepositoryEvidenceRejectsExternalCriterionWithoutExternalReport(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "contract_test.go")
	if err := os.WriteFile(testPath, []byte("package fixture\nimport \"testing\"\nfunc TestContract(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-16",
			Domain:    "review",
			Statement: "External integration validates public contracts.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "go_test", Path: "contract_test.go", Symbol: "TestContract"}},
		}},
	}
	err := ValidateRepositoryEvidence(root, ledger)
	if err == nil || !strings.Contains(err.Error(), "verified external_report evidence") {
		t.Fatalf("missing external report error = %v", err)
	}
}

func TestValidateRepositoryEvidenceForCandidateAcceptsBoundOperationalReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "operational-evidence.json")
	writeOperationalReport(t, reportPath, "VIGIL-AC-09", "verified")
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-09",
			Domain:    "platform",
			Statement: "Native downloaded archives passed.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "operational_report", Path: "operational-evidence.json"}},
		}},
	}
	err := ValidateRepositoryEvidenceForCandidate(root, ledger, EvidenceCandidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "0.4.0",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateRepositoryEvidenceForCandidateRejectsStaleOperationalReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "operational-evidence.json")
	writeOperationalReport(t, reportPath, "VIGIL-AC-09", "verified")
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-09",
			Domain:    "platform",
			Statement: "Native downloaded archives passed.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "operational_report", Path: "operational-evidence.json"}},
		}},
	}
	err := ValidateRepositoryEvidenceForCandidate(root, ledger, EvidenceCandidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "1.0.0",
	})
	if err == nil || !strings.Contains(err.Error(), "candidate version") {
		t.Fatalf("stale version error = %v", err)
	}
	err = ValidateRepositoryEvidenceForCandidate(root, ledger, EvidenceCandidate{
		Commit:  "abcdef0123456789abcdef0123456789abcdef01",
		Version: "0.4.0",
	})
	if err == nil || !strings.Contains(err.Error(), "candidate commit") {
		t.Fatalf("stale commit error = %v", err)
	}
}

func TestValidateRepositoryEvidenceForCandidateRejectsInProgressOperationalWorkflow(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "operational-evidence.json")
	writeOperationalReport(t, reportPath, "VIGIL-AC-09", "verified")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report operationalevidence.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	report.WorkflowRun.Status = "in_progress"
	report.WorkflowRun.Conclusion = ""
	data, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-09",
			Domain:    "platform",
			Statement: "Native downloaded archives passed.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "operational_report", Path: "operational-evidence.json"}},
		}},
	}
	err = ValidateRepositoryEvidenceForCandidate(root, ledger, EvidenceCandidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "0.4.0",
	})
	if err == nil || !strings.Contains(err.Error(), "completed successfully") {
		t.Fatalf("in-progress workflow error = %v", err)
	}
}

func TestValidateRepositoryEvidenceRejectsOperationalReportThatDoesNotProveCriterion(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "operational-evidence.json")
	writeOperationalReport(t, reportPath, "VIGIL-AC-09", "failed")
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-09",
			Domain:    "platform",
			Statement: "Native downloaded archives passed.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "operational_report", Path: "operational-evidence.json"}},
		}},
	}
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "verified operational_report evidence") {
		t.Fatalf("missing-proof error = %v", err)
	}
}

func TestValidateRepositoryEvidenceAcceptsVerifiedExternalReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "external-evidence.json")
	writeExternalReport(t, reportPath, "VIGIL-AC-16", "verified", true)
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-16",
			Domain:    "review",
			Statement: "External integration validates public contracts.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "external_report", Path: "external-evidence.json"}},
		}},
	}
	if err := ValidateRepositoryEvidence(root, ledger); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRepositoryEvidenceForCandidateAcceptsBoundExternalReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "external-evidence.json")
	writeExternalReport(t, reportPath, "VIGIL-AC-16", "verified", true)
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-16",
			Domain:    "review",
			Statement: "External integration validates public contracts.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "external_report", Path: "external-evidence.json"}},
		}},
	}
	err := ValidateRepositoryEvidenceForCandidate(root, ledger, EvidenceCandidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "1.0.0-rc.1",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateRepositoryEvidenceForCandidateRejectsStaleExternalReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "external-evidence.json")
	writeExternalReport(t, reportPath, "VIGIL-AC-16", "verified", true)
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-16",
			Domain:    "review",
			Statement: "External integration validates public contracts.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "external_report", Path: "external-evidence.json"}},
		}},
	}
	err := ValidateRepositoryEvidenceForCandidate(root, ledger, EvidenceCandidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "1.0.0",
	})
	if err == nil || !strings.Contains(err.Error(), "candidate_version") {
		t.Fatalf("stale version error = %v", err)
	}
	err = ValidateRepositoryEvidenceForCandidate(root, ledger, EvidenceCandidate{
		Commit:  "abcdef0123456789abcdef0123456789abcdef01",
		Version: "1.0.0-rc.1",
	})
	if err == nil || !strings.Contains(err.Error(), "candidate_commit") {
		t.Fatalf("stale commit error = %v", err)
	}
}

func TestValidateRepositoryEvidenceRejectsExternalReportThatDoesNotProveCriterion(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "external-evidence.json")
	writeExternalReport(t, reportPath, "VIGIL-AC-16", "failed", true)
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-16",
			Domain:    "review",
			Statement: "External integration validates public contracts.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "external_report", Path: "external-evidence.json"}},
		}},
	}
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "verified external_report evidence") {
		t.Fatalf("missing-proof error = %v", err)
	}
}

func TestValidateRepositoryEvidenceRejectsNonIndependentExternalReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "external-evidence.json")
	writeExternalReport(t, reportPath, "VIGIL-AC-16", "verified", false)
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-16",
			Domain:    "review",
			Statement: "External integration validates public contracts.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "external_report", Path: "external-evidence.json"}},
		}},
	}
	if err := ValidateRepositoryEvidence(root, ledger); err == nil || !strings.Contains(err.Error(), "independent reviewer") {
		t.Fatalf("independence error = %v", err)
	}
}

func validLedger() Ledger {
	ledger := Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
	}
	for index := 1; index <= RequiredSafetyCriteria; index++ {
		ledger.Criteria = append(ledger.Criteria, verifiedCriterion("VIGIL-SI-", index))
	}
	for index := 1; index <= RequiredAcceptanceCriteria; index++ {
		ledger.Criteria = append(ledger.Criteria, verifiedCriterion("VIGIL-AC-", index))
	}
	return ledger
}

func localEvidenceLedger() Ledger {
	return Ledger{
		SchemaVersion: SchemaVersion,
		Target:        "v1.0",
		Criteria: []Criterion{{
			ID:        "VIGIL-AC-01",
			Domain:    "command",
			Statement: "Local command contract is verified.",
			Status:    StatusVerified,
			Evidence:  []Evidence{{Kind: "go_test", Path: "contract_test.go", Symbol: "TestContract"}},
		}},
	}
}

func verifiedCriterion(prefix string, index int) Criterion {
	return Criterion{
		ID:        fmt.Sprintf("%s%02d", prefix, index),
		Domain:    "contract",
		Statement: "Contract is verified.",
		Status:    StatusVerified,
		Evidence:  []Evidence{{Kind: "go_test", Path: "contract_test.go", Symbol: "TestContract"}},
	}
}

func writeLedger(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeOperationalReport(t *testing.T, path, criterionID, status string) {
	t.Helper()
	version := "0.4.0"
	tag := "v0.4.0"
	commit := "0123456789abcdef0123456789abcdef01234567"
	report := operationalevidence.Report{
		SchemaVersion:    operationalevidence.SchemaVersion,
		GeneratedAt:      time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Repository:       "PayCal-Technologies/vigil-public",
		Tag:              tag,
		Version:          version,
		AcceptanceLedger: "docs/v1-acceptance.json",
		Release: &operationalevidence.ReleaseSummary{
			TagName:         tag,
			TargetCommitish: commit,
			ResolvedCommit:  commit,
			URL:             "https://github.com/PayCal-Technologies/vigil-public/releases/tag/v0.4.0",
			IsImmutable:     true,
			PublishedAt:     time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
			Assets:          []operationalevidence.ReleaseAsset{{Name: "vigil_0.4.0_darwin_arm64.tar.gz", Size: 128}},
		},
		WorkflowRun: &operationalevidence.WorkflowRunSummary{
			DatabaseID: 1,
			URL:        "https://github.com/PayCal-Technologies/vigil-public/actions/runs/1",
			Status:     "completed",
			Conclusion: "success",
			HeadSHA:    commit,
			Jobs:       operationalReportTestJobs(),
		},
		Criteria: []operationalevidence.CriterionResult{{
			ID:       criterionID,
			Status:   status,
			Detail:   "criterion status",
			Evidence: operationalReportTestEvidence(status),
		}},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func operationalReportTestJobs() []operationalevidence.JobSummary {
	jobs := make([]operationalevidence.JobSummary, 0)
	for index, name := range operationalevidence.RequiredNativeReleaseSmokeJobs() {
		jobs = append(jobs, operationalevidence.JobSummary{
			Name:       name,
			Status:     "completed",
			Conclusion: "success",
			URL:        fmt.Sprintf("https://github.com/PayCal-Technologies/vigil-public/actions/runs/1/job/%d", index+1),
		})
	}
	return jobs
}

func operationalReportTestEvidence(status string) []string {
	if status != "verified" {
		return nil
	}
	return []string{"https://github.com/PayCal-Technologies/vigil-public/actions/runs/1"}
}

func writeExternalReport(t *testing.T, path, criterionID, status string, independent bool) {
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
			Independent:  independent,
		},
		Criteria: []externalevidence.CriterionResult{{
			ID:       criterionID,
			Status:   status,
			Detail:   "criterion status",
			Evidence: externalReportTestEvidence(status),
		}},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func externalReportTestEvidence(status string) []string {
	if status != "verified" {
		return nil
	}
	return []string{"https://github.com/PayCal-Technologies/vigil-public/issues/17"}
}

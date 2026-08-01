package externalevidence

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptsVerifiedExternalReportAndSortsCriteria(t *testing.T) {
	report := validReport()
	report.Criteria = []CriterionResult{
		verifiedCriterion("VIGIL-AC-20"),
		verifiedCriterion("VIGIL-AC-16"),
	}
	SortCriteria(report.Criteria)
	if report.Criteria[0].ID != "VIGIL-AC-16" {
		t.Fatalf("criteria were not sorted: %#v", report.Criteria)
	}
	if err := Validate(report); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsInvalidExternalReportFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Report)
		want   string
	}{
		{"schema", func(report *Report) { report.SchemaVersion = "2" }, "schema_version"},
		{"target", func(report *Report) { report.Target = "v2.0" }, "target"},
		{"commit", func(report *Report) { report.CandidateCommit = "main" }, "candidate_commit"},
		{"version", func(report *Report) { report.CandidateVersion = "v1.0.0" }, "candidate_version"},
		{"public url", func(report *Report) { report.PublicURL = "http://example.test/report" }, "HTTPS URL"},
		{"reviewer", func(report *Report) { report.Reviewer.Independent = false }, "independent reviewer"},
		{"reviewer name", func(report *Report) { report.Reviewer.Name = " " }, "reviewer name"},
		{"reviewer organization", func(report *Report) { report.Reviewer.Organization = " " }, "reviewer organization"},
		{"reviewer relationship", func(report *Report) { report.Reviewer.Relationship = " " }, "reviewer relationship"},
		{"unsupported criterion", func(report *Report) { report.Criteria[0].ID = "VIGIL-AC-09" }, "not accepted"},
		{"bad criterion status", func(report *Report) { report.Criteria[0].Status = "accepted" }, "unsupported status"},
		{"blank criterion detail", func(report *Report) { report.Criteria[0].Detail = " " }, "detail"},
		{"verified without evidence", func(report *Report) { report.Criteria[0].Evidence = nil }, "requires public evidence"},
		{"evidence url", func(report *Report) { report.Criteria[0].Evidence[0] = "file:///report" }, "HTTPS URL"},
		{"duplicate criterion", func(report *Report) { report.Criteria = append(report.Criteria, report.Criteria[0]) }, "duplicate criterion"},
		{"bad finding id", func(report *Report) { report.Findings[0].ID = "F-1" }, "finding id"},
		{"bad finding severity", func(report *Report) { report.Findings[0].Severity = "critical" }, "unsupported severity"},
		{"bad finding status", func(report *Report) { report.Findings[0].Status = "accepted" }, "unsupported status"},
		{"blank finding summary", func(report *Report) { report.Findings[0].Summary = " " }, "summary"},
		{"duplicate finding", func(report *Report) { report.Findings = append(report.Findings, report.Findings[0]) }, "duplicate finding"},
		{
			"open p1 blocks final severity",
			func(report *Report) {
				report.Criteria = []CriterionResult{verifiedCriterion("VIGIL-AC-21")}
				report.Findings[0].Severity = "P1"
				report.Findings[0].Status = "open"
			},
			"P0/P1",
		},
		{
			"final severity requires findings inventory",
			func(report *Report) {
				report.Criteria = []CriterionResult{verifiedCriterion("VIGIL-AC-21")}
				report.Findings = nil
			},
			"findings inventory",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validReport()
			test.mutate(&report)
			if err := Validate(report); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateAllowsClosedP1ForFinalSeverityDisposition(t *testing.T) {
	report := validReport()
	report.Criteria = []CriterionResult{verifiedCriterion("VIGIL-AC-21")}
	report.Findings = []Finding{{
		ID:       "VIGIL-F-001",
		Severity: "P1",
		Status:   "closed",
		Summary:  "reviewer-validated fix",
		URL:      "https://github.com/PayCal-Technologies/vigil-public/issues/1",
	}}
	if err := Validate(report); err != nil {
		t.Fatal(err)
	}
}

func TestValidateForCandidateRequiresMatchingCommitAndVersion(t *testing.T) {
	report := validReport()
	if err := ValidateForCandidate(report, Candidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "v1.0.0-rc.1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateForCandidate(report, Candidate{
		Commit:  "abcdef0123456789abcdef0123456789abcdef01",
		Version: "1.0.0-rc.1",
	}); err == nil || !strings.Contains(err.Error(), "candidate_commit") {
		t.Fatalf("commit mismatch error = %v", err)
	}
	if err := ValidateForCandidate(report, Candidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "1.0.0",
	}); err == nil || !strings.Contains(err.Error(), "candidate_version") {
		t.Fatalf("version mismatch error = %v", err)
	}
	report.CandidateVersion = ""
	if err := ValidateForCandidate(report, Candidate{
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Version: "1.0.0-rc.1",
	}); err == nil || !strings.Contains(err.Error(), "candidate_version is required") {
		t.Fatalf("missing version error = %v", err)
	}
}

func validReport() Report {
	return Report{
		SchemaVersion:    SchemaVersion,
		Target:           "v1.0",
		GeneratedAt:      time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		CandidateCommit:  "0123456789abcdef0123456789abcdef01234567",
		CandidateVersion: "1.0.0-rc.1",
		PublicURL:        "https://github.com/PayCal-Technologies/vigil-public/security/advisories/GHSA-example",
		Reviewer: Reviewer{
			Name:         "Independent Reviewer",
			Organization: "External Lab",
			Relationship: "No employment, authorship, or financial relationship with the implementation.",
			Independent:  true,
		},
		Criteria: []CriterionResult{verifiedCriterion("VIGIL-AC-16")},
		Findings: []Finding{{
			ID:       "VIGIL-F-001",
			Severity: "P2",
			Status:   "open",
			Summary:  "non-blocking documentation follow-up",
			URL:      "https://github.com/PayCal-Technologies/vigil-public/issues/1",
		}},
	}
}

func verifiedCriterion(id string) CriterionResult {
	return CriterionResult{
		ID:       id,
		Status:   "verified",
		Detail:   "independent evidence validates this criterion",
		Evidence: []string{"https://github.com/PayCal-Technologies/vigil-public/issues/2"},
	}
}

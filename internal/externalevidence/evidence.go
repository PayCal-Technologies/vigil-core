package externalevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = "1"

var (
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	semverPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	findingPattern = regexp.MustCompile(`^VIGIL-F-[0-9]{3}$`)
)

var externalCriterionIDs = map[string]bool{
	"VIGIL-AC-16": true,
	"VIGIL-AC-17": true,
	"VIGIL-AC-19": true,
	"VIGIL-AC-20": true,
	"VIGIL-AC-21": true,
}

type Report struct {
	SchemaVersion    string            `json:"schema_version"`
	Target           string            `json:"target"`
	GeneratedAt      string            `json:"generated_at"`
	CandidateCommit  string            `json:"candidate_commit"`
	CandidateVersion string            `json:"candidate_version,omitempty"`
	PublicURL        string            `json:"public_url"`
	Reviewer         Reviewer          `json:"reviewer"`
	Criteria         []CriterionResult `json:"criteria"`
	Findings         []Finding         `json:"findings,omitempty"`
}

type Candidate struct {
	Commit  string
	Version string
}

type Reviewer struct {
	Name         string `json:"name"`
	Organization string `json:"organization,omitempty"`
	Relationship string `json:"relationship"`
	Independent  bool   `json:"independent"`
}

type CriterionResult struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail"`
	Evidence []string `json:"evidence,omitempty"`
}

type Finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	URL      string `json:"url,omitempty"`
}

func Validate(report Report) error {
	if report.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported external evidence schema_version %q", report.SchemaVersion)
	}
	if report.Target != "v1.0" {
		return fmt.Errorf("unsupported external evidence target %q", report.Target)
	}
	if _, err := time.Parse(time.RFC3339Nano, report.GeneratedAt); err != nil {
		return fmt.Errorf("invalid generated_at: %w", err)
	}
	if !commitPattern.MatchString(report.CandidateCommit) {
		return fmt.Errorf("invalid candidate_commit %q", report.CandidateCommit)
	}
	if report.CandidateVersion != "" && !semverPattern.MatchString(report.CandidateVersion) {
		return fmt.Errorf("invalid candidate_version %q", report.CandidateVersion)
	}
	if err := validatePublicURL("public_url", report.PublicURL); err != nil {
		return err
	}
	if err := validateReviewer(report.Reviewer); err != nil {
		return err
	}
	if len(report.Criteria) == 0 {
		return fmt.Errorf("criteria are required")
	}
	seenCriteria := map[string]bool{}
	for _, criterion := range report.Criteria {
		if err := validateCriterion(criterion); err != nil {
			return err
		}
		if seenCriteria[criterion.ID] {
			return fmt.Errorf("duplicate criterion %s", criterion.ID)
		}
		seenCriteria[criterion.ID] = true
	}
	seenFindings := map[string]bool{}
	for _, finding := range report.Findings {
		if err := validateFinding(finding); err != nil {
			return err
		}
		if seenFindings[finding.ID] {
			return fmt.Errorf("duplicate finding %s", finding.ID)
		}
		seenFindings[finding.ID] = true
	}
	for _, criterion := range report.Criteria {
		if criterion.ID == "VIGIL-AC-21" && criterion.Status == "verified" {
			if report.Findings == nil {
				return fmt.Errorf("VIGIL-AC-21 requires an explicit findings inventory")
			}
			if HasOpenP0P1Findings(report) {
				return fmt.Errorf("VIGIL-AC-21 cannot be verified while a P0/P1 finding remains open")
			}
		}
	}
	return nil
}

func ValidateForCandidate(report Report, candidate Candidate) error {
	if err := Validate(report); err != nil {
		return err
	}
	expectedCommit := strings.ToLower(strings.TrimSpace(candidate.Commit))
	if expectedCommit != "" {
		if !commitPattern.MatchString(expectedCommit) {
			return fmt.Errorf("invalid expected candidate commit %q", candidate.Commit)
		}
		if !strings.EqualFold(report.CandidateCommit, expectedCommit) {
			return fmt.Errorf("external evidence candidate_commit %s does not match candidate commit %s", report.CandidateCommit, expectedCommit)
		}
	}
	expectedVersion := normalizeCandidateVersion(candidate.Version)
	if expectedVersion != "" {
		if !semverPattern.MatchString(expectedVersion) {
			return fmt.Errorf("invalid expected candidate version %q", candidate.Version)
		}
		if strings.TrimSpace(report.CandidateVersion) == "" {
			return fmt.Errorf("external evidence candidate_version is required for candidate version %s", expectedVersion)
		}
		if normalizeCandidateVersion(report.CandidateVersion) != expectedVersion {
			return fmt.Errorf("external evidence candidate_version %s does not match candidate version %s", report.CandidateVersion, expectedVersion)
		}
	}
	return nil
}

func ProvesCriterion(path, criterionID string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	report, err := Decode(data)
	if err != nil {
		return false, err
	}
	if err := Validate(report); err != nil {
		return false, err
	}
	return ReportProvesCriterion(report, criterionID)
}

func ProvesCriterionForCandidate(path, criterionID string, candidate Candidate) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	report, err := Decode(data)
	if err != nil {
		return false, err
	}
	if err := ValidateForCandidate(report, candidate); err != nil {
		return false, err
	}
	return ReportProvesCriterion(report, criterionID)
}

func Decode(data []byte) (Report, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var report Report
	if err := decoder.Decode(&report); err != nil {
		return Report{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Report{}, fmt.Errorf("report must contain exactly one JSON document")
	}
	return report, nil
}

func HasOpenP0P1Findings(report Report) bool {
	for _, finding := range report.Findings {
		if (finding.Severity == "P0" || finding.Severity == "P1") && finding.Status == "open" {
			return true
		}
	}
	return false
}

func SortCriteria(criteria []CriterionResult) {
	sort.Slice(criteria, func(i, j int) bool {
		return criteria[i].ID < criteria[j].ID
	})
}

func validateReviewer(reviewer Reviewer) error {
	if strings.TrimSpace(reviewer.Name) == "" {
		return fmt.Errorf("reviewer name is required")
	}
	if reviewer.Organization != "" && strings.TrimSpace(reviewer.Organization) == "" {
		return fmt.Errorf("reviewer organization must not be empty when provided")
	}
	if strings.TrimSpace(reviewer.Relationship) == "" {
		return fmt.Errorf("reviewer relationship is required")
	}
	if !reviewer.Independent {
		return fmt.Errorf("external evidence requires an independent reviewer")
	}
	return nil
}

func normalizeCandidateVersion(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "v") {
		return strings.TrimPrefix(value, "v")
	}
	return value
}

func validateCriterion(criterion CriterionResult) error {
	if !externalCriterionIDs[criterion.ID] {
		return fmt.Errorf("%s is not accepted in an external evidence report", criterion.ID)
	}
	switch criterion.Status {
	case "verified", "failed", "pending":
	default:
		return fmt.Errorf("%s has unsupported status %q", criterion.ID, criterion.Status)
	}
	if strings.TrimSpace(criterion.Detail) == "" {
		return fmt.Errorf("%s detail is required", criterion.ID)
	}
	hasEvidence := false
	for _, evidence := range criterion.Evidence {
		if err := validatePublicURL("criterion evidence", evidence); err != nil {
			return fmt.Errorf("%s: %w", criterion.ID, err)
		}
		hasEvidence = true
	}
	if criterion.Status == "verified" && !hasEvidence {
		return fmt.Errorf("%s verified criterion requires public evidence", criterion.ID)
	}
	return nil
}

func validateFinding(finding Finding) error {
	if !findingPattern.MatchString(finding.ID) {
		return fmt.Errorf("invalid finding id %q", finding.ID)
	}
	switch finding.Severity {
	case "P0", "P1", "P2", "P3":
	default:
		return fmt.Errorf("%s has unsupported severity %q", finding.ID, finding.Severity)
	}
	switch finding.Status {
	case "open", "closed", "false_positive", "not_applicable":
	default:
		return fmt.Errorf("%s has unsupported status %q", finding.ID, finding.Status)
	}
	if strings.TrimSpace(finding.Summary) == "" {
		return fmt.Errorf("%s summary is required", finding.ID)
	}
	if finding.URL != "" {
		if err := validatePublicURL("finding url", finding.URL); err != nil {
			return fmt.Errorf("%s: %w", finding.ID, err)
		}
	}
	return nil
}

func validatePublicURL(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%s must be an HTTPS URL", field)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s must not contain credentials or fragments", field)
	}
	return nil
}

package externalevidence

import (
	"fmt"
	"sort"
	"strings"
)

const ValidationReportSchemaVersion = "1"

type ValidationStatus string

const (
	ValidationStatusValid   ValidationStatus = "valid"
	ValidationStatusInvalid ValidationStatus = "invalid"
)

type ValidationReport struct {
	SchemaVersion    string           `json:"schema_version"`
	Target           string           `json:"target"`
	Report           string           `json:"report"`
	Criterion        string           `json:"criterion,omitempty"`
	Status           ValidationStatus `json:"status"`
	CandidateCommit  string           `json:"candidate_commit,omitempty"`
	CandidateVersion string           `json:"candidate_version,omitempty"`
	PublicURL        string           `json:"public_url,omitempty"`
	VerifiedCriteria []string         `json:"verified_criteria"`
	OpenP0P1Findings []string         `json:"open_p0_p1_findings"`
	Errors           []string         `json:"errors"`
}

func BuildValidationReport(path, criterionID string, data []byte) ValidationReport {
	result := baseValidationReport(path, criterionID)
	report, err := Decode(data)
	if err != nil {
		result.Status = ValidationStatusInvalid
		result.Errors = []string{err.Error()}
		return result
	}
	result.CandidateCommit = strings.TrimSpace(report.CandidateCommit)
	result.CandidateVersion = strings.TrimSpace(report.CandidateVersion)
	result.PublicURL = strings.TrimSpace(report.PublicURL)
	result.VerifiedCriteria = VerifiedCriterionIDs(report)
	result.OpenP0P1Findings = OpenP0P1FindingIDs(report)
	if err := Validate(report); err != nil {
		result.Status = ValidationStatusInvalid
		result.Errors = []string{err.Error()}
		return result
	}
	if strings.TrimSpace(criterionID) != "" {
		proves, err := ReportProvesCriterion(report, criterionID)
		if err != nil {
			result.Status = ValidationStatusInvalid
			result.Errors = []string{err.Error()}
			return result
		}
		if !proves {
			result.Status = ValidationStatusInvalid
			result.Errors = []string{fmt.Sprintf("external evidence report does not verify %s", criterionID)}
			return result
		}
	}
	result.Status = ValidationStatusValid
	return result
}

func InvalidValidationReport(path, criterionID string, err error) ValidationReport {
	result := baseValidationReport(path, criterionID)
	result.Status = ValidationStatusInvalid
	message := "unknown external evidence validation error"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	result.Errors = []string{message}
	return result
}

func ValidateValidationReport(report ValidationReport) error {
	if report.SchemaVersion != ValidationReportSchemaVersion {
		return fmt.Errorf("unsupported external evidence validation schema_version %q", report.SchemaVersion)
	}
	if report.Target != "v1.0" {
		return fmt.Errorf("unsupported external evidence validation target %q", report.Target)
	}
	if report.Criterion != "" && !externalCriterionIDs[report.Criterion] {
		return fmt.Errorf("%s is not an external evidence criterion", report.Criterion)
	}
	if report.VerifiedCriteria == nil {
		return fmt.Errorf("external evidence validation verified_criteria must be an array")
	}
	if report.OpenP0P1Findings == nil {
		return fmt.Errorf("external evidence validation open_p0_p1_findings must be an array")
	}
	if report.Errors == nil {
		return fmt.Errorf("external evidence validation errors must be an array")
	}
	for _, id := range report.VerifiedCriteria {
		if !externalCriterionIDs[id] {
			return fmt.Errorf("invalid verified criterion %q", id)
		}
	}
	for _, id := range report.OpenP0P1Findings {
		if !findingPattern.MatchString(id) {
			return fmt.Errorf("invalid open P0/P1 finding %q", id)
		}
	}
	for index, err := range report.Errors {
		if strings.TrimSpace(err) == "" {
			return fmt.Errorf("external evidence validation error %d is empty", index)
		}
	}
	switch report.Status {
	case ValidationStatusValid:
		if strings.TrimSpace(report.Report) == "" {
			return fmt.Errorf("valid external evidence validation report requires a report path")
		}
		if len(report.Errors) != 0 {
			return fmt.Errorf("valid external evidence validation report must not include errors")
		}
	case ValidationStatusInvalid:
		if len(report.Errors) == 0 {
			return fmt.Errorf("invalid external evidence validation report requires errors")
		}
	default:
		return fmt.Errorf("unsupported external evidence validation status %q", report.Status)
	}
	return nil
}

func ReportProvesCriterion(report Report, criterionID string) (bool, error) {
	if !externalCriterionIDs[criterionID] {
		return false, fmt.Errorf("%s is not an external evidence criterion", criterionID)
	}
	for _, criterion := range report.Criteria {
		if criterion.ID == criterionID && criterion.Status == "verified" {
			return true, nil
		}
	}
	return false, nil
}

func VerifiedCriterionIDs(report Report) []string {
	ids := make([]string, 0)
	for _, criterion := range report.Criteria {
		if criterion.Status == "verified" && externalCriterionIDs[criterion.ID] {
			ids = append(ids, criterion.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func OpenP0P1FindingIDs(report Report) []string {
	ids := make([]string, 0)
	for _, finding := range report.Findings {
		if (finding.Severity == "P0" || finding.Severity == "P1") && finding.Status == "open" {
			ids = append(ids, finding.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func baseValidationReport(path, criterionID string) ValidationReport {
	return ValidationReport{
		SchemaVersion:    ValidationReportSchemaVersion,
		Target:           "v1.0",
		Report:           strings.TrimSpace(path),
		Criterion:        strings.TrimSpace(criterionID),
		VerifiedCriteria: []string{},
		OpenP0P1Findings: []string{},
		Errors:           []string{},
	}
}

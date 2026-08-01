package acceptance

import (
	"fmt"
	"strings"
)

const GateReportSchemaVersion = "1"

type GateStatus string

const (
	GateStatusNotRequired GateStatus = "not_required"
	GateStatusBlocked     GateStatus = "blocked"
	GateStatusSatisfied   GateStatus = "satisfied"
	GateStatusInvalid     GateStatus = "invalid"
)

type GateReport struct {
	SchemaVersion    string                 `json:"schema_version"`
	Target           string                 `json:"target"`
	Version          string                 `json:"version"`
	AcceptanceLedger string                 `json:"acceptance_ledger"`
	GateRequired     bool                   `json:"gate_required"`
	Status           GateStatus             `json:"status"`
	PendingCount     int                    `json:"pending_count"`
	Pending          []GatePendingCriterion `json:"pending"`
	Errors           []string               `json:"errors"`
}

type GatePendingCriterion struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`
	Statement string `json:"statement"`
	Status    Status `json:"status"`
	Blocker   string `json:"blocker"`
}

func BuildGateReport(version, ledgerPath string, ledger Ledger) (GateReport, error) {
	required, pending, err := Gate(version, ledger)
	if err != nil {
		return GateReport{}, err
	}
	report := GateReport{
		SchemaVersion:    GateReportSchemaVersion,
		Target:           ledger.Target,
		Version:          strings.TrimSpace(version),
		AcceptanceLedger: strings.TrimSpace(ledgerPath),
		GateRequired:     required,
		Status:           GateStatusNotRequired,
		Pending:          []GatePendingCriterion{},
		Errors:           []string{},
	}
	if !required {
		return report, ValidateGateReport(report)
	}
	if len(pending) == 0 {
		report.Status = GateStatusSatisfied
		return report, ValidateGateReport(report)
	}
	report.Status = GateStatusBlocked
	report.Pending = pendingCriteria(pending)
	report.PendingCount = len(report.Pending)
	return report, ValidateGateReport(report)
}

func InvalidGateReport(version, ledgerPath string, err error) GateReport {
	message := "unknown acceptance gate error"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	return GateReport{
		SchemaVersion:    GateReportSchemaVersion,
		Target:           "v1.0",
		Version:          strings.TrimSpace(version),
		AcceptanceLedger: strings.TrimSpace(ledgerPath),
		Status:           GateStatusInvalid,
		Pending:          []GatePendingCriterion{},
		Errors:           []string{message},
	}
}

func ValidateGateReport(report GateReport) error {
	if report.SchemaVersion != GateReportSchemaVersion {
		return fmt.Errorf("unsupported acceptance gate schema_version %q", report.SchemaVersion)
	}
	if report.Target != "v1.0" {
		return fmt.Errorf("unsupported acceptance gate target %q", report.Target)
	}
	if report.Status != GateStatusInvalid && strings.TrimSpace(report.Version) == "" {
		return fmt.Errorf("acceptance gate version is required")
	}
	if strings.TrimSpace(report.AcceptanceLedger) == "" {
		return fmt.Errorf("acceptance gate ledger path is required")
	}
	if report.Pending == nil {
		return fmt.Errorf("acceptance gate pending must be an array")
	}
	if report.Errors == nil {
		return fmt.Errorf("acceptance gate errors must be an array")
	}
	if report.PendingCount != len(report.Pending) {
		return fmt.Errorf("acceptance gate pending_count %d does not match pending length %d", report.PendingCount, len(report.Pending))
	}
	for index, err := range report.Errors {
		if strings.TrimSpace(err) == "" {
			return fmt.Errorf("acceptance gate error %d is empty", index)
		}
	}
	for _, criterion := range report.Pending {
		if !criterionIDPattern.MatchString(criterion.ID) {
			return fmt.Errorf("acceptance gate pending criterion has invalid id %q", criterion.ID)
		}
		if strings.TrimSpace(criterion.Domain) == "" || strings.TrimSpace(criterion.Statement) == "" {
			return fmt.Errorf("acceptance gate pending criterion %s requires domain and statement", criterion.ID)
		}
		switch criterion.Status {
		case StatusOperationalPending, StatusExternalPending:
		case StatusVerified:
			return fmt.Errorf("acceptance gate pending criterion %s must not be verified", criterion.ID)
		default:
			return fmt.Errorf("acceptance gate pending criterion %s has unsupported status %q", criterion.ID, criterion.Status)
		}
		if strings.TrimSpace(criterion.Blocker) == "" {
			return fmt.Errorf("acceptance gate pending criterion %s requires blocker", criterion.ID)
		}
	}
	switch report.Status {
	case GateStatusNotRequired:
		if report.GateRequired || report.PendingCount != 0 || len(report.Errors) != 0 {
			return fmt.Errorf("not-required acceptance gate report must have no pending criteria or errors")
		}
	case GateStatusSatisfied:
		if !report.GateRequired || report.PendingCount != 0 || len(report.Errors) != 0 {
			return fmt.Errorf("satisfied acceptance gate report must be required with no pending criteria or errors")
		}
	case GateStatusBlocked:
		if !report.GateRequired || report.PendingCount == 0 || len(report.Errors) != 0 {
			return fmt.Errorf("blocked acceptance gate report must be required with pending criteria and no errors")
		}
	case GateStatusInvalid:
		if report.GateRequired || report.PendingCount != 0 || len(report.Errors) == 0 {
			return fmt.Errorf("invalid acceptance gate report must have errors and no pending criteria")
		}
	default:
		return fmt.Errorf("unsupported acceptance gate status %q", report.Status)
	}
	return nil
}

func ValidateReleaseGateReport(report GateReport, version string) error {
	if err := ValidateGateReport(report); err != nil {
		return err
	}
	version = strings.TrimSpace(version)
	if report.Version != version {
		return fmt.Errorf("acceptance gate report version %q does not match release version %q", report.Version, version)
	}
	if report.AcceptanceLedger != CanonicalLedgerPath {
		return fmt.Errorf("acceptance gate report ledger %q does not match canonical ledger %q", report.AcceptanceLedger, CanonicalLedgerPath)
	}
	required, err := GateRequiredForVersion(version)
	if err != nil {
		return err
	}
	if report.GateRequired != required {
		return fmt.Errorf("acceptance gate report gate_required=%v does not match release version %q", report.GateRequired, version)
	}
	if required {
		if report.Status != GateStatusSatisfied {
			return fmt.Errorf("stable release version %q requires satisfied acceptance gate status, got %q", version, report.Status)
		}
		return nil
	}
	if report.Status != GateStatusNotRequired {
		return fmt.Errorf("release version %q requires not_required acceptance gate status, got %q", version, report.Status)
	}
	return nil
}

func pendingCriteria(criteria []Criterion) []GatePendingCriterion {
	pending := make([]GatePendingCriterion, 0, len(criteria))
	for _, criterion := range criteria {
		pending = append(pending, GatePendingCriterion{
			ID:        criterion.ID,
			Domain:    criterion.Domain,
			Statement: criterion.Statement,
			Status:    criterion.Status,
			Blocker:   criterion.Blocker,
		})
	}
	return pending
}

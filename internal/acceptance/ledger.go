package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const SchemaVersion = "1"
const CanonicalLedgerPath = "docs/v1-acceptance.json"
const MaxLedgerBytes = 1 << 20
const RequiredSafetyCriteria = 10
const RequiredAcceptanceCriteria = 22

type Status string

const (
	StatusVerified           Status = "verified"
	StatusOperationalPending Status = "operational_pending"
	StatusExternalPending    Status = "external_pending"
)

type Ledger struct {
	SchemaVersion string      `json:"schema_version"`
	Target        string      `json:"target"`
	Criteria      []Criterion `json:"criteria"`
}

type Criterion struct {
	ID        string     `json:"id"`
	Domain    string     `json:"domain"`
	Statement string     `json:"statement"`
	Status    Status     `json:"status"`
	Blocker   string     `json:"blocker,omitempty"`
	Evidence  []Evidence `json:"evidence"`
}

type Evidence struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Symbol string `json:"symbol,omitempty"`
}

var criterionIDPattern = regexp.MustCompile(`^VIGIL-(SI|AC)-[0-9]{2}$`)
var semanticVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

var operationalEvidenceCriteria = map[string]bool{
	"VIGIL-AC-09": true,
	"VIGIL-AC-11": true,
	"VIGIL-AC-12": true,
	"VIGIL-AC-13": true,
	"VIGIL-AC-18": true,
}

var externalEvidenceCriteria = map[string]bool{
	"VIGIL-AC-16": true,
	"VIGIL-AC-17": true,
	"VIGIL-AC-19": true,
	"VIGIL-AC-20": true,
	"VIGIL-AC-21": true,
}

func Read(path string) (Ledger, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Ledger{}, fmt.Errorf("inspect acceptance ledger: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Ledger{}, fmt.Errorf("acceptance ledger must be a regular non-symlink file")
	}
	if info.Size() > MaxLedgerBytes {
		return Ledger{}, fmt.Errorf("acceptance ledger exceeds %d bytes", MaxLedgerBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return Ledger{}, fmt.Errorf("open acceptance ledger: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxLedgerBytes+1))
	if err != nil {
		return Ledger{}, fmt.Errorf("read acceptance ledger: %w", err)
	}
	if len(data) > MaxLedgerBytes {
		return Ledger{}, fmt.Errorf("acceptance ledger exceeds %d bytes", MaxLedgerBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var ledger Ledger
	if err := decoder.Decode(&ledger); err != nil {
		return Ledger{}, fmt.Errorf("decode acceptance ledger: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Ledger{}, fmt.Errorf("acceptance ledger must contain exactly one JSON document")
	}
	if err := Validate(ledger); err != nil {
		return Ledger{}, err
	}
	return ledger, nil
}

func Validate(ledger Ledger) error {
	if ledger.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported acceptance schema_version %q", ledger.SchemaVersion)
	}
	if ledger.Target != "v1.0" {
		return fmt.Errorf("unsupported acceptance target %q", ledger.Target)
	}
	if len(ledger.Criteria) == 0 {
		return fmt.Errorf("acceptance criteria are required")
	}
	seen := make(map[string]bool, len(ledger.Criteria))
	for index, criterion := range ledger.Criteria {
		if !criterionIDPattern.MatchString(criterion.ID) {
			return fmt.Errorf("criterion %d has invalid id %q", index, criterion.ID)
		}
		if seen[criterion.ID] {
			return fmt.Errorf("criterion id %q is duplicated", criterion.ID)
		}
		seen[criterion.ID] = true
		if strings.TrimSpace(criterion.Domain) == "" || strings.TrimSpace(criterion.Statement) == "" {
			return fmt.Errorf("criterion %s requires domain and statement", criterion.ID)
		}
		switch criterion.Status {
		case StatusVerified:
			if strings.TrimSpace(criterion.Blocker) != "" {
				return fmt.Errorf("verified criterion %s must not retain a blocker", criterion.ID)
			}
		case StatusOperationalPending, StatusExternalPending:
			if strings.TrimSpace(criterion.Blocker) == "" {
				return fmt.Errorf("pending criterion %s requires a blocker", criterion.ID)
			}
			if criterion.Status == StatusOperationalPending && !requiresOperationalEvidence(criterion.ID) {
				return fmt.Errorf("%s cannot use operational_pending status", criterion.ID)
			}
			if criterion.Status == StatusExternalPending && !requiresExternalEvidence(criterion.ID) {
				return fmt.Errorf("%s cannot use external_pending status", criterion.ID)
			}
		default:
			return fmt.Errorf("criterion %s has unsupported status %q", criterion.ID, criterion.Status)
		}
		if len(criterion.Evidence) == 0 {
			return fmt.Errorf("criterion %s requires evidence", criterion.ID)
		}
		for evidenceIndex, evidence := range criterion.Evidence {
			if strings.TrimSpace(evidence.Kind) == "" || strings.TrimSpace(evidence.Path) == "" {
				return fmt.Errorf("criterion %s evidence %d requires kind and path", criterion.ID, evidenceIndex)
			}
		}
	}
	return nil
}

func requiresOperationalEvidence(criterionID string) bool {
	return operationalEvidenceCriteria[criterionID]
}

func requiresExternalEvidence(criterionID string) bool {
	return externalEvidenceCriteria[criterionID]
}

func Gate(version string, ledger Ledger) (bool, []Criterion, error) {
	if err := ValidateV1Baseline(ledger); err != nil {
		return false, nil, err
	}
	requiresComplete, err := GateRequiredForVersion(version)
	if err != nil {
		return false, nil, err
	}
	if !requiresComplete {
		return false, []Criterion{}, nil
	}
	pending := make([]Criterion, 0)
	for _, criterion := range ledger.Criteria {
		if criterion.Status != StatusVerified {
			pending = append(pending, criterion)
		}
	}
	return true, pending, nil
}

func GateRequiredForVersion(version string) (bool, error) {
	match := semanticVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return false, fmt.Errorf("invalid semantic version %q", version)
	}
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return false, fmt.Errorf("invalid semantic version major %q", match[1])
	}
	return major >= 1 && match[4] == "", nil
}

func ValidateV1Baseline(ledger Ledger) error {
	if err := Validate(ledger); err != nil {
		return err
	}
	seen := make(map[string]bool, len(ledger.Criteria))
	for _, criterion := range ledger.Criteria {
		seen[criterion.ID] = true
	}
	for index := 1; index <= RequiredSafetyCriteria; index++ {
		id := fmt.Sprintf("VIGIL-SI-%02d", index)
		if !seen[id] {
			return fmt.Errorf("acceptance ledger is missing required safety criterion %s", id)
		}
	}
	for index := 1; index <= RequiredAcceptanceCriteria; index++ {
		id := fmt.Sprintf("VIGIL-AC-%02d", index)
		if !seen[id] {
			return fmt.Errorf("acceptance ledger is missing required v1 criterion %s", id)
		}
	}
	return nil
}

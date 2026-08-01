package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PayCal-Technologies/vigil-public/internal/externalevidence"
	"github.com/PayCal-Technologies/vigil-public/internal/operationalevidence"
)

const maxEvidenceBytes = 16 << 20

var fullCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

var allowedEvidenceKinds = map[string]bool{
	"go_test":            true,
	"document":           true,
	"external_report":    true,
	"operational_report": true,
	"schema":             true,
	"script":             true,
	"workflow":           true,
}

type EvidenceCandidate struct {
	Commit  string
	Version string
}

// AllowedEvidenceKinds returns the public evidence kinds accepted by the v1
// acceptance ledger.
func AllowedEvidenceKinds() []string {
	kinds := make([]string, 0, len(allowedEvidenceKinds))
	for kind := range allowedEvidenceKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func ValidateRepositoryEvidence(root string, ledger Ledger) error {
	return validateRepositoryEvidence(root, ledger, EvidenceCandidate{})
}

func ValidateRepositoryEvidenceForCandidate(root string, ledger Ledger, candidate EvidenceCandidate) error {
	return validateRepositoryEvidence(root, ledger, candidate)
}

func validateRepositoryEvidence(root string, ledger Ledger, candidate EvidenceCandidate) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve evidence root: %w", err)
	}
	candidate.Commit = strings.ToLower(strings.TrimSpace(candidate.Commit))
	candidate.Version = strings.TrimSpace(candidate.Version)
	if candidate.Commit != "" && !fullCommitPattern.MatchString(candidate.Commit) {
		return fmt.Errorf("invalid evidence candidate commit %q", candidate.Commit)
	}
	if candidate.Version != "" {
		normalizedVersion, err := normalizeEvidenceVersion(candidate.Version)
		if err != nil {
			return err
		}
		candidate.Version = normalizedVersion
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return fmt.Errorf("resolve evidence root symlinks: %w", err)
	}
	for _, criterion := range ledger.Criteria {
		hasVerifiedEvidence := false
		hasOperationalReportEvidence := false
		hasExternalReportEvidence := false
		for _, evidence := range criterion.Evidence {
			if !allowedEvidenceKinds[evidence.Kind] {
				return fmt.Errorf("%s has unsupported evidence kind %q", criterion.ID, evidence.Kind)
			}
			clean := filepath.Clean(filepath.FromSlash(evidence.Path))
			if clean == "." || filepath.IsAbs(clean) || clean == ".." ||
				strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%s has unconfined evidence path %q", criterion.ID, evidence.Path)
			}
			fullPath := filepath.Join(absoluteRoot, clean)
			info, err := os.Lstat(fullPath)
			if err != nil {
				return fmt.Errorf("%s evidence %q: %w", criterion.ID, evidence.Path, err)
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s evidence %q must be a regular non-symlink file", criterion.ID, evidence.Path)
			}
			if info.Size() > maxEvidenceBytes {
				return fmt.Errorf("%s evidence %q exceeds %d bytes", criterion.ID, evidence.Path, maxEvidenceBytes)
			}
			resolvedPath, err := filepath.EvalSymlinks(fullPath)
			if err != nil {
				return fmt.Errorf("%s resolve evidence %q: %w", criterion.ID, evidence.Path, err)
			}
			if !pathInside(resolvedRoot, resolvedPath) {
				return fmt.Errorf("%s evidence %q resolves outside the repository", criterion.ID, evidence.Path)
			}
			if evidence.Kind == "go_test" {
				hasVerifiedEvidence = true
				if !strings.HasSuffix(filepath.ToSlash(clean), "_test.go") {
					return fmt.Errorf("%s Go evidence path %q must name a *_test.go file", criterion.ID, evidence.Path)
				}
				if !strings.HasPrefix(evidence.Symbol, "Test") {
					return fmt.Errorf("%s Go evidence has invalid symbol %q", criterion.ID, evidence.Symbol)
				}
				exists, err := goTestSymbolExists(fullPath, evidence.Symbol)
				if err != nil {
					return fmt.Errorf("%s parse Go evidence %q: %w", criterion.ID, evidence.Path, err)
				}
				if !exists {
					return fmt.Errorf("%s references missing Go test %s in %s", criterion.ID, evidence.Symbol, evidence.Path)
				}
			} else if evidence.Kind == "operational_report" {
				if evidence.Symbol != "" {
					return fmt.Errorf("%s operational report evidence must not name symbol %q", criterion.ID, evidence.Symbol)
				}
				proves, err := operationalReportProvesCriterion(fullPath, criterion.ID, candidate)
				if err != nil {
					return fmt.Errorf("%s operational report evidence %q: %w", criterion.ID, evidence.Path, err)
				}
				hasVerifiedEvidence = hasVerifiedEvidence || proves
				hasOperationalReportEvidence = hasOperationalReportEvidence || proves
			} else if evidence.Kind == "external_report" {
				if evidence.Symbol != "" {
					return fmt.Errorf("%s external report evidence must not name symbol %q", criterion.ID, evidence.Symbol)
				}
				proves, err := externalevidence.ProvesCriterionForCandidate(fullPath, criterion.ID, externalevidence.Candidate{
					Commit:  candidate.Commit,
					Version: candidate.Version,
				})
				if err != nil {
					return fmt.Errorf("%s external report evidence %q: %w", criterion.ID, evidence.Path, err)
				}
				hasVerifiedEvidence = hasVerifiedEvidence || proves
				hasExternalReportEvidence = hasExternalReportEvidence || proves
			} else if evidence.Symbol != "" {
				return fmt.Errorf("%s non-Go evidence must not name symbol %q", criterion.ID, evidence.Symbol)
			}
		}
		if criterion.Status == StatusVerified {
			if requiresOperationalEvidence(criterion.ID) && !hasOperationalReportEvidence {
				return fmt.Errorf("%s is verified without verified operational_report evidence", criterion.ID)
			}
			if requiresExternalEvidence(criterion.ID) && !hasExternalReportEvidence {
				return fmt.Errorf("%s is verified without verified external_report evidence", criterion.ID)
			}
			if !hasVerifiedEvidence {
				return fmt.Errorf("%s is verified without named automated, operational, or external evidence", criterion.ID)
			}
		}
	}
	return nil
}

func operationalReportProvesCriterion(path, criterionID string, candidate EvidenceCandidate) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var report operationalevidence.Report
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return false, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false, fmt.Errorf("report must contain exactly one JSON document")
	}
	if err := operationalevidence.Validate(report); err != nil {
		return false, err
	}
	if err := validateOperationalReportCandidate(report, criterionID, candidate); err != nil {
		return false, err
	}
	for _, criterion := range report.Criteria {
		if criterion.ID == criterionID && criterion.Status == "verified" {
			return true, nil
		}
	}
	return false, nil
}

func validateOperationalReportCandidate(report operationalevidence.Report, criterionID string, candidate EvidenceCandidate) error {
	if candidate.Version != "" {
		reportVersion, err := normalizeEvidenceVersion(report.Version)
		if err != nil {
			return fmt.Errorf("operational report version: %w", err)
		}
		if reportVersion != candidate.Version {
			return fmt.Errorf("operational report version %s does not match candidate version %s", report.Version, candidate.Version)
		}
		reportTag, err := normalizeEvidenceVersion(report.Tag)
		if err != nil {
			return fmt.Errorf("operational report tag: %w", err)
		}
		if reportTag != candidate.Version {
			return fmt.Errorf("operational report tag %s does not match candidate version %s", report.Tag, candidate.Version)
		}
	}
	if candidate.Commit == "" {
		return nil
	}
	if operationalCriterionRequiresCompletedWorkflow(criterionID) {
		if report.WorkflowRun == nil {
			return fmt.Errorf("%s operational report is missing workflow_run evidence", criterionID)
		}
		if report.WorkflowRun.Status != "completed" || report.WorkflowRun.Conclusion != "success" {
			return fmt.Errorf("%s operational report workflow_run must be completed successfully for candidate-bound evidence", criterionID)
		}
	}
	reportCommit := operationalReportCommit(report)
	if reportCommit != "" {
		if !strings.EqualFold(reportCommit, candidate.Commit) {
			return fmt.Errorf("operational report commit %s does not match candidate commit %s", reportCommit, candidate.Commit)
		}
		return nil
	}
	if operationalCriterionRequiresReleaseCommit(criterionID) {
		return fmt.Errorf("%s operational report is missing release candidate commit evidence", criterionID)
	}
	return nil
}

func operationalReportCommit(report operationalevidence.Report) string {
	if report.Release != nil {
		if fullCommitPattern.MatchString(strings.ToLower(report.Release.ResolvedCommit)) {
			return strings.ToLower(report.Release.ResolvedCommit)
		}
		if fullCommitPattern.MatchString(strings.ToLower(report.Release.TargetCommitish)) {
			return strings.ToLower(report.Release.TargetCommitish)
		}
	}
	if report.WorkflowRun != nil && fullCommitPattern.MatchString(strings.ToLower(report.WorkflowRun.HeadSHA)) {
		return strings.ToLower(report.WorkflowRun.HeadSHA)
	}
	return ""
}

func operationalCriterionRequiresReleaseCommit(criterionID string) bool {
	switch criterionID {
	case "VIGIL-AC-09", "VIGIL-AC-11", "VIGIL-AC-12", "VIGIL-AC-13":
		return true
	default:
		return false
	}
}

func operationalCriterionRequiresCompletedWorkflow(criterionID string) bool {
	switch criterionID {
	case "VIGIL-AC-09", "VIGIL-AC-12", "VIGIL-AC-13":
		return true
	default:
		return false
	}
}

func normalizeEvidenceVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "v") {
		value = strings.TrimPrefix(value, "v")
	}
	if value == "" || !semanticVersionPattern.MatchString(value) {
		return "", fmt.Errorf("invalid evidence candidate version %q", value)
	}
	return value, nil
}

func goTestSymbolExists(path, symbol string) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && isGoTestFunction(function, symbol) {
			return true, nil
		}
	}
	return false, nil
}

func isGoTestFunction(function *ast.FuncDecl, symbol string) bool {
	if function.Recv != nil || function.Name.Name != symbol || function.Type == nil {
		return false
	}
	if !isGoTestName(symbol) {
		return false
	}
	if function.Type.TypeParams != nil && len(function.Type.TypeParams.List) != 0 {
		return false
	}
	if function.Type.Results != nil && len(function.Type.Results.List) != 0 {
		return false
	}
	if function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	parameterType, ok := function.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := parameterType.X.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != "T" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "testing"
}

func isGoTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if len(name) == len("Test") {
		return true
	}
	runeValue, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return !unicode.IsLower(runeValue)
}

func pathInside(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil &&
		(relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

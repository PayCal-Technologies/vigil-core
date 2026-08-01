package contracts

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/PayCal-Technologies/vigil-public/internal/acceptance"
	"github.com/PayCal-Technologies/vigil-public/internal/config"
	"github.com/PayCal-Technologies/vigil-public/internal/output"
	"github.com/PayCal-Technologies/vigil-public/internal/packs"
	"github.com/PayCal-Technologies/vigil-public/internal/plan"
)

type acceptanceLedger struct {
	SchemaVersion string                `json:"schema_version"`
	Target        string                `json:"target"`
	Criteria      []acceptanceCriterion `json:"criteria"`
}

type acceptanceCriterion struct {
	ID        string               `json:"id"`
	Domain    string               `json:"domain"`
	Statement string               `json:"statement"`
	Status    string               `json:"status"`
	Blocker   string               `json:"blocker,omitempty"`
	Evidence  []acceptanceEvidence `json:"evidence"`
}

type acceptanceEvidence struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Symbol string `json:"symbol,omitempty"`
}

var acceptanceIDPattern = regexp.MustCompile(`^VIGIL-(SI|AC)-[0-9]{2}$`)
var safetyInvariantPattern = regexp.MustCompile(`\*\*(VIGIL-SI-[0-9]{2})\*\*`)

func TestV1AcceptanceLedgerIsCompleteAndExecutable(t *testing.T) {
	root := filepath.Join("..", "..")
	ledgerPath := filepath.Join(root, "docs", "v1-acceptance.json")
	file, err := os.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var ledger acceptanceLedger
	if err := decoder.Decode(&ledger); err != nil {
		t.Fatalf("decode acceptance ledger: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("acceptance ledger must contain one JSON document: %v", err)
	}
	if ledger.SchemaVersion != "1" || ledger.Target != "v1.0" {
		t.Fatalf("unexpected acceptance ledger contract: schema=%q target=%q", ledger.SchemaVersion, ledger.Target)
	}
	if len(ledger.Criteria) == 0 {
		t.Fatal("acceptance ledger has no criteria")
	}

	allowedStatuses := map[string]bool{
		"verified":            true,
		"operational_pending": true,
		"external_pending":    true,
	}
	allowedEvidence := map[string]bool{}
	for _, kind := range acceptance.AllowedEvidenceKinds() {
		allowedEvidence[kind] = true
	}
	operationalCriteria := map[string]bool{
		"VIGIL-AC-09": true,
		"VIGIL-AC-11": true,
		"VIGIL-AC-12": true,
		"VIGIL-AC-13": true,
		"VIGIL-AC-18": true,
	}
	externalCriteria := map[string]bool{
		"VIGIL-AC-16": true,
		"VIGIL-AC-17": true,
		"VIGIL-AC-19": true,
		"VIGIL-AC-20": true,
		"VIGIL-AC-21": true,
	}
	requiredDomains := map[string]bool{
		"safety":       false,
		"command":      false,
		"config":       false,
		"plan":         false,
		"output":       false,
		"exit":         false,
		"plugin":       false,
		"deprecation":  false,
		"platform":     false,
		"distribution": false,
		"installation": false,
		"workflow":     false,
		"review":       false,
		"governance":   false,
		"severity":     false,
	}
	seenIDs := map[string]bool{}
	ledgerSafetyIDs := map[string]bool{}

	for _, criterion := range ledger.Criteria {
		if !acceptanceIDPattern.MatchString(criterion.ID) {
			t.Errorf("criterion has invalid id %q", criterion.ID)
		}
		if seenIDs[criterion.ID] {
			t.Errorf("criterion id %q is duplicated", criterion.ID)
		}
		seenIDs[criterion.ID] = true
		if _, ok := requiredDomains[criterion.Domain]; !ok {
			t.Errorf("%s has unknown domain %q", criterion.ID, criterion.Domain)
		} else {
			requiredDomains[criterion.Domain] = true
		}
		if strings.TrimSpace(criterion.Statement) == "" {
			t.Errorf("%s has no statement", criterion.ID)
		}
		if !allowedStatuses[criterion.Status] {
			t.Errorf("%s has unsupported status %q", criterion.ID, criterion.Status)
		}
		if criterion.Status == "verified" && strings.TrimSpace(criterion.Blocker) != "" {
			t.Errorf("%s is verified but still has a blocker", criterion.ID)
		}
		if criterion.Status != "verified" && strings.TrimSpace(criterion.Blocker) == "" {
			t.Errorf("%s is pending without a concrete blocker", criterion.ID)
		}
		if criterion.Status == string(acceptance.StatusOperationalPending) && !operationalCriteria[criterion.ID] {
			t.Errorf("%s cannot use operational_pending status", criterion.ID)
		}
		if criterion.Status == string(acceptance.StatusExternalPending) && !externalCriteria[criterion.ID] {
			t.Errorf("%s cannot use external_pending status", criterion.ID)
		}
		if len(criterion.Evidence) == 0 {
			t.Errorf("%s has no evidence", criterion.ID)
		}

		hasVerifiedEvidence := false
		hasOperationalReportEvidence := false
		hasExternalReportEvidence := false
		for _, evidence := range criterion.Evidence {
			if !allowedEvidence[evidence.Kind] {
				t.Errorf("%s has unsupported evidence kind %q", criterion.ID, evidence.Kind)
			}
			clean := filepath.Clean(evidence.Path)
			if evidence.Path == "" || filepath.IsAbs(evidence.Path) || clean == ".." ||
				strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				t.Errorf("%s has unconfined evidence path %q", criterion.ID, evidence.Path)
				continue
			}
			fullPath := filepath.Join(root, clean)
			info, err := os.Lstat(fullPath)
			if err != nil {
				t.Errorf("%s evidence %q: %v", criterion.ID, evidence.Path, err)
				continue
			}
			if !info.Mode().IsRegular() {
				t.Errorf("%s evidence %q is not a regular file", criterion.ID, evidence.Path)
			}
			if evidence.Kind == "go_test" {
				hasVerifiedEvidence = true
				if !strings.HasSuffix(filepath.ToSlash(clean), "_test.go") {
					t.Errorf("%s Go evidence path %q must name a *_test.go file", criterion.ID, evidence.Path)
					continue
				}
				if !strings.HasPrefix(evidence.Symbol, "Test") {
					t.Errorf("%s Go evidence has invalid symbol %q", criterion.ID, evidence.Symbol)
					continue
				}
				if !goTestSymbolExists(t, fullPath, evidence.Symbol) {
					t.Errorf("%s references missing Go test %s in %s", criterion.ID, evidence.Symbol, evidence.Path)
				}
			} else if evidence.Kind == "operational_report" || evidence.Kind == "external_report" {
				hasVerifiedEvidence = true
				hasOperationalReportEvidence = hasOperationalReportEvidence || evidence.Kind == "operational_report"
				hasExternalReportEvidence = hasExternalReportEvidence || evidence.Kind == "external_report"
				if evidence.Symbol != "" {
					t.Errorf("%s %s evidence must not name symbol %q", criterion.ID, evidence.Kind, evidence.Symbol)
				}
			} else if evidence.Symbol != "" {
				t.Errorf("%s non-Go evidence must not name symbol %q", criterion.ID, evidence.Symbol)
			}
		}
		if criterion.Status == "verified" && !hasVerifiedEvidence {
			t.Errorf("%s is verified without named automated, operational, or external evidence", criterion.ID)
		}
		if criterion.Status == "verified" && operationalCriteria[criterion.ID] && !hasOperationalReportEvidence {
			t.Errorf("%s is verified without operational_report evidence", criterion.ID)
		}
		if criterion.Status == "verified" && externalCriteria[criterion.ID] && !hasExternalReportEvidence {
			t.Errorf("%s is verified without external_report evidence", criterion.ID)
		}
		if strings.HasPrefix(criterion.ID, "VIGIL-SI-") {
			if criterion.Domain != "safety" {
				t.Errorf("%s must use the safety domain", criterion.ID)
			}
			ledgerSafetyIDs[criterion.ID] = true
		}
	}

	for domain, present := range requiredDomains {
		if !present {
			t.Errorf("acceptance ledger is missing required domain %q", domain)
		}
	}
	for index := 1; index <= 10; index++ {
		id := fmt.Sprintf("VIGIL-SI-%02d", index)
		if !seenIDs[id] {
			t.Errorf("acceptance ledger is missing required safety criterion %s", id)
		}
	}
	for index := 1; index <= 22; index++ {
		id := fmt.Sprintf("VIGIL-AC-%02d", index)
		if !seenIDs[id] {
			t.Errorf("acceptance ledger is missing required v1 criterion %s", id)
		}
	}

	productContract, err := os.ReadFile(filepath.Join(root, "docs", "product-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	documentedSafetyIDs := map[string]bool{}
	for _, match := range safetyInvariantPattern.FindAllStringSubmatch(string(productContract), -1) {
		documentedSafetyIDs[match[1]] = true
	}
	if len(documentedSafetyIDs) == 0 {
		t.Fatal("product contract has no identified safety invariants")
	}
	for id := range documentedSafetyIDs {
		if !ledgerSafetyIDs[id] {
			t.Errorf("documented safety invariant %s has no acceptance criterion", id)
		}
	}
	for id := range ledgerSafetyIDs {
		if !documentedSafetyIDs[id] {
			t.Errorf("acceptance ledger safety criterion %s is absent from the product contract", id)
		}
	}
}

func TestAcceptanceLedgerSchemaMatchesRuntimeContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "vigil-v1-acceptance-ledger-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			AllOf      []json.RawMessage          `json:"allOf"`
			Enum       []string                   `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	var schemaVersion struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion.Const != acceptance.SchemaVersion {
		t.Fatalf("acceptance ledger schema version = %q, runtime = %q", schemaVersion.Const, acceptance.SchemaVersion)
	}

	var target struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["target"], &target); err != nil {
		t.Fatal(err)
	}
	if target.Const != "v1.0" {
		t.Fatalf("acceptance ledger target = %q", target.Const)
	}

	var criteria struct {
		MinItems int `json:"minItems"`
	}
	if err := json.Unmarshal(schema.Properties["criteria"], &criteria); err != nil {
		t.Fatal(err)
	}
	requiredCriteria := acceptance.RequiredSafetyCriteria + acceptance.RequiredAcceptanceCriteria
	if criteria.MinItems != requiredCriteria {
		t.Fatalf("acceptance ledger minItems = %d, required baseline = %d", criteria.MinItems, requiredCriteria)
	}

	criterionFields := schema.Defs["criterion"].Properties
	for _, field := range []string{"id", "domain", "statement", "status", "blocker", "evidence"} {
		if _, ok := criterionFields[field]; !ok {
			t.Fatalf("acceptance criterion schema is missing field %q", field)
		}
	}
	evidenceFields := schema.Defs["evidence"].Properties
	for _, field := range []string{"kind", "path", "symbol"} {
		if _, ok := evidenceFields[field]; !ok {
			t.Fatalf("acceptance evidence schema is missing field %q", field)
		}
	}
	schemaText := string(data)
	if !strings.Contains(schemaText, `_test\\.go$`) {
		t.Fatalf("acceptance evidence schema must restrict go_test paths to *_test.go")
	}
	if !strings.Contains(schemaText, `^Test($|[A-Z0-9_][A-Za-z0-9_]*)$`) {
		t.Fatalf("acceptance evidence schema must restrict go_test symbols to go-test-discoverable names")
	}

	assertEnumValues(t, criterionFields["status"], []string{
		string(acceptance.StatusVerified),
		string(acceptance.StatusOperationalPending),
		string(acceptance.StatusExternalPending),
	})
	assertEnumValues(t, evidenceFields["kind"], acceptance.AllowedEvidenceKinds())
	operationalCriterionIDs := []string{"VIGIL-AC-09", "VIGIL-AC-11", "VIGIL-AC-12", "VIGIL-AC-13", "VIGIL-AC-18"}
	externalCriterionIDs := []string{"VIGIL-AC-16", "VIGIL-AC-17", "VIGIL-AC-19", "VIGIL-AC-20", "VIGIL-AC-21"}
	if !sameStringSet(schema.Defs["operational_criterion_id"].Enum, operationalCriterionIDs) {
		t.Fatalf("acceptance ledger schema operational criteria = %#v", schema.Defs["operational_criterion_id"].Enum)
	}
	if !sameStringSet(schema.Defs["external_criterion_id"].Enum, externalCriterionIDs) {
		t.Fatalf("acceptance ledger schema external criteria = %#v", schema.Defs["external_criterion_id"].Enum)
	}
	criterionRules := string(mustMarshalJSON(t, schema.Defs["criterion"].AllOf))
	for _, required := range []string{
		"operational_pending",
		"external_pending",
		"operational_criterion_id",
		"external_criterion_id",
		"operational_report",
		"external_report",
	} {
		if !strings.Contains(criterionRules, required) {
			t.Fatalf("acceptance ledger schema criterion rules are missing %q", required)
		}
	}
}

func TestAcceptanceGateReportSchemaMatchesRuntimeContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "vigil-v1-acceptance-gate-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		AllOf      []json.RawMessage          `json:"allOf"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Pattern    string                     `json:"pattern"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	var versionProperty struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
		t.Fatal(err)
	}
	if versionProperty.Const != acceptance.GateReportSchemaVersion {
		t.Fatalf("acceptance gate schema version = %q, runtime = %q", versionProperty.Const, acceptance.GateReportSchemaVersion)
	}
	for _, field := range []string{
		"schema_version",
		"target",
		"version",
		"acceptance_ledger",
		"gate_required",
		"status",
		"pending_count",
		"pending",
		"errors",
	} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("acceptance gate schema is missing field %q", field)
		}
	}
	assertEnumValues(t, schema.Properties["status"], []string{
		string(acceptance.GateStatusNotRequired),
		string(acceptance.GateStatusBlocked),
		string(acceptance.GateStatusSatisfied),
		string(acceptance.GateStatusInvalid),
	})
	var errorsProperty struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(schema.Properties["errors"], &errorsProperty); err != nil {
		t.Fatal(err)
	}
	assertNonBlankStringProperty(t, "acceptance gate error item", errorsProperty.Items)
	if !strings.Contains(schema.Defs["relative_path"].Pattern, `\S`) {
		t.Fatalf("acceptance gate relative path pattern must reject whitespace-only values: %q", schema.Defs["relative_path"].Pattern)
	}
	pendingFields := schema.Defs["pending_criterion"].Properties
	for _, field := range []string{"id", "domain", "statement", "status", "blocker"} {
		if _, ok := pendingFields[field]; !ok {
			t.Fatalf("acceptance gate pending criterion schema is missing field %q", field)
		}
	}
	for _, field := range []string{"domain", "statement", "blocker"} {
		assertNonBlankStringProperty(t, "acceptance gate pending criterion "+field, pendingFields[field])
	}
	assertEnumValues(t, pendingFields["status"], []string{
		string(acceptance.StatusOperationalPending),
		string(acceptance.StatusExternalPending),
	})

	versionRuleStatuses := map[string]bool{}
	for index, rawRule := range schema.AllOf {
		var rule struct {
			If struct {
				Properties map[string]struct {
					Const string `json:"const"`
				} `json:"properties"`
			} `json:"if"`
			Then struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"then"`
		}
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			t.Fatalf("allOf[%d]: %v", index, err)
		}
		status := rule.If.Properties["status"].Const
		switch acceptance.GateStatus(status) {
		case acceptance.GateStatusBlocked, acceptance.GateStatusSatisfied, acceptance.GateStatusNotRequired:
			assertStringPattern(t, "acceptance gate "+status+" version", rule.Then.Properties["version"], `\S`)
			versionRuleStatuses[status] = true
		}
	}
	for _, status := range []acceptance.GateStatus{acceptance.GateStatusBlocked, acceptance.GateStatusSatisfied, acceptance.GateStatusNotRequired} {
		if !versionRuleStatuses[string(status)] {
			t.Fatalf("acceptance gate schema is missing nonblank version rule for status %s", status)
		}
	}
}

func TestPublishedCoreSchemaVersionsMatchRuntime(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"schemas/vigil-config-v3.schema.json", config.SchemaVersion},
		{"schemas/vigil-output-v1.schema.json", output.EnvelopeSchemaVersion},
		{"schemas/vigil-pack-v1.schema.json", packs.SchemaVersion},
		{"schemas/vigil-plan-v1.schema.json", plan.SchemaVersion},
		{"schemas/vigil-v1-acceptance-gate-v1.schema.json", acceptance.GateReportSchemaVersion},
		{"schemas/vigil-v1-acceptance-ledger-v1.schema.json", acceptance.SchemaVersion},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", test.path))
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			var versionProperty struct {
				Const string `json:"const"`
			}
			if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
				t.Fatal(err)
			}
			if got := versionProperty.Const; got != test.want {
				t.Fatalf("schema const = %q, runtime = %q", got, test.want)
			}
		})
	}
}

func TestPlanSchemaPublishesRuntimeDurationContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "vigil-plan-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Pattern    string                     `json:"pattern"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	positiveDurationPattern := `^(?=.*[1-9])(?:[0-9]+(?:ns|us|µs|ms|s|m|h))+$`
	nonNegativeDurationPattern := `^(?:[0-9]+(?:ns|us|µs|ms|s|m|h))+$`
	if schema.Defs["positive_duration"].Pattern != positiveDurationPattern {
		t.Fatalf("positive duration pattern = %q", schema.Defs["positive_duration"].Pattern)
	}
	if schema.Defs["non_negative_duration"].Pattern != nonNegativeDurationPattern {
		t.Fatalf("non-negative duration pattern = %q", schema.Defs["non_negative_duration"].Pattern)
	}
	assertStringPattern(t, "plan relative path", mustMarshalJSON(t, schema.Defs["relative_path"]), `^(?=.*\S)(?!\s)(?!/)(?!.*\s$)(?!.*(?:^|/)\.\.?(?:/|$)).+$`)
	assertSchemaRef(t, "plan default_timeout", schema.Defs["options"].Properties["default_timeout"], "#/$defs/positive_duration")
	assertSchemaRef(t, "plan gate timeout", schema.Defs["gate"].Properties["timeout"], "#/$defs/positive_duration")
	assertSchemaRef(t, "plan retry delay", schema.Defs["retry"].Properties["delay"], "#/$defs/non_negative_duration")
	assertSchemaRef(t, "plan gate cwd", schema.Defs["gate"].Properties["cwd"], "#/$defs/relative_path")
	assertSchemaRef(t, "plan artifact path", schema.Defs["artifact"].Properties["path"], "#/$defs/relative_path")
}

func TestDeprecationPolicyRetainsMinimumTransition(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "deprecations.md"))
	if err != nil {
		t.Fatal(err)
	}
	policy := string(data)
	for _, required := range []string{
		"accepted RFC or narrowly scoped compatibility decision",
		"stable machine warning code",
		"two minor releases",
		"90 calendar days",
		"replacement, migration, rollback",
		"Security issues may shorten the window",
	} {
		if !strings.Contains(policy, required) {
			t.Errorf("deprecation policy is missing %q", required)
		}
	}
}

func TestV1SupportPolicyDefinesEvidenceTiersAndWindows(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "support-policy.md"))
	if err != nil {
		t.Fatal(err)
	}
	policy := string(data)
	for _, required := range []string{
		"latest stable minor",
		"90 days",
		"CGO_ENABLED=0",
		"Linux | amd64",
		"Linux | arm64",
		"macOS | amd64",
		"macOS | arm64",
		"Windows",
		"cross-compiled archive is not called supported",
		"current Linux and macOS",
	} {
		if !strings.Contains(policy, required) {
			t.Errorf("support policy is missing %q", required)
		}
	}
}

func TestV1ExternalEvidenceBriefsAreActionable(t *testing.T) {
	root := filepath.Join("..", "..")
	tests := []struct {
		path     string
		required []string
	}{
		{
			path: "docs/rfcs/0001-v1-contract-freeze.md",
			required: []string{
				"Status: proposed",
				"at least 14 calendar days",
				"ten safety invariants",
				"Migration and Rollback",
				"Unresolved Questions",
				"Pending public discussion",
			},
		},
		{
			path: "docs/reviews/README.md",
			required: []string{
				"validate-v1-external-evidence.go",
				"vigil-external-evidence-v1.schema.json",
				"vigil-external-evidence-validation-v1.schema.json",
				"`external_report`",
				"VIGIL-AC-21",
			},
		},
		{
			path: "docs/reviews/external-integration-report.md",
			required: []string{
				"independent of Vigil's",
				"Vigil version, commit, and binary digest",
				"timeout or cancellation",
				"executable or metadata digest drift rejection",
				"P0/P1 findings",
			},
		},
		{
			path: "docs/reviews/security-review-brief.md",
			required: []string{
				"independent of the implementation",
				"every `VIGIL-SI-*` invariant",
				"documented non-sandbox boundary",
				"Do not use or request production private keys",
				"V1 cannot ship with an open P0/P1",
			},
		},
		{
			path: "docs/reviews/usability-review-brief.md",
			required: []string{
				"at least three participants",
				"Do not coach participants",
				"believes read-only is an operating-system sandbox",
				"stale-plan",
				"P0/P1 findings",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, test.path))
			if err != nil {
				t.Fatal(err)
			}
			document := string(data)
			for _, required := range test.required {
				if !strings.Contains(document, required) {
					t.Errorf("%s is missing %q", test.path, required)
				}
			}
		})
	}
}

func assertEnumValues(t *testing.T, raw json.RawMessage, want []string) {
	t.Helper()
	var property struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(raw, &property); err != nil {
		t.Fatal(err)
	}
	if len(property.Enum) != len(want) {
		t.Fatalf("enum length = %d, want %d: %#v", len(property.Enum), len(want), property.Enum)
	}
	seen := make(map[string]bool, len(property.Enum))
	for _, value := range property.Enum {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			t.Fatalf("enum is missing %q from %#v", value, property.Enum)
		}
	}
}

func assertStringPattern(t *testing.T, name string, raw json.RawMessage, want string) {
	t.Helper()
	var property struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &property); err != nil {
		t.Fatal(err)
	}
	if property.Pattern != want {
		t.Fatalf("%s pattern = %q, want %q", name, property.Pattern, want)
	}
}

func assertSchemaRef(t *testing.T, name string, raw json.RawMessage, want string) {
	t.Helper()
	var property struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &property); err != nil {
		t.Fatal(err)
	}
	if property.Ref != want {
		t.Fatalf("%s ref = %q, want %q", name, property.Ref, want)
	}
}

func goTestSymbolExists(t *testing.T, path, symbol string) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Errorf("parse Go evidence %s: %v", path, err)
		return false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && isGoTestFunction(function, symbol) {
			return true
		}
	}
	return false
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

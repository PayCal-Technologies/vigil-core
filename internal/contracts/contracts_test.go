package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
	"github.com/PayCal-Technologies/vigil-public/internal/config"
	"github.com/PayCal-Technologies/vigil-public/internal/externalevidence"
	"github.com/PayCal-Technologies/vigil-public/internal/operationalevidence"
	"github.com/PayCal-Technologies/vigil-public/internal/output"
	"github.com/PayCal-Technologies/vigil-public/internal/packs"
	"github.com/PayCal-Technologies/vigil-public/internal/plugins"
	"github.com/PayCal-Technologies/vigil-public/internal/runartifact"
)

func TestPublishedSchemasAreValidUniqueJSONDocuments(t *testing.T) {
	root := filepath.Join("..", "..")
	paths := []string{
		"schemas/vigil-config-v3.schema.json",
		"schemas/vigil-external-evidence-v1.schema.json",
		"schemas/vigil-external-evidence-validation-v1.schema.json",
		"schemas/vigil-operational-evidence-v1.schema.json",
		"schemas/vigil-output-v1.schema.json",
		"schemas/vigil-jsonl-event-v1.schema.json",
		"schemas/vigil-pack-v1.schema.json",
		"schemas/vigil-plugin-conformance-v1.schema.json",
		"schemas/vigil-plugin-index-v1.schema.json",
		"schemas/vigil-plugin-lock-v1.schema.json",
		"schemas/vigil-plugin-protocol-v1.schema.json",
		"schemas/vigil-plugin-publishers-v1.schema.json",
		"schemas/vigil-plugin-trust-v1.schema.json",
		"schemas/vigil-plan-v1.schema.json",
		"schemas/vigil-run-artifact-manifest-v1.schema.json",
		"schemas/vigil-v1-acceptance-gate-v1.schema.json",
		"schemas/vigil-v1-acceptance-ledger-v1.schema.json",
	}
	ids := map[string]string{}
	for _, relative := range paths {
		t.Run(relative, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			var schema map[string]any
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatalf("invalid schema JSON: %v", err)
			}
			if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
				t.Fatalf("unexpected dialect: %#v", schema["$schema"])
			}
			id, ok := schema["$id"].(string)
			if !ok || id == "" {
				t.Fatal("schema is missing $id")
			}
			if previous, duplicate := ids[id]; duplicate {
				t.Fatalf("schema ID duplicates %s", previous)
			}
			ids[id] = relative
			required, ok := schema["required"].([]any)
			if !ok || len(required) == 0 {
				t.Fatal("schema is missing required fields")
			}
		})
	}
}

func TestExternalEvidenceSchemaMatchesRuntimeContract(t *testing.T) {
	root := filepath.Join("..", "..")
	schemaData, err := os.ReadFile(filepath.Join(root, "schemas", "vigil-external-evidence-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		AllOf      []json.RawMessage          `json:"allOf"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatal(err)
	}
	var versionProperty struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
		t.Fatal(err)
	}
	if versionProperty.Const != externalevidence.SchemaVersion {
		t.Fatalf("external evidence schema version = %q", versionProperty.Const)
	}
	for _, field := range []string{
		"schema_version",
		"target",
		"generated_at",
		"candidate_commit",
		"candidate_version",
		"public_url",
		"reviewer",
		"criteria",
		"findings",
	} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("external evidence schema is missing report field %q", field)
		}
	}
	for definition, fields := range map[string][]string{
		"reviewer":  {"name", "organization", "relationship", "independent"},
		"criterion": {"id", "status", "detail", "evidence"},
		"finding":   {"id", "severity", "status", "summary", "url"},
	} {
		properties := schema.Defs[definition].Properties
		if properties == nil {
			t.Fatalf("external evidence schema is missing definition %q", definition)
		}
		for _, field := range fields {
			if _, ok := properties[field]; !ok {
				t.Fatalf("external evidence schema definition %q is missing field %q", definition, field)
			}
		}
	}
	assertEnumValues(t, schema.Defs["criterion"].Properties["status"], []string{"verified", "failed", "pending"})
	assertEnumValues(t, schema.Defs["finding"].Properties["severity"], []string{"P0", "P1", "P2", "P3"})
	assertEnumValues(t, schema.Defs["finding"].Properties["status"], []string{"open", "closed", "false_positive", "not_applicable"})
	for definition, fields := range map[string][]string{
		"reviewer":  {"name", "organization", "relationship"},
		"criterion": {"detail"},
		"finding":   {"summary"},
	} {
		for _, field := range fields {
			assertNonBlankStringProperty(t, "external evidence "+definition+" "+field, schema.Defs[definition].Properties[field])
		}
	}
	for _, criterionID := range []string{"VIGIL-AC-16", "VIGIL-AC-17", "VIGIL-AC-19", "VIGIL-AC-20", "VIGIL-AC-21"} {
		if !strings.Contains(string(schemaData), criterionID) {
			t.Fatalf("external evidence schema is missing criterion %s", criterionID)
		}
	}
	for _, required := range []string{"independent", "open_p0_p1_finding", "VIGIL-AC-21"} {
		if !strings.Contains(string(schemaData), required) {
			t.Fatalf("external evidence schema is missing %q", required)
		}
	}
}

func TestExternalEvidenceValidationSchemaMatchesRuntimeContract(t *testing.T) {
	root := filepath.Join("..", "..")
	schemaData, err := os.ReadFile(filepath.Join(root, "schemas", "vigil-external-evidence-validation-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		AllOf      []json.RawMessage          `json:"allOf"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatal(err)
	}
	var versionProperty struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
		t.Fatal(err)
	}
	if versionProperty.Const != externalevidence.ValidationReportSchemaVersion {
		t.Fatalf("external evidence validation schema version = %q", versionProperty.Const)
	}
	for _, field := range []string{
		"schema_version",
		"target",
		"report",
		"criterion",
		"status",
		"candidate_commit",
		"candidate_version",
		"public_url",
		"verified_criteria",
		"open_p0_p1_findings",
		"errors",
	} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("external evidence validation schema is missing report field %q", field)
		}
	}
	assertEnumValues(t, schema.Properties["status"], []string{
		string(externalevidence.ValidationStatusValid),
		string(externalevidence.ValidationStatusInvalid),
	})
	var errorsProperty struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(schema.Properties["errors"], &errorsProperty); err != nil {
		t.Fatal(err)
	}
	assertNonBlankStringProperty(t, "external evidence validation error item", errorsProperty.Items)
	foundValidReportRule := false
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
		if rule.If.Properties["status"].Const == string(externalevidence.ValidationStatusValid) {
			assertStringPattern(t, "external evidence validation valid report", rule.Then.Properties["report"], `\S`)
			foundValidReportRule = true
		}
	}
	if !foundValidReportRule {
		t.Fatal("external evidence validation schema must reject blank valid report paths")
	}
	for _, criterionID := range []string{"VIGIL-AC-16", "VIGIL-AC-17", "VIGIL-AC-19", "VIGIL-AC-20", "VIGIL-AC-21"} {
		if !strings.Contains(string(schemaData), criterionID) {
			t.Fatalf("external evidence validation schema is missing criterion %s", criterionID)
		}
	}
	for _, required := range []string{"valid", "invalid", "open_p0_p1_findings"} {
		if !strings.Contains(string(schemaData), required) {
			t.Fatalf("external evidence validation schema is missing %q", required)
		}
	}
}

func TestOperationalEvidenceSchemaMatchesCollectorReportContract(t *testing.T) {
	root := filepath.Join("..", "..")
	schemaData, err := os.ReadFile(filepath.Join(root, "schemas", "vigil-operational-evidence-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		AllOf      []json.RawMessage          `json:"allOf"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			AllOf      []json.RawMessage          `json:"allOf"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatal(err)
	}
	var versionProperty struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
		t.Fatal(err)
	}
	if versionProperty.Const != operationalevidence.SchemaVersion {
		t.Fatalf("operational evidence schema version = %q", versionProperty.Const)
	}
	for _, field := range []string{
		"schema_version",
		"generated_at",
		"repository",
		"tag",
		"version",
		"tap_repository",
		"plugin_index_url",
		"acceptance_ledger",
		"release",
		"workflow_run",
		"plugin_index",
		"downloaded_assets",
		"criteria",
		"commands",
		"notes",
	} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("operational evidence schema is missing report field %q", field)
		}
	}
	for definition, fields := range map[string][]string{
		"release":          {"tag_name", "target_commitish", "resolved_commit", "url", "is_draft", "is_prerelease", "is_immutable", "published_at", "assets"},
		"workflow_run":     {"database_id", "url", "status", "conclusion", "head_sha", "jobs"},
		"job":              {"name", "status", "conclusion", "url"},
		"plugin_index":     {"source", "index_digest", "ceremony_url", "signature_threshold", "signer_ids", "publisher_keys", "plugin_count", "artifact_count", "artifacts_verified"},
		"publisher_key":    {"key_id", "algorithm", "source"},
		"downloaded_asset": {"name", "sha256", "size"},
		"criterion":        {"id", "status", "detail", "evidence"},
		"command":          {"name", "command", "exit_code", "duration_millis", "stdout", "stderr", "truncated"},
	} {
		properties := schema.Defs[definition].Properties
		if properties == nil {
			t.Fatalf("operational evidence schema is missing definition %q", definition)
		}
		for _, field := range fields {
			if _, ok := properties[field]; !ok {
				t.Fatalf("operational evidence schema definition %q is missing field %q", definition, field)
			}
		}
	}
	assertNonBlankStringProperty(t, "operational evidence acceptance_ledger", schema.Properties["acceptance_ledger"])
	for definition, fields := range map[string][]string{
		"release":          {"tag_name", "target_commitish"},
		"job":              {"name", "status"},
		"publisher_key":    {"source"},
		"downloaded_asset": {"name"},
		"criterion":        {"detail"},
		"command":          {"name", "command"},
	} {
		for _, field := range fields {
			assertNonBlankStringProperty(t, "operational evidence "+definition+" "+field, schema.Defs[definition].Properties[field])
		}
	}
	var evidenceProperty struct {
		MinItems int             `json:"minItems"`
		Items    json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(schema.Defs["criterion"].Properties["evidence"], &evidenceProperty); err != nil {
		t.Fatal(err)
	}
	if evidenceProperty.MinItems != 1 {
		t.Fatalf("operational evidence schema must require non-empty verified evidence")
	}
	if !strings.Contains(string(evidenceProperty.Items), "#/$defs/https_url") {
		t.Fatalf("operational evidence schema must require public HTTPS evidence entries")
	}
	foundVerifiedEvidenceRule := false
	foundVerifiedIDAllowlistRule := false
	for _, rule := range schema.Defs["criterion"].AllOf {
		text := string(rule)
		if strings.Contains(text, `"const": "verified"`) && strings.Contains(text, `"evidence"`) {
			foundVerifiedEvidenceRule = true
		}
		var typedRule struct {
			Then struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"then"`
		}
		if err := json.Unmarshal(rule, &typedRule); err == nil {
			if sameStringSet(typedRule.Then.Properties["id"].Enum, []string{"VIGIL-AC-09", "VIGIL-AC-11", "VIGIL-AC-12", "VIGIL-AC-13", "VIGIL-AC-18"}) {
				foundVerifiedIDAllowlistRule = true
			}
		}
	}
	if !foundVerifiedEvidenceRule {
		t.Fatalf("operational evidence schema is missing the verified criterion evidence rule")
	}
	if !foundVerifiedIDAllowlistRule {
		t.Fatalf("operational evidence schema must restrict verified criteria to operational proof IDs")
	}
	rootRules := string(mustMarshalJSON(t, schema.AllOf))
	schemaText := string(schemaData)
	for _, criterionID := range []string{"VIGIL-AC-09", "VIGIL-AC-11", "VIGIL-AC-12", "VIGIL-AC-13", "VIGIL-AC-18"} {
		if !strings.Contains(schemaText, criterionID) {
			t.Fatalf("operational evidence schema is missing verified proof rule for %s", criterionID)
		}
	}
	for _, required := range []string{"release", "resolved_commit", "published_at", "workflow_run", "downloaded_assets", "tap_repository", "plugin_index_url", "plugin_index", "ceremony_url", "publisher_keys"} {
		if !strings.Contains(rootRules, required) {
			t.Fatalf("operational evidence schema proof rules are missing %q", required)
		}
	}

	model, err := os.ReadFile(filepath.Join(root, "internal", "operationalevidence", "evidence.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`json:"workflow_run,omitempty"`,
		`json:"plugin_index,omitempty"`,
		`json:"downloaded_assets,omitempty"`,
		`json:"criteria"`,
		`json:"commands,omitempty"`,
		`func ExpectedReleaseAssetNames`,
		`func RequiredNativeReleaseSmokeJobs`,
		`func validateVerifiedCriterionClaim`,
		`type PublisherKeyEvidence struct`,
	} {
		if !strings.Contains(string(model), required) {
			t.Fatalf("operational evidence model is missing schema contract token %q", required)
		}
	}
	collector, err := os.ReadFile(filepath.Join(root, "scripts", "collect-v1-operational-evidence.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`reportSchemaVersion = operationalevidence.SchemaVersion`,
		`operationalevidence.Validate(result)`,
		`operationalevidence.ExpectedReleaseAssetNames`,
		`operationalevidence.RequiredJobsPassed`,
		`plugin-ceremony-url`,
		`PublisherKeyEvidence`,
		`acceptance_ledger_validation`,
	} {
		if !strings.Contains(string(collector), required) {
			t.Fatalf("operational evidence collector is missing validation token %q", required)
		}
	}
	if strings.Contains(string(collector), `setCriterion(&result, "VIGIL-AC-22", "verified"`) {
		t.Fatal("operational evidence collector must not mark AC22 verified")
	}
}

func TestOutputSchemaMatchesRuntimeExitStatusContract(t *testing.T) {
	root := filepath.Join("..", "..")
	schemaData, err := os.ReadFile(filepath.Join(root, "schemas", "vigil-output-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		AllOf      []json.RawMessage          `json:"allOf"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatal(err)
	}
	var versionProperty struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
		t.Fatal(err)
	}
	if versionProperty.Const != output.EnvelopeSchemaVersion {
		t.Fatalf("output schema version = %q", versionProperty.Const)
	}
	assertNonBlankStringProperty(t, "output command", schema.Properties["command"])
	assertEnumValues(t, schema.Properties["status"], []string{
		"ok",
		"failed",
		"invalid",
		"blocked",
		"dependency_missing",
		"interrupted",
		"mutation_violation",
		"internal_error",
	})
	var exitCodeProperty struct {
		Type    string `json:"type"`
		Minimum int    `json:"minimum"`
		Maximum int    `json:"maximum"`
	}
	if err := json.Unmarshal(schema.Properties["exit_code"], &exitCodeProperty); err != nil {
		t.Fatal(err)
	}
	if exitCodeProperty.Type != "integer" || exitCodeProperty.Minimum != cli.ExitSuccess || exitCodeProperty.Maximum != cli.ExitInternal {
		t.Fatalf("exit_code schema = %#v", exitCodeProperty)
	}

	schemaStatusesByExitCode := map[int]string{}
	for index, rawRule := range schema.AllOf {
		var rule struct {
			If struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"if"`
			Then struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"then"`
		}
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			t.Fatalf("allOf[%d]: %v", index, err)
		}
		if !containsString(rule.If.Required, "exit_code") {
			t.Fatalf("allOf[%d] does not require exit_code in the if branch", index)
		}
		var exitCode struct {
			Const int `json:"const"`
		}
		if err := json.Unmarshal(rule.If.Properties["exit_code"], &exitCode); err != nil {
			t.Fatalf("allOf[%d] exit_code const: %v", index, err)
		}
		var status struct {
			Const string `json:"const"`
		}
		if err := json.Unmarshal(rule.Then.Properties["status"], &status); err != nil {
			t.Fatalf("allOf[%d] status const: %v", index, err)
		}
		if _, exists := schemaStatusesByExitCode[exitCode.Const]; exists {
			t.Fatalf("duplicate output schema rule for exit_code %d", exitCode.Const)
		}
		schemaStatusesByExitCode[exitCode.Const] = status.Const
	}

	expectedCodes := []int{
		cli.ExitSuccess,
		cli.ExitCheckFailed,
		cli.ExitUsage,
		cli.ExitPolicyBlocked,
		cli.ExitDependencyMissing,
		cli.ExitInterrupted,
		cli.ExitMutationViolation,
		cli.ExitInternal,
	}
	if len(schemaStatusesByExitCode) != len(expectedCodes) {
		t.Fatalf("output schema exit/status rule count = %d, want %d", len(schemaStatusesByExitCode), len(expectedCodes))
	}
	for _, code := range expectedCodes {
		class := cli.ClassifyExit(code)
		if got := schemaStatusesByExitCode[code]; got != class.Status {
			t.Fatalf("output schema exit_code %d maps to status %q, runtime maps to %q", code, got, class.Status)
		}
	}
	assertNonBlankStringProperty(t, "output diagnostic message", schema.Defs["diagnostic"].Properties["message"])
	assertNonBlankStringProperty(t, "output artifact kind", schema.Defs["artifact"].Properties["kind"])
	assertNonBlankStringProperty(t, "output artifact path", schema.Defs["artifact"].Properties["path"])
}

func TestJSONLEventSchemaMatchesRuntimeContract(t *testing.T) {
	root := filepath.Join("..", "..")
	schemaData, err := os.ReadFile(filepath.Join(root, "schemas", "vigil-jsonl-event-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Required             []string
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("JSONL event schema must reject unknown top-level fields")
	}
	if !sameStringSet(schema.Required, []string{"schema_version", "sequence", "type", "command", "timestamp", "data"}) {
		t.Fatalf("JSONL event required fields = %#v", schema.Required)
	}
	var versionProperty struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
		t.Fatal(err)
	}
	if versionProperty.Const != output.EventSchemaVersion {
		t.Fatalf("JSONL event schema version = %q", versionProperty.Const)
	}
	var sequenceProperty struct {
		Type    string `json:"type"`
		Minimum int    `json:"minimum"`
	}
	if err := json.Unmarshal(schema.Properties["sequence"], &sequenceProperty); err != nil {
		t.Fatal(err)
	}
	if sequenceProperty.Type != "integer" || sequenceProperty.Minimum != 1 {
		t.Fatalf("JSONL event sequence schema = %#v", sequenceProperty)
	}
	for _, field := range []string{"type", "command"} {
		assertNonBlankStringProperty(t, "JSONL event "+field, schema.Properties[field])
	}
	var timestampProperty struct {
		Type   string `json:"type"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(schema.Properties["timestamp"], &timestampProperty); err != nil {
		t.Fatal(err)
	}
	if timestampProperty.Type != "string" || timestampProperty.Format != "date-time" {
		t.Fatalf("JSONL event timestamp schema = %#v", timestampProperty)
	}
}

func TestRunArtifactManifestSchemaMatchesRuntimeContract(t *testing.T) {
	root := filepath.Join("..", "..")
	schemaData, err := os.ReadFile(filepath.Join(root, "schemas", "vigil-run-artifact-manifest-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Required             []string
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("run artifact manifest schema must reject unknown top-level fields")
	}
	if !sameStringSet(schema.Required, []string{
		"schema_version",
		"run_id",
		"created_at",
		"artifact_dir",
		"plan_path",
		"result_path",
		"gates_dir",
		"max_gate_log_bytes",
		"max_run_log_bytes",
	}) {
		t.Fatalf("run artifact manifest required fields = %#v", schema.Required)
	}
	var versionProperty struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &versionProperty); err != nil {
		t.Fatal(err)
	}
	if versionProperty.Const != runartifact.ManifestSchemaVersion {
		t.Fatalf("run artifact manifest schema version = %q", versionProperty.Const)
	}
	var runIDProperty struct {
		Type      string `json:"type"`
		MinLength int    `json:"minLength"`
		Pattern   string `json:"pattern"`
	}
	if err := json.Unmarshal(schema.Properties["run_id"], &runIDProperty); err != nil {
		t.Fatal(err)
	}
	wantRunIDPattern := `^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`
	if runIDProperty.Type != "string" || runIDProperty.MinLength != 1 || runIDProperty.Pattern != wantRunIDPattern {
		t.Fatalf("run artifact manifest run_id schema = %#v", runIDProperty)
	}
	for _, field := range []string{"artifact_dir"} {
		assertNonBlankStringProperty(t, "run artifact manifest "+field, schema.Properties[field])
	}
	for field, want := range map[string]string{
		"plan_path":   "plan.json",
		"result_path": "result.json",
		"gates_dir":   "gates",
	} {
		var property struct {
			Const string `json:"const"`
		}
		if err := json.Unmarshal(schema.Properties[field], &property); err != nil {
			t.Fatal(err)
		}
		if property.Const != want {
			t.Fatalf("run artifact manifest %s const = %q", field, property.Const)
		}
	}
	for field, want := range map[string]int64{
		"max_gate_log_bytes": runartifact.MaxGateLogBytes,
		"max_run_log_bytes":  runartifact.MaxRunLogBytes,
	} {
		var property struct {
			Type    string `json:"type"`
			Minimum int64  `json:"minimum"`
		}
		if err := json.Unmarshal(schema.Properties[field], &property); err != nil {
			t.Fatal(err)
		}
		if property.Type != "integer" || property.Minimum != 1 || want < property.Minimum {
			t.Fatalf("run artifact manifest %s schema = %#v, runtime = %d", field, property, want)
		}
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range got {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			return false
		}
	}
	return true
}

func assertNonBlankStringProperty(t *testing.T, name string, raw json.RawMessage) {
	t.Helper()
	var property struct {
		Type      string `json:"type"`
		MinLength int    `json:"minLength"`
		Pattern   string `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &property); err != nil {
		t.Fatal(err)
	}
	if property.Type != "string" || property.MinLength != 1 || property.Pattern != `\S` {
		t.Fatalf("%s schema = %#v", name, property)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestPublishedPluginSchemaVersionsMatchRuntime(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"schemas/vigil-plugin-conformance-v1.schema.json", plugins.ConformanceSchemaVersion},
		{"schemas/vigil-plugin-index-v1.schema.json", plugins.IndexSchemaVersion},
		{"schemas/vigil-plugin-lock-v1.schema.json", plugins.LockSchemaVersion},
		{"schemas/vigil-plugin-protocol-v1.schema.json", plugins.HandshakeSchema},
		{"schemas/vigil-plugin-publishers-v1.schema.json", plugins.PublisherSchemaVersion},
		{"schemas/vigil-plugin-trust-v1.schema.json", plugins.TrustSchemaVersion},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", test.path))
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]struct {
					Const string `json:"const"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			if got := schema.Properties["schema_version"].Const; got != test.want {
				t.Fatalf("schema const = %q, runtime = %q", got, test.want)
			}
		})
	}
}

func TestPackSchemaMatchesRuntimeSourceRootContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "vigil-pack-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Pattern    string                     `json:"pattern"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	var sourceRoot struct {
		Type      string `json:"type"`
		MinLength int    `json:"minLength"`
		Pattern   string `json:"pattern"`
	}
	if err := json.Unmarshal(schema.Properties["source_root"], &sourceRoot); err != nil {
		t.Fatal(err)
	}
	wantPattern := `^(?![A-Za-z][A-Za-z0-9+.-]*:)(?!.*(?:^|/)\.\.?($|/))(?:[^\s/?#:%\\]+)(?:/[^\s/?#:%\\]+)*$`
	if sourceRoot.Type != "string" || sourceRoot.MinLength != 1 || sourceRoot.Pattern != wantPattern {
		t.Fatalf("pack source_root schema = %#v", sourceRoot)
	}
	wantDurationPattern := `^(?=.*[1-9])(?:[0-9]+(?:ns|us|µs|ms|s|m|h))+$`
	if schema.Defs["positive_duration"].Pattern != wantDurationPattern {
		t.Fatalf("pack positive duration pattern = %q", schema.Defs["positive_duration"].Pattern)
	}
	assertSchemaRef(t, "pack command contract timeout", schema.Defs["command_contract"].Properties["timeout"], "#/$defs/positive_duration")
	for _, sourceRoot := range []string{"../pack", "/tmp/pack", "https://example.test/pack", "extensions\\pack", "extensions/pack with spaces"} {
		manifest := packs.Manifest{
			SchemaVersion:  packs.SchemaVersion,
			HostAPIVersion: packs.HostAPIVersion,
			ID:             "fixture",
			Name:           "Fixture",
			Kind:           "custom",
			Status:         "local",
			Description:    "fixture",
			SourceRoot:     sourceRoot,
			Commands:       []string{"fixture:check"},
			CommandContracts: []packs.CommandContract{{
				Command:       "fixture:check",
				Access:        "read",
				Capabilities:  []string{"filesystem:read"},
				Binding:       "builtin:fixture:check",
				Timeout:       "10m",
				Stability:     "stable",
				RequiredTools: []string{},
				Network:       "none",
				OutputFormats: []string{"text"},
				Usage:         "vigil fixture:check",
				Description:   "Run fixture check.",
			}},
		}
		if issues := strings.Join(packs.Validate(manifest), "\n"); !strings.Contains(issues, "source_root") {
			t.Fatalf("runtime accepted unsafe source_root %q: %s", sourceRoot, issues)
		}
	}
}

func TestPluginProtocolSchemaMatchesRuntimeContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "vigil-plugin-protocol-v1.schema.json"))
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
	relativePathPattern := `^(?=.*\S)(?!\s)(?!/)(?!.*\s$)(?!.*(?:^|/)\.\.?(?:/|$)).+$`
	if schema.Defs["positive_duration"].Pattern != positiveDurationPattern {
		t.Fatalf("plugin protocol positive duration pattern = %q", schema.Defs["positive_duration"].Pattern)
	}
	if schema.Defs["relative_path"].Pattern != relativePathPattern {
		t.Fatalf("plugin protocol relative path pattern = %q", schema.Defs["relative_path"].Pattern)
	}
	assertSchemaRef(t, "plugin protocol command timeout", schema.Defs["command"].Properties["timeout"], "#/$defs/positive_duration")
	assertNonBlankStringProperty(t, "plugin protocol diagnostic message", schema.Defs["diagnostic"].Properties["message"])
	assertNonBlankStringProperty(t, "plugin protocol artifact kind", schema.Defs["artifact"].Properties["kind"])
	assertSchemaRef(t, "plugin protocol artifact path", schema.Defs["artifact"].Properties["path"], "#/$defs/relative_path")
}

func TestPluginIndexSchemaMatchesRuntimeURLAndSignatureContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "vigil-plugin-index-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			AnyOf      []json.RawMessage          `json:"anyOf"`
			Pattern    string                     `json:"pattern"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	artifactURL, ok := schema.Defs["artifact_url"]
	if !ok || len(artifactURL.AnyOf) != 2 {
		t.Fatalf("plugin index schema must define HTTPS-or-relative artifact_url: %#v", artifactURL)
	}
	hasHTTPSRule := false
	hasRelativeRule := false
	for _, rawRule := range artifactURL.AnyOf {
		var rule struct {
			Ref     string `json:"$ref"`
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			t.Fatal(err)
		}
		if rule.Ref == "#/$defs/https_url" {
			hasHTTPSRule = true
		}
		if rule.Pattern == `^(?![A-Za-z][A-Za-z0-9+.-]*:)(?!/)(?!\.?$)(?!.*(?:^|/)\.\.(?:/|$))(?!.*[?#:%\\]).+$` {
			hasRelativeRule = true
		}
	}
	if !hasHTTPSRule || !hasRelativeRule {
		t.Fatalf("plugin index artifact URL schema rules = %#v", artifactURL.AnyOf)
	}
	artifactURLProperty := string(schema.Defs["artifact"].Properties["url"])
	if !strings.Contains(artifactURLProperty, "#/$defs/artifact_url") {
		t.Fatalf("plugin index artifact url must reference artifact_url: %s", artifactURLProperty)
	}
	var signatureProperty struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(schema.Defs["signature"].Properties["signature"], &signatureProperty); err != nil {
		t.Fatal(err)
	}
	if signatureProperty.Pattern != "^[A-Za-z0-9+/]{86}==$" {
		t.Fatalf("plugin index signature pattern = %q", signatureProperty.Pattern)
	}
}

func TestRepositoryConfigMatchesPublishedV3Contract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "vigil.config.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := config.ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(document.Config); err != nil {
		t.Fatal(err)
	}
	if document.Config.SchemaVersion != config.SchemaVersion {
		t.Fatalf("schema_version = %q", document.Config.SchemaVersion)
	}
	if document.Config.Plugins == nil || document.Config.Plugins.MinSignatureThreshold != 2 {
		t.Fatalf("repository plugin policy must require a two-signature index threshold: %#v", document.Config.Plugins)
	}
}

func TestConfigSchemaPublishesPluginPolicyMinimumThreshold(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "vigil-config-v3.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	var minimumThreshold struct {
		Type    string `json:"type"`
		Minimum int    `json:"minimum"`
	}
	if err := json.Unmarshal(schema.Properties["plugins"].Properties["min_signature_threshold"], &minimumThreshold); err != nil {
		t.Fatal(err)
	}
	if minimumThreshold.Type != "integer" || minimumThreshold.Minimum != 0 {
		t.Fatalf("min_signature_threshold schema = %#v", minimumThreshold)
	}
}

func TestWorkflowGraphExampleMatchesRuntimeContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "workflow-graph.config.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := config.ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(document.Config); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeGuideRetainsFailClosedRollbackProcedure(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "upgrading.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(data)
	for _, required := range []string{
		"vigil config:migrate --json",
		"vigil.config.json.bak-*",
		"Plans bind the exact Vigil",
		"executable digest",
		"plugins:index:verify",
		"Do not weaken plugin policy",
	} {
		if !strings.Contains(guide, required) {
			t.Fatalf("upgrade guide is missing %q", required)
		}
	}
}

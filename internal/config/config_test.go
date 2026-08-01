package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyDocumentDefaultsMigratesLegacyShellAndArgvGates(t *testing.T) {
	data := []byte(`{
  "schema_version": "2",
  "profile": "generic",
  "project": "fixture",
  "coordination": {
    "mode": "custom",
    "authoritative_surfaces": ["repository"],
    "mutation_requires": ["explicit-confirmation"]
  },
  "gates": [
    {"name": "argv", "command": "go test ./...", "read_only": true},
    {"name": "shell", "command": "go test ./... | tee result.log", "read_only": false}
  ],
  "extensions": {"enabled": false}
}`)
	doc, err := ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ApplyDocumentDefaults(doc, "generic", "fixture")
	if cfg.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %q", cfg.SchemaVersion)
	}
	if cfg.Gates[0].Command != "go" || strings.Join(cfg.Gates[0].Args, " ") != "test ./..." || cfg.Gates[0].Shell {
		t.Fatalf("argv gate = %#v", cfg.Gates[0])
	}
	if !cfg.Gates[1].Shell {
		t.Fatalf("shell gate = %#v", cfg.Gates[1])
	}
}

func TestMarshalDocumentPreservesUnknownFieldsAndDropsLegacyAuthority(t *testing.T) {
	data := []byte(`{
  "schema_version": "1",
  "profile": "generic",
  "project": "fixture",
  "authority": {"local_first": true, "mutation_requires": ["explicit-confirmation"]},
  "coordination": {"custom_nested": "keep"},
  "gates": [],
  "extensions": {"custom_nested": "keep"},
  "future_top_level": {"keep": true}
}`)
	doc, err := ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ApplyDocumentDefaults(doc, "generic", "fixture")
	encoded, err := MarshalDocument(doc.Raw, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if _, ok := output["authority"]; ok {
		t.Fatal("legacy authority survived migration")
	}
	if _, ok := output["future_top_level"]; !ok {
		t.Fatal("unknown top-level field was dropped")
	}
	if !strings.Contains(string(output["coordination"]), "custom_nested") || !strings.Contains(string(output["extensions"]), "custom_nested") {
		t.Fatal("unknown nested fields were dropped")
	}
}

func TestValidateIssuesRejectsAmbiguousArgvAndEscapingPackRoot(t *testing.T) {
	cfg := Template("generic", "fixture")
	cfg.Gates[0].Command = "go test ./..."
	cfg.Extensions.ManifestRoot = "../outside"
	issues := ValidateIssues(cfg)
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	if !codes["gates.argv.command"] || !codes["extensions.manifest_root.escape"] {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestGateIssuesRejectInvalidWorkflowGraphAndUnsafeExecutionControls(t *testing.T) {
	cfg := Template("generic", "fixture")
	cfg.Gates = []Gate{
		{
			Name:          "lint",
			Command:       "true",
			ReadOnly:      true,
			DependsOn:     []string{"test", "missing"},
			ParallelGroup: "analysis",
			Retry:         &GateRetry{MaxAttempts: 8, On: []string{"cancelled"}},
			CWD:           "../outside",
			Environment:   map[string]string{"VIGIL_CONFIG": "override", "BAD-NAME": "value"},
			Artifacts:     []GateArtifact{{Path: "../result.xml"}},
		},
		{
			Name:          "test",
			Command:       "true",
			ReadOnly:      false,
			DependsOn:     []string{"lint"},
			ParallelGroup: "analysis",
		},
	}
	issues := ValidateIssues(cfg)
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{
		"gates.dependency.unknown",
		"gates.dependency.cycle",
		"gates.parallel_group.mutation",
		"gates.retry.attempts",
		"gates.retry.network",
		"gates.retry.state",
		"gates.cwd.escape",
		"gates.environment.reserved",
		"gates.environment.name",
		"gates.artifact.path.escape",
	} {
		if !codes[code] {
			t.Errorf("missing issue code %s in %#v", code, issues)
		}
	}
}

func TestGateRequiredDefaultsToTrue(t *testing.T) {
	gate := Gate{}
	if !gate.IsRequired() {
		t.Fatal("omitted required field must preserve fail-closed behavior")
	}
	required := false
	gate.Required = &required
	if gate.IsRequired() {
		t.Fatal("explicit optional gate was treated as required")
	}
}

func FuzzConfigDocumentRoundTrip(f *testing.F) {
	f.Add([]byte(`{
	  "schema_version": "3",
	  "profile": "generic",
	  "project": "fixture",
	  "coordination": {
	    "mode": "custom",
	    "authoritative_surfaces": ["repository"],
	    "mutation_requires": ["explicit-confirmation"]
	  },
	  "gates": [{"name": "test", "command": "go", "args": ["test", "./..."], "read_only": true}],
	  "extensions": {
	    "enabled": true,
	    "manifest_root": "extensions",
	    "allowed_kinds": ["adapter"],
	    "require_private": false
	  }
	}`))
	f.Add([]byte(`{"schema_version":"99","future":true}`))
	f.Add([]byte(`not-json`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		document, err := ParseDocument(data)
		if err != nil {
			return
		}
		cfg := ApplyDocumentDefaults(document, "generic", "fuzz")
		_ = ValidateIssues(cfg)
		encoded, err := MarshalDocument(document.Raw, cfg)
		if err != nil {
			t.Fatalf("parsed config could not be marshaled: %v", err)
		}
		if _, err := ParseDocument(encoded); err != nil {
			t.Fatalf("marshaled config could not be parsed: %v", err)
		}
	})
}

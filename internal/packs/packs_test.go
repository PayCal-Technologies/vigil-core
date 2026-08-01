package packs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadIncludesEmbeddedOfficialPacksWithoutFilesystemRoot(t *testing.T) {
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(t.TempDir(), "missing"))
	boundary := t.TempDir()
	report := Load(Options{
		RepositoryRoot:     filepath.Join(boundary, "missing"),
		RepositoryBoundary: boundary,
		Settings: Settings{
			Enabled:      true,
			AllowedKinds: []string{"custom"},
		},
	})
	if report.Status != "ok" || report.Count != 10 {
		t.Fatalf("report = %#v", report)
	}
	for _, manifest := range report.Extensions {
		if manifest.Origin != "embedded-official" {
			t.Fatalf("origin = %q", manifest.Origin)
		}
	}
}

func TestDisabledPackReportUsesEmptyExtensionArray(t *testing.T) {
	report := Load(Options{Settings: Settings{Enabled: false}})
	if report.Extensions == nil || len(report.Extensions) != 0 {
		t.Fatalf("disabled extensions = %#v", report.Extensions)
	}
}

func TestRepositoryLayerOverridesOfficialPackAndReportsIt(t *testing.T) {
	boundary := t.TempDir()
	root := filepath.Join(boundary, "extensions")
	manifest := Manifest{
		SchemaVersion:  SchemaVersion,
		HostAPIVersion: HostAPIVersion,
		ID:             "github-cicd",
		Name:           "Repository GitHub Pack",
		Kind:           "custom",
		Status:         "local",
		Description:    "repository override",
		SourceRoot:     "extensions/github-cicd",
		Commands:       []string{"github:init-ci"},
		CommandContracts: []CommandContract{{
			Command:       "github:init-ci",
			Access:        "conditional-write",
			Capabilities:  []string{"filesystem:read", "filesystem:write"},
			Binding:       "builtin:github:init-ci",
			Timeout:       "10m",
			Stability:     "stable",
			RequiredTools: []string{},
			Network:       "none",
			OutputFormats: []string{"text"},
			WriteFlags:    []string{"--write"},
			Usage:         "vigil github:init-ci [--write]",
			Description:   "Generate CI.",
		}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, manifest.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extension.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	report := Load(Options{
		RepositoryRoot:     root,
		RepositoryBoundary: boundary,
		UserRoot:           filepath.Join(boundary, "missing-user"),
		Settings:           Settings{Enabled: true, AllowedKinds: []string{"custom"}},
	})
	if report.Status != "ok" {
		t.Fatalf("issues = %#v", report.Issues)
	}
	found := false
	for _, loaded := range report.Extensions {
		if loaded.ID == manifest.ID {
			found = true
			if loaded.Origin != "repository" || loaded.Description != manifest.Description {
				t.Fatalf("loaded override = %#v", loaded)
			}
		}
	}
	if !found || len(report.Overrides) != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestConfineRepositoryRootRejectsTraversal(t *testing.T) {
	boundary := t.TempDir()
	if _, err := ConfineRepositoryRoot(filepath.Join(boundary, "..", "outside"), boundary); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestValidateRejectsUnsupportedHostAPI(t *testing.T) {
	manifest := Manifest{
		SchemaVersion:  SchemaVersion,
		HostAPIVersion: "v999",
		ID:             "fixture",
		Name:           "Fixture",
		Kind:           "custom",
		Status:         "local",
		Description:    "fixture",
		SourceRoot:     "extensions/fixture",
	}
	issues := Validate(manifest)
	found := false
	for _, issue := range issues {
		if issue == ": unsupported host_api_version v999" {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestValidateRequiresCompleteCommandContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CommandContract)
		want   string
	}{
		{name: "capabilities", mutate: func(contract *CommandContract) { contract.Capabilities = nil }, want: "missing capabilities"},
		{name: "binding", mutate: func(contract *CommandContract) { contract.Binding = "" }, want: "binding must be builtin:fixture:check"},
		{name: "timeout", mutate: func(contract *CommandContract) { contract.Timeout = "" }, want: "timeout must be a positive Go duration"},
		{name: "stability", mutate: func(contract *CommandContract) { contract.Stability = "" }, want: "unsupported stability"},
		{name: "required tools", mutate: func(contract *CommandContract) { contract.RequiredTools = nil }, want: "missing required_tools"},
		{name: "network", mutate: func(contract *CommandContract) { contract.Network = "" }, want: "unsupported network behavior"},
		{name: "output formats", mutate: func(contract *CommandContract) { contract.OutputFormats = nil }, want: "missing output_formats"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := completeManifest()
			test.mutate(&manifest.CommandContracts[0])
			issues := strings.Join(Validate(manifest), "\n")
			if !strings.Contains(issues, test.want) {
				t.Fatalf("issues = %q, want %q", issues, test.want)
			}
		})
	}
}

func TestValidateRejectsContradictoryNetworkContract(t *testing.T) {
	manifest := completeManifest()
	manifest.CommandContracts[0].Network = "optional"
	issues := strings.Join(Validate(manifest), "\n")
	if !strings.Contains(issues, "network behavior and network capability disagree") {
		t.Fatalf("issues = %q", issues)
	}
}

func TestValidateRejectsReadContractWithWriteCapability(t *testing.T) {
	manifest := completeManifest()
	manifest.CommandContracts[0].Capabilities = append(
		manifest.CommandContracts[0].Capabilities,
		"filesystem:write",
	)
	issues := strings.Join(Validate(manifest), "\n")
	if !strings.Contains(issues, "read access cannot declare a write capability") {
		t.Fatalf("issues = %q", issues)
	}
}

func TestValidateRejectsUnsafeSourceRoot(t *testing.T) {
	tests := []string{
		" extensions/fixture",
		"/tmp/fixture",
		"../fixture",
		"extensions/../fixture",
		"extensions//fixture",
		"extensions/fixture/",
		"extensions\\fixture",
		"https://example.test/fixture",
		"extensions/fixture?debug=true",
		"extensions/fixture with spaces",
	}
	for _, sourceRoot := range tests {
		t.Run(sourceRoot, func(t *testing.T) {
			manifest := completeManifest()
			manifest.SourceRoot = sourceRoot
			issues := strings.Join(Validate(manifest), "\n")
			if !strings.Contains(issues, "source_root must be a safe relative slash-separated path") {
				t.Fatalf("issues = %q", issues)
			}
		})
	}
}

func completeManifest() Manifest {
	return Manifest{
		SchemaVersion:  SchemaVersion,
		HostAPIVersion: HostAPIVersion,
		ID:             "fixture",
		Name:           "Fixture",
		Kind:           "custom",
		Status:         "local",
		Description:    "fixture",
		SourceRoot:     "extensions/fixture",
		Commands:       []string{"fixture:check"},
		CommandContracts: []CommandContract{{
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
		Path: "fixture/extension.json",
	}
}

func FuzzPackManifestParsing(f *testing.F) {
	valid, err := json.Marshal(completeManifest())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{"schema_version":"1","unknown":true}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxManifestSize {
			return
		}
		manifest, err := parseManifest(data)
		if err != nil {
			return
		}
		_ = Validate(manifest)
		encoded, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("parsed manifest could not be marshaled: %v", err)
		}
		if _, err := parseManifest(encoded); err != nil {
			t.Fatalf("marshaled manifest could not be parsed: %v", err)
		}
	})
}

func FuzzPathConfinement(f *testing.F) {
	f.Add("/repo", "/repo/sub/file")
	f.Add("/repo", "/repo/../outside")
	f.Add(".", "extensions/pack")
	f.Fuzz(func(t *testing.T, root, candidate string) {
		if len(root) > 4096 || len(candidate) > 4096 {
			return
		}
		relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
		want := err == nil &&
			(relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
		if got := PathInside(root, candidate); got != want {
			t.Fatalf("PathInside(%q, %q) = %v, want %v", root, candidate, got, want)
		}
	})
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveCommandsIncludePublicCICD(t *testing.T) {
	commands := map[string]bool{}
	for _, command := range activeCommands() {
		commands[command.Command] = true
	}
	for _, want := range []string{
		"doctor",
		"status",
		"plan",
		"workflow:local",
		"hooks:install",
		"hooks:pre-commit",
		"hooks:pre-push",
		"checks:public-assumptions",
		"checks:command-catalog",
		"files:iterate",
		"checks:public-parity",
		"readme:generate",
		"readme:check",
		"a11y:inventory",
		"checks:dependency-security",
		"checks:release-policy",
		"checks:tracked-assistant-artifacts",
		"security:gitleaks",
		"repo:health",
		"config:report",
		"config:migrate",
		"config:template",
		"explain",
		"init:ci",
		"github:init-ci",
		"guards:summary",
		"self-heal:plan",
		"next",
		"tools:catalog",
		"resources:catalog",
		"deploy:verify",
		"tests:history",
		"tests:affected",
		"javascript:quality",
		"php:lint",
		"phpstan:analyse",
	} {
		if !commands[want] {
			t.Fatalf("active command %s missing", want)
		}
	}
}

func TestManualForExtensionCommandUsesContract(t *testing.T) {
	manual, ok := manualForCommand("github:init-ci")
	if !ok {
		t.Fatal("github:init-ci manual missing")
	}
	if manual.Usage != "vigil github:init-ci [--write] [--json]" {
		t.Fatalf("unexpected usage: %s", manual.Usage)
	}
	if manual.Access != "conditional-write" {
		t.Fatalf("unexpected access: %s", manual.Access)
	}
}

func TestManualForCoreCommandUsesCanonicalAccess(t *testing.T) {
	manual, ok := manualForCommand("status")
	if !ok {
		t.Fatal("status manual missing")
	}
	if manual.Access != "read" {
		t.Fatalf("status access = %q, want read", manual.Access)
	}
	manual, ok = manualForCommand("config:migrate")
	if !ok {
		t.Fatal("config:migrate manual missing")
	}
	if manual.Access != "conditional-write" {
		t.Fatalf("config:migrate access = %q, want conditional-write", manual.Access)
	}
}

func TestGuardsSummaryUsesCanonicalAccess(t *testing.T) {
	commands := activeCommands()
	access := map[string]string{}
	for _, command := range commands {
		access[command.Command] = command.Access
	}
	for _, command := range []string{"github:init-ci", "init:ci", "config:migrate"} {
		if access[command] != "conditional-write" {
			t.Fatalf("%s access = %q, want conditional-write", command, access[command])
		}
	}
}

func TestActiveCommandsExposeWriteFlags(t *testing.T) {
	commands := activeCommands()
	for _, command := range commands {
		if command.Command != "github:init-ci" {
			continue
		}
		if len(command.WriteFlags) != 1 || command.WriteFlags[0] != "--write" {
			t.Fatalf("github:init-ci write flags = %#v, want --write", command.WriteFlags)
		}
		return
	}
	t.Fatal("github:init-ci missing")
}

func TestInvalidExtensionManifestCannotContributeActiveCommand(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	extDir := filepath.Join(temp, "extensions", "invalid")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "schema_version": "1",
  "id": "invalid",
  "name": "Invalid Extension",
  "kind": "custom",
  "status": "local",
  "private": false,
  "public_core": true,
  "description": "invalid extension",
  "source_root": "extensions/invalid",
  "packages": [],
  "commands": ["invalid:active-command"],
  "command_contracts": [
    {"command": "invalid:active-command", "access": "r/w", "usage": "vigil invalid:active-command", "description": "invalid"}
  ]
}`
	if err := os.WriteFile(filepath.Join(extDir, "extension.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	report := loadExtensions(extensionRoot())
	if report.Status != "fail" {
		t.Fatalf("extension report status = %q issues=%#v", report.Status, report.Issues)
	}
	if len(report.Extensions) != 0 {
		t.Fatalf("invalid extension loaded: %#v", report.Extensions)
	}
	if extensionCommandLoaded("invalid:active-command") {
		t.Fatal("invalid extension command should not be loadable")
	}
}

func TestGithubWorkflowRunsPolicyEngine(t *testing.T) {
	cfg := templateConfig("generic")
	out := githubWorkflow(cfg)
	if !strings.Contains(out, "vigil verify --json") || !strings.Contains(out, "vigil workflow:local --json") {
		t.Fatalf("workflow missing expected commands:\n%s", out)
	}
	for _, disallowed := range []string{"actions/checkout@v5", "actions/checkout@v4", "actions/setup-go@v6", "actions/setup-go@v5", "@latest", "go-version: 'stable'", "go-version: stable", "go build -o bin/vigil ./cmd/vigil", "git status --short"} {
		if strings.Contains(out, disallowed) {
			t.Fatalf("workflow contains unpinned value %q:\n%s", disallowed, out)
		}
	}
	for _, want := range []string{
		"actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09",
		"actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
		"cache: false",
		"go install github.com/PayCal-Technologies/vigil-core/cmd/vigil@",
		"go-version: '1.26.0'",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("workflow missing %q:\n%s", want, out)
		}
	}
}

func TestGithubWorkflowInstallRefUsesCurrentSourceCheckout(t *testing.T) {
	want := gitHeadRef(filepath.Clean(filepath.Join(mustGetwd(), "..", "..")))
	if want == "" {
		t.Skip("source checkout has no git HEAD")
	}
	got := vigilCoreInstallRef()
	if got != want {
		t.Fatalf("vigilCoreInstallRef = %q, want current source %s", got, want)
	}
	if got == "7ae14422483359eb6a9d0c25cb827e7de392012d" {
		t.Fatal("vigilCoreInstallRef still pins stale ref")
	}
}

func TestGithubWorkflowUsesVigilOwnedGoVersion(t *testing.T) {
	if got := goVersionForWorkflow(); got != vigilCoreGoVersion {
		t.Fatalf("goVersionForWorkflow = %q, want %q", got, vigilCoreGoVersion)
	}
}

func TestMutationConfirmationPolicy(t *testing.T) {
	if !requiresMutationConfirmation("readme:generate", nil) {
		t.Fatal("readme:generate should require mutation confirmation")
	}
	if requiresMutationConfirmation("readme:generate", []string{"--dry-run"}) {
		t.Fatal("readme:generate --dry-run should not require mutation confirmation")
	}
	if !(confirmationArgs{Auto: true}).Allowed("readme:generate") {
		t.Fatal("--auto should allow deterministic README generation")
	}
	if (confirmationArgs{Auto: true}).Allowed("config:repair") {
		t.Fatal("--auto should not allow broad mutating repair")
	}
	if !(confirmationArgs{AllowMutation: true}).Allowed("config:repair") {
		t.Fatal("--allow-mutation should allow explicit mutating repair")
	}
}

func TestMutationRequirementsFailClosed(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	cfg := templateConfig("generic")
	cfg.Coordination.MutationRequires = []string{"explicit-confirmation", "unknown-policy"}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultConfigName, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if mutationRequirementsSatisfied("", "readme:generate", confirmationArgs{Auto: true}) {
		t.Fatal("unknown mutation requirement should fail closed")
	}
}

func TestMutationRequirementsCleanTree(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	cfg := templateConfig("generic")
	cfg.Coordination.MutationRequires = []string{"explicit-confirmation", "clean-config", "clean-tree"}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultConfigName, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "add", defaultConfigName); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "config"); code != 0 {
		t.Fatalf("git commit failed: %s", out)
	}
	if !mutationRequirementsSatisfied("", "readme:generate", confirmationArgs{Auto: true}) {
		t.Fatal("clean tree should satisfy clean-tree")
	}
	if err := os.WriteFile("dirty.txt", []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mutationRequirementsSatisfied("", "readme:generate", confirmationArgs{Auto: true}) {
		t.Fatal("dirty tree should fail clean-tree")
	}
}

func TestExtensionCommandAccessRequiresMutationConfirmation(t *testing.T) {
	if !requiresMutationConfirmation("github:init-ci", []string{"--write"}) {
		t.Fatal("github:init-ci --write should require mutation confirmation from explicit command handling")
	}
	if requiresMutationConfirmation("github:init-ci", nil) {
		t.Fatal("github:init-ci without --write should be preview-safe")
	}
	if !requiresMutationConfirmation("readme:generate", nil) {
		t.Fatal("readme:generate should require mutation confirmation from extension contract")
	}
	if requiresMutationConfirmation("readme:check", nil) {
		t.Fatal("readme:check should remain read-only")
	}
}

func TestGenericConditionalExtensionRequiresMutationOnlyForWriteFlags(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	extDir := filepath.Join(temp, "extensions", "conditional")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "schema_version": "1",
  "id": "conditional",
  "name": "Conditional Extension",
  "kind": "custom",
  "status": "local",
  "private": false,
  "public_core": true,
  "description": "conditional extension",
  "source_root": "extensions/conditional",
  "packages": [],
  "commands": ["conditional:preview"],
  "command_contracts": [
    {"command": "conditional:preview", "access": "conditional-write", "write_flags": ["--write", "--execute"], "usage": "vigil conditional:preview [--write]", "description": "conditional"}
  ]
}`
	if err := os.WriteFile(filepath.Join(extDir, "extension.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if report := loadExtensions(extensionRoot()); report.Status != "ok" {
		t.Fatalf("extension report status = %q issues=%#v", report.Status, report.Issues)
	}
	if requiresMutationConfirmation("conditional:preview", nil) {
		t.Fatal("conditional extension preview should not require mutation confirmation")
	}
	if !requiresMutationConfirmation("conditional:preview", []string{"--write"}) {
		t.Fatal("conditional extension --write should require mutation confirmation")
	}
	if !requiresMutationConfirmation("conditional:preview", []string{"--execute=true"}) {
		t.Fatal("conditional extension --execute=true should require mutation confirmation")
	}
}

func TestConditionalExtensionRequiresWriteFlags(t *testing.T) {
	ext := extensionManifest{
		SchemaVersion: configSchemaVersion,
		ID:            "conditional",
		Name:          "Conditional Extension",
		Kind:          "custom",
		Status:        "local",
		Description:   "conditional extension",
		SourceRoot:    "extensions/conditional",
		Commands:      []string{"conditional:preview"},
		CommandContracts: []extensionCommandContract{{
			Command:     "conditional:preview",
			Access:      "conditional-write",
			Usage:       "vigil conditional:preview",
			Description: "conditional",
		}},
		Path: "extensions/conditional/extension.json",
	}
	issues := validateExtension(ext)
	found := false
	for _, issue := range issues {
		if strings.Contains(issue, "conditional-write missing write_flags") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing write_flags issue: %#v", issues)
	}
}

func TestActiveCommandsMarksReadmeGenerateAutoEnabled(t *testing.T) {
	for _, command := range activeCommands() {
		if command.Command == "readme:generate" {
			if !command.AutoEnabled || command.AutoReason == "" {
				t.Fatalf("readme:generate missing auto metadata: %#v", command)
			}
			return
		}
	}
	t.Fatal("readme:generate missing")
}

func TestHooksInstallRefusesExistingHookOverwrite(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	hookPath := filepath.Join(temp, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hookPath, []byte("#!/usr/bin/env sh\necho existing\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := hooksInstall(nil); code == 0 {
		t.Fatal("hooksInstall should refuse to overwrite an existing differing hook")
	}
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#!/usr/bin/env sh\necho existing\n" {
		t.Fatalf("existing hook was modified:\n%s", data)
	}
}

func TestHooksInstallDoesNotGrantBroadMutationConfirmation(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	if code := hooksInstall(nil); code != 0 {
		t.Fatalf("hooksInstall failed: %d", code)
	}
	prePush, err := os.ReadFile(filepath.Join(temp, ".git", "hooks", "pre-push"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prePush), "--allow-mutation") {
		t.Fatalf("installed hook should not grant broad mutation confirmation:\n%s", prePush)
	}
}

func TestHooksInstallPreflightsBeforeWriting(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	preCommitPath := filepath.Join(temp, ".git", "hooks", "pre-commit")
	prePushPath := filepath.Join(temp, ".git", "hooks", "pre-push")
	preCommitBody := "#!/usr/bin/env sh\nvigil hooks:pre-commit \"$@\"\n"
	if err := os.WriteFile(preCommitPath, []byte(preCommitBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prePushPath, []byte("#!/usr/bin/env sh\necho existing\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := hooksInstall(nil); code == 0 {
		t.Fatal("hooksInstall should refuse conflicting pre-push")
	}
	data, err := os.ReadFile(preCommitPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != preCommitBody {
		t.Fatalf("pre-commit changed despite pre-push conflict:\n%s", data)
	}
}

func TestHookRunUsesTagSubset(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{
		{Name: "commit gate", Command: "true", ReadOnly: true, Tags: []string{"pre-commit"}},
		{Name: "push gate", Command: "false", ReadOnly: true, Tags: []string{"pre-push"}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultConfigName, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "add", defaultConfigName); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "config"); code != 0 {
		t.Fatalf("git commit failed: %s", out)
	}
	if code := hookRun("", "pre-commit", nil); code != 0 {
		t.Fatalf("pre-commit should run only passing gate, got %d", code)
	}
	if code := hookRun("", "pre-push", nil); code == 0 {
		t.Fatal("pre-push should run failing push gate")
	}
}

func TestReadOnlyFingerprintDetectsDirtyFileContentChangeOnFailedGate(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	if err := os.WriteFile("tracked.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{{Name: "dirty failure", Command: "printf changed > tracked.txt; false", ReadOnly: true}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultConfigName, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "add", "tracked.txt", defaultConfigName); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "base"); code != 0 {
		t.Fatalf("git commit failed: %s", out)
	}
	if code := workflowLocal("", []string{"--json"}, false); code == 0 {
		t.Fatal("read-only failed gate that mutates should fail")
	}
}

func TestPublicAssumptionScanIsClean(t *testing.T) {
	findings, err := publicAssumptionFindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("public assumption findings: %#v", findings)
	}
}

func TestConfigTemplateValidates(t *testing.T) {
	for _, profile := range []string{"generic", "go-tool", "static-site"} {
		if err := validateStruct(templateConfig(profile)); err != nil {
			t.Fatalf("templateConfig(%s) failed validation: %v", profile, err)
		}
	}
}

func TestValidateConfigIssuesExplainsMissingFields(t *testing.T) {
	issues := validateConfigIssues(config{})
	if len(issues) == 0 {
		t.Fatal("expected missing config issues")
	}
	fields := map[string]bool{}
	for _, issue := range issues {
		fields[issue.Field] = true
		if issue.Code == "" || issue.Message == "" {
			t.Fatalf("issue missing code or message: %#v", issue)
		}
	}
	for _, want := range []string{"schema_version", "profile", "project", "coordination.mutation_requires", "gates"} {
		if !fields[want] {
			t.Fatalf("expected issue for %s, got %#v", want, issues)
		}
	}
}

func TestApplyConfigDefaultsRepairsMinimalConfig(t *testing.T) {
	cfg := applyConfigDefaults(config{}, "generic")
	if err := validateStruct(cfg); err != nil {
		t.Fatalf("default repair failed validation: %v", err)
	}
}

func TestApplyConfigDefaultsDropsInvalidPublicAssumptionPatterns(t *testing.T) {
	cfg := applyConfigDefaults(config{PublicAssumptionPatterns: []string{"(?i)sample-pattern", "["}}, "generic")
	if err := validateStruct(cfg); err != nil {
		t.Fatalf("default repair failed validation: %v", err)
	}
	if len(cfg.PublicAssumptionPatterns) != 1 || cfg.PublicAssumptionPatterns[0] != "(?i)sample-pattern" {
		t.Fatalf("unexpected repaired patterns: %#v", cfg.PublicAssumptionPatterns)
	}
}

func TestScribeReadmeRenderIsStable(t *testing.T) {
	input := "# App\n\nHuman intro.\n\n## Install\n"
	next, changed := renderScribeReadme(input)
	if !changed {
		t.Fatal("expected Scribe render to add managed block")
	}
	again, changed := renderScribeReadme(next)
	if changed {
		t.Fatalf("expected Scribe render to be stable, changed to:\n%s", again)
	}
	if next != again {
		t.Fatal("stable Scribe render changed content")
	}
}

func TestUniqueStringsPreservesOrder(t *testing.T) {
	got := uniqueStrings([]string{"a", "b", "a", "", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("uniqueStrings length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("uniqueStrings[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

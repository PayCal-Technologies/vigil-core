package main

import (
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
	if manual.Access != "r/w" {
		t.Fatalf("unexpected access: %s", manual.Access)
	}
}

func TestGithubWorkflowRunsPolicyEngine(t *testing.T) {
	cfg := templateConfig("generic")
	out := githubWorkflow(cfg)
	if !strings.Contains(out, "vigil verify --json") || !strings.Contains(out, "vigil workflow:local --json") {
		t.Fatalf("workflow missing expected commands:\n%s", out)
	}
	for _, disallowed := range []string{"actions/checkout@v4", "actions/setup-go@v5", "@latest", "go-version: 'stable'", "go-version: stable", "go build -o bin/vigil ./cmd/vigil", "git status --short"} {
		if strings.Contains(out, disallowed) {
			t.Fatalf("workflow contains unpinned value %q:\n%s", disallowed, out)
		}
	}
	for _, want := range []string{
		"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff",
		"go install github.com/PayCal-Technologies/vigil-core/cmd/vigil@f48f19c3764a5a256455cc1d858d74eda2775745",
		"go-version: '1.26.0'",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("workflow missing %q:\n%s", want, out)
		}
	}
}

func TestMutationAuthorityPolicy(t *testing.T) {
	if !requiresMutationAuthority("readme:generate", nil) {
		t.Fatal("readme:generate should require mutation authority")
	}
	if requiresMutationAuthority("readme:generate", []string{"--dry-run"}) {
		t.Fatal("readme:generate --dry-run should not require mutation authority")
	}
	if !(authorityArgs{Auto: true}).Allowed("readme:generate") {
		t.Fatal("--auto should allow deterministic README generation")
	}
	if (authorityArgs{Auto: true}).Allowed("config:repair") {
		t.Fatal("--auto should not allow broad mutating repair")
	}
	if !(authorityArgs{AllowMutation: true}).Allowed("config:repair") {
		t.Fatal("--allow-mutation should allow explicit mutating repair")
	}
}

func TestExtensionCommandAccessRequiresMutationAuthority(t *testing.T) {
	if !requiresMutationAuthority("github:init-ci", []string{"--write"}) {
		t.Fatal("github:init-ci --write should require mutation authority from explicit command handling")
	}
	if !requiresMutationAuthority("readme:generate", nil) {
		t.Fatal("readme:generate should require mutation authority from extension contract")
	}
	if requiresMutationAuthority("readme:check", nil) {
		t.Fatal("readme:check should remain read-only")
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

func TestHooksInstallDoesNotGrantBroadMutationAuthority(t *testing.T) {
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
		t.Fatalf("installed hook should not grant broad mutation authority:\n%s", prePush)
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
	for _, want := range []string{"schema_version", "profile", "project", "authority.mutation_requires", "gates"} {
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

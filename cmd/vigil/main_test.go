package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	vigilconfig "github.com/PayCal-Technologies/vigil-public/internal/config"
	vigilplan "github.com/PayCal-Technologies/vigil-public/internal/plan"
	vigilplugins "github.com/PayCal-Technologies/vigil-public/internal/plugins"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
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
		"setup",
		"setup:wizard",
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

func TestCommandRegistryCacheReusesEquivalentInputsAndInvalidatesContractChanges(t *testing.T) {
	commandRegistries.Clear()
	root := t.TempDir()
	t.Setenv("VIGIL_PLUGIN_ROOT", filepath.Join(root, "plugins"))
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(root, "packs"))
	configPath := filepath.Join(root, defaultConfigName)
	cfg := templateConfig("generic")
	writeJSONFile(t, configPath, cfg)

	first, err := newCommandRegistryForConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCommandRegistryForConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || commandRegistries.Len() != 1 {
		t.Fatalf("equivalent registry was not reused: first=%p second=%p size=%d", first, second, commandRegistries.Len())
	}

	cfg.Extensions.EnabledIDs = []string{"scribe"}
	writeJSONFile(t, configPath, cfg)
	third, err := newCommandRegistryForConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("changed pack contract reused stale registry")
	}
	if commandRegistries.Len() != 2 {
		t.Fatalf("cache size after invalidation = %d", commandRegistries.Len())
	}
}

func TestCommandSourcesUseNamedExitTaxonomy(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fileset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			statement, ok := node.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, result := range statement.Results {
				literal, ok := result.(*ast.BasicLit)
				if ok && literal.Kind == token.INT {
					t.Errorf("%s: raw numeric return %s bypasses the named exit taxonomy", fileset.Position(literal.Pos()), literal.Value)
				}
			}
			return true
		})
	}
}

func TestPreferExitCodeKeepsMostSpecificClass(t *testing.T) {
	exitCode := preferExitCode(exitSuccess, exitCheckFailed)
	exitCode = preferExitCode(exitCode, exitDependencyMissing)
	exitCode = preferExitCode(exitCode, exitUsage)
	if exitCode != exitDependencyMissing {
		t.Fatalf("exit = %d, want dependency missing", exitCode)
	}
	if got := preferExitCode(exitCheckFailed, 99); got != exitInternal {
		t.Fatalf("unknown exit merged as %d, want internal", got)
	}
}

func TestStructuredStatusPreservesEveryExitClass(t *testing.T) {
	for _, want := range []int{
		exitCheckFailed,
		exitUsage,
		exitPolicyBlocked,
		exitDependencyMissing,
		exitInterrupted,
		exitMutationViolation,
		exitInternal,
	} {
		t.Run(fmt.Sprintf("exit-%d", want), func(t *testing.T) {
			var got int
			output := captureStdout(t, func() {
				got = printStatusJSON(map[string]any{"status": "fail", "reason": "fixture"}, want)
			})
			if got != want {
				t.Fatalf("return = %d, want %d", got, want)
			}
			var envelope struct {
				ExitCode int `json:"exit_code"`
			}
			if err := json.Unmarshal([]byte(output), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.ExitCode != want {
				t.Fatalf("envelope exit = %d, want %d", envelope.ExitCode, want)
			}
		})
	}
}

func TestCheckAggregationUsesExitClassNotFailureCount(t *testing.T) {
	status, exitCode := summarizeChecks([]checkResult{
		{Name: "first", Status: "fail", ExitCode: exitCheckFailed},
		{Name: "second", Status: "fail", ExitCode: exitDependencyMissing},
		{Name: "third", Status: "fail", ExitCode: exitUsage},
	})
	if status != "fail" || exitCode != exitDependencyMissing {
		t.Fatalf("status=%q exit=%d", status, exitCode)
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
	if len(report.Extensions) == 0 {
		t.Fatal("embedded official packs should remain available")
	}
	for _, extension := range report.Extensions {
		if extension.ID == "invalid" {
			t.Fatalf("invalid extension loaded: %#v", extension)
		}
	}
	if extensionCommandLoaded("invalid:active-command") {
		t.Fatal("invalid extension command should not be loadable")
	}
}

func TestExtensionPolicyEnforcesAllowedKindsAndPrivacy(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*config)
		issue  string
	}{
		{
			name: "allowed kinds",
			mutate: func(cfg *config) {
				cfg.Extensions.AllowedKinds = []string{"restricted"}
			},
			issue: "allowed_kinds",
		},
		{
			name: "require private",
			mutate: func(cfg *config) {
				cfg.Extensions.RequirePrivate = true
			},
			issue: "require_private",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			temp := t.TempDir()
			cfg := templateConfig("generic")
			test.mutate(&cfg)
			configPath := filepath.Join(temp, defaultConfigName)
			writeJSONFile(t, configPath, cfg)
			t.Setenv("VIGIL_CONFIG", configPath)
			t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))

			report := loadExtensions(extensionRoot())
			if report.Status != "fail" || report.Count != 0 {
				t.Fatalf("status=%q count=%d issues=%v", report.Status, report.Count, report.Issues)
			}
			if !strings.Contains(strings.Join(report.Issues, "\n"), test.issue) {
				t.Fatalf("issues do not mention %s: %v", test.issue, report.Issues)
			}
		})
	}
}

func TestExtensionPolicyHonorsDisabledIDs(t *testing.T) {
	temp := t.TempDir()
	cfg := templateConfig("generic")
	cfg.Extensions.DisabledIDs = []string{"scribe"}
	configPath := filepath.Join(temp, defaultConfigName)
	writeJSONFile(t, configPath, cfg)
	t.Setenv("VIGIL_CONFIG", configPath)
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))

	report := loadExtensions(extensionRoot())
	if report.Status != "ok" || report.Count != 9 {
		t.Fatalf("status=%q count=%d issues=%v", report.Status, report.Count, report.Issues)
	}
	for _, pack := range report.Extensions {
		if pack.ID == "scribe" {
			t.Fatal("disabled pack was loaded")
		}
	}
}

func TestExtensionPrecedenceRepositoryOverridesUserAndEmbedded(t *testing.T) {
	temp := t.TempDir()
	userRoot := filepath.Join(temp, "user-packs")
	repositoryRoot := filepath.Join(temp, "repository")
	configPath := filepath.Join(repositoryRoot, defaultConfigName)
	cfg := templateConfig("generic")
	writeJSONFile(t, configPath, cfg)
	writeTestPack(t, userRoot, "user")
	writeTestPack(t, filepath.Join(repositoryRoot, "extensions"), "repository")
	t.Setenv("VIGIL_CONFIG", configPath)
	t.Setenv("VIGIL_USER_PACK_ROOT", userRoot)

	report := loadExtensions(extensionRoot())
	if report.Status != "ok" {
		t.Fatalf("issues=%v", report.Issues)
	}
	var found extensionManifest
	for _, pack := range report.Extensions {
		if pack.ID == "github-cicd" {
			found = pack
		}
	}
	if found.Origin != "repository" || found.Description != "repository override" {
		t.Fatalf("selected pack origin=%q description=%q", found.Origin, found.Description)
	}
	if len(report.Overrides) != 2 {
		t.Fatalf("overrides=%v, want embedded->user and user->repository", report.Overrides)
	}
}

func TestPackRegistryUsesExplicitContractMetadata(t *testing.T) {
	temp := t.TempDir()
	repositoryRoot := filepath.Join(temp, "repository")
	configPath := filepath.Join(repositoryRoot, defaultConfigName)
	cfg := templateConfig("generic")
	writeJSONFile(t, configPath, cfg)
	packRoot := filepath.Join(repositoryRoot, "extensions")
	writeTestPack(t, packRoot, "explicit")

	manifestPath := filepath.Join(packRoot, "github-cicd", "extension.json")
	var manifest extensionManifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	contract := &manifest.CommandContracts[0]
	contract.Capabilities = []string{"filesystem:read", "filesystem:write", "environment"}
	contract.Timeout = "37s"
	contract.Stability = "experimental"
	contract.RequiredTools = []string{"git"}
	contract.OutputFormats = []string{"json"}
	writeJSONFile(t, manifestPath, manifest)

	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "missing-user-packs"))
	registry, err := newCommandRegistryForConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	command, ok := registry.Resolve("github:init-ci")
	if !ok {
		t.Fatal("github:init-ci missing")
	}
	if got := fmt.Sprint(command.Capabilities); got != "[filesystem:read filesystem:write environment]" {
		t.Fatalf("capabilities = %s", got)
	}
	if command.Binding != contract.Binding || command.Timeout != 37*time.Second ||
		string(command.Stability) != contract.Stability || command.Network != contract.Network {
		t.Fatalf("registry metadata = %#v, contract = %#v", command, contract)
	}
	if strings.Join(command.RequiredTools, ",") != "git" || strings.Join(command.OutputFormats, ",") != "json" {
		t.Fatalf("tools=%v formats=%v", command.RequiredTools, command.OutputFormats)
	}
}

func TestExtensionRootRejectsTraversalAndSymlinkEscape(t *testing.T) {
	t.Run("traversal", func(t *testing.T) {
		temp := t.TempDir()
		repositoryRoot := filepath.Join(temp, "repository")
		cfg := templateConfig("generic")
		cfg.Extensions.ManifestRoot = "../outside"
		configPath := filepath.Join(repositoryRoot, defaultConfigName)
		writeJSONFile(t, configPath, cfg)
		t.Setenv("VIGIL_CONFIG", configPath)
		t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))

		report := loadExtensions(extensionRoot())
		if report.Status != "fail" || !strings.Contains(strings.Join(report.Issues, "\n"), "escapes repository boundary") {
			t.Fatalf("status=%q issues=%v", report.Status, report.Issues)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		temp := t.TempDir()
		repositoryRoot := filepath.Join(temp, "repository")
		outsideRoot := filepath.Join(temp, "outside")
		if err := os.MkdirAll(repositoryRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideRoot, filepath.Join(repositoryRoot, "extensions")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		cfg := templateConfig("generic")
		configPath := filepath.Join(repositoryRoot, defaultConfigName)
		writeJSONFile(t, configPath, cfg)
		t.Setenv("VIGIL_CONFIG", configPath)
		t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))

		report := loadExtensions(extensionRoot())
		if report.Status != "fail" || !strings.Contains(strings.Join(report.Issues, "\n"), "symlink escapes repository boundary") {
			t.Fatalf("status=%q issues=%v", report.Status, report.Issues)
		}
	})
}

func TestSupportBundleIsMinimalDeterministicAndPrivate(t *testing.T) {
	temp := t.TempDir()
	cfg := templateConfig("generic")
	cfg.Metadata = map[string]string{"local_note": "sensitive-marker"}
	configPath := filepath.Join(temp, defaultConfigName)
	writeJSONFile(t, configPath, cfg)
	t.Setenv("VIGIL_CONFIG", configPath)
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))

	preview := func(args ...string) map[string]any {
		t.Helper()
		var exitCode int
		output := captureStdout(t, func() {
			exitCode = supportBundle("", append([]string{"--dry-run"}, args...))
		})
		if exitCode != 0 {
			t.Fatalf("preview exit=%d output=%s", exitCode, output)
		}
		var bundle map[string]any
		decodeEnvelopeData(t, []byte(output), &bundle)
		return bundle
	}

	first := preview()
	second := preview()
	if first["bundle_id"] != second["bundle_id"] {
		t.Fatalf("bundle IDs are not deterministic: %v != %v", first["bundle_id"], second["bundle_id"])
	}
	if _, exists := first["config"]; exists {
		t.Fatal("full config was collected without --include-config")
	}
	if _, exists := first["git_status"]; exists {
		t.Fatal("Git status was collected without --include-git-status")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), temp) || strings.Contains(string(encoded), "sensitive-marker") {
		t.Fatalf("preview leaked a path or config metadata: %s", encoded)
	}
	redaction, ok := first["redaction_report"].(map[string]any)
	if !ok || redaction["environment"] != "excluded" || redaction["uploads"] != "disabled" {
		t.Fatalf("redaction report=%#v", first["redaction_report"])
	}

	included := preview("--include-config", "--include-git-status")
	if _, exists := included["config"]; !exists {
		t.Fatal("--include-config did not include config")
	}
	if _, exists := included["git_status"]; !exists {
		t.Fatal("--include-git-status did not include Git status")
	}

	outputPath := filepath.Join(temp, "support", "bundle.json")
	if exitCode := supportBundle("", []string{"--output", outputPath}); exitCode != 0 {
		t.Fatalf("write exit=%d", exitCode)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("permissions=%#o, want 0600", permissions)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), temp) || strings.Contains(string(written), "sensitive-marker") {
		t.Fatalf("written bundle leaked a path or config metadata: %s", written)
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
		"go install github.com/PayCal-Technologies/vigil-public/cmd/vigil@",
		"go-version: '1.26.5'",
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
  "host_api_version": "v1",
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
    {
      "command": "conditional:preview",
      "access": "conditional-write",
      "capabilities": ["filesystem:read", "filesystem:write"],
      "binding": "builtin:conditional:preview",
      "timeout": "10m",
      "stability": "stable",
      "required_tools": [],
      "network": "none",
      "output_formats": ["text"],
      "write_flags": ["--write", "--execute"],
      "usage": "vigil conditional:preview [--write]",
      "description": "conditional"
    }
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

func TestHooksInstallHonorsCoreHooksPath(t *testing.T) {
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
	if out, code := runCommand("git", "config", "core.hooksPath", ".githooks"); code != 0 {
		t.Fatalf("git config failed: %s", out)
	}
	if code := hooksInstall(nil); code != 0 {
		t.Fatalf("hooksInstall failed: %d", code)
	}
	for _, hook := range []string{"pre-commit", "pre-push"} {
		data, err := os.ReadFile(filepath.Join(temp, ".githooks", hook))
		if err != nil {
			t.Fatal(err)
		}
		if !isVigilManagedHook(data) {
			t.Fatalf("%s is not Vigil-managed:\n%s", hook, data)
		}
		if fileExists(filepath.Join(temp, ".git", "hooks", hook)) {
			t.Fatalf("%s was incorrectly installed in .git/hooks", hook)
		}
	}
}

func TestHooksChainAndUninstallRestoreExistingHook(t *testing.T) {
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
	original := []byte("#!/usr/bin/env sh\necho existing\n")
	if err := os.WriteFile(hookPath, original, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := hooksInstall([]string{"--chain"}); code != 0 {
		t.Fatalf("chain install failed: %d", code)
	}
	managed, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if !isVigilManagedHook(managed) || !strings.Contains(string(managed), ".vigil-original") {
		t.Fatalf("managed chain missing backup invocation:\n%s", managed)
	}
	backupPath := hookPath + ".vigil-original"
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(original) {
		t.Fatalf("backup changed:\n%s", backup)
	}
	inspections := inspectVigilHooks(filepath.Dir(hookPath))
	if inspections[0].State != "managed" || inspections[0].BackupPath != backupPath {
		t.Fatalf("inspection=%#v", inspections[0])
	}

	if code := hooksUninstall(nil); code != 0 {
		t.Fatalf("uninstall failed: %d", code)
	}
	restored, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(original) {
		t.Fatalf("restored hook changed:\n%s", restored)
	}
	if fileExists(backupPath) {
		t.Fatal("chain backup remains after successful restoration")
	}
	if fileExists(filepath.Join(temp, ".git", "hooks", "pre-push")) {
		t.Fatal("Vigil-only pre-push hook remains after uninstall")
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
	cfg.Gates = []gateConfig{{Name: "dirty failure", Command: "printf changed > tracked.txt; false", Shell: true, ReadOnly: true}}
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

func TestBoundedHelperCommandFailsClosedOnTruncatedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	output, exitCode := runCommandWithCaptureLimit("sh", 4, "-c", "printf 123456789")
	if exitCode != exitInternal {
		t.Fatalf("exit = %d, output = %q", exitCode, output)
	}
	if !strings.Contains(output, "[truncated]") {
		t.Fatalf("truncation marker missing from %q", output)
	}
}

func TestWorkflowLocalTimeoutAndCancellationStates(t *testing.T) {
	for _, test := range []struct {
		name        string
		gateTimeout string
		cancel      bool
		wantState   string
	}{
		{name: "timeout", gateTimeout: "50ms", wantState: string(runner.StateTimedOut)},
		{name: "cancel", gateTimeout: "1m", cancel: true, wantState: string(runner.StateCancelled)},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			cfg.Gates = []gateConfig{{
				Name:     test.name,
				Command:  "sleep",
				Args:     []string{"30"},
				ReadOnly: true,
				Timeout:  test.gateTimeout,
			}}
			writeJSONFile(t, defaultConfigName, cfg)
			if out, code := runCommand("git", "add", defaultConfigName); code != 0 {
				t.Fatalf("git add failed: %s", out)
			}
			if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "config"); code != 0 {
				t.Fatalf("git commit failed: %s", out)
			}

			ctx := context.Background()
			stop := func() {}
			if test.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				timer := time.AfterFunc(50*time.Millisecond, cancel)
				stop = func() {
					timer.Stop()
					cancel()
				}
			}
			defer stop()
			var exitCode int
			started := time.Now()
			output := captureStdout(t, func() {
				exitCode = workflowLocalContext(ctx, "", []string{"--json"}, false)
			})
			if exitCode != 5 {
				t.Fatalf("exit=%d output=%s", exitCode, output)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("gate stopped after %s", elapsed)
			}
			var payload struct {
				Results []gateResult `json:"results"`
			}
			decodeEnvelopeData(t, []byte(output), &payload)
			if len(payload.Results) != 1 || payload.Results[0].State != test.wantState {
				t.Fatalf("results=%#v", payload.Results)
			}
		})
	}
}

func TestWorkflowGraphRunsExplicitParallelGroupAndPreservesResultOrder(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{
		{
			Name:          "parallel-a",
			Command:       "mkdir -p .vigil/parallel; touch .vigil/parallel/a; for i in {1..40}; do test -f .vigil/parallel/b && exit 0; sleep 0.025; done; exit 1",
			Shell:         true,
			ReadOnly:      true,
			ParallelGroup: "analysis",
		},
		{
			Name:          "parallel-b",
			Command:       "mkdir -p .vigil/parallel; touch .vigil/parallel/b; for i in {1..40}; do test -f .vigil/parallel/a && exit 0; sleep 0.025; done; exit 1",
			Shell:         true,
			ReadOnly:      true,
			ParallelGroup: "analysis",
		},
		{
			Name:      "dependent",
			Command:   "test",
			Args:      []string{"-f", ".vigil/parallel/a"},
			ReadOnly:  true,
			DependsOn: []string{"parallel-a", "parallel-b"},
		},
	}
	setupWorkflowGraphRepository(t, cfg)

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = workflowLocalContext(context.Background(), "", []string{"--json", "--jobs=2"}, false)
	})
	if exitCode != exitSuccess {
		t.Fatalf("workflow exit = %d, output = %s", exitCode, output)
	}
	var payload struct {
		Results []gateResult `json:"results"`
	}
	decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Results) != 3 {
		t.Fatalf("results = %#v", payload.Results)
	}
	for index, want := range []string{"parallel-a", "parallel-b", "dependent"} {
		if payload.Results[index].Name != want || payload.Results[index].State != string(runner.StateOK) {
			t.Fatalf("results[%d] = %#v", index, payload.Results[index])
		}
	}
}

func TestWorkflowGraphContinuesIndependentWorkAndSkipsFailedDependencies(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{
		{Name: "failure", Command: "false", ReadOnly: true, ContinueOnError: true},
		{Name: "dependent", Command: "true", ReadOnly: true, DependsOn: []string{"failure"}},
		{Name: "independent", Command: "true", ReadOnly: true},
	}
	setupWorkflowGraphRepository(t, cfg)

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = workflowLocalContext(context.Background(), "", []string{"--json"}, false)
	})
	if exitCode != exitCheckFailed {
		t.Fatalf("workflow exit = %d, output = %s", exitCode, output)
	}
	var payload struct {
		Results []gateResult `json:"results"`
	}
	decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Results) != 3 ||
		payload.Results[0].State != string(runner.StateFailed) ||
		payload.Results[1].State != string(runner.StateSkipped) ||
		payload.Results[2].State != string(runner.StateOK) {
		t.Fatalf("results = %#v", payload.Results)
	}
}

func TestWorkflowGraphOptionalToolRetryAndDependencySemantics(t *testing.T) {
	optional := false
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{
		{
			Name:     "optional-tool",
			Command:  "vigil-test-tool-that-does-not-exist",
			ReadOnly: true,
			Required: &optional,
		},
		{
			Name:      "retry-network-check",
			Command:   "if test ! -f .vigil/retry-marker; then mkdir -p .vigil; touch .vigil/retry-marker; exit 1; fi",
			Shell:     true,
			ReadOnly:  true,
			Tags:      []string{"network"},
			DependsOn: []string{"optional-tool"},
			Retry:     &vigilconfig.GateRetry{MaxAttempts: 2, Delay: "0s", On: []string{"failed"}},
		},
		{
			Name:      "after-retry",
			Command:   "true",
			ReadOnly:  true,
			DependsOn: []string{"retry-network-check"},
		},
	}
	setupWorkflowGraphRepository(t, cfg)

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = workflowLocalContext(context.Background(), "", []string{"--json"}, false)
	})
	if exitCode != exitSuccess {
		t.Fatalf("workflow exit = %d, output = %s", exitCode, output)
	}
	var payload struct {
		Results []gateResult `json:"results"`
	}
	decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Results) != 3 ||
		payload.Results[0].State != string(runner.StateSkipped) ||
		payload.Results[1].State != string(runner.StateOK) ||
		payload.Results[1].Attempts != 2 ||
		payload.Results[2].State != string(runner.StateOK) {
		t.Fatalf("results = %#v", payload.Results)
	}
}

func TestWorkflowGraphAppliesCWDEnvironmentAndVerifiesArtifacts(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{
		{
			Name:        "frontend-report",
			Command:     "mkdir -p reports; printf '%s' \"$FIXTURE_VALUE\" > reports/result.txt",
			Shell:       true,
			ReadOnly:    true,
			CWD:         "frontend",
			Environment: map[string]string{"FIXTURE_VALUE": "controlled-value"},
			Artifacts: []vigilconfig.GateArtifact{{
				Path:      "reports/result.txt",
				Kind:      "fixture-report",
				MediaType: "text/plain",
			}},
		},
	}
	root := setupWorkflowGraphRepository(t, cfg)
	if err := os.MkdirAll(filepath.Join(root, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", ".keep"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "add", "frontend/.keep"); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "frontend"); code != 0 {
		t.Fatalf("git commit failed: %s", out)
	}

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = workflowLocalContext(context.Background(), "", []string{"--json"}, false)
	})
	if exitCode != exitSuccess {
		t.Fatalf("workflow exit = %d, output = %s", exitCode, output)
	}
	var payload struct {
		Results []gateResult `json:"results"`
	}
	envelope := decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Results) != 1 || len(payload.Results[0].Artifacts) != 1 {
		t.Fatalf("results = %#v", payload.Results)
	}
	artifact := payload.Results[0].Artifacts[0]
	if artifact.Kind != "fixture-report" || artifact.Digest == "" || len(envelope.Artifacts) != 1 {
		t.Fatalf("artifact = %#v envelope artifacts = %#v", artifact, envelope.Artifacts)
	}
	data, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "controlled-value" {
		t.Fatalf("artifact data = %q", data)
	}
}

func TestWorkflowGraphRejectsMissingRequiredArtifact(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{{
		Name:     "missing-report",
		Command:  "true",
		ReadOnly: true,
		Artifacts: []vigilconfig.GateArtifact{{
			Path: ".vigil/missing-report.xml",
			Kind: "junit",
		}},
	}}
	setupWorkflowGraphRepository(t, cfg)

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = workflowLocalContext(context.Background(), "", []string{"--json"}, false)
	})
	if exitCode != exitCheckFailed {
		t.Fatalf("workflow exit = %d, output = %s", exitCode, output)
	}
	var payload struct {
		Results []gateResult `json:"results"`
	}
	decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Results) != 1 ||
		payload.Results[0].State != string(runner.StateFailed) ||
		!strings.Contains(payload.Results[0].Output, "declared artifact") {
		t.Fatalf("results = %#v", payload.Results)
	}
}

func TestParallelReadOnlyMutationMarksEntireBatchAsViolated(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{
		{
			Name:          "mutates",
			Command:       "printf changed > tracked.txt",
			Shell:         true,
			ReadOnly:      true,
			ParallelGroup: "analysis",
		},
		{
			Name:          "peer",
			Command:       "true",
			ReadOnly:      true,
			ParallelGroup: "analysis",
		},
	}
	root := setupWorkflowGraphRepository(t, cfg)
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "add", "tracked.txt"); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "tracked fixture"); code != 0 {
		t.Fatalf("git commit failed: %s", out)
	}

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = workflowLocalContext(context.Background(), "", []string{"--json", "--jobs=2"}, false)
	})
	if exitCode != exitMutationViolation {
		t.Fatalf("workflow exit = %d, output = %s", exitCode, output)
	}
	var payload struct {
		Results []gateResult `json:"results"`
	}
	decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Results) != 2 {
		t.Fatalf("results = %#v", payload.Results)
	}
	for _, result := range payload.Results {
		if result.State != string(runner.StateMutationDetected) {
			t.Fatalf("result = %#v", result)
		}
		if !strings.Contains(result.Output, `parallel group "analysis"`) ||
			!strings.Contains(result.Output, "individual attribution is unavailable") {
			t.Fatalf("mutation diagnostic = %q", result.Output)
		}
	}
}

func TestWorkflowArtifactsWriteSeparatePrivateLogsAndEvidence(t *testing.T) {
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
	if err := os.WriteFile(".gitignore", []byte(".vigil/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{{
		Name:     "separate streams",
		Command:  "printf stdout; printf stderr >&2",
		Shell:    true,
		ReadOnly: true,
	}}
	writeJSONFile(t, defaultConfigName, cfg)

	var payload struct {
		ArtifactDir string       `json:"artifact_dir"`
		Results     []gateResult `json:"results"`
	}
	output := captureStdout(t, func() {
		if code := workflowLocalContext(context.Background(), "", []string{"--json", "--artifacts"}, true); code != 0 {
			t.Fatalf("workflow exit = %d", code)
		}
	})
	decodeEnvelopeData(t, []byte(output), &payload)
	if payload.ArtifactDir == "" || len(payload.Results) != 1 {
		t.Fatalf("payload = %#v", payload)
	}
	result := payload.Results[0]
	stdout, err := os.ReadFile(result.StdoutLog)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(result.StderrLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "stdout" || string(stderr) != "stderr" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	for _, path := range []string{
		result.StdoutLog,
		result.StderrLog,
		filepath.Join(payload.ArtifactDir, "manifest.json"),
		filepath.Join(payload.ArtifactDir, "plan.json"),
		filepath.Join(payload.ArtifactDir, "result.json"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %#o", path, info.Mode().Perm())
		}
	}
}

func TestWorkflowMarksStructuredOutputTruncation(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{{
		Name:     "large output",
		Command:  `i=0; while [ "$i" -lt 9000 ]; do printf x; i=$((i + 1)); done`,
		Shell:    true,
		ReadOnly: true,
	}}
	setupWorkflowGraphRepository(t, cfg)

	var payload struct {
		Results []gateResult `json:"results"`
	}
	output := captureStdout(t, func() {
		if code := workflowLocalContext(context.Background(), "", []string{"--json"}, false); code != exitSuccess {
			t.Fatalf("workflow exit = %d", code)
		}
	})
	decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Results) != 1 {
		t.Fatalf("results = %#v", payload.Results)
	}
	result := payload.Results[0]
	if !result.OutputTruncated || len(result.Warnings) == 0 || !strings.HasSuffix(result.Output, "[truncated]") {
		t.Fatalf("truncated result = %#v", result)
	}
}

func TestWorkflowArtifactsRejectUnignoredRepositoryPath(t *testing.T) {
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
	writeJSONFile(t, defaultConfigName, templateConfig("generic"))
	if code := workflowLocalContext(context.Background(), "", []string{"--json", "--artifacts-dir=visible-runs"}, true); code != 3 {
		t.Fatalf("workflow exit = %d, want policy block", code)
	}
	if _, err := os.Stat("visible-runs"); !os.IsNotExist(err) {
		t.Fatalf("unignored artifact path was written: %v", err)
	}
}

func TestReviewedPlanRoundTripAppliesExactGates(t *testing.T) {
	planPath := setupReviewedPlanFixture(t)
	var planPayload struct {
		Plan       vigilplan.Document `json:"plan"`
		OutputPath string             `json:"output_path"`
	}
	planOutput := captureStdout(t, func() {
		if code := plan("", []string{"--json", "--output", planPath}, true); code != 0 {
			t.Fatalf("plan exit = %d", code)
		}
	})
	planEnvelope := decodeEnvelopeData(t, []byte(planOutput), &planPayload)
	if planEnvelope.Command != "plan" || planPayload.Plan.PlanID == "" || planPayload.OutputPath == "" {
		t.Fatalf("plan envelope=%#v payload=%#v", planEnvelope, planPayload)
	}
	info, err := os.Stat(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("plan mode = %#o", info.Mode().Perm())
	}

	var applyPayload struct {
		PlanID  string       `json:"plan_id"`
		Results []gateResult `json:"results"`
	}
	applyOutput := captureStdout(t, func() {
		if code := applyPlanCommand(context.Background(), []string{"--json", planPath}, true); code != 0 {
			t.Fatalf("apply exit = %d", code)
		}
	})
	applyEnvelope := decodeEnvelopeData(t, []byte(applyOutput), &applyPayload)
	if applyEnvelope.Command != "apply" || applyPayload.PlanID != planPayload.Plan.PlanID {
		t.Fatalf("apply envelope=%#v payload=%#v", applyEnvelope, applyPayload)
	}
	if len(applyPayload.Results) != 1 || applyPayload.Results[0].State != string(runner.StateOK) {
		t.Fatalf("apply results = %#v", applyPayload.Results)
	}
}

func TestReviewedPlanRejectsConfigAndRepositoryDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T)
		field  string
	}{
		{
			name: "config",
			mutate: func(t *testing.T) {
				cfg := templateConfig("generic")
				cfg.Project = "changed-after-review"
				cfg.Gates = []gateConfig{{Name: "status", Command: "git", Args: []string{"status", "--short"}, ReadOnly: true}}
				writeJSONFile(t, defaultConfigName, cfg)
			},
			field: "config_digest",
		},
		{
			name: "workspace",
			mutate: func(t *testing.T) {
				if err := os.WriteFile("unreviewed.txt", []byte("changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			field: "workspace_digest",
		},
		{
			name: "head",
			mutate: func(t *testing.T) {
				if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "new head"); code != 0 {
					t.Fatalf("git commit failed: %s", out)
				}
			},
			field: "repository_head",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			planPath := setupReviewedPlanFixture(t)
			captureStdout(t, func() {
				if code := plan("", []string{"--json", "--output", planPath}, true); code != 0 {
					t.Fatalf("plan exit = %d", code)
				}
			})
			test.mutate(t)
			var payload struct {
				Mismatches []vigilplan.Mismatch `json:"mismatches"`
			}
			output := captureStdout(t, func() {
				if code := applyPlanCommand(context.Background(), []string{"--json", planPath}, true); code != 3 {
					t.Fatalf("apply exit = %d, want policy block", code)
				}
			})
			envelope := decodeEnvelopeData(t, []byte(output), &payload)
			if envelope.ExitCode != 3 || !containsPlanMismatch(payload.Mismatches, test.field) {
				t.Fatalf("envelope=%#v mismatches=%#v", envelope, payload.Mismatches)
			}
		})
	}
}

func TestReviewedPlanRejectsPackRegistryAndBinaryDrift(t *testing.T) {
	for _, field := range []string{"pack_digest", "command_registry_digest", "binary_digest"} {
		t.Run(field, func(t *testing.T) {
			planPath := setupReviewedPlanFixture(t)
			captureStdout(t, func() {
				if code := plan("", []string{"--json", "--output", planPath}, true); code != 0 {
					t.Fatalf("plan exit = %d", code)
				}
			})
			document, err := vigilplan.Read(planPath)
			if err != nil {
				t.Fatal(err)
			}
			staleDigest := vigilplan.DigestBytes([]byte("stale-" + field))
			switch field {
			case "pack_digest":
				document.Inputs.PackDigest = staleDigest
			case "command_registry_digest":
				document.Inputs.CommandRegistryDigest = staleDigest
			case "binary_digest":
				document.Inputs.BinaryDigest = staleDigest
			}
			document, err = vigilplan.New(document.Command, mustParseTime(t, document.CreatedAt), document.Inputs, document.Options, document.Gates)
			if err != nil {
				t.Fatal(err)
			}
			if err := vigilplan.Write(planPath, document); err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Mismatches []vigilplan.Mismatch `json:"mismatches"`
			}
			output := captureStdout(t, func() {
				if code := applyPlanCommand(context.Background(), []string{"--json", planPath}, true); code != 3 {
					t.Fatalf("apply exit = %d, want policy block", code)
				}
			})
			decodeEnvelopeData(t, []byte(output), &payload)
			if !containsPlanMismatch(payload.Mismatches, field) {
				t.Fatalf("mismatches = %#v", payload.Mismatches)
			}
		})
	}
}

func TestReviewedPlanRejectsRealPackChangeOutsideWorkspaceDigest(t *testing.T) {
	planPath := setupReviewedPlanFixture(t)
	writeTestPack(t, "extensions", "before review")
	captureStdout(t, func() {
		if code := plan("", []string{"--json", "--output", planPath}, true); code != 0 {
			t.Fatalf("plan exit = %d", code)
		}
	})
	writeTestPack(t, "extensions", "after review")

	var payload struct {
		Mismatches []vigilplan.Mismatch `json:"mismatches"`
	}
	output := captureStdout(t, func() {
		if code := applyPlanCommand(context.Background(), []string{"--json", planPath}, true); code != 3 {
			t.Fatalf("apply exit = %d, want policy block", code)
		}
	})
	decodeEnvelopeData(t, []byte(output), &payload)
	if !containsPlanMismatch(payload.Mismatches, "pack_digest") {
		t.Fatalf("mismatches = %#v", payload.Mismatches)
	}
	if containsPlanMismatch(payload.Mismatches, "workspace_digest") {
		t.Fatalf("ignored pack change should be caught by pack digest, not workspace digest: %#v", payload.Mismatches)
	}
}

func TestPlanOutputInsideRepositoryMustBeIgnored(t *testing.T) {
	setupReviewedPlanFixture(t)
	output := captureStdout(t, func() {
		if code := plan("", []string{"--json", "--output", "reviewed-plan.json"}, true); code != 3 {
			t.Fatalf("plan exit = %d, want policy block", code)
		}
	})
	var payload map[string]any
	envelope := decodeEnvelopeData(t, []byte(output), &payload)
	if envelope.ExitCode != 3 || len(envelope.Errors) == 0 {
		t.Fatalf("envelope = %#v", envelope)
	}
	if _, err := os.Stat("reviewed-plan.json"); !os.IsNotExist(err) {
		t.Fatalf("unignored plan path was written: %v", err)
	}
}

func TestWorkflowMachineFormatAdaptersUseOneResultModel(t *testing.T) {
	setupReviewedPlanFixture(t)

	jsonl := captureStdout(t, func() {
		if code := workflowLocalContext(context.Background(), "", []string{"--format", "jsonl"}, false); code != 0 {
			t.Fatalf("JSONL workflow exit = %d", code)
		}
	})
	lines := strings.Split(strings.TrimSpace(jsonl), "\n")
	if len(lines) != 4 {
		t.Fatalf("JSONL event count = %d, want 4\n%s", len(lines), jsonl)
	}
	wantTypes := []string{"run_started", "gate_started", "gate_finished", "run_finished"}
	for index, line := range lines {
		var event struct {
			SchemaVersion string `json:"schema_version"`
			Sequence      int    `json:"sequence"`
			Type          string `json:"type"`
			Command       string `json:"command"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSONL line %d: %v", index, err)
		}
		if event.SchemaVersion != "1" || event.Sequence != index+1 || event.Type != wantTypes[index] || event.Command != "workflow:local" {
			t.Fatalf("event[%d] = %#v", index, event)
		}
	}

	junit := captureStdout(t, func() {
		if code := workflowLocalContext(context.Background(), "", []string{"--format=junit"}, false); code != 0 {
			t.Fatalf("JUnit workflow exit = %d", code)
		}
	})
	for _, want := range []string{`<testsuites name="Vigil" tests="1" failures="0"`, `<testcase name="status" classname="workflow:local"`} {
		if !strings.Contains(junit, want) {
			t.Fatalf("JUnit output missing %q:\n%s", want, junit)
		}
	}

	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{{Name: "failing gate", Command: "sh", Args: []string{"-c", "exit 1"}, ReadOnly: true}}
	writeJSONFile(t, defaultConfigName, cfg)
	github := captureStdout(t, func() {
		if code := workflowLocalContext(context.Background(), "", []string{"--format=github"}, false); code != 1 {
			t.Fatalf("GitHub workflow exit = %d", code)
		}
	})
	if !strings.Contains(github, "::error title=vigil.workflow.failing-gate::") {
		t.Fatalf("GitHub annotation missing:\n%s", github)
	}
}

func TestWorkflowRejectsConflictingMachineFormats(t *testing.T) {
	setupReviewedPlanFixture(t)
	if code := workflowLocalContext(context.Background(), "", []string{"--json", "--format=junit"}, false); code != 2 {
		t.Fatalf("workflow exit = %d, want usage error", code)
	}
}

func TestDispatchPolicyAndUsageFailuresRemainMachineReadable(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{name: "unknown", args: []string{"unknown-command", "--json"}, code: 2},
		{name: "mutation", args: []string{"apply", "--json", "missing-plan.json"}, code: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			var exitCode int
			output := captureStdout(t, func() {
				exitCode = run(test.args)
			})
			if exitCode != test.code {
				t.Fatalf("exit = %d, want %d", exitCode, test.code)
			}
			var payload map[string]any
			envelope := decodeEnvelopeData(t, []byte(output), &payload)
			if envelope.ExitCode != test.code || len(envelope.Errors) == 0 {
				t.Fatalf("envelope = %#v", envelope)
			}
		})
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
	for _, profile := range []string{"generic", "go-tool", "static-site", "js-app", "php-app", "native-app", "mixed", "custom"} {
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
	for _, want := range []string{"schema_version", "profile", "project", "coordination.mode", "coordination.authoritative_surfaces", "coordination.mutation_requires", "gates"} {
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

func TestPublicAssumptionScanIgnoresWorktreeGitFile(t *testing.T) {
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
	cfg.PublicAssumptionPatterns = []string{`/private/`}
	writeJSONFile(t, defaultConfigName, cfg)
	if err := os.WriteFile(".git", []byte("gitdir: /private/repository/.git/worktrees/fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("public.txt", []byte("safe\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := publicAssumptionFindings(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestPublicAssumptionScanUsesGitVisibleFilesOnly(t *testing.T) {
	temp := t.TempDir()
	t.Chdir(temp)
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	cfg := templateConfig("generic")
	cfg.PublicAssumptionPatterns = []string{`forbidden-content`}
	writeJSONFile(t, defaultConfigName, cfg)
	if err := os.WriteFile(".gitignore", []byte(".cache/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(".cache", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".cache/tool.db", []byte("forbidden-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := publicAssumptionFindings(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("ignored tool state produced findings: %#v", findings)
	}
}

func TestPublicAssumptionScanDoesNotFollowSymlinks(t *testing.T) {
	temp := t.TempDir()
	t.Chdir(temp)
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	cfg := templateConfig("generic")
	cfg.PublicAssumptionPatterns = []string{`forbidden-content`}
	writeJSONFile(t, defaultConfigName, cfg)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("forbidden-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, "external-link"); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	findings, err := publicAssumptionFindings(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("scan followed symlink content: %#v", findings)
	}
}

func TestPublicAssumptionScanPreservesNewlinesInGitPaths(t *testing.T) {
	temp := t.TempDir()
	t.Chdir(temp)
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	cfg := templateConfig("generic")
	cfg.PublicAssumptionPatterns = []string{`forbidden-content`}
	writeJSONFile(t, defaultConfigName, cfg)
	path := "line\nbreak.txt"
	if err := os.WriteFile(path, []byte("forbidden-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := publicAssumptionFindings(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0] != path {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestPublicAssumptionScanFailsClosedOnOversizedFile(t *testing.T) {
	temp := t.TempDir()
	t.Chdir(temp)
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	cfg := templateConfig("generic")
	cfg.PublicAssumptionPatterns = []string{`forbidden-content`}
	writeJSONFile(t, defaultConfigName, cfg)
	if err := os.WriteFile("oversized.dat", []byte{0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate("oversized.dat", maxPublicAssumptionFileBytes+1); err != nil {
		t.Skipf("sparse files unsupported: %v", err)
	}

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = checkPublicAssumptions("", []string{"--json"})
	})
	if exitCode != exitInternal {
		t.Fatalf("exit = %d, output = %s", exitCode, output)
	}
	var data struct {
		Check string `json:"check"`
	}
	envelope := decodeEnvelopeData(t, []byte(output), &data)
	if envelope.ExitCode != exitInternal || data.Check != "public_assumptions" {
		t.Fatalf("envelope = %#v, data = %#v", envelope, data)
	}
}

func TestIgnoredRepositoryOutputRejectsSymlinkEscapeAndGitMetadata(t *testing.T) {
	repository := t.TempDir()
	t.Chdir(repository)
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	if err := os.WriteFile(".gitignore", []byte(".vigil/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, ".vigil"); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := validateArtifactRoot(filepath.Join(".vigil", "runs")); err == nil ||
		!strings.Contains(err.Error(), "symlink path escapes repository") {
		t.Fatalf("artifact symlink escape error = %v", err)
	}
	if err := os.Remove(".vigil"); err != nil {
		t.Fatal(err)
	}
	if err := validateArtifactRoot(filepath.Join(".git", "vigil-runs")); err == nil ||
		!strings.Contains(err.Error(), "inside Git metadata") {
		t.Fatalf("Git metadata output error = %v", err)
	}
	if err := validateArtifactRoot(filepath.Join(outside, "explicit-runs")); err != nil {
		t.Fatalf("explicit external artifact root was rejected: %v", err)
	}
}

func TestConfigMigratePreservesLegacyAuthorityMutationRequirements(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "schema_version": "1",
  "profile": "generic",
  "project": "legacy",
  "authority": {
    "local_first": true,
    "mutation_requires": [
      "explicit-confirmation",
      "clean-config",
      "clean-tree"
    ]
  },
  "gates": [
    {
      "name": "status",
      "command": "git status --short",
      "read_only": true
    }
  ],
  "extensions": {
    "enabled": true,
    "manifest_root": "extensions",
    "allowed_kinds": ["custom"],
    "require_private": false,
    "x_extension": "keep"
  },
  "x_custom": {
    "keep": true
  }
}`
	if err := os.WriteFile(defaultConfigName, []byte(legacy+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := configMigrate("", []string{"--write", "--json"}); code != 0 {
		t.Fatalf("configMigrate exit = %d", code)
	}
	backups, err := filepath.Glob(defaultConfigName + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("migration backup count = %d", len(backups))
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != legacy+"\n" {
		t.Fatalf("migration backup changed:\n%s", backup)
	}
	data, err := os.ReadFile(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	var migrated config
	if err := json.Unmarshal(data, &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.SchemaVersion != configSchemaVersion {
		t.Fatalf("schema_version = %q, want %q", migrated.SchemaVersion, configSchemaVersion)
	}
	if strings.Contains(string(data), `"authority"`) {
		t.Fatalf("legacy authority block was not removed:\n%s", data)
	}
	want := []string{"explicit-confirmation", "clean-config", "clean-tree"}
	if strings.Join(migrated.Coordination.MutationRequires, ",") != strings.Join(want, ",") {
		t.Fatalf("mutation requirements = %#v, want %#v", migrated.Coordination.MutationRequires, want)
	}
	if migrated.Coordination.Mode != "github-adjacent-helper" {
		t.Fatalf("coordination.mode = %q", migrated.Coordination.Mode)
	}
	if len(migrated.Coordination.AuthoritativeSurfaces) == 0 {
		t.Fatal("coordination.authoritative_surfaces was not populated")
	}
	if len(migrated.Gates) != 1 || migrated.Gates[0].Command != "git" || strings.Join(migrated.Gates[0].Args, ",") != "status,--short" || migrated.Gates[0].Shell {
		t.Fatalf("legacy gate was not migrated to argv: %#v", migrated.Gates)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["x_custom"].(map[string]any); !ok {
		t.Fatalf("custom top-level field not preserved:\n%s", data)
	}
	extensions, ok := raw["extensions"].(map[string]any)
	if !ok || extensions["x_extension"] != "keep" {
		t.Fatalf("custom extension field not preserved:\n%s", data)
	}
}

func TestConfigMigrationMarksShellSyntaxExplicit(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.SchemaVersion = "2"
	cfg.Gates = []gateConfig{{
		Name:     "pipeline",
		Command:  "printf value | wc -c",
		ReadOnly: true,
	}}
	migrated := applyConfigDefaults(cfg, "generic")
	if !migrated.Gates[0].Shell || migrated.Gates[0].Command != cfg.Gates[0].Command || len(migrated.Gates[0].Args) != 0 {
		t.Fatalf("shell migration=%#v", migrated.Gates[0])
	}
}

func TestConfigValidationRequiresExplicitExecutionMode(t *testing.T) {
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{{
		Name:     "ambiguous",
		Command:  "go test ./...",
		ReadOnly: true,
	}}
	issues := issueMessages(validateConfigIssues(cfg))
	if !strings.Contains(strings.Join(issues, "\n"), "one executable") {
		t.Fatalf("issues=%v", issues)
	}
	cfg.Gates[0].Shell = true
	if issues := validateConfigIssues(cfg); len(issues) != 0 {
		t.Fatalf("explicit shell gate should validate: %v", issues)
	}
}

func TestConfigReadsRejectSymlinksAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	writeJSONFile(t, target, templateConfig("generic"))
	link := filepath.Join(root, "linked.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, _, err := loadConfig(link); err == nil ||
		!strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("loadConfig symlink error = %v", err)
	}

	oversized := filepath.Join(root, "oversized.json")
	if err := os.WriteFile(oversized, []byte{0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(oversized, maxConfigBytes+1); err != nil {
		t.Skipf("sparse files unsupported: %v", err)
	}
	if _, _, err := loadConfig(oversized); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("loadConfig oversized error = %v", err)
	}
}

func TestConfigRepairPartialConfigPreservesUnknownFieldsAndExplicitFalse(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	partial := `{
  "schema_version": "2",
  "coordination": {
    "x_coordination": "keep"
  },
  "extensions": {
    "enabled": false,
    "x_extension": "keep"
  },
  "x_custom": {
    "keep": true
  }
}`
	if err := os.WriteFile(defaultConfigName, []byte(partial+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := repairConfig("", []string{"--yes", "--json"}); code != 0 {
		t.Fatalf("repairConfig exit = %d", code)
	}
	cfg, _, err := loadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Extensions.Enabled {
		t.Fatal("explicit extensions.enabled=false was overwritten")
	}
	data, err := os.ReadFile(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["x_custom"].(map[string]any); !ok {
		t.Fatalf("custom top-level field not preserved:\n%s", data)
	}
	coordination, ok := raw["coordination"].(map[string]any)
	if !ok || coordination["x_coordination"] != "keep" {
		t.Fatalf("custom coordination field not preserved:\n%s", data)
	}
	extensions, ok := raw["extensions"].(map[string]any)
	if !ok || extensions["x_extension"] != "keep" {
		t.Fatalf("custom extension field not preserved:\n%s", data)
	}
	backups, err := filepath.Glob(defaultConfigName + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d, want 1 (%#v)", len(backups), backups)
	}
}

func TestFutureConfigSchemaIsBlockedNotDowngraded(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultConfigName, []byte(`{"schema_version":"99","profile":"future"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := buildSetupPlan("", "auto")
	if plan["config_state"] != "unsupported-newer-schema" || plan["overall"] != "blocked" {
		t.Fatalf("future schema plan = state %v overall %v", plan["config_state"], plan["overall"])
	}
	if plan["execution_status"] != "blocked" {
		t.Fatalf("future schema execution_status = %v, want blocked", plan["execution_status"])
	}
	if got := len(plan["proposed_mutations"].([]map[string]any)); got != 0 {
		t.Fatalf("future schema proposed mutation count = %d, want 0", got)
	}
	if got := len(plan["recommended_actions"].([]map[string]any)); got != 1 {
		t.Fatalf("blocked plan recommended action count = %d, want 1", got)
	}
	if code := repairConfig("", []string{"--yes", "--json"}); code == 0 {
		t.Fatal("repairConfig should refuse future schema")
	}
	if code := configMigrate("", []string{"--write", "--json"}); code == 0 {
		t.Fatal("configMigrate should refuse future schema")
	}
	if code := setupWizard("", []string{"--write", "--json"}); code == 0 {
		t.Fatal("setup --write should refuse future schema")
	}
	data, err := os.ReadFile(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"schema_version":"99"`) {
		t.Fatalf("future config was modified:\n%s", data)
	}
}

func TestSetupPlanMissingGoConfigIsReadOnlyAndVersioned(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.mod", []byte("module example.test/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := buildSetupPlan("", "auto")
	if plan["output_contract_version"] != "1" {
		t.Fatalf("output contract version = %v", plan["output_contract_version"])
	}
	if plan["execution_status"] != "planned" {
		t.Fatalf("execution_status = %v", plan["execution_status"])
	}
	if plan["config_state"] != "missing" || plan["overall"] != "changes_required" {
		t.Fatalf("setup plan state=%v overall=%v", plan["config_state"], plan["overall"])
	}
	validation := plan["validation"].(map[string]any)
	currentIssues := validation["current_issues"].([]configIssue)
	if len(currentIssues) != 1 || currentIssues[0].Code != "config.missing" {
		t.Fatalf("current_issues = %#v, want config.missing", currentIssues)
	}
	profile := plan["profile"].(map[string]any)
	if profile["selected"] != "go-tool" {
		t.Fatalf("selected profile = %v", profile["selected"])
	}
	if profile["profile_confidence"] != "certain" {
		t.Fatalf("profile_confidence = %v", profile["profile_confidence"])
	}
	if fileExists(defaultConfigName) {
		t.Fatal("read-only setup plan wrote config")
	}
}

func TestSetupWriteIsIdempotentForValidConfig(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if code := setupWizard("", []string{"--write", "--json"}); code != 0 {
		t.Fatalf("first setup --write exit = %d", code)
	}
	if _, _, err := loadConfig(""); err != nil {
		t.Fatal(err)
	}
	plan := buildSetupPlan("", "auto")
	if got := len(plan["proposed_mutations"].([]map[string]any)); got != 0 {
		t.Fatalf("valid setup proposed mutation count = %d, want 0", got)
	}
	if code := setupWizard("", []string{"--write", "--json"}); code != 0 {
		t.Fatalf("second setup --write exit = %d", code)
	}
}

func TestSetupWriteBootstrapsThroughMutationGate(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"--allow-mutation", "setup", "--write", "--json"}); code != 0 {
		t.Fatalf("setup through run exit = %d", code)
	}
	if _, _, err := loadConfig(""); err != nil {
		t.Fatal(err)
	}
}

func TestProfileDetectionKeepsPhpPrimaryWithJavascriptAssets(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("composer.json", []byte(`{"require":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("package.json", []byte(`{"scripts":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	detected := detectSetupProfile()
	if detected["primary"] != "php-app" {
		t.Fatalf("primary = %v, want php-app", detected["primary"])
	}
	if detected["profile_confidence"] != "ambiguous" {
		t.Fatalf("profile_confidence = %v, want ambiguous", detected["profile_confidence"])
	}
	if !containsString(detected["capabilities"].([]string), "javascript-assets") {
		t.Fatalf("capabilities missing javascript-assets: %#v", detected["capabilities"])
	}
	if len(detected["ambiguities"].([]string)) == 0 {
		t.Fatalf("expected supporting-toolchain ambiguity: %#v", detected)
	}
}

func TestProfileDetectionUsesEmptyArraysForGenericCollections(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	detected := detectSetupProfile()
	if capabilities := detected["capabilities"].([]string); len(capabilities) != 0 {
		t.Fatalf("generic capabilities = %#v, want empty array", capabilities)
	}
	if ambiguities := detected["ambiguities"].([]string); len(ambiguities) != 0 {
		t.Fatalf("generic ambiguities = %#v, want empty array", ambiguities)
	}
}

func TestScribeReadmeRenderIsStable(t *testing.T) {
	input := "# App\n\nHuman intro.\n\n## Install\n"
	next, changed, err := renderScribeReadme(input)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected Scribe render to add managed block")
	}
	again, changed, err := renderScribeReadme(next)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("expected Scribe render to be stable, changed to:\n%s", again)
	}
	if next != again {
		t.Fatal("stable Scribe render changed content")
	}
}

func TestScribeDetectsGoTestsOutsideTestDirectories(t *testing.T) {
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
	if err := os.WriteFile("sample_test.go", []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "add", "sample_test.go"); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if paths := existingTestPaths(); len(paths) != 1 || paths[0] != "Go *_test.go (1)" {
		t.Fatalf("test paths=%v", paths)
	}
}

func TestScribeGeneratePreservesExistingReadmeMode(t *testing.T) {
	temp := t.TempDir()
	t.Chdir(temp)
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	if err := os.WriteFile("README.md", []byte("# Fixture\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	output := captureStdout(t, func() {
		if code := scribeCommand("readme:generate", []string{"--path", "README.md", "--json"}); code != exitSuccess {
			t.Fatalf("readme:generate exit = %d", code)
		}
	})
	var payload struct {
		Changed bool `json:"changed"`
	}
	decodeEnvelopeData(t, []byte(output), &payload)
	if !payload.Changed {
		t.Fatal("readme:generate did not report a change")
	}
	info, err := os.Stat("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("README mode = %#o", info.Mode().Perm())
	}
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<!-- scribe:begin -->") {
		t.Fatalf("generated README lacks managed block:\n%s", data)
	}
}

func TestPHPLintChecksEveryTrackedFile(t *testing.T) {
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
	for i := 0; i < 30; i++ {
		path := fmt.Sprintf("file-%02d.php", i)
		if err := os.WriteFile(path, []byte("<?php\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, code := runCommand("git", "add", "."); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	binDir := filepath.Join(temp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakePHP := filepath.Join(binDir, "php")
	if err := os.WriteFile(fakePHP, []byte("#!/bin/sh\nprintf 'valid %s\\n' \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var exitCode int
	output := captureStdout(t, func() {
		exitCode = phpLint([]string{"--json"})
	})
	if exitCode != 0 {
		t.Fatalf("exit=%d output=%s", exitCode, output)
	}
	var payload struct {
		Checks []checkResult `json:"checks"`
	}
	decodeEnvelopeData(t, []byte(output), &payload)
	if len(payload.Checks) != 30 {
		t.Fatalf("checked %d files, want 30", len(payload.Checks))
	}
}

func TestDepsWhyUsesExactPackageManagerDependency(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"name": "fixture",
		"dependencies": map[string]string{
			"react":     "1.0.0",
			"react-dom": "1.0.0",
		},
	}
	writeJSONFile(t, "package.json", manifest)
	binDir := filepath.Join(temp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeNPM := filepath.Join(binDir, "npm")
	if err := os.WriteFile(fakeNPM, []byte("#!/bin/sh\nprintf 'fixture -> %s\\n' \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)
	if code := depsWhy([]string{"--json", "react"}); code != 0 {
		t.Fatalf("exact dependency exit=%d", code)
	}
	if code := depsWhy([]string{"rea"}); code != 1 {
		t.Fatalf("substring query exit=%d, want 1", code)
	}
	t.Setenv("PATH", filepath.Join(temp, "empty-bin"))
	if code := depsWhy([]string{"react"}); code != 4 {
		t.Fatalf("missing required npm exit=%d, want 4", code)
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

func TestInteractiveSetupWizardDryRunUsesInjectedInput(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.mod", []byte("module example.test/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldReader := promptReader
	promptReader = bufio.NewReader(strings.NewReader("\n\n\n\n\n\n\n\n\n"))
	t.Cleanup(func() { promptReader = oldReader })
	output := captureStdout(t, func() {
		if code := runInteractiveSetupWizard("", "auto", false, false, true); code != 0 {
			t.Fatalf("wizard dry-run exit = %d", code)
		}
	})
	if !strings.Contains(output, "Vigil Setup Wizard") || !strings.Contains(output, "Review Vigil setup") {
		t.Fatalf("wizard output missing expected sections:\n%s", output)
	}
	if fileExists(defaultConfigName) {
		t.Fatal("dry-run wizard wrote config")
	}
}

func TestInteractiveSetupWizardBackReturnsToPreviousStep(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.mod", []byte("module example.test/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldReader := promptReader
	promptReader = bufio.NewReader(strings.NewReader("\n\n\n\nback\n\n\n\n\n\n\n\n"))
	t.Cleanup(func() { promptReader = oldReader })
	output := captureStdout(t, func() {
		if code := runInteractiveSetupWizard("", "auto", false, false, true); code != 0 {
			t.Fatalf("wizard dry-run exit = %d", code)
		}
	})
	if strings.Count(output, "Select project checks:") != 2 {
		t.Fatalf("back did not revisit gate step:\n%s", output)
	}
	if strings.Contains(output, "restarting the current wizard") {
		t.Fatalf("wizard used obsolete restart behavior:\n%s", output)
	}
}

func TestInteractiveSetupWizardEOFExitsWithoutWriting(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	oldReader := promptReader
	promptReader = bufio.NewReader(strings.NewReader(""))
	t.Cleanup(func() { promptReader = oldReader })
	if code := runInteractiveSetupWizard("", "generic", true, false, false); code == 0 {
		t.Fatal("EOF should cancel interactive setup")
	}
	if fileExists(defaultConfigName) {
		t.Fatal("EOF wrote config")
	}
}

func TestInitAliasWritesConfigWithMutationConfirmation(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"--allow-mutation", "init", "--yes"}); code != 0 {
		t.Fatalf("init alias exit = %d", code)
	}
	if _, _, err := loadConfig(""); err != nil {
		t.Fatal(err)
	}
}

func TestHelpCategoriesPutSetupLast(t *testing.T) {
	output := captureStdout(t, printHelp)
	setupIndex := strings.LastIndex(output, "\nsetup\n")
	if setupIndex < 0 {
		t.Fatalf("setup category missing:\n%s", output)
	}
	if strings.Contains(output[setupIndex+1:], "\ncore\n") {
		t.Fatalf("setup was not the final category:\n%s", output)
	}
}

func TestBeginnerCommandsAreRegistryBacked(t *testing.T) {
	registry, err := newCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check", "fix", "learn", "advanced"} {
		command, ok := registry.Resolve(name)
		if !ok {
			t.Fatalf("beginner command %s missing", name)
		}
		if command.Binding != "builtin:"+name {
			t.Fatalf("%s binding = %q", name, command.Binding)
		}
	}
	check, _ := registry.Resolve("check")
	workflow, _ := registry.Resolve("workflow:local")
	if check.Access != workflow.Access || check.Network != workflow.Network || check.Timeout != workflow.Timeout {
		t.Fatalf("check command drifted from workflow:local metadata: check=%#v workflow=%#v", check, workflow)
	}
}

func TestBeginnerHelpUsesPlainTerminology(t *testing.T) {
	output := captureStdout(t, func() {
		if code := commandHelp([]string{"--beginner", "plan"}); code != 0 {
			t.Fatalf("help exit = %d", code)
		}
	})
	if strings.Contains(output, "digest-bound") || strings.Contains(output, "mutation") {
		t.Fatalf("beginner help leaked internal terminology:\n%s", output)
	}
	for _, want := range []string{"reviewed plan", "changes:", "network:", "next:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("beginner help missing %q:\n%s", want, output)
		}
	}
}

func TestCheckWithoutConfigExplainsSetup(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	output := captureStdout(t, func() {
		if code := run([]string{"check"}); code != exitUsage {
			t.Fatalf("check exit = %d", code)
		}
	})
	for _, want := range []string{"Vigil is not set up", "What this means:", "vigil setup"} {
		if !strings.Contains(output, want) {
			t.Fatalf("check guidance missing %q:\n%s", want, output)
		}
	}
}

func TestStatusTextIsBeginnerSummary(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(temp, defaultConfigName), templateConfig("generic"))
	output := captureStdout(t, func() {
		if code := run([]string{"status"}); code != 0 {
			t.Fatalf("status exit = %d", code)
		}
	})
	for _, want := range []string{"Project status:", "Configuration", "Recommended next step:", "vigil check"} {
		if !strings.Contains(output, want) {
			t.Fatalf("status summary missing %q:\n%s", want, output)
		}
	}
}

func TestExplainWithoutCommandSummarizesConfiguredChecks(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(temp, defaultConfigName), templateConfig("generic"))
	output := captureStdout(t, func() {
		if code := run([]string{"explain"}); code != 0 {
			t.Fatalf("explain exit = %d", code)
		}
	})
	for _, want := range []string{"Vigil will run these project checks:", "Changes:", "Command:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("explain output missing %q:\n%s", want, output)
		}
	}
}

func TestManpageIncludesSetupAndLicense(t *testing.T) {
	page := generateManpage()
	for _, want := range []string{".TH VIGIL", "setup:wizard", "0BSD"} {
		if !strings.Contains(page, want) {
			t.Fatalf("manpage missing %q:\n%s", want, page)
		}
	}
}

func TestInstalledPluginRegistersAndRunsThroughCommonEnvelope(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_CONFIG", "")
	t.Setenv("VIGIL_PLUGIN_ROOT", filepath.Join(temp, "user-plugins"))
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))
	writeJSONFile(t, defaultConfigName, templateConfig("generic"))

	pluginPath := writeCommandPathPluginFixture(t, temp)

	layout, err := vigilplugins.NewLayout(os.Getenv("VIGIL_PLUGIN_ROOT"), temp)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := vigilplugins.Install(context.Background(), vigilplugins.InstallOptions{
		Layout:       layout,
		Candidate:    pluginPath,
		ApproveAll:   true,
		ApprovalTime: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newCommandRegistryForConfig(defaultConfigName)
	if err != nil {
		t.Fatal(err)
	}
	command, ok := registry.Resolve("fixture:echo")
	if !ok {
		t.Fatal("installed plugin command missing from registry")
	}
	if command.Source != "plugin:fixture@1.0.0" || command.Binding != installed.Plugin.Binding {
		t.Fatalf("unexpected plugin command provenance: source=%q binding=%q", command.Source, command.Binding)
	}

	var exitCode int
	encoded := captureStdout(t, func() {
		exitCode = runContext(context.Background(), []string{"--config", defaultConfigName, "fixture:echo", "hello", "--json"})
	})
	if exitCode != exitSuccess {
		t.Fatalf("plugin command exit = %d\n%s", exitCode, encoded)
	}
	var data struct {
		Status  string `json:"status"`
		Plugin  string `json:"plugin"`
		Version string `json:"version"`
		Output  string `json:"output"`
		Result  struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	envelope := decodeEnvelopeData(t, []byte(encoded), &data)
	if envelope.Command != "fixture:echo" || envelope.ExitCode != exitSuccess || envelope.Status != "ok" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	if data.Plugin != "fixture" || data.Version != "1.0.0" || data.Output != "plugin ok" || data.Result.Value != "ok" {
		t.Fatalf("unexpected plugin payload: %#v", data)
	}

	if err := os.WriteFile(installed.ExecutablePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var unavailableExit int
	unavailableOutput := captureStdout(t, func() {
		unavailableExit = runContext(context.Background(), []string{"--config", defaultConfigName, "fixture:echo", "--json"})
	})
	if unavailableExit != exitPolicyBlocked {
		t.Fatalf("unavailable plugin command exit = %d\n%s", unavailableExit, unavailableOutput)
	}
	var unavailableData struct {
		Error         string `json:"error"`
		PluginCommand string `json:"plugin_command"`
		Issues        []struct {
			Code string `json:"code"`
		} `json:"issues"`
	}
	unavailableEnvelope := decodeEnvelopeData(t, []byte(unavailableOutput), &unavailableData)
	if unavailableEnvelope.Command != "fixture:echo" || unavailableEnvelope.Status != "blocked" ||
		unavailableData.PluginCommand != "fixture:echo" || len(unavailableData.Issues) != 1 ||
		unavailableData.Issues[0].Code != "VIGIL_PLUGIN_DIGEST_MISMATCH" {
		t.Fatalf("unexpected unavailable-plugin diagnostic: envelope=%#v data=%#v", unavailableEnvelope, unavailableData)
	}
	for _, healthCommand := range []string{"doctor", "status", "verify"} {
		t.Run("tampered-"+healthCommand, func(t *testing.T) {
			var healthExit int
			healthOutput := captureStdout(t, func() {
				healthExit = runContext(context.Background(), []string{"--config", defaultConfigName, healthCommand, "--json"})
			})
			if healthExit != exitPolicyBlocked {
				t.Fatalf("%s exit = %d, want policy blocked\n%s", healthCommand, healthExit, healthOutput)
			}
			var healthData map[string]any
			healthEnvelope := decodeEnvelopeData(t, []byte(healthOutput), &healthData)
			if healthEnvelope.Command != healthCommand || healthEnvelope.Status != "blocked" {
				t.Fatalf("unexpected %s envelope: %#v", healthCommand, healthEnvelope)
			}
			if !strings.Contains(string(healthEnvelope.Data), "plugin") {
				t.Fatalf("%s omitted plugin health: %s", healthCommand, healthEnvelope.Data)
			}
		})
	}
}

func TestPluginLifecycleCLIRequiresMutationAndRevokesOnRemove(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_CONFIG", "")
	t.Setenv("VIGIL_PLUGIN_ROOT", filepath.Join(temp, "user-plugins"))
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))
	writeJSONFile(t, defaultConfigName, templateConfig("generic"))
	pluginPath := writeCommandPathPluginFixture(t, temp)

	var blockedInstallExit int
	blockedInstall := captureStdout(t, func() {
		blockedInstallExit = runContext(context.Background(), []string{
			"plugins:install", "--file", pluginPath, "--approve-all", "--json",
		})
	})
	if blockedInstallExit != exitPolicyBlocked {
		t.Fatalf("unconfirmed install exit = %d\n%s", blockedInstallExit, blockedInstall)
	}

	var installExit int
	installOutput := captureStdout(t, func() {
		installExit = runContext(context.Background(), []string{
			"--allow-mutation", "plugins:install", "--file", pluginPath, "--approve-all", "--json",
		})
	})
	if installExit != exitSuccess {
		t.Fatalf("install exit = %d\n%s", installExit, installOutput)
	}
	var installData struct {
		Result vigilplugins.InstallResult `json:"result"`
	}
	decodeEnvelopeData(t, []byte(installOutput), &installData)
	if installData.Result.Action != "installed" || installData.Result.Plugin.ID != "fixture" {
		t.Fatalf("install result = %#v", installData.Result)
	}

	var listExit int
	listOutput := captureStdout(t, func() {
		listExit = runContext(context.Background(), []string{"plugins:list", "--json"})
	})
	if listExit != exitSuccess {
		t.Fatalf("list exit = %d\n%s", listExit, listOutput)
	}
	var discovery vigilplugins.Discovery
	decodeEnvelopeData(t, []byte(listOutput), &discovery)
	if discovery.Status != "ok" || len(discovery.Plugins) != 1 || discovery.Plugins[0].ID != "fixture" {
		t.Fatalf("plugin list = %#v", discovery)
	}

	var blockedRemoveExit int
	blockedRemove := captureStdout(t, func() {
		blockedRemoveExit = runContext(context.Background(), []string{"plugins:remove", "--json", "fixture"})
	})
	if blockedRemoveExit != exitPolicyBlocked {
		t.Fatalf("unconfirmed remove exit = %d\n%s", blockedRemoveExit, blockedRemove)
	}

	var removeExit int
	removeOutput := captureStdout(t, func() {
		removeExit = runContext(context.Background(), []string{
			"--allow-mutation", "plugins:remove", "--json", "fixture",
		})
	})
	if removeExit != exitSuccess {
		t.Fatalf("remove exit = %d\n%s", removeExit, removeOutput)
	}
	layout, err := vigilplugins.NewLayout(os.Getenv("VIGIL_PLUGIN_ROOT"), temp)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := vigilplugins.ReadTrust(layout.TrustPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(trust.Records) != 0 || len(trust.RevokedDigests) != 1 ||
		trust.RevokedDigests[0] != installData.Result.Plugin.Digest {
		t.Fatalf("trust after remove = %#v", trust)
	}
}

func TestSignedIndexCLITrustInstallRevokeAndRestore(t *testing.T) {
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_CONFIG", "")
	t.Setenv("VIGIL_PLUGIN_ROOT", filepath.Join(temp, "user-plugins"))
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))
	writeJSONFile(t, defaultConfigName, templateConfig("generic"))

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID := fmt.Sprintf("sha256:%x", sha256.Sum256(publicKey))
	cfg := templateConfig("generic")
	cfg.Plugins.Local = "deny"
	cfg.Plugins.RequireSigned = true
	cfg.Plugins.AllowedIDs = []string{"fixture"}
	cfg.Plugins.AllowedPublisherKeyIDs = []string{keyID}
	writeJSONFile(t, defaultConfigName, cfg)
	keyPath := filepath.Join(temp, "publisher.pub")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(publicKey)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var trustExit int
	trustOutput := captureStdout(t, func() {
		trustExit = runContext(context.Background(), []string{
			"--allow-mutation", "plugins:trust-publisher",
			"--key", keyPath, "--name", "Fixture Publisher", "--json",
		})
	})
	if trustExit != exitSuccess {
		t.Fatalf("trust publisher exit = %d\n%s", trustExit, trustOutput)
	}

	pluginPath := writeCommandPathPluginFixture(t, temp)
	var localInstallExit int
	localInstallOutput := captureStdout(t, func() {
		localInstallExit = runContext(context.Background(), []string{
			"--allow-mutation", "plugins:install",
			"--file", pluginPath, "--approve-all", "--json",
		})
	})
	if localInstallExit != exitPolicyBlocked ||
		!strings.Contains(localInstallOutput, "local plugin acquisition is denied") {
		t.Fatalf("local install policy exit = %d\n%s", localInstallExit, localInstallOutput)
	}
	handshake, pluginDigest, err := vigilplugins.HandshakeExecutable(context.Background(), pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	metadataDigest, err := vigilplugins.MetadataDigest(handshake.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	pluginInfo, err := os.Stat(pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	payload := vigilplugins.IndexPayload{
		GeneratedAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt:          now.Add(time.Hour).Format(time.RFC3339Nano),
		SignatureThreshold: 1,
		Plugins: []vigilplugins.IndexRelease{{
			ID: "fixture", Name: "Fixture", Version: "1.0.0", Description: "CLI fixture.",
			ProtocolVersion: vigilplugins.ProtocolVersion, HostAPIVersion: vigilplugins.HostAPIVersion,
			MetadataDigest: metadataDigest, Capabilities: vigilplugins.MetadataCapabilities(handshake.Plugin),
			Artifacts: []vigilplugins.IndexArtifact{{
				OS: runtime.GOOS, Arch: runtime.GOARCH, URL: filepath.Base(pluginPath),
				Digest: pluginDigest, Size: pluginInfo.Size(),
			}},
		}},
	}
	signingBytes, err := vigilplugins.IndexSigningBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	index := vigilplugins.IndexDocument{
		SchemaVersion: vigilplugins.IndexSchemaVersion,
		Signed:        payload,
		Signatures: []vigilplugins.IndexSignature{{
			KeyID: keyID, Algorithm: vigilplugins.PublisherAlgorithm,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, signingBytes)),
		}},
	}
	indexPath := filepath.Join(temp, "index-v1.json")
	writeJSONFile(t, indexPath, index)

	var verifyExit int
	verifyOutput := captureStdout(t, func() {
		verifyExit = runContext(context.Background(), []string{
			"plugins:index:verify", "--index", indexPath, "--json",
		})
	})
	if verifyExit != exitSuccess {
		t.Fatalf("verify index exit = %d\n%s", verifyExit, verifyOutput)
	}

	var installExit int
	installOutput := captureStdout(t, func() {
		installExit = runContext(context.Background(), []string{
			"--allow-mutation", "plugins:install",
			"--index", indexPath, "--id", "fixture", "--version", "1.0.0",
			"--approve-all", "--json",
		})
	})
	if installExit != exitSuccess {
		t.Fatalf("signed install exit = %d\n%s", installExit, installOutput)
	}
	layout, err := vigilplugins.NewLayout(os.Getenv("VIGIL_PLUGIN_ROOT"), temp)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := vigilplugins.ReadLock(layout.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Plugins) != 1 || lock.Plugins[0].Acquisition != "signed-index" ||
		lock.Plugins[0].SignatureThreshold != 1 || len(lock.Plugins[0].PublisherKeyIDs) != 1 ||
		lock.Plugins[0].PublisherKeyIDs[0] != keyID {
		t.Fatalf("signed lock = %#v", lock)
	}
	assertFixturePluginRuns(t)

	var revokeExit int
	revokeOutput := captureStdout(t, func() {
		revokeExit = runContext(context.Background(), []string{
			"--allow-mutation", "plugins:revoke-publisher", "--json", keyID,
		})
	})
	if revokeExit != exitSuccess {
		t.Fatalf("revoke publisher exit = %d\n%s", revokeExit, revokeOutput)
	}
	var unavailableExit int
	unavailableOutput := captureStdout(t, func() {
		unavailableExit = runContext(context.Background(), []string{"fixture:echo", "--json"})
	})
	if unavailableExit != exitPolicyBlocked ||
		!strings.Contains(unavailableOutput, "VIGIL_PLUGIN_PUBLISHER_THRESHOLD") {
		t.Fatalf("revoked publisher command exit = %d\n%s", unavailableExit, unavailableOutput)
	}

	var restoreExit int
	restoreOutput := captureStdout(t, func() {
		restoreExit = runContext(context.Background(), []string{
			"--allow-mutation", "plugins:trust-publisher",
			"--key", keyPath, "--name", "Fixture Publisher", "--restore-trust", "--json",
		})
	})
	if restoreExit != exitSuccess {
		t.Fatalf("restore publisher exit = %d\n%s", restoreExit, restoreOutput)
	}
	assertFixturePluginRuns(t)
}

func assertFixturePluginRuns(t *testing.T) {
	t.Helper()
	var exitCode int
	output := captureStdout(t, func() {
		exitCode = runContext(context.Background(), []string{"fixture:echo", "--json"})
	})
	if exitCode != exitSuccess {
		t.Fatalf("fixture plugin exit = %d\n%s", exitCode, output)
	}
}

func writeCommandPathPluginFixture(t *testing.T, root string) string {
	t.Helper()
	handshake := map[string]any{
		"schema_version":   vigilplugins.HandshakeSchema,
		"protocol_version": vigilplugins.ProtocolVersion,
		"plugin": map[string]any{
			"id":               "fixture",
			"name":             "Fixture",
			"version":          "1.0.0",
			"description":      "Command-path integration fixture.",
			"host_api_version": vigilplugins.HostAPIVersion,
			"commands": []any{map[string]any{
				"name":            "fixture:echo",
				"aliases":         []string{},
				"summary":         "Return a deterministic fixture response.",
				"access":          "read",
				"capabilities":    []string{"filesystem:read"},
				"args":            "[TEXT]",
				"flags":           []any{},
				"arguments":       []any{map[string]any{"name": "TEXT", "description": "Text to echo."}},
				"stability":       "stable",
				"timeout":         "2s",
				"network":         "none",
				"required_tools":  []string{},
				"output_formats":  []string{"text", "json"},
				"interactive":     false,
				"write_flags":     []string{},
				"read_only_flags": []string{},
				"usage":           "vigil fixture:echo [TEXT]",
				"examples":        []string{"vigil fixture:echo hello"},
			}},
		},
	}
	handshakeJSON, err := json.Marshal(handshake)
	if err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(root, "vigil-plugin-fixture")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  handshake) printf '%s\\n' " + shellQuote(string(handshakeJSON)) + " ;;\n" +
		"  execute)\n" +
		"    request=\"$(cat)\"\n" +
		"    request_id=\"$(printf '%s\\n' \"$request\" | sed -n 's/.*\"request_id\":\"\\([^\"]*\\)\".*/\\1/p')\"\n" +
		"    printf '{\"schema_version\":\"1\",\"protocol_version\":\"1\",\"request_id\":\"%s\",\"exit_code\":0,\"output\":\"plugin ok\",\"data\":{\"value\":\"ok\"},\"warnings\":[],\"errors\":[],\"artifacts\":[]}\\n' \"$request_id\"\n" +
		"    ;;\n" +
		"  *) exit 64 ;;\n" +
		"esac\n"
	if err := os.WriteFile(pluginPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return pluginPath
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestPack(t *testing.T, root, description string) {
	t.Helper()
	manifest := extensionManifest{
		SchemaVersion:  extensionSchemaVersion,
		HostAPIVersion: "v1",
		ID:             "github-cicd",
		Name:           "GitHub Actions Override",
		Kind:           "custom",
		Status:         "local",
		Private:        false,
		PublicCore:     true,
		Description:    description + " override",
		SourceRoot:     "extensions/github-cicd",
		Packages:       []string{},
		Commands:       []string{"github:init-ci"},
		CommandContracts: []extensionCommandContract{{
			Command:       "github:init-ci",
			Access:        "conditional-write",
			Capabilities:  []string{"filesystem:read", "filesystem:write"},
			Binding:       "builtin:github:init-ci",
			Timeout:       "10m",
			Stability:     "stable",
			RequiredTools: []string{},
			Network:       "none",
			OutputFormats: []string{"text", "json"},
			WriteFlags:    []string{"--write"},
			Usage:         "vigil github:init-ci [--write] [--json]",
			Description:   "Generate a GitHub Actions workflow.",
		}},
	}
	writeJSONFile(t, filepath.Join(root, manifest.ID, "extension.json"), manifest)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		data []byte
		err  error
	}
	readDone := make(chan readResult, 1)
	go func() {
		data, readErr := io.ReadAll(reader)
		readDone <- readResult{data: data, err: readErr}
	}()
	os.Stdout = writer
	fn()
	_ = writer.Close()
	os.Stdout = old
	result := <-readDone
	_ = reader.Close()
	if result.err != nil {
		t.Fatal(result.err)
	}
	return string(result.data)
}

type testMachineEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	Command       string          `json:"command"`
	Status        string          `json:"status"`
	ExitCode      int             `json:"exit_code"`
	StartedAt     string          `json:"started_at"`
	FinishedAt    string          `json:"finished_at"`
	DurationMS    int64           `json:"duration_ms"`
	Warnings      []any           `json:"warnings"`
	Errors        []any           `json:"errors"`
	Data          json.RawMessage `json:"data"`
	Artifacts     []any           `json:"artifacts"`
}

func decodeEnvelopeData(t *testing.T, encoded []byte, target any) testMachineEnvelope {
	t.Helper()
	var envelope testMachineEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, encoded)
	}
	if envelope.SchemaVersion == "" || envelope.Command == "" || envelope.StartedAt == "" || envelope.FinishedAt == "" {
		t.Fatalf("incomplete envelope: %#v", envelope)
	}
	if envelope.Warnings == nil || envelope.Errors == nil || envelope.Artifacts == nil {
		t.Fatalf("envelope arrays must not be null: %#v", envelope)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		t.Fatalf("decode envelope data: %v\n%s", err, envelope.Data)
	}
	return envelope
}

func setupReviewedPlanFixture(t *testing.T) string {
	t.Helper()
	temp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIGIL_CONFIG", "")
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(temp, "user-packs"))
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	if err := os.WriteFile(".gitignore", []byte(".vigil/\nextensions/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := templateConfig("generic")
	cfg.Gates = []gateConfig{{Name: "status", Command: "git", Args: []string{"status", "--short"}, ReadOnly: true}}
	writeJSONFile(t, defaultConfigName, cfg)
	if out, code := runCommand("git", "add", ".gitignore", defaultConfigName); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "fixture"); code != 0 {
		t.Fatalf("git commit failed: %s", out)
	}
	return filepath.Join(".vigil", "plans", "reviewed.json")
}

func setupWorkflowGraphRepository(t *testing.T, cfg config) string {
	t.Helper()
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	if out, code := runCommand("git", "init"); code != 0 {
		t.Fatalf("git init failed: %s", out)
	}
	if err := os.WriteFile(".gitignore", []byte(".vigil/\nfrontend/reports/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, defaultConfigName, cfg)
	if out, code := runCommand("git", "add", ".gitignore", defaultConfigName); code != 0 {
		t.Fatalf("git add failed: %s", out)
	}
	if out, code := runCommand("git", "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "workflow config"); code != 0 {
		t.Fatalf("git commit failed: %s", out)
	}
	return root
}

func containsPlanMismatch(mismatches []vigilplan.Mismatch, field string) bool {
	for _, mismatch := range mismatches {
		if mismatch.Field == field {
			return true
		}
	}
	return false
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

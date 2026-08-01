package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	vigilplugins "github.com/PayCal-Technologies/vigil-public/internal/plugins"
)

func TestBuiltBinaryLoadsEmbeddedOfficialPacksOutsideCheckout(t *testing.T) {
	if testing.Short() {
		t.Skip("build-backed black-box test")
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	emptyDirectory := t.TempDir()
	binaryName := "vigil"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-trimpath", "-o", binaryPath, "./cmd/vigil")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	run := func(args ...string) []byte {
		t.Helper()
		command := exec.Command(binaryPath, args...)
		command.Dir = emptyDirectory
		command.Env = append(
			os.Environ(),
			"VIGIL_USER_PACK_ROOT="+filepath.Join(emptyDirectory, "user-packs"),
			"VIGIL_PLUGIN_ROOT="+filepath.Join(emptyDirectory, "plugins"),
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("run %v: %v\n%s", args, err, output)
		}
		return output
	}

	var report extensionReport
	reportEnvelope := decodeEnvelopeData(t, run("extensions:list", "--json"), &report)
	if reportEnvelope.Command != "extensions:list" || reportEnvelope.ExitCode != 0 {
		t.Fatalf("extensions envelope = %#v", reportEnvelope)
	}
	if report.Status != "ok" || report.Count != 10 {
		t.Fatalf("embedded report status=%q count=%d issues=%v", report.Status, report.Count, report.Issues)
	}
	for _, pack := range report.Extensions {
		if pack.Origin != "embedded-official" {
			t.Fatalf("pack %s origin=%q, want embedded-official", pack.ID, pack.Origin)
		}
	}

	var commands []commandInfo
	listEnvelope := decodeEnvelopeData(t, run("list", "--json"), &commands)
	if listEnvelope.Command != "list" || listEnvelope.ExitCode != 0 {
		t.Fatalf("list envelope = %#v", listEnvelope)
	}
	foundGitHubPack := false
	for _, command := range commands {
		if command.Command == "github:init-ci" && command.Pack == "github-cicd" && command.Binding == "builtin:github:init-ci" {
			foundGitHubPack = true
			break
		}
	}
	if !foundGitHubPack {
		t.Fatal("embedded github:init-ci command contract missing")
	}

	versionOutput := string(run("--version"))
	for _, field := range []string{"commit=", "built=", "dirty=", "go=", "os=", "arch=", "config_schema="} {
		if !strings.Contains(versionOutput, field) {
			t.Fatalf("version output missing %q: %s", field, versionOutput)
		}
	}

	if output, err := exec.Command("git", "-C", emptyDirectory, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(emptyDirectory, ".gitignore"), []byte(".vigil/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `{
  "schema_version": "3",
  "profile": "generic",
  "project": "black-box",
  "coordination": {
    "mode": "policy-aware-preflight",
    "authoritative_surfaces": ["reviewed repository configuration"],
    "mutation_requires": ["explicit-confirmation", "clean-config"]
  },
  "gates": [
    {
      "name": "status",
      "command": "git",
      "args": ["status", "--short"],
      "read_only": true
    }
  ],
  "extensions": {
    "enabled": true,
    "manifest_root": "extensions",
    "allowed_kinds": ["custom"],
    "require_private": false
  }
}
`
	if err := os.WriteFile(filepath.Join(emptyDirectory, defaultConfigName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "-C", emptyDirectory, "add", ".gitignore", defaultConfigName)
	if output, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}
	commit := exec.Command("git", "-C", emptyDirectory, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "fixture")
	if output, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, output)
	}
	if runtime.GOOS != "windows" {
		referencePlugin := filepath.Join(repositoryRoot, "examples", "plugins", "reference", "vigil-plugin-reference")
		var conformance vigilplugins.ConformanceReport
		envelope := decodeEnvelopeData(t, run(
			"--allow-mutation",
			"plugins:conformance",
			"--file",
			referencePlugin,
			"--execute",
			"--json",
		), &conformance)
		if envelope.Command != "plugins:conformance" || envelope.ExitCode != 0 ||
			conformance.Status != "ok" || conformance.Plugin == nil || conformance.Plugin.ID != "reference" {
			t.Fatalf("conformance envelope=%#v report=%#v", envelope, conformance)
		}
	}
	planPath := filepath.Join(".vigil", "plans", "reviewed.json")
	var planPayload struct {
		Plan struct {
			PlanID string `json:"plan_id"`
		} `json:"plan"`
	}
	planEnvelope := decodeEnvelopeData(t, run("--allow-mutation", "plan", "--json", "--output", planPath), &planPayload)
	if planEnvelope.Command != "plan" || planPayload.Plan.PlanID == "" {
		t.Fatalf("plan envelope=%#v payload=%#v", planEnvelope, planPayload)
	}
	var applyPayload struct {
		PlanID  string       `json:"plan_id"`
		Results []gateResult `json:"results"`
	}
	applyEnvelope := decodeEnvelopeData(t, run("--allow-mutation", "apply", "--json", planPath), &applyPayload)
	if applyEnvelope.Command != "apply" || applyPayload.PlanID != planPayload.Plan.PlanID || len(applyPayload.Results) != 1 {
		t.Fatalf("apply envelope=%#v payload=%#v", applyEnvelope, applyPayload)
	}
}

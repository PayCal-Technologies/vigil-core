package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	version             = "0.1.0"
	configSchemaVersion = "1"
	defaultConfigName   = "vigil.config.json"
)

type config struct {
	SchemaVersion string            `json:"schema_version"`
	Profile       string            `json:"profile"`
	Project       string            `json:"project"`
	Authority     authorityConfig   `json:"authority"`
	Gates         []gateConfig      `json:"gates"`
	Extensions    extensionsConfig  `json:"extensions"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type authorityConfig struct {
	LocalFirst       bool     `json:"local_first"`
	MutationRequires []string `json:"mutation_requires"`
}

type gateConfig struct {
	Name     string   `json:"name"`
	Command  string   `json:"command"`
	ReadOnly bool     `json:"read_only"`
	Tags     []string `json:"tags,omitempty"`
}

type extensionsConfig struct {
	Enabled        bool     `json:"enabled"`
	ManifestRoot   string   `json:"manifest_root"`
	AllowedKinds   []string `json:"allowed_kinds"`
	RequirePrivate bool     `json:"require_private"`
}

type extensionManifest struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Status        string   `json:"status"`
	Private       bool     `json:"private"`
	PublicCore    bool     `json:"public_core"`
	Description   string   `json:"description"`
	SourceRoot    string   `json:"source_root"`
	Packages      []string `json:"packages"`
	Commands      []string `json:"commands"`
	Path          string   `json:"path,omitempty"`
}

type extensionReport struct {
	SchemaVersion string              `json:"schema_version"`
	Status        string              `json:"status"`
	Root          string              `json:"root"`
	Count         int                 `json:"count"`
	Extensions    []extensionManifest `json:"extensions"`
	Issues        []string            `json:"issues,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	global := flag.NewFlagSet("vigil", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	configPath := global.String("config", "", "config file path")
	if err := global.Parse(args); err != nil {
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		printHelp()
		return 0
	}
	command := rest[0]
	commandArgs := rest[1:]
	switch command {
	case "help", "--help", "-h":
		printHelp()
		return 0
	case "list", "commands":
		return listCommands(commandArgs)
	case "version":
		fmt.Printf("vigil-core %s config_schema=%s\n", version, configSchemaVersion)
		return 0
	case "doctor":
		return doctor(*configPath, commandArgs)
	case "status":
		return status(*configPath, commandArgs)
	case "plan":
		return plan(*configPath, commandArgs)
	case "workflow:local":
		return workflowLocal(*configPath, commandArgs)
	case "verify":
		return verify(*configPath, commandArgs)
	case "hooks:install":
		return hooksInstall(commandArgs)
	case "hooks:pre-commit":
		return hookRun(*configPath, "pre-commit", commandArgs)
	case "hooks:pre-push":
		return hookRun(*configPath, "pre-push", commandArgs)
	case "checks:staged-sensitive":
		return checkStagedSensitive(commandArgs)
	case "checks:workspace-hygiene":
		return checkWorkspaceHygiene(commandArgs)
	case "checks:command-catalog":
		return checkCommandCatalog(commandArgs)
	case "checks:public-assumptions":
		return checkPublicAssumptions(commandArgs)
	case "deps:inventory":
		return depsInventory(commandArgs)
	case "support:bundle":
		return supportBundle(*configPath, commandArgs)
	case "completion":
		return completion(commandArgs)
	case "config:schema":
		return printConfigSchema(commandArgs)
	case "config:init":
		return initConfig(*configPath, commandArgs)
	case "config:validate":
		return validateConfig(*configPath, commandArgs)
	case "extensions:list", "extensions:doctor":
		return extensionCommand(command, commandArgs)
	case "files:iterate":
		if !extensionCommandLoaded("files:iterate") {
			fmt.Fprintln(os.Stderr, "files:iterate is not provided by a valid loaded extension")
			return 2
		}
		return filesIterate(commandArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		printHelp()
		return 2
	}
}

func printHelp() {
	fmt.Println(`Vigil Core

Usage:
  vigil [--config PATH] <command> [args]

Commands:
  version
  list [--json]
  doctor [--json]
  status [--json]
  plan [--json]
  workflow:local [--json] [--dry-run]
  verify [--json]
  hooks:install
  hooks:pre-commit
  hooks:pre-push
  checks:staged-sensitive [--json]
  checks:workspace-hygiene [--json]
  checks:command-catalog [--json]
  checks:public-assumptions [--json]
  deps:inventory [--json]
  support:bundle [--json] [--dry-run]
  completion bash|zsh|fish
  config:schema [--json]
  config:init [--profile=go-tool|static-site|generic] [--write] [--force] [--json]
  config:validate [--json]
  extensions:list [--json]
  extensions:doctor [--json]
  files:iterate --root=PATH --glob=PATTERN [--jsonl]`)
}

type commandInfo struct {
	Command     string `json:"command"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

func listCommands(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	commands := activeCommands()
	if jsonOut {
		return printJSON(commands)
	}
	for _, command := range commands {
		fmt.Printf("%-22s %-10s %s\n", command.Command, command.Source, command.Description)
	}
	return 0
}

func printConfigSchema(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	schema := map[string]any{
		"schema_version": configSchemaVersion,
		"format":         "json",
		"required":       []string{"schema_version", "profile", "project", "authority", "gates", "extensions"},
		"profiles":       []string{"generic", "go-tool", "static-site"},
		"extension_manifest": map[string]any{
			"required": []string{"schema_version", "id", "name", "kind", "status", "private", "public_core", "description", "source_root", "packages", "commands"},
		},
	}
	if jsonOut {
		return printJSON(schema)
	}
	fmt.Println("Vigil config format: JSON")
	fmt.Println("Schema version: 1")
	fmt.Println("Required: schema_version, profile, project, authority, gates, extensions")
	return 0
}

func initConfig(configPath string, args []string) int {
	fs := flag.NewFlagSet("config:init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "generic", "profile")
	write := fs.Bool("write", false, "write config")
	force := fs.Bool("force", false, "overwrite existing config")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := templateConfig(*profile)
	if err := validateStruct(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data = append(data, '\n')
	path := resolvedConfigPath(configPath)
	if *write {
		if !*force && fileExists(path) {
			fmt.Fprintf(os.Stderr, "config already exists: %s\n", path)
			return 1
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": path, "written": true})
		}
		fmt.Printf("wrote %s\n", path)
		return 0
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "path": path, "written": false, "config": cfg})
	}
	fmt.Print(string(data))
	return 0
}

func validateConfig(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	path := resolvedConfigPath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return validationOutput(jsonOut, path, []string{err.Error()})
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return validationOutput(jsonOut, path, []string{"invalid JSON: " + err.Error()})
	}
	var issues []string
	if err := validateStruct(cfg); err != nil {
		issues = append(issues, err.Error())
	}
	return validationOutput(jsonOut, path, issues)
}

func validationOutput(jsonOut bool, path string, issues []string) int {
	status := "ok"
	exit := 0
	if len(issues) > 0 {
		status = "fail"
		exit = 1
	}
	if jsonOut {
		_ = printJSON(map[string]any{"status": status, "path": path, "issues": issues})
		return exit
	}
	if status == "ok" {
		fmt.Printf("[config] OK: %s\n", path)
	} else {
		fmt.Printf("[config] FAIL: %s\n", path)
		for _, issue := range issues {
			fmt.Printf("- %s\n", issue)
		}
	}
	return exit
}

func extensionCommand(command string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	report := loadExtensions(extensionRoot())
	if jsonOut {
		_ = printJSON(report)
	} else {
		fmt.Printf("[extensions] status=%s root=%s count=%d\n", report.Status, report.Root, report.Count)
		for _, ext := range report.Extensions {
			fmt.Printf("- %s (%s): %s\n", ext.ID, ext.Kind, ext.Description)
		}
		for _, issue := range report.Issues {
			fmt.Printf("[issue] %s\n", issue)
		}
	}
	if command == "extensions:doctor" && report.Status == "fail" {
		return 1
	}
	return 0
}

func activeCommands() []commandInfo {
	commands := []commandInfo{
		{Command: "checks:command-catalog", Source: "core", Description: "Audit the active command catalog for duplicate or malformed entries."},
		{Command: "checks:public-assumptions", Source: "core", Description: "Scan public Vigil source for product-specific assumptions and reserved category leaks."},
		{Command: "checks:staged-sensitive", Source: "core", Description: "Scan staged files for common secret patterns before commit."},
		{Command: "checks:workspace-hygiene", Source: "core", Description: "Detect local OS, editor, backup, and temporary artifacts."},
		{Command: "config:schema", Source: "core", Description: "Print the supported JSON config schema summary."},
		{Command: "config:init", Source: "core", Description: "Generate or write a starter JSON config."},
		{Command: "config:validate", Source: "core", Description: "Validate the effective JSON config."},
		{Command: "completion", Source: "core", Description: "Generate shell completion for bash, zsh, or fish."},
		{Command: "deps:inventory", Source: "core", Description: "Inventory common dependency manifests and lockfiles."},
		{Command: "doctor", Source: "core", Description: "Check local readiness for using Vigil Core in CI/CD workflows."},
		{Command: "extensions:list", Source: "core", Description: "List loaded extension manifests."},
		{Command: "extensions:doctor", Source: "core", Description: "Validate loaded extension manifests."},
		{Command: "hooks:install", Source: "core", Description: "Install Vigil git hook shims into the current repository."},
		{Command: "hooks:pre-commit", Source: "core", Description: "Run pre-commit gates from Vigil config."},
		{Command: "hooks:pre-push", Source: "core", Description: "Run pre-push gates from Vigil config."},
		{Command: "list", Source: "core", Description: "List core and loaded extension commands."},
		{Command: "plan", Source: "core", Description: "Explain which configured gates Vigil would run."},
		{Command: "status", Source: "core", Description: "Summarize config, extension, git, and command readiness."},
		{Command: "support:bundle", Source: "core", Description: "Write or preview a redacted local diagnostic bundle."},
		{Command: "verify", Source: "core", Description: "Run the public readiness proof set."},
		{Command: "version", Source: "core", Description: "Print Vigil Core version metadata."},
		{Command: "workflow:local", Source: "core", Description: "Run configured local CI/CD gates with optional dry-run."},
	}
	for _, ext := range loadExtensions(extensionRoot()).Extensions {
		for _, command := range ext.Commands {
			commands = append(commands, commandInfo{Command: command, Source: "extension:" + ext.ID, Description: ext.Description})
		}
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Command < commands[j].Command })
	return commands
}

func extensionCommandLoaded(command string) bool {
	for _, ext := range loadExtensions(extensionRoot()).Extensions {
		for _, candidate := range ext.Commands {
			if candidate == command {
				return true
			}
		}
	}
	return false
}

type checkResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func doctor(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	checks := []checkResult{
		commandCheck("git", "git executable"),
		commandCheck("bash", "bash executable"),
	}
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	if cfgErr != nil {
		checks = append(checks, checkResult{Name: "config", Status: "fail", Detail: cfgErr.Error()})
	} else if err := validateStruct(cfg); err != nil {
		checks = append(checks, checkResult{Name: "config", Status: "fail", Detail: cfgPath + ": " + err.Error()})
	} else {
		checks = append(checks, checkResult{Name: "config", Status: "ok", Detail: cfgPath})
	}
	ext := loadExtensions(extensionRoot())
	checks = append(checks, checkResult{Name: "extensions", Status: ext.Status, Detail: fmt.Sprintf("%d extension(s)", ext.Count)})
	status, failures := summarizeChecks(checks)
	if jsonOut {
		return printStatusJSON(map[string]any{"status": status, "checks": checks}, failures)
	}
	for _, check := range checks {
		fmt.Printf("[%s] %s: %s\n", check.Status, check.Name, check.Detail)
	}
	return failures
}

func status(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	ext := loadExtensions(extensionRoot())
	payload := map[string]any{
		"status":           "ok",
		"config_path":      cfgPath,
		"config_loaded":    cfgErr == nil,
		"extension_status": ext.Status,
		"extension_count":  ext.Count,
		"command_count":    len(activeCommands()),
		"git_root":         gitRoot(),
	}
	if cfgErr != nil {
		payload["status"] = "fail"
		payload["config_error"] = cfgErr.Error()
	} else {
		payload["project"] = cfg.Project
		payload["profile"] = cfg.Profile
		payload["gate_count"] = len(cfg.Gates)
	}
	if jsonOut {
		return printStatusJSON(payload, boolExit(payload["status"] != "ok"))
	}
	for key, value := range payload {
		fmt.Printf("%s=%v\n", key, value)
	}
	return boolExit(payload["status"] != "ok")
}

func plan(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg, _, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if jsonOut {
		return printJSON(map[string]any{"status": "ok", "gates": cfg.Gates})
	}
	for _, gate := range cfg.Gates {
		fmt.Printf("%s\tread_only=%t\t%s\n", gate.Name, gate.ReadOnly, gate.Command)
	}
	return 0
}

type gateResult struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
}

func workflowLocal(configPath string, args []string) int {
	fs := flag.NewFlagSet("workflow:local", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	dryRun := fs.Bool("dry-run", false, "show gates without running")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *dryRun {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "dry_run": true, "gates": cfg.Gates})
		}
		for _, gate := range cfg.Gates {
			fmt.Printf("[dry-run] %s: %s\n", gate.Name, gate.Command)
		}
		return 0
	}
	var results []gateResult
	failures := 0
	for _, gate := range cfg.Gates {
		start := time.Now()
		out, code := runShell(gate.Command)
		status := "ok"
		if code != 0 {
			status = "fail"
			failures++
		}
		result := gateResult{Name: gate.Name, Command: gate.Command, Status: status, ExitCode: code, DurationMS: time.Since(start).Milliseconds(), Output: trimOutput(out)}
		results = append(results, result)
		if !*jsonOut {
			fmt.Printf("[%s] %s (%d ms)\n", result.Status, result.Name, result.DurationMS)
			if result.Output != "" {
				fmt.Println(result.Output)
			}
		}
		if code != 0 {
			break
		}
	}
	status := "ok"
	if failures > 0 {
		status = "fail"
	}
	if *jsonOut {
		return printStatusJSON(map[string]any{"status": status, "results": results}, failures)
	}
	return boolExit(failures > 0)
}

func verify(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	checks := []checkResult{}
	if _, cfgPath, err := loadConfig(configPath); err != nil {
		checks = append(checks, checkResult{Name: "config:validate", Status: "fail", Detail: cfgPath + ": " + err.Error()})
	} else {
		checks = append(checks, checkResult{Name: "config:validate", Status: "ok", Detail: cfgPath})
	}
	ext := loadExtensions(extensionRoot())
	checks = append(checks, checkResult{Name: "extensions:doctor", Status: ext.Status, Detail: fmt.Sprintf("%d extension(s)", ext.Count)})
	catalogIssues := commandCatalogIssues()
	checks = append(checks, checkResult{Name: "checks:command-catalog", Status: okFail(len(catalogIssues)), Detail: fmt.Sprintf("%d issue(s)", len(catalogIssues))})
	assumptions := publicAssumptionFindings()
	checks = append(checks, checkResult{Name: "checks:public-assumptions", Status: okFail(len(assumptions)), Detail: fmt.Sprintf("%d finding(s)", len(assumptions))})
	status, failures := summarizeChecks(checks)
	if jsonOut {
		return printStatusJSON(map[string]any{"status": status, "checks": checks}, failures)
	}
	for _, check := range checks {
		fmt.Printf("[%s] %s\n", check.Status, check.Name)
	}
	return failures
}

func hooksInstall(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "unknown option: %s\n", args[0])
		return 2
	}
	root := gitRoot()
	if root == "" {
		fmt.Fprintln(os.Stderr, "not inside a git repository")
		return 1
	}
	hookDir := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, hook := range []string{"pre-commit", "pre-push"} {
		body := fmt.Sprintf("#!/usr/bin/env sh\nvigil hooks:%s \"$@\"\n", hook)
		if err := os.WriteFile(filepath.Join(hookDir, hook), []byte(body), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("installed %s\n", filepath.Join(hookDir, hook))
	}
	return 0
}

func hookRun(configPath, hook string, args []string) int {
	_ = args
	return workflowLocal(configPath, nil)
}

func checkStagedSensitive(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	files := gitLines("diff", "--cached", "--name-only", "--diff-filter=ACMR")
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[A-Za-z0-9_./+=-]{16,}`),
		regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----`),
	}
	var findings []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, pattern := range patterns {
			if pattern.Match(data) {
				findings = append(findings, file)
				break
			}
		}
	}
	return findingsOutput(jsonOut, "staged_sensitive", findings)
}

func checkWorkspaceHygiene(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var findings []string
	badNames := map[string]bool{".DS_Store": true, "Thumbs.db": true}
	_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "tmp") {
			return filepath.SkipDir
		}
		if badNames[entry.Name()] || strings.HasSuffix(entry.Name(), "~") || strings.HasSuffix(entry.Name(), ".bak") {
			findings = append(findings, path)
		}
		return nil
	})
	return findingsOutput(jsonOut, "workspace_hygiene", findings)
}

func checkCommandCatalog(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	issues := commandCatalogIssues()
	if jsonOut {
		return printStatusJSON(map[string]any{"status": okFail(len(issues)), "command_count": len(activeCommands()), "issues": issues}, len(issues))
	}
	if len(issues) == 0 {
		fmt.Printf("[command-catalog] OK: %d commands\n", len(activeCommands()))
		return 0
	}
	for _, issue := range issues {
		fmt.Println(issue)
	}
	return 1
}

func commandCatalogIssues() []string {
	seen := map[string]bool{}
	var issues []string
	for _, command := range activeCommands() {
		if strings.TrimSpace(command.Command) == "" || strings.TrimSpace(command.Description) == "" {
			issues = append(issues, "command has empty metadata")
		}
		if seen[command.Command] {
			issues = append(issues, "duplicate command: "+command.Command)
		}
		seen[command.Command] = true
	}
	return issues
}

func checkPublicAssumptions(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	findings := publicAssumptionFindings()
	return findingsOutput(jsonOut, "public_assumptions", findings)
}

func publicAssumptionFindings() []string {
	terms := []string{
		"paycal" + "-private",
		"/private" + "/var",
		"so" + "c\\s*2",
		"so" + "c2",
		"red" + "is",
		"led" + "ger",
		"m" + "cp",
		"mo" + "at",
		"github\\.com/vigil-ci" + "/vigil",
		"internal/(so" + "c2|led" + "ger|red" + "is|red" + "isaudit|red" + "isclient|m" + "cp|agent" + "meta|red" + "act|connection" + "audit)",
		"so" + "c2:",
		"led" + "ger:",
		"m" + "cp:smoke",
		"sync:red" + "is",
		"migrate:red" + "is",
		"business:connect" + "ions",
		"user:connect" + "ions",
	}
	deny := regexp.MustCompile("(?i)(" + strings.Join(terms, "|") + ")")
	var findings []string
	_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "tmp") {
			return filepath.SkipDir
		}
		if entry.IsDir() || strings.HasSuffix(path, ".sum") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil && deny.Match(data) {
			findings = append(findings, path)
		}
		return nil
	})
	sort.Strings(findings)
	return findings
}

func depsInventory(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	files := []string{"go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "composer.json", "composer.lock", "Cargo.toml", "Cargo.lock", "requirements.txt", "pyproject.toml"}
	found := map[string]string{}
	for _, file := range files {
		if data, err := os.ReadFile(file); err == nil {
			sum := sha256.Sum256(data)
			found[file] = fmt.Sprintf("%x", sum[:8])
		}
	}
	if jsonOut {
		return printJSON(map[string]any{"status": "ok", "files": found, "count": len(found)})
	}
	for file, sum := range found {
		fmt.Printf("%s\t%s\n", file, sum)
	}
	return 0
}

func supportBundle(configPath string, args []string) int {
	fs := flag.NewFlagSet("support:bundle", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	dryRun := fs.Bool("dry-run", false, "preview only")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	bundle := map[string]any{
		"schema_version": configSchemaVersion,
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"config_path":    cfgPath,
		"config_error":   "",
		"commands":       activeCommands(),
		"extensions":     loadExtensions(extensionRoot()),
		"git_status":     gitLines("status", "--short"),
	}
	if cfgErr != nil {
		bundle["config_error"] = cfgErr.Error()
	} else {
		bundle["config"] = cfg
	}
	if *dryRun {
		return printJSON(bundle)
	}
	_ = os.MkdirAll(filepath.Join("tmp", "vigil-support"), 0o755)
	path := filepath.Join("tmp", "vigil-support", "support-bundle-"+time.Now().UTC().Format("20060102T150405Z")+".json")
	data, _ := json.MarshalIndent(bundle, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "path": path})
	}
	fmt.Println(path)
	return 0
}

func completion(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil completion bash|zsh|fish")
		return 2
	}
	names := []string{}
	for _, command := range activeCommands() {
		names = append(names, command.Command)
	}
	joined := strings.Join(names, " ")
	switch args[0] {
	case "bash":
		fmt.Printf("complete -W %q vigil\n", joined)
	case "zsh":
		fmt.Println("#compdef vigil")
		fmt.Printf("_arguments '1:command:(%s)'\n", joined)
	case "fish":
		for _, name := range names {
			fmt.Printf("complete -c vigil -f -a %q\n", name)
		}
	default:
		fmt.Fprintln(os.Stderr, "Usage: vigil completion bash|zsh|fish")
		return 2
	}
	return 0
}

func filesIterate(args []string) int {
	fs := flag.NewFlagSet("files:iterate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "root directory")
	glob := fs.String("glob", "*", "file glob")
	jsonl := fs.Bool("jsonl", false, "emit JSON lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var matches []string
	err := filepath.WalkDir(*root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(*root, path)
		if err != nil {
			return err
		}
		ok, err := matchFileGlob(*glob, rel)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sort.Strings(matches)
	for _, rel := range matches {
		path := filepath.Join(*root, rel)
		info, err := os.Stat(path)
		if err != nil {
			return 1
		}
		if *jsonl {
			data, _ := json.Marshal(map[string]any{"path": rel, "size_bytes": info.Size()})
			fmt.Println(string(data))
		} else {
			fmt.Printf("%s\t%d\n", rel, info.Size())
		}
	}
	return 0
}

func matchFileGlob(pattern, rel string) (bool, error) {
	if strings.HasPrefix(pattern, "**/") {
		if ok, err := filepath.Match(strings.TrimPrefix(pattern, "**/"), filepath.Base(rel)); err != nil || ok {
			return ok, err
		}
		return filepath.Match(strings.TrimPrefix(pattern, "**/"), rel)
	}
	return filepath.Match(pattern, rel)
}

func loadExtensions(root string) extensionReport {
	report := extensionReport{SchemaVersion: configSchemaVersion, Status: "ok", Root: root}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return report
		}
		report.Status = "fail"
		report.Issues = append(report.Issues, err.Error())
		return report
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "extension.json")
		data, err := os.ReadFile(path)
		if err != nil {
			report.Issues = append(report.Issues, path+": "+err.Error())
			continue
		}
		var ext extensionManifest
		if err := json.Unmarshal(data, &ext); err != nil {
			report.Issues = append(report.Issues, path+": invalid JSON: "+err.Error())
			continue
		}
		ext.Path = path
		report.Issues = append(report.Issues, validateExtension(ext)...)
		report.Extensions = append(report.Extensions, ext)
	}
	sort.Slice(report.Extensions, func(i, j int) bool { return report.Extensions[i].ID < report.Extensions[j].ID })
	report.Count = len(report.Extensions)
	if len(report.Issues) > 0 {
		report.Status = "fail"
	}
	return report
}

func extensionRoot() string {
	if root := findUpward(mustGetwd(), "extensions"); root != "" {
		return root
	}
	return "extensions"
}

func findUpward(start, name string) string {
	dir := filepath.Clean(start)
	for {
		candidate := filepath.Join(dir, name)
		if extensionRootExists(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func extensionRootExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && fileExists(filepath.Join(path, entry.Name(), "extension.json")) {
			return true
		}
	}
	return false
}

func validateExtension(ext extensionManifest) []string {
	var issues []string
	required := map[string]string{
		"schema_version": ext.SchemaVersion,
		"id":             ext.ID,
		"name":           ext.Name,
		"kind":           ext.Kind,
		"status":         ext.Status,
		"description":    ext.Description,
		"source_root":    ext.SourceRoot,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			issues = append(issues, ext.Path+": missing "+field)
		}
	}
	if ext.SchemaVersion != "" && ext.SchemaVersion != configSchemaVersion {
		issues = append(issues, ext.Path+": unsupported schema_version "+ext.SchemaVersion)
	}
	if ext.ID != "" && !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(ext.ID) {
		issues = append(issues, ext.Path+": id must be lowercase kebab-case")
	}
	return issues
}

func templateConfig(profile string) config {
	cfg := config{
		SchemaVersion: configSchemaVersion,
		Profile:       profile,
		Project:       filepath.Base(mustGetwd()),
		Authority: authorityConfig{
			LocalFirst:       true,
			MutationRequires: []string{"explicit-confirmation", "clean-config"},
		},
		Gates: []gateConfig{
			{Name: "go test", Command: "go test ./...", ReadOnly: true, Tags: []string{"test"}},
		},
		Extensions: extensionsConfig{
			Enabled:        true,
			ManifestRoot:   "extensions",
			AllowedKinds:   []string{"custom"},
			RequirePrivate: false,
		},
	}
	switch profile {
	case "generic":
		cfg.Gates = []gateConfig{{Name: "status", Command: "git status --short", ReadOnly: true, Tags: []string{"diagnostic"}}}
	case "go-tool":
		cfg.Gates = []gateConfig{{Name: "go test", Command: "go test ./...", ReadOnly: true, Tags: []string{"test"}}, {Name: "go build", Command: "go build ./...", ReadOnly: true, Tags: []string{"build"}}}
	case "static-site":
		cfg.Gates = []gateConfig{{Name: "html/php lint", Command: "php -l index.php", ReadOnly: true, Tags: []string{"lint"}}}
	default:
		cfg.Profile = "generic"
		cfg.Gates = []gateConfig{{Name: "status", Command: "git status --short", ReadOnly: true, Tags: []string{"diagnostic"}}}
	}
	return cfg
}

func validateStruct(cfg config) error {
	var issues []string
	if cfg.SchemaVersion != configSchemaVersion {
		issues = append(issues, "schema_version must be "+configSchemaVersion)
	}
	if strings.TrimSpace(cfg.Profile) == "" {
		issues = append(issues, "profile is required")
	}
	if strings.TrimSpace(cfg.Project) == "" {
		issues = append(issues, "project is required")
	}
	if len(cfg.Authority.MutationRequires) == 0 {
		issues = append(issues, "authority.mutation_requires must name at least one confirmation requirement")
	}
	if len(cfg.Gates) == 0 {
		issues = append(issues, "gates must include at least one read-only diagnostic gate")
	}
	for i, gate := range cfg.Gates {
		if strings.TrimSpace(gate.Name) == "" || strings.TrimSpace(gate.Command) == "" {
			issues = append(issues, fmt.Sprintf("gates[%d] requires name and command", i))
		}
	}
	if cfg.Extensions.Enabled && strings.TrimSpace(cfg.Extensions.ManifestRoot) == "" {
		issues = append(issues, "extensions.manifest_root is required when extensions are enabled")
	}
	if len(issues) > 0 {
		return errors.New(strings.Join(issues, "; "))
	}
	return nil
}

func loadConfig(path string) (config, string, error) {
	resolved := resolvedConfigPath(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return config{}, resolved, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, resolved, err
	}
	return cfg, resolved, validateStruct(cfg)
}

func commandCheck(command, label string) checkResult {
	if path, err := exec.LookPath(command); err == nil {
		return checkResult{Name: label, Status: "ok", Detail: path}
	}
	return checkResult{Name: label, Status: "fail", Detail: command + " not found"}
}

func runCheck(name string, fn func() int) checkResult {
	code := fn()
	if code == 0 {
		return checkResult{Name: name, Status: "ok", Detail: "exit 0"}
	}
	return checkResult{Name: name, Status: "fail", Detail: fmt.Sprintf("exit %d", code)}
}

func summarizeChecks(checks []checkResult) (string, int) {
	failures := 0
	for _, check := range checks {
		if check.Status == "fail" {
			failures++
		}
	}
	if failures > 0 {
		return "fail", 1
	}
	return "ok", 0
}

func printStatusJSON(payload map[string]any, exit int) int {
	if _, ok := payload["status"]; !ok {
		payload["status"] = okFail(exit)
	}
	_ = printJSON(payload)
	return boolExit(exit != 0)
}

func okFail(count int) string {
	if count > 0 {
		return "fail"
	}
	return "ok"
}

func boolExit(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

func runShell(command string) (string, int) {
	cmd := exec.Command("bash", "-lc", command)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	return string(out) + err.Error(), 1
}

func trimOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > 8000 {
		return output[:8000] + "\n[truncated]"
	}
	return output
}

func gitRoot() string {
	out, code := runCommand("git", "rev-parse", "--show-toplevel")
	if code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitLines(args ...string) []string {
	out, code := runCommand("git", args...)
	if code != 0 {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func runCommand(name string, args ...string) (string, int) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	return string(out) + err.Error(), 1
}

func findingsOutput(jsonOut bool, name string, findings []string) int {
	sort.Strings(findings)
	status := okFail(len(findings))
	if jsonOut {
		return printStatusJSON(map[string]any{"status": status, "check": name, "findings": findings, "count": len(findings)}, len(findings))
	}
	if len(findings) == 0 {
		fmt.Printf("[%s] OK\n", name)
		return 0
	}
	for _, finding := range findings {
		fmt.Println(finding)
	}
	return 1
}

func parseJSONOnly(args []string) (bool, error) {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			return false, fmt.Errorf("unknown option: %s", arg)
		}
	}
	return jsonOut, nil
}

func resolvedConfigPath(path string) string {
	if strings.TrimSpace(path) != "" {
		return path
	}
	return defaultConfigName
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "project"
	}
	return wd
}

func printJSON(v any) int {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(string(data))
	return 0
}

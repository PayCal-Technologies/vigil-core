package main

import (
	"bufio"
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
	vigilCoreInstallRef = "7ae14422483359eb6a9d0c25cb827e7de392012d"
	vigilCoreGoVersion  = "1.26.0"
)

var promptReader = bufio.NewReader(os.Stdin)

type config struct {
	SchemaVersion            string            `json:"schema_version"`
	Profile                  string            `json:"profile"`
	Project                  string            `json:"project"`
	Authority                authorityConfig   `json:"authority"`
	Gates                    []gateConfig      `json:"gates"`
	Extensions               extensionsConfig  `json:"extensions"`
	PublicAssumptionPatterns []string          `json:"public_assumption_patterns,omitempty"`
	Metadata                 map[string]string `json:"metadata,omitempty"`
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
	EnabledIDs     []string `json:"enabled_ids,omitempty"`
	DisabledIDs    []string `json:"disabled_ids,omitempty"`
	RequirePrivate bool     `json:"require_private"`
}

type configIssue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type extensionManifest struct {
	SchemaVersion    string                     `json:"schema_version"`
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Kind             string                     `json:"kind"`
	Status           string                     `json:"status"`
	Private          bool                       `json:"private"`
	PublicCore       bool                       `json:"public_core"`
	Description      string                     `json:"description"`
	SourceRoot       string                     `json:"source_root"`
	Packages         []string                   `json:"packages"`
	Commands         []string                   `json:"commands"`
	CommandContracts []extensionCommandContract `json:"command_contracts,omitempty"`
	Path             string                     `json:"path,omitempty"`
}

type extensionCommandContract struct {
	Command     string   `json:"command"`
	Access      string   `json:"access"`
	Usage       string   `json:"usage"`
	Description string   `json:"description"`
	Examples    []string `json:"examples,omitempty"`
	InstallHint string   `json:"install_hint,omitempty"`
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
	args, authority := extractAuthorityArgs(args)
	global := flag.NewFlagSet("vigil", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	configPath := global.String("config", "", "config file path")
	if err := global.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*configPath) != "" {
		_ = os.Setenv("VIGIL_CONFIG", *configPath)
	}
	rest := global.Args()
	if len(rest) == 0 {
		printHelp()
		return 0
	}
	command := rest[0]
	commandArgs := rest[1:]
	requiresMutation := requiresMutationAuthority(command, commandArgs)
	if requiresMutation && !authority.Allowed(command) {
		return mutationAuthorityError(command, authority)
	}
	if requiresMutation && !mutationRequirementsSatisfied(*configPath, command, authority) {
		return 1
	}
	switch command {
	case "help", "--help", "-h":
		if len(commandArgs) > 0 {
			return commandHelp(commandArgs)
		}
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
	case "next":
		return next(*configPath, commandArgs)
	case "plan":
		return plan(*configPath, commandArgs)
	case "explain":
		return explain(commandArgs)
	case "workflow:local":
		return workflowLocal(*configPath, commandArgs, authority.AllowMutation)
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
		return checkPublicAssumptions(*configPath, commandArgs)
	case "checks:public-parity":
		return checkPublicParity(*configPath, commandArgs)
	case "checks:tracked-assistant-artifacts":
		return checkTrackedAssistantArtifacts(commandArgs)
	case "checks:release-policy":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return releasePolicy(commandArgs)
	case "deps:inventory":
		return depsInventory(commandArgs)
	case "support:bundle":
		return supportBundle(*configPath, commandArgs)
	case "completion":
		return completion(commandArgs)
	case "init:ci":
		return initCI(*configPath, commandArgs)
	case "github:init-ci":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return initCI(*configPath, append([]string{"--provider=github"}, commandArgs...))
	case "config:schema":
		return printConfigSchema(commandArgs)
	case "config:init":
		return initConfig(*configPath, commandArgs)
	case "config:validate":
		return validateConfig(*configPath, commandArgs)
	case "config:repair":
		return repairConfig(*configPath, commandArgs)
	case "config:migrate":
		return configMigrate(*configPath, commandArgs)
	case "config:report", "settings:show":
		return configReport(*configPath, commandArgs)
	case "config:template":
		return configTemplate(commandArgs)
	case "guards:summary":
		return guardsSummary(commandArgs)
	case "self-heal:plan":
		return selfHealPlan(*configPath, commandArgs)
	case "tools:catalog", "resources:catalog":
		return catalogCommand(command, commandArgs)
	case "extensions:list", "extensions:doctor":
		return extensionCommand(command, commandArgs)
	case "files:iterate":
		if !extensionCommandLoaded("files:iterate") {
			fmt.Fprintln(os.Stderr, "files:iterate is not provided by a valid loaded extension")
			return 2
		}
		return filesIterate(commandArgs)
	case "readme:generate", "readme:check":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return scribeCommand(command, commandArgs)
	case "a11y:inventory", "a11y:smoke", "a11y:ci", "a11y:pa11y", "a11y:lighthouse", "a11y:playwright":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return accessibilityCommand(command, commandArgs)
	case "checks:dependency-security", "deps:why", "npm:audit", "composer:validate", "php:lint", "phpstan:analyse", "security:gitleaks", "javascript:quality":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return adapterCommand(command, commandArgs)
	case "repo:health", "history:diagnose":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return repoHealth(commandArgs)
	case "deploy:verify":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return deployVerify(commandArgs)
	case "tests:history", "tests:affected":
		if !extensionCommandLoaded(command) {
			fmt.Fprintf(os.Stderr, "%s is not provided by a valid loaded extension\n", command)
			return 2
		}
		return testsCommand(command, commandArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		printHelp()
		return 2
	}
}

func printHelp() {
	commands := activeCommands()
	width := 0
	for _, command := range commands {
		if len(command.Command) > width {
			width = len(command.Command)
		}
	}
	fmt.Println("Vigil Core")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  vigil [--config PATH] [--allow-mutation|--auto] <command> [args]")
	fmt.Println()
	fmt.Println("core")
	for _, command := range commands {
		fmt.Printf("  %-3s %-*s   %s\n", helpAccessMarker(command.Command), width, command.Command, compactHelpDescription(command.Description))
	}
}

type commandInfo struct {
	Command     string   `json:"command"`
	Source      string   `json:"source"`
	Description string   `json:"description"`
	Access      string   `json:"access,omitempty"`
	AutoEnabled bool     `json:"auto_enabled,omitempty"`
	AutoReason  string   `json:"auto_reason,omitempty"`
	Usage       string   `json:"usage,omitempty"`
	InstallHint string   `json:"install_hint,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

type commandManual struct {
	Command     string   `json:"command"`
	Source      string   `json:"source"`
	Access      string   `json:"access"`
	Usage       string   `json:"usage"`
	Description string   `json:"description"`
	Examples    []string `json:"examples,omitempty"`
	InstallHint string   `json:"install_hint,omitempty"`
	Related     []string `json:"related,omitempty"`
}

type authorityArgs struct {
	AllowMutation bool
	Auto          bool
}

func extractAuthorityArgs(args []string) ([]string, authorityArgs) {
	var authority authorityArgs
	clean := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--allow-mutation":
			authority.AllowMutation = true
		case "--auto":
			authority.Auto = true
		default:
			clean = append(clean, arg)
		}
	}
	return clean, authority
}

func (a authorityArgs) Allowed(command string) bool {
	if a.AllowMutation {
		return true
	}
	return a.Auto && autoEnabledCommand(command)
}

func autoEnabledCommand(command string) bool {
	switch command {
	case "readme:generate":
		return true
	default:
		return false
	}
}

func requiresMutationAuthority(command string, args []string) bool {
	switch command {
	case "config:init":
		return hasFlag(args, "write") || hasFlag(args, "force")
	case "config:migrate":
		return hasFlag(args, "write")
	case "config:repair", "hooks:install":
		return true
	case "init:ci", "github:init-ci":
		return hasFlag(args, "write")
	case "readme:generate":
		return !hasFlag(args, "dry-run")
	case "support:bundle":
		return !hasFlag(args, "dry-run")
	default:
		return extensionCommandRequiresMutation(command)
	}
}

func extensionCommandRequiresMutation(command string) bool {
	access := extensionCommandAccess(command)
	return access == "r/w" || access == "w" || access == "write" || access == "conditional-write"
}

func extensionCommandAccess(command string) string {
	for _, ext := range loadExtensions(extensionRoot()).Extensions {
		for _, contract := range ext.CommandContracts {
			if contract.Command == command {
				return strings.TrimSpace(contract.Access)
			}
		}
	}
	return ""
}

func mutationRequirementsSatisfied(configPath string, command string, authority authorityArgs) bool {
	switch command {
	case "config:init", "config:repair", "config:migrate":
		return true
	}
	cfg, path, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s clean-config required before mutation: %s: %v\n", statusLabel("fail"), path, err)
		return false
	}
	requirements := cfg.Authority.MutationRequires
	if len(requirements) == 0 {
		requirements = []string{"explicit-confirmation", "clean-config"}
	}
	for _, requirement := range requirements {
		switch strings.TrimSpace(requirement) {
		case "", "explicit-confirmation":
			if !authority.AllowMutation && !authority.Auto {
				fmt.Fprintf(os.Stderr, "%s explicit-confirmation required before mutation\n", statusLabel("fail"))
				return false
			}
		case "clean-config":
			// loadConfig above validates the active config.
		case "clean-tree":
			if snapshot, ok := gitMutationFingerprint(); !ok {
				fmt.Fprintf(os.Stderr, "%s clean-tree required but git fingerprint failed\n", statusLabel("fail"))
				return false
			} else if !snapshot.Clean {
				fmt.Fprintf(os.Stderr, "%s clean-tree required before mutation\n", statusLabel("fail"))
				return false
			}
		default:
			fmt.Fprintf(os.Stderr, "%s unsupported mutation requirement: %s\n", statusLabel("fail"), requirement)
			return false
		}
	}
	return true
}

func hasFlag(args []string, name string) bool {
	short := "-" + name
	long := "--" + name
	for _, arg := range args {
		if arg == short || arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func mutationAuthorityError(command string, authority authorityArgs) int {
	fmt.Fprintf(os.Stderr, "%s mutation authority required for %s\n", statusLabel("fail"), command)
	if autoEnabledCommand(command) {
		fmt.Fprintf(os.Stderr, "rerun with --auto for deterministic, idempotent repair or --allow-mutation for explicit write authority\n")
	} else if authority.Auto {
		fmt.Fprintf(os.Stderr, "--auto is not available for %s; rerun with --allow-mutation after review\n", command)
	} else {
		fmt.Fprintf(os.Stderr, "rerun with --allow-mutation after review\n")
	}
	return 1
}

func commandHelp(args []string) int {
	fs := flag.NewFlagSet("help", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil help [--json] <command>")
		return 2
	}
	manual, ok := manualForCommand(fs.Arg(0))
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", fs.Arg(0))
		return 1
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "manual": manual})
	}
	fmt.Println(manual.Command)
	fmt.Printf("  source: %s\n", manual.Source)
	fmt.Printf("  access: %s\n", manual.Access)
	fmt.Printf("  usage: %s\n", manual.Usage)
	fmt.Printf("  about: %s\n", manual.Description)
	if manual.InstallHint != "" {
		fmt.Printf("  install: %s\n", manual.InstallHint)
	}
	for _, example := range manual.Examples {
		fmt.Printf("  example: %s\n", example)
	}
	return 0
}

func manualForCommand(name string) (commandManual, bool) {
	for _, info := range activeCommands() {
		if info.Command != name {
			continue
		}
		usage := info.Usage
		if usage == "" {
			usage = "vigil " + info.Command + " [args]"
		}
		access := info.Access
		if access == "" {
			access = helpAccessMarker(info.Command)
		}
		return commandManual{Command: info.Command, Source: info.Source, Access: access, Usage: usage, Description: info.Description, Examples: info.Examples, InstallHint: info.InstallHint, Related: relatedCommands(info.Command)}, true
	}
	return commandManual{}, false
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
			"optional": []string{"command_contracts"},
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
	issues := validateConfigIssues(cfg)
	if jsonOut {
		messages := issueMessages(issues)
		status := okFail(len(messages))
		return printStatusJSON(map[string]any{"status": status, "path": path, "issues": messages, "structured_issues": issues, "repair_command": "vigil config:repair"}, len(messages))
	}
	return validationOutput(false, path, issueMessages(issues))
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
		fmt.Printf("%s config: %s\n", statusLabel("ok"), path)
	} else {
		fmt.Printf("%s config: %s\n", statusLabel("fail"), path)
		for _, issue := range issues {
			fmt.Printf("- %s\n", issue)
		}
	}
	return exit
}

func configReport(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	report := map[string]any{
		"schema_version": configSchemaVersion,
		"status":         "ok",
		"path":           cfgPath,
		"format":         "json",
		"discovery":      []string{defaultConfigName, "--config PATH"},
		"git_root":       redactedPath(gitRoot()),
		"extensions":     loadExtensions(extensionRoot()),
		"commands":       activeCommands(),
	}
	if cfgErr != nil {
		report["status"] = "fail"
		report["error"] = cfgErr.Error()
	} else {
		report["config"] = cfg
		report["issues"] = validateConfigIssues(cfg)
	}
	if jsonOut {
		return printStatusJSON(report, boolExit(report["status"] != "ok"))
	}
	fmt.Printf("status=%s\npath=%s\ncommands=%d\nextensions=%d\n", report["status"], cfgPath, len(activeCommands()), loadExtensions(extensionRoot()).Count)
	return boolExit(report["status"] != "ok")
}

func configTemplate(args []string) int {
	fs := flag.NewFlagSet("config:template", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "generic", "profile")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := templateConfig(*profile)
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "profile": cfg.Profile, "config": cfg})
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(data))
	return 0
}

func configMigrate(configPath string, args []string) int {
	fs := flag.NewFlagSet("config:migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	write := fs.Bool("write", false, "write migrated config")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := resolvedConfigPath(configPath)
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, "invalid JSON: "+err.Error())
		return 1
	}
	before := cfg.SchemaVersion
	cfg = applyConfigDefaults(cfg, cfg.Profile)
	cfg.SchemaVersion = configSchemaVersion
	next, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	next = append(next, '\n')
	changed := string(next) != string(data)
	if *write && changed {
		if err := os.WriteFile(path, next, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	payload := map[string]any{"status": "ok", "path": path, "from_schema": before, "to_schema": configSchemaVersion, "changed": changed, "written": *write && changed}
	if *jsonOut {
		return printJSON(payload)
	}
	if changed && !*write {
		fmt.Printf("%s config migration available; rerun with --write\n", statusLabel("warn"))
		return 0
	}
	fmt.Printf("%s config schema=%s\n", statusLabel("ok"), configSchemaVersion)
	return 0
}

func explain(args []string) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil explain [--json] <command>")
		return 2
	}
	name := fs.Arg(0)
	if manual, ok := manualForCommand(name); ok {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "manual": manual})
		}
		fmt.Printf("%s\nsource=%s\naccess=%s\nusage=%s\n%s\n", manual.Command, manual.Source, manual.Access, manual.Usage, manual.Description)
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown command: %s\n", name)
	return 1
}

func catalogCommand(command string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	payload := map[string]any{"status": "ok"}
	if command == "tools:catalog" {
		payload["tools"] = activeCommands()
	} else {
		payload["resources"] = []map[string]string{
			{"uri": "vigil://config/report", "description": "Effective public config report"},
			{"uri": "vigil://commands/catalog", "description": "Loaded public command catalog"},
			{"uri": "vigil://extensions/catalog", "description": "Loaded public extension manifests"},
			{"uri": "vigil://support/dry-run", "description": "Preview support bundle payload"},
		}
	}
	if jsonOut {
		return printJSON(payload)
	}
	if command == "tools:catalog" {
		for _, info := range activeCommands() {
			fmt.Printf("%s\t%s\t%s\n", info.Command, info.Source, info.Description)
		}
		return 0
	}
	for _, resource := range payload["resources"].([]map[string]string) {
		fmt.Printf("%s\t%s\n", resource["uri"], resource["description"])
	}
	return 0
}

func guardsSummary(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	readOnly := []string{}
	mutating := []string{}
	for _, command := range activeCommands() {
		if helpAccessMarker(command.Command) == "r" {
			readOnly = append(readOnly, command.Command)
		} else {
			mutating = append(mutating, command.Command)
		}
	}
	payload := map[string]any{"status": "ok", "read_only": readOnly, "mutating": mutating, "read_only_count": len(readOnly), "mutating_count": len(mutating), "confirmation": "mutating commands require explicit human or CI intent"}
	if jsonOut {
		return printJSON(payload)
	}
	fmt.Printf("%s read=%d write=%d\n", statusLabel("ok"), len(readOnly), len(mutating))
	return 0
}

func selfHealPlan(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	actions := []map[string]string{}
	if _, _, err := loadConfig(configPath); err != nil {
		actions = append(actions, map[string]string{"action": "repair config", "command": "vigil config:repair", "risk": "writes config only after confirmation"})
	}
	ext := loadExtensions(extensionRoot())
	if ext.Status != "ok" {
		actions = append(actions, map[string]string{"action": "inspect extensions", "command": "vigil extensions:doctor", "risk": "read-only"})
	}
	if gitRoot() == "" {
		actions = append(actions, map[string]string{"action": "run inside git checkout", "command": "git status", "risk": "read-only"})
	}
	payload := map[string]any{"status": okFail(0), "actions": actions, "count": len(actions)}
	if jsonOut {
		return printJSON(payload)
	}
	if len(actions) == 0 {
		fmt.Printf("%s no self-heal actions needed\n", statusLabel("ok"))
		return 0
	}
	for _, action := range actions {
		fmt.Printf("%s\t%s\n", action["action"], action["command"])
	}
	return 0
}

func next(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	recommendations := []string{}
	if _, _, err := loadConfig(configPath); err != nil {
		recommendations = append(recommendations, "vigil config:repair")
	}
	if loadExtensions(extensionRoot()).Status != "ok" {
		recommendations = append(recommendations, "vigil extensions:doctor")
	}
	recommendations = append(recommendations, "vigil verify --json")
	payload := map[string]any{"status": "ok", "recommendations": uniqueStrings(recommendations)}
	if jsonOut {
		return printJSON(payload)
	}
	for _, rec := range uniqueStrings(recommendations) {
		fmt.Println(rec)
	}
	return 0
}

func repairConfig(configPath string, args []string) int {
	fs := flag.NewFlagSet("config:repair", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "generic", "profile for defaults")
	yes := fs.Bool("yes", false, "accept defaults without prompting")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := resolvedConfigPath(configPath)
	cfg := templateConfig(*profile)
	existed := fileExists(path)
	replacedMalformed := false
	if existed {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &cfg); err != nil {
				if !*yes && !promptBool("Config is not valid JSON. Replace it with a valid template?", false) {
					fmt.Fprintln(os.Stderr, "config repair cancelled")
					return 1
				}
				cfg = templateConfig(*profile)
				replacedMalformed = true
			}
		}
	}
	before := validateConfigIssues(cfg)
	if existed && len(before) == 0 && !replacedMalformed {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": path, "changed": false, "issues": []configIssue{}})
		}
		fmt.Printf("%s config: %s\n", statusLabel("ok"), path)
		return 0
	}
	if *yes {
		cfg = applyConfigDefaults(cfg, *profile)
	} else {
		cfg = promptConfigRepair(cfg, *profile)
	}
	after := validateConfigIssues(cfg)
	if len(after) > 0 {
		if *jsonOut {
			return printStatusJSON(map[string]any{"status": "fail", "path": path, "issues": issueMessages(after), "structured_issues": after}, 1)
		}
		fmt.Fprintln(os.Stderr, "config still has issues:")
		for _, issue := range after {
			fmt.Fprintln(os.Stderr, "- "+issue.Message)
		}
		return 1
	}
	if !*yes && !promptBool("Write repaired config to "+path+"?", true) {
		fmt.Fprintln(os.Stderr, "config repair cancelled")
		return 1
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "path": path, "changed": true, "replaced_malformed": replacedMalformed, "fixed_issues": before})
	}
	fmt.Printf("%s repaired config: %s\n", statusLabel("ok"), path)
	return 0
}

func promptConfigRepair(cfg config, profile string) config {
	defaults := templateConfig(profile)
	if cfg.SchemaVersion != configSchemaVersion {
		cfg.SchemaVersion = promptString("schema_version", configSchemaVersion)
	}
	if strings.TrimSpace(cfg.Profile) == "" {
		cfg.Profile = promptString("profile", defaults.Profile)
	}
	if strings.TrimSpace(cfg.Project) == "" {
		cfg.Project = promptString("project", defaults.Project)
	}
	if len(cfg.Authority.MutationRequires) == 0 {
		cfg.Authority.LocalFirst = promptBool("authority.local_first", true)
		cfg.Authority.MutationRequires = splitCSV(promptString("authority.mutation_requires", strings.Join(defaults.Authority.MutationRequires, ",")))
	}
	if len(cfg.Gates) == 0 {
		if promptBool("Add a default read-only gate?", true) {
			cfg.Gates = defaults.Gates
		}
	}
	for i := range cfg.Gates {
		if strings.TrimSpace(cfg.Gates[i].Name) == "" {
			cfg.Gates[i].Name = promptString(fmt.Sprintf("gates[%d].name", i), "gate")
		}
		if strings.TrimSpace(cfg.Gates[i].Command) == "" {
			cfg.Gates[i].Command = promptString(fmt.Sprintf("gates[%d].command", i), "git status --short")
		}
	}
	if cfg.Extensions.Enabled && strings.TrimSpace(cfg.Extensions.ManifestRoot) == "" {
		cfg.Extensions.ManifestRoot = promptString("extensions.manifest_root", defaults.Extensions.ManifestRoot)
	}
	if len(cfg.Extensions.AllowedKinds) == 0 {
		cfg.Extensions.AllowedKinds = splitCSV(promptString("extensions.allowed_kinds", strings.Join(defaults.Extensions.AllowedKinds, ",")))
	}
	if len(cfg.Extensions.EnabledIDs) > 0 && len(cfg.Extensions.DisabledIDs) > 0 {
		cfg.Extensions.EnabledIDs = splitCSV(promptString("extensions.enabled_ids", strings.Join(cfg.Extensions.EnabledIDs, ",")))
		cfg.Extensions.DisabledIDs = splitCSV(promptString("extensions.disabled_ids", strings.Join(cfg.Extensions.DisabledIDs, ",")))
	}
	cfg.PublicAssumptionPatterns = repairPatternPrompts(cfg.PublicAssumptionPatterns)
	return cfg
}

func applyConfigDefaults(cfg config, profile string) config {
	defaults := templateConfig(profile)
	if cfg.SchemaVersion != configSchemaVersion {
		cfg.SchemaVersion = configSchemaVersion
	}
	if strings.TrimSpace(cfg.Profile) == "" {
		cfg.Profile = defaults.Profile
	}
	if strings.TrimSpace(cfg.Project) == "" {
		cfg.Project = defaults.Project
	}
	if len(cfg.Authority.MutationRequires) == 0 {
		cfg.Authority = defaults.Authority
	}
	if len(cfg.Gates) == 0 {
		cfg.Gates = defaults.Gates
	}
	for i := range cfg.Gates {
		if strings.TrimSpace(cfg.Gates[i].Name) == "" {
			cfg.Gates[i].Name = "gate"
		}
		if strings.TrimSpace(cfg.Gates[i].Command) == "" {
			cfg.Gates[i].Command = "git status --short"
		}
	}
	if cfg.Extensions.Enabled && strings.TrimSpace(cfg.Extensions.ManifestRoot) == "" {
		cfg.Extensions.ManifestRoot = defaults.Extensions.ManifestRoot
	}
	if len(cfg.Extensions.AllowedKinds) == 0 {
		cfg.Extensions.AllowedKinds = defaults.Extensions.AllowedKinds
	}
	if len(cfg.Extensions.EnabledIDs) > 0 && len(cfg.Extensions.DisabledIDs) > 0 {
		enabled := stringSet(cfg.Extensions.EnabledIDs)
		var disabled []string
		for _, id := range cfg.Extensions.DisabledIDs {
			if !enabled[id] {
				disabled = append(disabled, id)
			}
		}
		cfg.Extensions.DisabledIDs = disabled
	}
	cfg.PublicAssumptionPatterns = validPatternsOnly(cfg.PublicAssumptionPatterns)
	return cfg
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
		fmt.Printf("%s extensions root=%s count=%d\n", statusLabel(report.Status), report.Root, report.Count)
		for _, ext := range report.Extensions {
			fmt.Printf("- %s (%s): %s\n", ext.ID, ext.Kind, ext.Description)
		}
		for _, issue := range report.Issues {
			fmt.Printf("%s %s\n", statusLabel("fail"), issue)
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
		{Command: "checks:public-parity", Source: "core", Description: "Check public source stays inside configured public boundaries."},
		{Command: "checks:staged-sensitive", Source: "core", Description: "Scan staged files for common secret patterns before commit."},
		{Command: "checks:tracked-assistant-artifacts", Source: "core", Description: "Detect tracked local AI/editor instruction artifacts."},
		{Command: "checks:workspace-hygiene", Source: "core", Description: "Detect local OS, editor, backup, and temporary artifacts."},
		{Command: "config:schema", Source: "core", Description: "Print the supported JSON config schema summary."},
		{Command: "config:init", Source: "core", Description: "Generate or write a starter JSON config."},
		{Command: "config:validate", Source: "core", Description: "Validate the effective JSON config."},
		{Command: "config:repair", Source: "core", Description: "Interactively repair missing or broken Vigil JSON config fields."},
		{Command: "config:migrate", Source: "core", Description: "Migrate config files to the current schema."},
		{Command: "config:report", Source: "core", Description: "Report effective redacted Vigil configuration and discovery details."},
		{Command: "config:template", Source: "core", Description: "Print a versioned starter config for a selected profile."},
		{Command: "completion", Source: "core", Description: "Generate shell completion for bash, zsh, or fish."},
		{Command: "deps:inventory", Source: "core", Description: "Inventory common dependency manifests and lockfiles."},
		{Command: "doctor", Source: "core", Description: "Check local readiness for using Vigil Core in CI/CD workflows."},
		{Command: "explain", Source: "core", Description: "Explain a command's source, access, and usage."},
		{Command: "extensions:list", Source: "core", Description: "List loaded extension manifests."},
		{Command: "extensions:doctor", Source: "core", Description: "Validate loaded extension manifests."},
		{Command: "guards:summary", Source: "core", Description: "Summarize read-only and mutating command coverage."},
		{Command: "hooks:install", Source: "core", Description: "Install Vigil git hook shims into the current repository."},
		{Command: "hooks:pre-commit", Source: "core", Description: "Run pre-commit gates from Vigil config."},
		{Command: "hooks:pre-push", Source: "core", Description: "Run pre-push gates from Vigil config."},
		{Command: "init:ci", Source: "core", Description: "Generate CI workflow examples from loaded Vigil gates."},
		{Command: "list", Source: "core", Description: "List core and loaded extension commands."},
		{Command: "next", Source: "core", Description: "Prioritize next local setup and verification actions."},
		{Command: "plan", Source: "core", Description: "Explain which configured gates Vigil would run."},
		{Command: "resources:catalog", Source: "core", Description: "List public local diagnostic resources."},
		{Command: "self-heal:plan", Source: "core", Description: "Suggest safe repairs for local configuration and setup."},
		{Command: "settings:show", Source: "core", Description: "Alias for config:report."},
		{Command: "status", Source: "core", Description: "Summarize config, extension, git, and command readiness."},
		{Command: "support:bundle", Source: "core", Description: "Write or preview a redacted local diagnostic bundle."},
		{Command: "tools:catalog", Source: "core", Description: "List public tools and command contracts."},
		{Command: "verify", Source: "core", Description: "Run the public readiness proof set."},
		{Command: "version", Source: "core", Description: "Print Vigil Core version metadata."},
		{Command: "workflow:local", Source: "core", Description: "Run configured local CI/CD gates with optional dry-run."},
	}
	for _, ext := range loadExtensions(extensionRoot()).Extensions {
		contracts := map[string]extensionCommandContract{}
		for _, contract := range ext.CommandContracts {
			contracts[contract.Command] = contract
		}
		for _, command := range ext.Commands {
			contract := contracts[command]
			description := extensionCommandDescription(command, ext.Description)
			if strings.TrimSpace(contract.Description) != "" {
				description = contract.Description
			}
			commands = append(commands, commandInfo{Command: command, Source: "extension:" + ext.ID, Description: description, Access: contract.Access, Usage: contract.Usage, InstallHint: contract.InstallHint, Examples: contract.Examples})
		}
	}
	for i := range commands {
		if autoEnabledCommand(commands[i].Command) {
			commands[i].AutoEnabled = true
			commands[i].AutoReason = "deterministic idempotent repair"
		}
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Command < commands[j].Command })
	return commands
}

func relatedCommands(command string) []string {
	switch {
	case strings.HasPrefix(command, "config:"):
		return []string{"config:validate", "config:report", "config:repair"}
	case strings.HasPrefix(command, "extensions:"):
		return []string{"extensions:list", "extensions:doctor"}
	case strings.HasPrefix(command, "readme:"):
		return []string{"readme:generate", "readme:check"}
	case strings.HasPrefix(command, "a11y:"):
		return []string{"a11y:inventory", "a11y:smoke"}
	case strings.HasPrefix(command, "checks:"):
		return []string{"verify", "guards:summary"}
	default:
		return []string{"list", "explain"}
	}
}

func extensionCommandDescription(command, fallback string) string {
	descriptions := map[string]string{
		"a11y:ci":                    "Run configured public accessibility adapter checks.",
		"a11y:inventory":             "List available local accessibility tools.",
		"a11y:lighthouse":            "Run Lighthouse accessibility audit.",
		"a11y:pa11y":                 "Run Pa11y route audit.",
		"a11y:playwright":            "Run Playwright accessibility tests.",
		"a11y:smoke":                 "Run fast public accessibility smoke checks.",
		"checks:dependency-security": "Run public dependency and scanner adapters.",
		"checks:release-policy":      "Check generic release readiness.",
		"composer:validate":          "Run Composer manifest validation.",
		"deploy:verify":              "Verify a configured public endpoint.",
		"deps:why":                   "Find package references in known manifests.",
		"github:init-ci":             "Generate GitHub CI workflow from Vigil gates.",
		"history:diagnose":           "Alias for repo:health.",
		"javascript:quality":         "Run public JavaScript quality adapters.",
		"npm:audit":                  "Run npm audit.",
		"php:lint":                   "Run PHP syntax checks.",
		"phpstan:analyse":            "Run PHPStan analysis.",
		"readme:check":               "Check Scribe managed README block freshness.",
		"readme:generate":            "Generate Scribe managed README block.",
		"repo:health":                "Report lightweight git-history diagnostics.",
		"security:gitleaks":          "Run local secret scanner.",
		"tests:affected":             "List likely test files for changed files.",
		"tests:history":              "Summarize JUnit test history.",
	}
	if description, ok := descriptions[command]; ok {
		return description
	}
	return fallback
}

func helpAccessMarker(command string) string {
	switch command {
	case "config:init", "config:repair", "hooks:install", "hooks:pre-commit", "hooks:pre-push", "readme:generate", "support:bundle", "workflow:local":
		return "r/w"
	default:
		return "r"
	}
}

func compactHelpDescription(description string) string {
	description = strings.TrimSpace(description)
	replacements := []struct {
		old string
		new string
	}{
		{"Generate or write ", "Create "},
		{"Interactively repair ", "Repair "},
		{"Validate ", "Check "},
		{"Generate ", "Create "},
		{"Install ", "Install "},
		{"Inventory ", "List "},
		{"Summarize ", "Show "},
		{"Detect ", "Find "},
		{"Explain ", "Show "},
		{"Print ", "Show "},
		{"Run ", "Run "},
		{"Scan ", "Scan "},
		{"Write or preview ", "Create "},
	}
	for _, replacement := range replacements {
		if strings.HasPrefix(description, replacement.old) {
			description = replacement.new + strings.TrimPrefix(description, replacement.old)
			break
		}
	}
	for _, separator := range []string{" with ", " for ", " from ", " into ", " before ", ";", ","} {
		if idx := strings.Index(description, separator); idx > 0 {
			description = description[:idx]
			break
		}
	}
	const max = 36
	if len(description) <= max {
		return description
	}
	trimmed := strings.TrimSpace(description[:max-1])
	if idx := strings.LastIndex(trimmed, " "); idx >= 18 {
		trimmed = trimmed[:idx]
	}
	return trimmed + "..."
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
		fmt.Printf("%s %s: %s\n", statusLabel(check.Status), check.Name, check.Detail)
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

func filterGatesByTag(gates []gateConfig, tag string) []gateConfig {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return gates
	}
	filtered := make([]gateConfig, 0, len(gates))
	for _, gate := range gates {
		for _, candidate := range gate.Tags {
			if candidate == tag {
				filtered = append(filtered, gate)
				break
			}
		}
	}
	return filtered
}

func workflowLocal(configPath string, args []string, allowMutation bool) int {
	fs := flag.NewFlagSet("workflow:local", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	dryRun := fs.Bool("dry-run", false, "show gates without running")
	tagFilter := fs.String("tag", "", "run gates matching tag")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	gates := filterGatesByTag(cfg.Gates, *tagFilter)
	if *dryRun {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "dry_run": true, "gates": gates})
		}
		for _, gate := range gates {
			fmt.Printf("%s dry-run %s: %s\n", statusLabel("ok"), gate.Name, gate.Command)
		}
		return 0
	}
	var results []gateResult
	failures := 0
	for _, gate := range gates {
		if !gate.ReadOnly && !allowMutation {
			result := gateResult{Name: gate.Name, Command: gate.Command, Status: "fail", ExitCode: 1, Output: "mutation authority required for mutating gate; rerun workflow:local with --allow-mutation"}
			results = append(results, result)
			failures++
			if !*jsonOut {
				fmt.Fprintf(os.Stderr, "%s %s: %s\n", statusLabel("fail"), gate.Name, result.Output)
			}
			break
		}
		before, snapshotOK := gitMutationFingerprint()
		if gate.ReadOnly && !snapshotOK {
			result := gateResult{Name: gate.Name, Command: gate.Command, Status: "fail", ExitCode: 1, Output: "read-only mutation fingerprint unavailable before gate"}
			results = append(results, result)
			failures++
			if !*jsonOut {
				fmt.Fprintf(os.Stderr, "%s %s: %s\n", statusLabel("fail"), gate.Name, result.Output)
			}
			break
		}
		start := time.Now()
		out, code := runShell(gate.Command)
		status := "ok"
		if code != 0 {
			status = "fail"
			failures++
		}
		if gate.ReadOnly {
			after, afterOK := gitMutationFingerprint()
			if !afterOK {
				status = "fail"
				if code == 0 {
					failures++
				}
				code = 1
				out = strings.TrimSpace(out + "\nread-only mutation fingerprint unavailable after gate")
			} else if after.Hash != before.Hash {
				status = "fail"
				if code == 0 {
					failures++
				}
				code = 1
				out = strings.TrimSpace(out + "\nread-only gate changed git workspace fingerprint")
			}
		}
		result := gateResult{Name: gate.Name, Command: gate.Command, Status: status, ExitCode: code, DurationMS: time.Since(start).Milliseconds(), Output: trimOutput(out)}
		results = append(results, result)
		if !*jsonOut {
			fmt.Printf("%s %s (%d ms)\n", statusLabel(result.Status), result.Name, result.DurationMS)
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
	assumptions, assumptionErr := publicAssumptionFindings(configPath)
	if assumptionErr != nil {
		checks = append(checks, checkResult{Name: "checks:public-assumptions", Status: "fail", Detail: assumptionErr.Error()})
	} else {
		checks = append(checks, checkResult{Name: "checks:public-assumptions", Status: okFail(len(assumptions)), Detail: fmt.Sprintf("%d finding(s)", len(assumptions))})
	}
	status, failures := summarizeChecks(checks)
	if jsonOut {
		return printStatusJSON(map[string]any{"status": status, "checks": checks}, failures)
	}
	for _, check := range checks {
		fmt.Printf("%s %s\n", statusLabel(check.Status), check.Name)
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
	hookBodies := map[string]string{}
	for _, hook := range []string{"pre-commit", "pre-push"} {
		body := fmt.Sprintf("#!/usr/bin/env sh\nvigil hooks:%s \"$@\"\n", hook)
		path := filepath.Join(hookDir, hook)
		if existing, err := os.ReadFile(path); err == nil && string(existing) != body {
			fmt.Fprintf(os.Stderr, "%s existing hook differs, refusing to overwrite: %s\n", statusLabel("fail"), path)
			fmt.Fprintln(os.Stderr, "move, back up, or chain the existing hook before rerunning hooks:install")
			return 1
		}
		hookBodies[path] = body
	}
	for path, body := range hookBodies {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("installed %s\n", path)
	}
	return 0
}

func hookRun(configPath, hook string, args []string) int {
	_ = args
	switch hook {
	case "pre-commit":
		return workflowLocal(configPath, []string{"--tag=pre-commit"}, false)
	case "pre-push":
		return workflowLocal(configPath, []string{"--tag=pre-push"}, false)
	default:
		return workflowLocal(configPath, nil, false)
	}
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

func checkTrackedAssistantArtifacts(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(^|/)\.cursor($|/)`),
		regexp.MustCompile(`(^|/)\.continue($|/)`),
		regexp.MustCompile(`(^|/)\.windsurf($|/)`),
		regexp.MustCompile(`(^|/)CLAUDE\.md$`),
		regexp.MustCompile(`(^|/)AGENTS\.local\.md$`),
		regexp.MustCompile(`(^|/)\.codex($|/)`),
	}
	var findings []string
	for _, file := range gitLines("ls-files") {
		for _, pattern := range patterns {
			if pattern.MatchString(file) {
				findings = append(findings, file)
				break
			}
		}
	}
	return findingsOutput(jsonOut, "tracked_assistant_artifacts", findings)
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
		fmt.Printf("%s command-catalog: %d commands\n", statusLabel("ok"), len(activeCommands()))
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

func checkPublicAssumptions(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	findings, err := publicAssumptionFindings(configPath)
	if err != nil {
		if jsonOut {
			return printStatusJSON(map[string]any{"status": "fail", "check": "public_assumptions", "error": err.Error()}, 1)
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return findingsOutput(jsonOut, "public_assumptions", findings)
}

func checkPublicParity(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var findings []string
	if found, err := publicAssumptionFindings(configPath); err != nil {
		findings = append(findings, err.Error())
	} else {
		findings = append(findings, found...)
	}
	for _, ext := range loadExtensions(extensionRoot()).Extensions {
		if ext.Private {
			findings = append(findings, ext.Path+": public extension manifest cannot be private")
		}
		if !ext.PublicCore {
			findings = append(findings, ext.Path+": public extension manifest must set public_core=true")
		}
	}
	return findingsOutput(jsonOut, "public_parity", findings)
}

func publicAssumptionFindings(configPath string) ([]string, error) {
	cfg, cfgPath, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	patterns := append([]string{}, cfg.PublicAssumptionPatterns...)
	for _, term := range strings.Split(os.Getenv("VIGIL_PUBLIC_ASSUMPTION_DENY"), ",") {
		term = strings.TrimSpace(term)
		if term != "" {
			patterns = append(patterns, regexp.QuoteMeta(term))
		}
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid public_assumption_patterns entry %q: %w", pattern, err)
		}
		compiled = append(compiled, re)
	}
	var findings []string
	_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "tmp") {
			return filepath.SkipDir
		}
		if entry.IsDir() || strings.HasSuffix(path, ".sum") || samePath(path, cfgPath) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, re := range compiled {
			if re.Match(data) {
				findings = append(findings, path)
				break
			}
		}
		return nil
	})
	sort.Strings(findings)
	return findings, nil
}

func initCI(configPath string, args []string) int {
	fs := flag.NewFlagSet("init:ci", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	provider := fs.String("provider", "github", "ci provider")
	write := fs.Bool("write", false, "write workflow file")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *provider != "github" {
		fmt.Fprintln(os.Stderr, "only --provider=github is supported")
		return 2
	}
	cfg, _, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	content := githubWorkflow(cfg)
	path := filepath.Join(".github", "workflows", "vigil.yml")
	if *write {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "provider": *provider, "path": path, "written": *write, "content": content})
	}
	if *write {
		fmt.Printf("%s wrote %s\n", statusLabel("ok"), path)
		return 0
	}
	fmt.Print(content)
	return 0
}

func githubWorkflow(cfg config) string {
	_ = cfg
	var b strings.Builder
	b.WriteString("name: Vigil\n\n")
	b.WriteString("on:\n  pull_request:\n  push:\n    branches: [main]\n\n")
	b.WriteString("jobs:\n  vigil:\n    runs-on: ubuntu-latest\n    steps:\n")
	b.WriteString("      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4\n")
	b.WriteString("      - uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5\n        with:\n          go-version: '" + goVersionForWorkflow() + "'\n")
	b.WriteString("      - name: Install Vigil\n        run: |\n          mkdir -p bin\n          GOBIN=\"$PWD/bin\" go install github.com/PayCal-Technologies/vigil-core/cmd/vigil@" + vigilCoreInstallRef + "\n          echo \"$PWD/bin\" >> \"$GITHUB_PATH\"\n")
	b.WriteString("      - name: Verify Vigil\n        run: vigil verify --json\n")
	b.WriteString("      - name: Run Vigil Gates\n        run: vigil workflow:local --json\n")
	return b.String()
}

func goVersionForWorkflow() string {
	return vigilCoreGoVersion
}

func yamlScalar(value string) string {
	value = strings.ReplaceAll(value, "'", "''")
	return "'" + value + "'"
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

func scribeCommand(command string, args []string) int {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("path", "README.md", "README path")
	dryRun := fs.Bool("dry-run", false, "preview changes")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	current, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	next, changed := renderScribeReadme(string(current))
	if command == "readme:check" {
		if *jsonOut {
			return printStatusJSON(map[string]any{"status": okFail(boolExit(changed)), "path": *path, "changed": changed}, boolExit(changed))
		}
		if changed {
			fmt.Fprintf(os.Stderr, "%s Scribe README block is stale; run: vigil readme:generate\n", statusLabel("fail"))
			return 1
		}
		fmt.Printf("%s Scribe README block is current\n", statusLabel("ok"))
		return 0
	}
	if *dryRun {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": *path, "changed": changed, "content": next})
		}
		fmt.Print(next)
		return 0
	}
	if !changed {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": *path, "changed": false})
		}
		fmt.Printf("%s README already current: %s\n", statusLabel("ok"), *path)
		return 0
	}
	if err := os.WriteFile(*path, []byte(next), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "path": *path, "changed": true})
	}
	fmt.Printf("%s updated README: %s\n", statusLabel("ok"), *path)
	return 0
}

func renderScribeReadme(current string) (string, bool) {
	block := scribeBlock()
	begin := "<!-- scribe:begin -->"
	end := "<!-- scribe:end -->"
	start := strings.Index(current, begin)
	stop := strings.Index(current, end)
	if start >= 0 && stop > start {
		stop += len(end)
		next := current[:start] + strings.TrimRight(block, "\n") + current[stop:]
		return next, next != current
	}
	insertAt := len(current)
	if idx := strings.Index(current, "\n## "); idx > 0 {
		insertAt = idx + 1
	}
	prefix := strings.TrimRight(current[:insertAt], "\n")
	suffix := strings.TrimLeft(current[insertAt:], "\n")
	next := prefix + "\n\n" + block + "\n"
	if suffix != "" {
		next += "\n" + suffix
	}
	return next, next != current
}

func scribeBlock() string {
	var b strings.Builder
	b.WriteString("<!-- scribe:begin -->\n")
	b.WriteString("## Repository Snapshot\n\n")
	b.WriteString("| Signal | Value |\n")
	b.WriteString("| --- | --- |\n")
	b.WriteString("| Repository | " + tableValue(filepath.Base(gitRoot())) + " |\n")
	b.WriteString("| Dependency manifests | " + tableValue(strings.Join(existingFiles(dependencyManifestFiles()), ", ")) + " |\n")
	b.WriteString("| Test paths | " + tableValue(strings.Join(existingTestPaths(), ", ")) + " |\n")
	b.WriteString("| Vigil commands | " + fmt.Sprintf("%d", len(activeCommands())) + " |\n")
	b.WriteString("\nGenerated by Vigil Scribe from local repository facts.\n")
	b.WriteString("<!-- scribe:end -->\n")
	return b.String()
}

func tableValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "not detected"
	}
	return strings.ReplaceAll(value, "|", "\\|")
}

func existingFiles(files []string) []string {
	var out []string
	for _, file := range files {
		if fileExists(file) {
			out = append(out, file)
		}
	}
	return out
}

func existingTestPaths() []string {
	candidates := []string{"test", "tests", "__tests__", "spec", "specs"}
	var out []string
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			out = append(out, path)
		}
	}
	return out
}

func accessibilityCommand(command string, args []string) int {
	switch command {
	case "a11y:inventory":
		jsonOut, err := parseJSONOnly(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		checks := []checkResult{
			optionalCommandCheck("npx", "npx"),
			optionalCommandCheck("pa11y", "pa11y"),
			optionalCommandCheck("lighthouse", "lighthouse"),
		}
		return checksOutput(jsonOut, "a11y_inventory", checks)
	case "a11y:smoke", "a11y:ci":
		return optionalAdapterChecks(command, args, []adapterSpec{
			{Name: "pa11y", Binary: "pa11y", Args: []string{"http://localhost:3000"}},
			{Name: "lighthouse", Binary: "lighthouse", Args: []string{"http://localhost:3000", "--only-categories=accessibility", "--quiet", "--chrome-flags=--headless"}},
		})
	case "a11y:pa11y":
		target := "http://localhost:3000"
		if len(args) > 0 && args[0] != "--json" {
			target = args[0]
			args = args[1:]
		}
		return runAdapterCommand("pa11y", "pa11y", append([]string{target}, args...)...)
	case "a11y:lighthouse":
		target := "http://localhost:3000"
		if len(args) > 0 && args[0] != "--json" {
			target = args[0]
			args = args[1:]
		}
		return runAdapterCommand("lighthouse", "lighthouse", append([]string{target, "--only-categories=accessibility"}, args...)...)
	case "a11y:playwright":
		return runAdapterCommand("playwright", "npx", append([]string{"playwright", "test"}, args...)...)
	default:
		return 2
	}
}

type adapterSpec struct {
	Name   string
	Binary string
	Args   []string
}

func optionalAdapterChecks(command string, args []string, specs []adapterSpec) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var checks []checkResult
	for _, spec := range specs {
		if _, err := exec.LookPath(spec.Binary); err != nil {
			checks = append(checks, checkResult{Name: spec.Name, Status: "warn", Detail: spec.Binary + " not found"})
			continue
		}
		checks = append(checks, runExternalCheck(spec.Name, spec.Binary, spec.Args...))
	}
	return checksOutput(jsonOut, command, checks)
}

func adapterCommand(command string, args []string) int {
	switch command {
	case "checks:dependency-security":
		return dependencySecurity(args)
	case "deps:why":
		return depsWhy(args)
	case "npm:audit":
		return runAdapterCommand("npm:audit", "npm", append([]string{"audit"}, args...)...)
	case "composer:validate":
		return runAdapterCommand("composer:validate", "composer", append([]string{"validate"}, args...)...)
	case "php:lint":
		return phpLint(args)
	case "phpstan:analyse":
		return runAdapterCommand("phpstan:analyse", "phpstan", args...)
	case "javascript:quality":
		return javascriptQuality(args)
	case "security:gitleaks":
		return runAdapterCommand("security:gitleaks", "gitleaks", append([]string{"detect"}, args...)...)
	default:
		return 2
	}
}

func javascriptQuality(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var checks []checkResult
	if !fileExists("package.json") {
		checks = append(checks, checkResult{Name: "javascript:quality", Status: "warn", Detail: "package.json not found"})
		return checksOutput(jsonOut, "javascript_quality", checks)
	}
	checks = append(checks, runExternalIfAvailable("npm:test", "npm", "test", "--", "--runInBand"))
	checks = append(checks, runExternalIfAvailable("npm:audit", "npm", "audit", "--audit-level=moderate"))
	return checksOutput(jsonOut, "javascript_quality", checks)
}

func phpLint(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	files := trackedFilesBySuffix(".php")
	if len(files) == 0 {
		return checksOutput(jsonOut, "php_lint", []checkResult{{Name: "php:lint", Status: "warn", Detail: "no tracked PHP files"}})
	}
	if _, err := exec.LookPath("php"); err != nil {
		return checksOutput(jsonOut, "php_lint", []checkResult{{Name: "php:lint", Status: "warn", Detail: "php not found"}})
	}
	var checks []checkResult
	for _, file := range files {
		checks = append(checks, runExternalCheck(file, "php", "-l", file))
		if len(checks) >= 25 {
			break
		}
	}
	return checksOutput(jsonOut, "php_lint", checks)
}

func dependencySecurity(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var checks []checkResult
	if fileExists("package.json") {
		checks = append(checks, runExternalIfAvailable("npm:audit", "npm", "audit", "--audit-level=moderate"))
	}
	if fileExists("composer.json") {
		checks = append(checks, runExternalIfAvailable("composer:audit", "composer", "audit"))
	}
	if gitRoot() != "" {
		checks = append(checks, runExternalIfAvailable("security:gitleaks", "gitleaks", "detect", "--redact"))
	}
	if len(checks) == 0 {
		checks = append(checks, checkResult{Name: "dependency-security", Status: "warn", Detail: "no supported manifests or tools detected"})
	}
	return checksOutput(jsonOut, "dependency_security", checks)
}

func depsWhy(args []string) int {
	fs := flag.NewFlagSet("deps:why", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil deps:why [--json] <package>")
		return 2
	}
	name := fs.Arg(0)
	var findings []string
	for _, file := range dependencyManifestFiles() {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(name)) {
			findings = append(findings, file)
		}
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": okFail(boolExit(len(findings) == 0)), "package": name, "files": findings})
	}
	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "%s package not found in known manifests: %s\n", statusLabel("fail"), name)
		return 1
	}
	for _, file := range findings {
		fmt.Println(file)
	}
	return 0
}

func dependencyManifestFiles() []string {
	return []string{"go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "composer.json", "composer.lock", "Cargo.toml", "Cargo.lock", "requirements.txt", "pyproject.toml"}
}

func runAdapterCommand(name, binary string, args ...string) int {
	if _, err := exec.LookPath(binary); err != nil {
		fmt.Fprintf(os.Stderr, "%s %s not found\n", statusLabel("fail"), binary)
		return 1
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s %s\n", statusLabel("ok"), name)
	return 0
}

func runExternalIfAvailable(name, binary string, args ...string) checkResult {
	if _, err := exec.LookPath(binary); err != nil {
		return checkResult{Name: name, Status: "warn", Detail: binary + " not found"}
	}
	return runExternalCheck(name, binary, args...)
}

func optionalCommandCheck(command, label string) checkResult {
	if path, err := exec.LookPath(command); err == nil {
		return checkResult{Name: label, Status: "ok", Detail: path}
	}
	return checkResult{Name: label, Status: "warn", Detail: command + " not found"}
}

func runExternalCheck(name, binary string, args ...string) checkResult {
	out, code := runCommand(binary, args...)
	if code == 0 {
		return checkResult{Name: name, Status: "ok", Detail: trimOneLine(out)}
	}
	return checkResult{Name: name, Status: "fail", Detail: trimOneLine(out)}
}

func checksOutput(jsonOut bool, name string, checks []checkResult) int {
	failures := 0
	for _, check := range checks {
		if check.Status == "fail" {
			failures++
		}
	}
	status := okFail(failures)
	if jsonOut {
		return printStatusJSON(map[string]any{"status": status, "check": name, "checks": checks}, failures)
	}
	for _, check := range checks {
		fmt.Printf("%s %s: %s\n", statusLabel(check.Status), check.Name, check.Detail)
	}
	return boolExit(failures > 0)
}

func repoHealth(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if gitRoot() == "" {
		fmt.Fprintln(os.Stderr, "not inside a git repository")
		return 1
	}
	commits := strings.TrimSpace(firstLine(gitLines("rev-list", "--count", "HEAD")))
	contributors := gitLines("shortlog", "-sn", "--all")
	churn := gitLines("log", "--name-only", "--pretty=format:")
	counts := map[string]int{}
	for _, file := range churn {
		file = strings.TrimSpace(file)
		if file != "" {
			counts[file]++
		}
	}
	hotspots := topCounts(counts, 10)
	payload := map[string]any{"status": "ok", "commit_count": commits, "contributors": contributors, "hotspots": hotspots}
	if jsonOut {
		return printJSON(payload)
	}
	fmt.Printf("%s commits=%s contributors=%d hotspots=%d\n", statusLabel("ok"), commits, len(contributors), len(hotspots))
	for _, item := range hotspots {
		fmt.Printf("%s\t%d\n", item.Name, item.Count)
	}
	return 0
}

func releasePolicy(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	checks := []checkResult{}
	if gitRoot() == "" {
		checks = append(checks, checkResult{Name: "git", Status: "fail", Detail: "not inside git repository"})
	} else if len(gitLines("status", "--short")) > 0 {
		checks = append(checks, checkResult{Name: "clean_tree", Status: "fail", Detail: "working tree has changes"})
	} else {
		checks = append(checks, checkResult{Name: "clean_tree", Status: "ok", Detail: "clean"})
	}
	if len(existingFiles([]string{"VERSION", "package.json", "composer.json", "go.mod"})) == 0 {
		checks = append(checks, checkResult{Name: "version_source", Status: "warn", Detail: "no common version source found"})
	} else {
		checks = append(checks, checkResult{Name: "version_source", Status: "ok", Detail: strings.Join(existingFiles([]string{"VERSION", "package.json", "composer.json", "go.mod"}), ", ")})
	}
	return checksOutput(jsonOut, "release_policy", checks)
}

func deployVerify(args []string) int {
	fs := flag.NewFlagSet("deploy:verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", "", "endpoint URL")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	checks := []checkResult{}
	if strings.TrimSpace(*url) == "" {
		checks = append(checks, checkResult{Name: "url", Status: "warn", Detail: "provide --url to verify an endpoint"})
		return checksOutput(*jsonOut, "deploy_verify", checks)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		checks = append(checks, checkResult{Name: "curl", Status: "warn", Detail: "curl not found"})
		return checksOutput(*jsonOut, "deploy_verify", checks)
	}
	checks = append(checks, runExternalCheck("endpoint", "curl", "-fsSIL", "--max-time", "10", *url))
	return checksOutput(*jsonOut, "deploy_verify", checks)
}

func testsCommand(command string, args []string) int {
	switch command {
	case "tests:history":
		return testsHistory(args)
	case "tests:affected":
		return testsAffected(args)
	default:
		return 2
	}
}

func testsHistory(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	files := []string{}
	_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == "tmp") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".xml") || strings.HasSuffix(entry.Name(), ".junit")) && strings.Contains(strings.ToLower(path), "junit") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	if jsonOut {
		return printJSON(map[string]any{"status": "ok", "files": files, "count": len(files)})
	}
	for _, file := range files {
		fmt.Println(file)
	}
	if len(files) == 0 {
		fmt.Printf("%s no JUnit history files found\n", statusLabel("warn"))
	}
	return 0
}

func testsAffected(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	changed := append(gitLines("diff", "--name-only", "HEAD"), gitLines("diff", "--cached", "--name-only")...)
	tests := map[string]bool{}
	for _, file := range changed {
		base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		for _, dir := range existingTestPaths() {
			_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
				if err == nil && !entry.IsDir() && strings.Contains(strings.ToLower(filepath.Base(path)), strings.ToLower(base)) {
					tests[path] = true
				}
				return nil
			})
		}
	}
	out := mapKeys(tests)
	if jsonOut {
		return printJSON(map[string]any{"status": "ok", "tests": out, "count": len(out)})
	}
	for _, file := range out {
		fmt.Println(file)
	}
	return 0
}

type countItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func topCounts(counts map[string]int, limit int) []countItem {
	items := make([]countItem, 0, len(counts))
	for name, count := range counts {
		items = append(items, countItem{Name: name, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Name < items[j].Name
		}
		return items[i].Count > items[j].Count
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func firstLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func trimOneLine(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "exit 0"
	}
	output = strings.ReplaceAll(output, "\n", " ")
	if len(output) > 160 {
		return output[:160] + "..."
	}
	return output
}

func trackedFilesBySuffix(suffix string) []string {
	var out []string
	for _, file := range gitLines("ls-files") {
		if strings.HasSuffix(file, suffix) {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return out
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func redactedPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Base(path)
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
	settings := extensionSettings()
	if !settings.Enabled {
		return report
	}
	enabledIDs := stringSet(settings.EnabledIDs)
	disabledIDs := stringSet(settings.DisabledIDs)
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
		if len(enabledIDs) > 0 && !enabledIDs[ext.ID] {
			continue
		}
		if disabledIDs[ext.ID] {
			continue
		}
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

func extensionSettings() extensionsConfig {
	settings := extensionsConfig{Enabled: true}
	path := resolvedConfigPath("")
	data, err := os.ReadFile(path)
	if err != nil {
		return settings
	}
	var raw struct {
		Extensions extensionsConfig `json:"extensions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return settings
	}
	settings = raw.Extensions
	if strings.TrimSpace(settings.ManifestRoot) == "" {
		settings.ManifestRoot = "extensions"
	}
	return settings
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func extensionRoot() string {
	settings := extensionSettings()
	if strings.TrimSpace(settings.ManifestRoot) != "" {
		if root := findUpward(mustGetwd(), settings.ManifestRoot); root != "" {
			return root
		}
		return settings.ManifestRoot
	}
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
	commands := stringSet(ext.Commands)
	for i, contract := range ext.CommandContracts {
		if strings.TrimSpace(contract.Command) == "" {
			issues = append(issues, fmt.Sprintf("%s: command_contracts[%d] missing command", ext.Path, i))
			continue
		}
		if !commands[contract.Command] {
			issues = append(issues, fmt.Sprintf("%s: command_contracts[%d] command is not listed in commands", ext.Path, i))
		}
		if contract.Access != "" && contract.Access != "r" && contract.Access != "r/w" && contract.Access != "w" {
			issues = append(issues, fmt.Sprintf("%s: command_contracts[%d] unsupported access", ext.Path, i))
		}
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
			{Name: "go test", Command: "go test ./...", ReadOnly: true, Tags: []string{"test", "pre-push"}},
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
		cfg.Gates = []gateConfig{{Name: "status", Command: "git status --short", ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	case "go-tool":
		cfg.Gates = []gateConfig{{Name: "go test", Command: "go test ./...", ReadOnly: true, Tags: []string{"test", "pre-push"}}, {Name: "go build", Command: "go build ./...", ReadOnly: true, Tags: []string{"build", "pre-push"}}}
	case "static-site":
		cfg.Gates = []gateConfig{{Name: "html/php lint", Command: "php -l index.php", ReadOnly: true, Tags: []string{"lint", "pre-commit"}}}
	default:
		cfg.Profile = "generic"
		cfg.Gates = []gateConfig{{Name: "status", Command: "git status --short", ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	}
	return cfg
}

func validateStruct(cfg config) error {
	issues := issueMessages(validateConfigIssues(cfg))
	if len(issues) > 0 {
		return errors.New(strings.Join(issues, "; "))
	}
	return nil
}

func validateConfigIssues(cfg config) []configIssue {
	var issues []configIssue
	add := func(field, code, message string) {
		issues = append(issues, configIssue{Field: field, Code: code, Message: message})
	}
	if cfg.SchemaVersion != configSchemaVersion {
		add("schema_version", "schema_version.invalid", "schema_version must be "+configSchemaVersion)
	}
	if strings.TrimSpace(cfg.Profile) == "" {
		add("profile", "profile.required", "profile is required")
	}
	if strings.TrimSpace(cfg.Project) == "" {
		add("project", "project.required", "project is required")
	}
	if len(cfg.Authority.MutationRequires) == 0 {
		add("authority.mutation_requires", "authority.mutation_requires.required", "authority.mutation_requires must name at least one confirmation requirement")
	}
	if len(cfg.Gates) == 0 {
		add("gates", "gates.required", "gates must include at least one read-only diagnostic gate")
	}
	for i, gate := range cfg.Gates {
		if strings.TrimSpace(gate.Name) == "" || strings.TrimSpace(gate.Command) == "" {
			add(fmt.Sprintf("gates[%d]", i), "gates.entry.invalid", fmt.Sprintf("gates[%d] requires name and command", i))
		}
	}
	if cfg.Extensions.Enabled && strings.TrimSpace(cfg.Extensions.ManifestRoot) == "" {
		add("extensions.manifest_root", "extensions.manifest_root.required", "extensions.manifest_root is required when extensions are enabled")
	}
	enabled := stringSet(cfg.Extensions.EnabledIDs)
	for _, id := range cfg.Extensions.DisabledIDs {
		if enabled[id] {
			add("extensions.disabled_ids", "extensions.selection.conflict", "extension cannot be both enabled and disabled: "+id)
		}
	}
	for i, pattern := range cfg.PublicAssumptionPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			add(fmt.Sprintf("public_assumption_patterns[%d]", i), "public_assumption_patterns.invalid", fmt.Sprintf("public_assumption_patterns[%d] is invalid: %v", i, err))
		}
	}
	return issues
}

func issueMessages(issues []configIssue) []string {
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		messages = append(messages, issue.Message)
	}
	return messages
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

type mutationFingerprint struct {
	Hash  string
	Clean bool
}

func gitMutationFingerprint() (mutationFingerprint, bool) {
	if gitRoot() == "" {
		return mutationFingerprint{}, false
	}
	status, code := runCommand("git", "status", "--porcelain")
	if code != 0 {
		return mutationFingerprint{}, false
	}
	unstaged, code := runCommand("git", "diff", "--binary")
	if code != 0 {
		return mutationFingerprint{}, false
	}
	staged, code := runCommand("git", "diff", "--cached", "--binary")
	if code != 0 {
		return mutationFingerprint{}, false
	}
	untracked, ok := untrackedFingerprint()
	if !ok {
		return mutationFingerprint{}, false
	}
	sum := sha256.Sum256([]byte(status + "\x00" + unstaged + "\x00" + staged + "\x00" + untracked))
	return mutationFingerprint{Hash: fmt.Sprintf("%x", sum[:]), Clean: strings.TrimSpace(status) == ""}, true
}

func untrackedFingerprint() (string, bool) {
	out, code := runCommand("git", "ls-files", "--others", "--exclude-standard")
	if code != 0 {
		return "", false
	}
	files := strings.Split(strings.TrimSpace(out), "\n")
	if len(files) == 1 && files[0] == "" {
		return "", true
	}
	var b strings.Builder
	root := gitRoot()
	for _, file := range files {
		path := filepath.Join(root, file)
		info, err := os.Stat(path)
		if err != nil {
			return "", false
		}
		if info.IsDir() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", false
		}
		sum := sha256.Sum256(data)
		b.WriteString(file)
		b.WriteByte('\x00')
		b.WriteString(fmt.Sprintf("%x", sum[:]))
		b.WriteByte('\n')
	}
	return b.String(), true
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
		fmt.Printf("%s %s\n", statusLabel("ok"), name)
		return 0
	}
	for _, finding := range findings {
		fmt.Println(finding)
	}
	return 1
}

func promptString(label, def string) string {
	fmt.Printf("%s [default: %s]: ", label, def)
	value, _ := promptReader.ReadString('\n')
	value = strings.TrimSpace(value)
	if value == "" {
		return def
	}
	return value
}

func promptBool(label string, def bool) bool {
	defText := "y"
	if !def {
		defText = "n"
	}
	for {
		value := strings.ToLower(promptString(label+" (y/n)", defText))
		switch value {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Println("Please answer y or n.")
	}
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func repairPatternPrompts(patterns []string) []string {
	var repaired []string
	for i, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err == nil {
			repaired = append(repaired, pattern)
			continue
		}
		if promptBool(fmt.Sprintf("Remove invalid public_assumption_patterns[%d]?", i), true) {
			continue
		}
		repaired = append(repaired, promptString(fmt.Sprintf("Replacement regex for public_assumption_patterns[%d]", i), ""))
	}
	return validPatternsOnly(repaired)
}

func validPatternsOnly(patterns []string) []string {
	var out []string
	for _, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err == nil {
			out = append(out, pattern)
		}
	}
	return out
}

func statusLabel(status string) string {
	label := "[" + strings.ToUpper(status) + "]"
	if !colorEnabled() {
		return label
	}
	switch strings.ToLower(status) {
	case "ok":
		return "\033[32m" + label + "\033[0m"
	case "fail":
		return "\033[31m" + label + "\033[0m"
	case "warn":
		return "\033[33m" + label + "\033[0m"
	default:
		return label
	}
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("CI") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func samePath(path, other string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path == other
	}
	absOther, err := filepath.Abs(other)
	if err != nil {
		return path == other
	}
	return absPath == absOther
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
	if envPath := strings.TrimSpace(os.Getenv("VIGIL_CONFIG")); envPath != "" {
		return envPath
	}
	if found := findFileUpward(mustGetwd(), defaultConfigName); found != "" {
		return found
	}
	return defaultConfigName
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findFileUpward(start, name string) string {
	dir := filepath.Clean(start)
	for {
		candidate := filepath.Join(dir, name)
		if fileExists(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
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

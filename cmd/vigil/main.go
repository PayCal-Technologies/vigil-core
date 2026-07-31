package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	report := loadExtensions("extensions")
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
		{Command: "version", Source: "core", Description: "Print Vigil Core version metadata."},
		{Command: "list", Source: "core", Description: "List core and loaded extension commands."},
		{Command: "config:schema", Source: "core", Description: "Print the supported JSON config schema summary."},
		{Command: "config:init", Source: "core", Description: "Generate or write a starter JSON config."},
		{Command: "config:validate", Source: "core", Description: "Validate the effective JSON config."},
		{Command: "extensions:list", Source: "core", Description: "List loaded extension manifests."},
		{Command: "extensions:doctor", Source: "core", Description: "Validate loaded extension manifests."},
	}
	for _, ext := range loadExtensions("extensions").Extensions {
		for _, command := range ext.Commands {
			commands = append(commands, commandInfo{Command: command, Source: "extension:" + ext.ID, Description: ext.Description})
		}
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Command < commands[j].Command })
	return commands
}

func extensionCommandLoaded(command string) bool {
	for _, ext := range loadExtensions("extensions").Extensions {
		for _, candidate := range ext.Commands {
			if candidate == command {
				return true
			}
		}
	}
	return false
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

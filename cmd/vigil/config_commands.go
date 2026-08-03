package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"

	vigilconfig "github.com/PayCal-Technologies/vigil-public/internal/config"

	vigilpacks "github.com/PayCal-Technologies/vigil-public/internal/packs"
	"os"

	"path/filepath"

	"strings"
)

type config = vigilconfig.Config
type gateConfig = vigilconfig.Gate

type extensionsConfig = vigilpacks.Settings

type configIssue = vigilconfig.Issue

type configDocument = vigilconfig.Document

type atomicWriteResult = atomicfile.Result

const maxConfigBytes = int64(4 * 1024 * 1024)

func printConfigSchema(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	schema := map[string]any{
		"schema_version": configSchemaVersion,
		"format":         "json",
		"required":       []string{"schema_version", "profile", "project", "coordination", "gates", "extensions"},
		"profiles":       []string{"generic", "go-tool", "static-site", "js-app", "php-app", "native-app", "mixed", "custom"},
		"gate_execution": map[string]any{
			"default":        "argv, sequential, required, fail-fast",
			"argv_fields":    []string{"command", "args"},
			"shell_opt_in":   "shell=true",
			"graph_fields":   []string{"depends_on", "parallel_group", "continue_on_error", "required"},
			"context_fields": []string{"timeout", "retry", "cwd", "environment", "artifacts"},
			"parallel_limit": "workflow:local --jobs=N",
		},
		"extension_manifest": map[string]any{
			"required": []string{"schema_version", "host_api_version", "id", "name", "kind", "status", "private", "public_core", "description", "source_root", "packages", "commands", "command_contracts"},
		},
		"plugin_policy": map[string]any{
			"optional": true,
			"default":  "enabled with explicit local acquisition allowed",
			"fields":   []string{"mode", "local", "require_signed", "min_signature_threshold", "allowed_ids", "denied_ids", "allowed_publisher_key_ids", "denied_capabilities"},
		},
	}
	if jsonOut {
		return printJSON(schema)
	}
	fmt.Println("Vigil config format: JSON")
	fmt.Println("Schema version: " + configSchemaVersion)
	fmt.Println("Required: schema_version, profile, project, coordination, gates, extensions")
	return exitSuccess
}

func initConfig(configPath string, args []string) int {
	fs := flag.NewFlagSet("config:init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "generic", "profile")
	write := fs.Bool("write", false, "write config")
	force := fs.Bool("force", false, "overwrite existing config")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	cfg := templateConfig(*profile)
	if err := validateStruct(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	data = append(data, '\n')
	path := resolvedConfigPath(configPath)
	if *write {
		if !*force && fileExists(path) {
			fmt.Fprintf(os.Stderr, "config already exists: %s\n", path)
			return exitPolicyBlocked
		}
		if _, err := atomicWriteFile(path, data, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitInternal
		}
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": path, "written": true})
		}
		fmt.Printf("wrote %s\n", path)
		return exitSuccess
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "path": path, "written": false, "config": cfg})
	}
	fmt.Print(string(data))
	return exitSuccess
}

func validateConfig(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	path := resolvedConfigPath(configPath)
	data, err := readConfigFile(path)
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
		exitCode := exitSuccess
		if len(messages) > 0 {
			exitCode = exitUsage
		}
		return printStatusJSON(map[string]any{"status": status, "path": path, "issues": messages, "structured_issues": issues, "repair_command": "vigil config:repair"}, exitCode)
	}
	return validationOutput(false, path, issueMessages(issues))
}

func validationOutput(jsonOut bool, path string, issues []string) int {
	status := "ok"
	exit := exitSuccess
	if len(issues) > 0 {
		status = "fail"
		exit = exitUsage
	}
	if jsonOut {
		_ = printJSON(map[string]any{"status": status, "path": path, "issues": issues}, exit)
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
		return exitUsage
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
		exitCode := exitSuccess
		if report["status"] != "ok" {
			exitCode = exitUsage
		}
		return printStatusJSON(report, exitCode)
	}
	fmt.Printf("status=%s\npath=%s\ncommands=%d\nextensions=%d\n", report["status"], cfgPath, len(activeCommands()), loadExtensions(extensionRoot()).Count)
	if report["status"] != "ok" {
		return exitUsage
	}
	return exitSuccess
}

func configTemplate(args []string) int {
	fs := flag.NewFlagSet("config:template", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "generic", "profile")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	cfg := templateConfig(*profile)
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "profile": cfg.Profile, "config": cfg})
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(data))
	return exitSuccess
}

func compareSchemaVersion(a string, b string) int {
	return vigilconfig.CompareSchemaVersion(a, b)
}

func readinessStatus(ok bool, state string) string {
	if ok && state == validConfigState() {
		return "ready"
	}
	if ok {
		return "repair_available"
	}
	return "changes_required"
}

func policyPreservationStatus(state string, diff map[string]any) string {
	if state == "legacy-v1" && diff != nil {
		return "verified"
	}
	if state == validConfigState() || state == "legacy-v2" {
		return "not_applicable"
	}
	return "pending"
}

func validConfigState() string {
	return "valid-v" + configSchemaVersion
}

func existingPathStatus(path string) string {
	if fileExists(path) {
		return "present"
	}
	return "not_configured"
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}

func configMigrate(configPath string, args []string) int {
	fs := flag.NewFlagSet("config:migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	write := fs.Bool("write", false, "write migrated config")
	jsonOut := fs.Bool("json", false, "json output")
	stream := fs.String("stream", "", "stream phase status: text or jsonl")
	verbose := fs.Bool("verbose", false, "stream text phase status")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	reporter, err := commandStreamReporter("config:migrate", *stream, *verbose, *jsonOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	path := resolvedConfigPath(configPath)
	readStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("read config", path)
	}
	data, err := readConfigFile(path)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("read config", exitUsage, time.Since(readStarted), err.Error())
		}
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if reporter != nil {
		_ = reporter.OK("read config", time.Since(readStarted), path)
	}
	parseStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("parse config", path)
	}
	doc, err := parseConfigDocument(data)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("parse config", exitUsage, time.Since(parseStarted), err.Error())
		}
		fmt.Fprintln(os.Stderr, "invalid JSON: "+err.Error())
		return exitUsage
	}
	if reporter != nil {
		_ = reporter.OK("parse config", time.Since(parseStarted), doc.SchemaVersion)
	}
	before := doc.SchemaVersion
	if before != "unknown" && compareSchemaVersion(before, configSchemaVersion) > 0 {
		fmt.Fprintf(os.Stderr, "config schema %s is newer than supported schema %s; refusing to downgrade\n", before, configSchemaVersion)
		return exitUsage
	}
	cfg := applyConfigDocumentDefaults(doc, doc.Config.Profile)
	cfg.SchemaVersion = configSchemaVersion
	next, err := marshalConfigDocument(doc.Raw, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	changed := string(next) != string(data)
	writeResult := atomicWriteResult{}
	if *write && changed {
		writeStarted := time.Now()
		if reporter != nil {
			_ = reporter.Start("write migrated config", path)
		}
		result, err := atomicWriteFile(path, next, true)
		if err != nil {
			if reporter != nil {
				_ = reporter.Fail("write migrated config", exitInternal, time.Since(writeStarted), err.Error())
			}
			fmt.Fprintln(os.Stderr, err)
			return exitInternal
		}
		writeResult = result
		if reporter != nil {
			_ = reporter.OK("write migrated config", time.Since(writeStarted), writeResult.BackupPath)
		}
	} else if reporter != nil {
		_ = reporter.Info("write migrated config", fmt.Sprintf("skipped changed=%t write=%t", changed, *write))
	}
	payload := map[string]any{"status": "ok", "path": path, "from_schema": before, "to_schema": configSchemaVersion, "changed": changed, "written": *write && changed, "backup_path": writeResult.BackupPath}
	if *jsonOut {
		return printJSON(payload)
	}
	if changed && !*write {
		fmt.Printf("%s config migration available; rerun with --write\n", statusLabel("warn"))
		return exitSuccess
	}
	fmt.Printf("%s config schema=%s\n", statusLabel("ok"), configSchemaVersion)
	return exitSuccess
}

func parseConfigDocument(data []byte) (configDocument, error) {
	return vigilconfig.ParseDocument(data)
}

func marshalConfigDocument(raw map[string]json.RawMessage, cfg config) ([]byte, error) {
	return vigilconfig.MarshalDocument(raw, cfg)
}

func repairConfig(configPath string, args []string) int {
	fs := flag.NewFlagSet("config:repair", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "generic", "profile for defaults")
	yes := fs.Bool("yes", false, "accept defaults without prompting")
	jsonOut := fs.Bool("json", false, "json output")
	stream := fs.String("stream", "", "stream phase status: text or jsonl")
	verbose := fs.Bool("verbose", false, "stream text phase status")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	reporter, err := commandStreamReporter("config:repair", *stream, *verbose, *jsonOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	path := resolvedConfigPath(configPath)
	if reporter != nil {
		_ = reporter.Start("inspect config", path)
	}
	cfg := config{}
	rawDoc := map[string]json.RawMessage{}
	existed := fileExists(path)
	replacedMalformed := false
	if existed {
		inspectStarted := time.Now()
		data, err := readConfigFile(path)
		if err != nil {
			if reporter != nil {
				_ = reporter.Fail("inspect config", exitInternal, time.Since(inspectStarted), err.Error())
			}
			fmt.Fprintln(os.Stderr, err)
			return exitInternal
		}
		if strings.TrimSpace(string(data)) != "" {
			doc, err := parseConfigDocument(data)
			if err != nil {
				if !*yes && !promptBool("Config is not valid JSON. Replace it with a valid template?", false) {
					fmt.Fprintln(os.Stderr, "config repair cancelled")
					return exitPolicyBlocked
				}
				cfg = templateConfig(*profile)
				replacedMalformed = true
			} else {
				if doc.SchemaVersion != "unknown" && compareSchemaVersion(doc.SchemaVersion, configSchemaVersion) > 0 {
					message := "config schema " + doc.SchemaVersion + " is newer than supported schema " + configSchemaVersion + "; refusing to downgrade"
					if *jsonOut {
						return printStatusJSON(map[string]any{"status": "fail", "path": path, "issues": []string{message}}, exitUsage)
					}
					fmt.Fprintln(os.Stderr, message)
					return exitUsage
				}
				cfg = doc.Config
				rawDoc = doc.Raw
			}
		}
	} else {
		cfg = templateConfig(*profile)
	}
	if reporter != nil {
		_ = reporter.OK("inspect config", 0, fmt.Sprintf("exists=%t", existed))
	}
	before := validateConfigIssues(cfg)
	if existed && len(before) == 0 && !replacedMalformed {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": path, "changed": false, "issues": []configIssue{}})
		}
		fmt.Printf("%s config: %s\n", statusLabel("ok"), path)
		return exitSuccess
	}
	if *yes {
		if reporter != nil {
			_ = reporter.Start("apply defaults", *profile)
		}
		if len(rawDoc) > 0 {
			cfg = applyConfigDocumentDefaults(configDocument{Config: cfg, Raw: rawDoc}, *profile)
		} else {
			cfg = applyConfigDefaults(cfg, *profile)
		}
		if reporter != nil {
			_ = reporter.OK("apply defaults", 0, *profile)
		}
	} else {
		cfg = promptConfigRepair(cfg, *profile)
	}
	after := validateConfigIssues(cfg)
	if len(after) > 0 {
		if reporter != nil {
			_ = reporter.Fail("validate repaired config", exitUsage, 0, issueMessages(after))
		}
		if *jsonOut {
			return printStatusJSON(map[string]any{"status": "fail", "path": path, "issues": issueMessages(after), "structured_issues": after}, exitUsage)
		}
		fmt.Fprintln(os.Stderr, "config still has issues:")
		for _, issue := range after {
			fmt.Fprintln(os.Stderr, "- "+issue.Message)
		}
		return exitUsage
	}
	if !*yes && !promptBool("Write repaired config to "+path+"?", true) {
		fmt.Fprintln(os.Stderr, "config repair cancelled")
		return exitPolicyBlocked
	}
	data, err := marshalConfigDocument(rawDoc, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	writeStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("write repaired config", path)
	}
	writeResult, err := atomicWriteFile(path, data, existed)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("write repaired config", exitInternal, time.Since(writeStarted), err.Error())
		}
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if _, _, err := loadConfig(path); err != nil {
		if reporter != nil {
			_ = reporter.Fail("validate repaired config", exitInternal, time.Since(writeStarted), err.Error())
		}
		fmt.Fprintln(os.Stderr, "post-write validation failed: "+err.Error())
		return exitInternal
	}
	if reporter != nil {
		_ = reporter.OK("write repaired config", time.Since(writeStarted), writeResult.BackupPath)
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "path": path, "changed": true, "replaced_malformed": replacedMalformed, "fixed_issues": before, "backup_path": writeResult.BackupPath})
	}
	fmt.Printf("%s repaired config: %s\n", statusLabel("ok"), path)
	return exitSuccess
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
	if len(cfg.Coordination.MutationRequires) == 0 {
		cfg.Coordination.Mode = promptString("coordination.mode", defaults.Coordination.Mode)
		if len(cfg.Coordination.AuthoritativeSurfaces) == 0 {
			cfg.Coordination.AuthoritativeSurfaces = defaults.Coordination.AuthoritativeSurfaces
		}
		cfg.Coordination.MutationRequires = splitCSV(promptString("coordination.mutation_requires", strings.Join(defaults.Coordination.MutationRequires, ",")))
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
			cfg.Gates[i].Command = promptString(fmt.Sprintf("gates[%d].command", i), "git")
			if cfg.Gates[i].Command == "git" && len(cfg.Gates[i].Args) == 0 {
				cfg.Gates[i].Args = []string{"status", "--short"}
			}
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
	return vigilconfig.ApplyDefaults(cfg, profile, filepath.Base(mustGetwd()))
}

func applyConfigDocumentDefaults(doc configDocument, profile string) config {
	return vigilconfig.ApplyDocumentDefaults(doc, profile, filepath.Base(mustGetwd()))
}

func templateConfig(profile string) config {
	return vigilconfig.Template(profile, filepath.Base(mustGetwd()))
}

func validateStruct(cfg config) error {
	return vigilconfig.Validate(cfg)
}

func validateConfigIssues(cfg config) []configIssue {
	return vigilconfig.ValidateIssues(cfg)
}

func issueMessages(issues []configIssue) []string {
	return vigilconfig.IssueMessages(issues)
}

func loadConfig(path string) (config, string, error) {
	resolved := resolvedConfigPath(path)
	data, err := readConfigFile(resolved)
	if err != nil {
		return config{}, resolved, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, resolved, err
	}
	return cfg, resolved, validateStruct(cfg)
}

func readConfigFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("config must be a regular non-symlink file: %s", path)
	}
	if info.Size() > maxConfigBytes {
		return nil, fmt.Errorf("config exceeds %d bytes: %s", maxConfigBytes, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, fmt.Errorf("config changed while opening: %s", path)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > maxConfigBytes {
		return nil, fmt.Errorf("config exceeds %d bytes: %s", maxConfigBytes, path)
	}
	return data, nil
}

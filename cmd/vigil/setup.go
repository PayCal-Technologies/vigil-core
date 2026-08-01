package main

import (
	"bufio"

	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"path/filepath"

	"strconv"
	"strings"
)

var promptReader = bufio.NewReader(os.Stdin)

func setupWizard(configPath string, args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "auto", "profile or auto")
	dryRun := fs.Bool("dry-run", false, "interactive preview without writing")
	write := fs.Bool("write", false, "write or repair config")
	force := fs.Bool("force", false, "overwrite existing config when writing")
	yes := fs.Bool("yes", false, "accept detected defaults without prompting")
	gatesCSV := fs.String("gates", "", "comma-separated gate names for non-interactive setup")
	installHooks := fs.Bool("install-hooks", false, "install Git hooks in non-interactive setup")
	noDoctor := fs.Bool("no-doctor", false, "skip doctor in non-interactive setup")
	workflowMode := fs.String("workflow", "dry-run", "non-interactive workflow mode: dry-run, execute, or skip")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch *workflowMode {
	case "dry-run", "execute", "skip":
	default:
		fmt.Fprintln(os.Stderr, "--workflow must be dry-run, execute, or skip")
		return exitUsage
	}
	if *yes && !*jsonOut {
		return runNonInteractiveSetupWizard(configPath, *profile, *write && !*dryRun, *force, *dryRun, *gatesCSV, *installHooks, !*noDoctor, *workflowMode)
	}
	if !*jsonOut && !*yes {
		if !isInteractiveTerminal() {
			fmt.Fprintln(os.Stderr, "setup wizard requires an interactive terminal; rerun with --yes for defaults or --json for deterministic output")
			return exitUsage
		}
		return runInteractiveSetupWizard(configPath, *profile, *write && !*dryRun, *force, *dryRun)
	}
	plan := buildSetupPlan(configPath, *profile)
	if *write && !*dryRun {
		if plan["overall"] == "blocked" {
			message := "setup write blocked by current repository state"
			plan["mode"] = "write"
			plan["execution_status"] = "blocked"
			if *jsonOut {
				return printStatusJSON(withSetupError(plan, message), exitPolicyBlocked)
			}
			fmt.Fprintln(os.Stderr, message)
			return exitPolicyBlocked
		}
		if plan["config_state"] == validConfigState() && !*force {
			plan["mode"] = "write"
			plan["execution_status"] = "skipped"
			plan["written"] = false
			plan["changed"] = false
			plan["backup_path"] = "none"
			delete(plan, "proposed_config")
			delete(plan, "raw_document")
			if *jsonOut {
				return printStatusJSON(plan, setupPlanExit(plan))
			}
			renderSetupPlan(plan)
			return setupPlanExit(plan)
		}
		cfg := plan["proposed_config"].(config)
		rawDoc, _ := plan["raw_document"].(map[string]json.RawMessage)
		data, err := marshalConfigDocument(rawDoc, cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitInternal
		}
		path := plan["config_path"].(string)
		writeResult, err := atomicWriteFile(path, data, fileExists(path))
		if err != nil {
			if *jsonOut {
				plan["mode"] = "write"
				plan["execution_status"] = "failed"
				return printStatusJSON(withSetupError(plan, err.Error()), exitInternal)
			}
			fmt.Fprintln(os.Stderr, err)
			return exitInternal
		}
		if _, _, err := loadConfig(path); err != nil {
			if *jsonOut {
				plan["mode"] = "write"
				plan["execution_status"] = "failed"
				return printStatusJSON(withSetupError(plan, "post-write validation failed: "+err.Error()), exitInternal)
			}
			fmt.Fprintln(os.Stderr, "post-write validation failed: "+err.Error())
			return exitInternal
		}
		plan = buildSetupPlan(configPath, *profile)
		plan["mode"] = "write"
		plan["execution_status"] = "applied"
		plan["written"] = true
		plan["backup_path"] = setupBackupPath(writeResult.BackupPath)
	} else {
		if plan["overall"] == "blocked" {
			plan["execution_status"] = "blocked"
		} else {
			plan["execution_status"] = "planned"
		}
		plan["written"] = false
	}
	delete(plan, "proposed_config")
	delete(plan, "raw_document")
	if *jsonOut {
		return printStatusJSON(plan, setupPlanExit(plan))
	}
	if *yes {
		renderSetupReview(setupWizardAnswers{
			Profile:       plan["profile"].(map[string]any)["selected"].(string),
			ConfigPath:    plan["config_path"].(string),
			CreateConfig:  plan["config_state"] != validConfigState(),
			RunDoctor:     true,
			WorkflowMode:  "dry-run",
			SelectedGates: templateConfig(plan["profile"].(map[string]any)["selected"].(string)).Gates,
		}, *write && !*dryRun)
	} else {
		renderSetupPlan(plan)
	}
	return setupPlanExit(plan)
}

func setupPlanExit(plan map[string]any) int {
	if plan["overall"] == "blocked" {
		return exitPolicyBlocked
	}
	return exitSuccess
}

type setupWizardAnswers struct {
	Profile       string
	ConfigPath    string
	CreateConfig  bool
	SelectedGates []gateConfig
	InstallHooks  bool
	InspectHooks  bool
	RunDoctor     bool
	WorkflowMode  string
}

func runInteractiveSetupWizard(configPath, requestedProfile string, write bool, force bool, dryRun bool) int {
	detected := detectSetupProfile()
	selectedProfile := firstNonEmpty(requestedProfile, "auto")
	if selectedProfile == "auto" {
		selectedProfile = fmt.Sprint(detected["primary"])
	}
	path := resolvedConfigPath(configPath)
	answers := setupWizardAnswers{Profile: selectedProfile, ConfigPath: path, RunDoctor: true, WorkflowMode: "dry-run"}
	fmt.Println("Vigil Setup Wizard")
	const (
		wizardProfile = iota
		wizardConfig
		wizardGates
		wizardHooks
		wizardDoctor
		wizardWorkflow
		wizardReview
	)
	state := wizardProfile
	for {
		var control string
		switch state {
		case wizardProfile:
			fmt.Println()
			fmt.Printf("Detected project profile: %s\n", profileDisplayName(selectedProfile))
			useProfile, nextControl := wizardBool("Use this profile?", answers.Profile == selectedProfile)
			control = nextControl
			if control == "" {
				if useProfile {
					answers.Profile = selectedProfile
				} else {
					answers.Profile, control = wizardSelect(
						"Select profile",
						[]string{"generic", "go-tool", "static-site", "js-app", "php-app", "native-app", "mixed", "custom"},
						answers.Profile,
					)
				}
			}
			if control == "" {
				answers.SelectedGates = append([]gateConfig{}, templateConfig(answers.Profile).Gates...)
				state = wizardConfig
			}
		case wizardConfig:
			fmt.Println()
			fmt.Println("Configuration file:")
			fmt.Printf("  %s\n", path)
			answers.CreateConfig, control = wizardBool("Create or update this file?", !fileExists(path) || force)
			if control == "" {
				state = wizardGates
			}
		case wizardGates:
			fmt.Println()
			fmt.Println("Select local workflow gates:")
			answers.SelectedGates, control = promptWizardGates(templateConfig(answers.Profile).Gates, answers.SelectedGates)
			if control == "" {
				state = wizardHooks
			}
		case wizardHooks:
			answers.InstallHooks, control = wizardBool("Install missing Git hooks?", answers.InstallHooks)
			if control == "" {
				answers.InspectHooks, control = wizardBool("Inspect existing hooks before installation?", true)
			}
			if control == "" {
				state = wizardDoctor
			}
		case wizardDoctor:
			answers.RunDoctor, control = wizardBool("Run readiness checks after setup?", answers.RunDoctor)
			if control == "" {
				state = wizardWorkflow
			}
		case wizardWorkflow:
			answers.WorkflowMode, control = wizardSelect(
				"Run the local workflow after setup?",
				[]string{"dry-run", "execute", "skip"},
				answers.WorkflowMode,
			)
			if control == "" {
				state = wizardReview
			}
		case wizardReview:
			fmt.Println()
			renderSetupReview(answers, write && !dryRun)
			confirmed, nextControl := wizardBool("Write configuration and complete setup?", true)
			control = nextControl
			if control == "" {
				if !confirmed {
					fmt.Println("setup cancelled")
					return exitInterrupted
				}
				if dryRun || !write {
					fmt.Println()
					fmt.Println("No files were written.")
					fmt.Println("To apply these selections, rerun:")
					fmt.Println("  " + setupRestartCommand(answers))
					return exitSuccess
				}
				if code := writeSetupConfig(answers, force); code != 0 {
					return code
				}
				return finishSetupWizard(answers)
			}
		}
		if control == "" {
			continue
		}
		if control == "quit" {
			fmt.Println("setup cancelled")
			return exitInterrupted
		}
		if control == "back" {
			if state > wizardProfile {
				state--
			} else {
				fmt.Println("already at the first setup step")
			}
		}
	}
}

func promptWizardGates(gates, current []gateConfig) ([]gateConfig, string) {
	selectedNames := stringSet(gateNamesSlice(current))
	selected := make([]gateConfig, 0, len(gates))
	for _, gate := range gates {
		include, control := wizardBool("  "+gate.Name+" ("+gateDisplayCommand(gate)+")?", selectedNames[gate.Name])
		if control != "" {
			return current, control
		}
		if include {
			selected = append(selected, gate)
		}
	}
	return selected, ""
}

func gateNamesSlice(gates []gateConfig) []string {
	names := make([]string, 0, len(gates))
	for _, gate := range gates {
		names = append(names, gate.Name)
	}
	return names
}

func runNonInteractiveSetupWizard(configPath, requestedProfile string, write bool, force bool, dryRun bool, gatesCSV string, installHooks bool, runDoctor bool, workflowMode string) int {
	detected := detectSetupProfile()
	profile := firstNonEmpty(requestedProfile, "auto")
	if profile == "auto" {
		profile = fmt.Sprint(detected["primary"])
	}
	cfg := templateConfig(profile)
	gates := cfg.Gates
	if strings.TrimSpace(gatesCSV) != "" {
		gates = selectGatesByName(cfg.Gates, splitCSV(gatesCSV))
	}
	answers := setupWizardAnswers{
		Profile:       profile,
		ConfigPath:    resolvedConfigPath(configPath),
		CreateConfig:  true,
		SelectedGates: gates,
		InstallHooks:  installHooks,
		InspectHooks:  true,
		RunDoctor:     runDoctor,
		WorkflowMode:  workflowMode,
	}
	renderSetupReview(answers, write && !dryRun)
	if dryRun || !write {
		fmt.Println()
		fmt.Println("No files were written.")
		return exitSuccess
	}
	if code := writeSetupConfig(answers, force); code != 0 {
		return code
	}
	return finishSetupWizard(answers)
}

func writeSetupConfig(answers setupWizardAnswers, force bool) int {
	cfg := templateConfig(answers.Profile)
	cfg.Gates = answers.SelectedGates
	if !answers.CreateConfig {
		return exitSuccess
	}
	data, err := marshalConfigDocument(map[string]json.RawMessage{}, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if !force && fileExists(answers.ConfigPath) {
		fmt.Fprintf(os.Stderr, "config already exists: %s\n", answers.ConfigPath)
		return exitPolicyBlocked
	}
	if _, err := atomicWriteFile(answers.ConfigPath, data, fileExists(answers.ConfigPath)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if _, _, err := loadConfig(answers.ConfigPath); err != nil {
		fmt.Fprintln(os.Stderr, "post-write validation failed: "+err.Error())
		return exitInternal
	}
	return exitSuccess
}

func finishSetupWizard(answers setupWizardAnswers) int {
	exitCode := exitSuccess
	hooksStatus := "unchanged"
	if answers.InstallHooks {
		if answers.InspectHooks {
			inspectHooks()
		}
		if code := hooksInstall(nil); code != 0 {
			hooksStatus = "failed"
			exitCode = preferExitCode(exitCode, code)
		} else {
			hooksStatus = "installed"
		}
	}
	doctorStatus := "skipped"
	if answers.RunDoctor {
		if code := doctor(answers.ConfigPath, nil); code == 0 {
			doctorStatus = "passed"
		} else {
			doctorStatus = "failed"
			exitCode = preferExitCode(exitCode, code)
		}
	}
	workflowStatus := "skipped"
	if answers.WorkflowMode == "dry-run" {
		if code := workflowLocal(answers.ConfigPath, []string{"--dry-run"}, false); code == 0 {
			workflowStatus = "dry-run passed"
		} else {
			workflowStatus = "dry-run failed"
			exitCode = preferExitCode(exitCode, code)
		}
	} else if answers.WorkflowMode == "execute" {
		if code := workflowLocal(answers.ConfigPath, nil, true); code == 0 {
			workflowStatus = "passed"
		} else {
			workflowStatus = "failed"
			exitCode = preferExitCode(exitCode, code)
		}
	}
	fmt.Println()
	if exitCode == exitSuccess {
		fmt.Println("Vigil setup complete.")
	} else {
		fmt.Println("Vigil setup completed with failures.")
	}
	fmt.Println()
	fmt.Printf("  Profile:       %s\n", answers.Profile)
	fmt.Printf("  Configuration: %s\n", answers.ConfigPath)
	fmt.Printf("  Hooks:         %s\n", hooksStatus)
	fmt.Printf("  Doctor:        %s\n", doctorStatus)
	fmt.Printf("  Workflow:      %s\n", workflowStatus)
	fmt.Println()
	fmt.Println("Next: vigil workflow:local")
	return exitCode
}

func setupRestartCommand(answers setupWizardAnswers) string {
	parts := []string{"vigil", "--allow-mutation", "setup:wizard", "--write", "--yes", "--profile=" + shellQuote(answers.Profile)}
	if len(answers.SelectedGates) > 0 {
		parts = append(parts, "--gates="+shellQuote(gateNames(answers.SelectedGates)))
	}
	if answers.InstallHooks {
		parts = append(parts, "--install-hooks")
	}
	if !answers.RunDoctor {
		parts = append(parts, "--no-doctor")
	}
	if answers.WorkflowMode != "" {
		parts = append(parts, "--workflow="+shellQuote(answers.WorkflowMode))
	}
	return strings.Join(parts, " ")
}

func renderSetupReview(answers setupWizardAnswers, write bool) {
	fmt.Println("Review configuration:")
	fmt.Println()
	fmt.Printf("  Profile:            %s\n", answers.Profile)
	fmt.Printf("  Config path:        %s\n", answers.ConfigPath)
	fmt.Printf("  Gates:              %s\n", gateNames(answers.SelectedGates))
	fmt.Printf("  Install hooks:      %s\n", yesNo(answers.InstallHooks))
	fmt.Printf("  Inspect hooks:      %s\n", yesNo(answers.InspectHooks))
	fmt.Printf("  Run doctor:         %s\n", yesNo(answers.RunDoctor))
	fmt.Printf("  Workflow mode:      %s\n", answers.WorkflowMode)
	if !write {
		fmt.Println()
		fmt.Println("Mode: preview. Mutation requires --allow-mutation and --write.")
	}
}

func wizardBool(label string, def bool) (bool, string) {
	for {
		value, control := wizardPrompt(label+" "+boolDefaultLabel(def)+":", "")
		if control != "" {
			return false, control
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return def, ""
		}
		switch value {
		case "y", "yes":
			return true, ""
		case "n", "no":
			return false, ""
		default:
			fmt.Println("Please answer y or n. Type help, back, or quit for wizard controls.")
		}
	}
}

func wizardSelect(label string, options []string, def string) (string, string) {
	defaultIndex := 1
	for i, option := range options {
		fmt.Printf("%d. %s\n", i+1, option)
		if option == def {
			defaultIndex = i + 1
		}
	}
	for {
		value, control := wizardPrompt(fmt.Sprintf("%s [%d]:", label, defaultIndex), "")
		if control != "" {
			return "", control
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return options[defaultIndex-1], ""
		}
		if idx, err := strconv.Atoi(value); err == nil && idx >= 1 && idx <= len(options) {
			return options[idx-1], ""
		}
		for _, option := range options {
			if value == option {
				return option, ""
			}
		}
		fmt.Println("Please select a listed number or value.")
	}
}

func wizardPrompt(label string, def string) (string, string) {
	for {
		fmt.Print(label + " ")
		value, err := promptReader.ReadString('\n')
		if err != nil && len(value) == 0 {
			fmt.Println()
			if err != io.EOF {
				fmt.Fprintln(os.Stderr, "setup input error:", err)
			}
			return "", "quit"
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(value) {
		case "help":
			fmt.Println("Enter accepts the default. Type back for the previous step, or quit to exit without writing.")
			continue
		case "back", "quit":
			return "", strings.ToLower(value)
		default:
			if value == "" {
				return def, ""
			}
			return value, ""
		}
	}
}

func profileDisplayName(profile string) string {
	names := map[string]string{
		"generic":     "Generic project",
		"go-tool":     "Go CLI tool",
		"static-site": "Static site",
		"js-app":      "JavaScript app",
		"php-app":     "PHP app",
		"native-app":  "Native app",
		"mixed":       "Mixed project",
		"custom":      "Custom project",
	}
	if name, ok := names[profile]; ok {
		return name
	}
	return profile
}

func boolDefaultLabel(def bool) string {
	if def {
		return "[Y/n]"
	}
	return "[y/N]"
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func gateNames(gates []gateConfig) string {
	names := make([]string, 0, len(gates))
	for _, gate := range gates {
		names = append(names, gate.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func selectGatesByName(gates []gateConfig, names []string) []gateConfig {
	wanted := stringSet(names)
	selected := []gateConfig{}
	for _, gate := range gates {
		if wanted[gate.Name] {
			selected = append(selected, gate)
		}
	}
	return selected
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("-_./:", r)
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func inspectHooks() {
	hookDir, err := gitHooksDir()
	if err != nil {
		fmt.Printf("%s %s\n", statusLabel("warn"), err)
		return
	}
	for _, hook := range []string{"pre-commit", "pre-push"} {
		path := filepath.Join(hookDir, hook)
		if !fileExists(path) {
			fmt.Printf("%s hook missing: %s\n", statusLabel("warn"), hook)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("%s hook unreadable: %s\n", statusLabel("warn"), hook)
			continue
		}
		sum := sha256.Sum256(data)
		fmt.Printf("%s hook present: %s sha256=%x\n", statusLabel("ok"), hook, sum[:6])
	}
}

func isInteractiveTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func buildSetupPlan(configPath string, requestedProfile string) map[string]any {
	path := resolvedConfigPath(configPath)
	detected := detectSetupProfile()
	selectedProfile := strings.TrimSpace(requestedProfile)
	if selectedProfile == "" || selectedProfile == "auto" {
		selectedProfile = detected["primary"].(string)
	}
	if selectedProfile == "" {
		selectedProfile = "generic"
	}
	proposed := templateConfig(selectedProfile)
	rawDoc := map[string]json.RawMessage{}
	state := "missing"
	issues := []configIssue{}
	warnings := []string{}
	blockers := []string{}
	var policyDiff map[string]any
	if data, err := readConfigFile(path); err != nil {
		if !os.IsNotExist(err) {
			state = "unreadable"
			blockers = append(blockers, err.Error())
			issues = append(issues, configIssue{Field: "config", Code: "config.unreadable", Message: err.Error()})
		} else {
			issues = append(issues, configIssue{Field: "config", Code: "config.missing", Message: "config file does not exist; setup --write can create it"})
		}
	} else {
		doc, err := parseConfigDocument(data)
		if err != nil {
			state = "malformed"
			issues = append(issues, configIssue{Field: "config", Code: "config.malformed", Message: "config is malformed JSON; setup --write can replace it after creating a backup"})
			warnings = append(warnings, "config is malformed JSON; setup --write will replace it after creating a backup")
		} else {
			rawDoc = doc.Raw
			proposed = applyConfigDocumentDefaults(doc, firstNonEmpty(doc.Config.Profile, selectedProfile))
			proposed.SchemaVersion = configSchemaVersion
			issues = validateConfigIssues(doc.Config)
			switch {
			case doc.SchemaVersion != "unknown" && compareSchemaVersion(doc.SchemaVersion, configSchemaVersion) > 0:
				state = "unsupported-newer-schema"
				blockers = append(blockers, "config schema "+doc.SchemaVersion+" is newer than supported schema "+configSchemaVersion)
			case doc.LegacyAuthority != nil || doc.SchemaVersion == "1":
				state = "legacy-v1"
				policyDiff = map[string]any{
					"legacy_mutation_requires":   doc.LegacyAuthority,
					"proposed_mutation_requires": proposed.Coordination.MutationRequires,
					"policy_removed":             false,
				}
			case doc.SchemaVersion != configSchemaVersion:
				state = "legacy-v" + doc.SchemaVersion
			case len(issues) > 0:
				state = "partial-v" + configSchemaVersion
			default:
				state = validConfigState()
			}
		}
	}
	if gitRoot() == "" {
		warnings = append(warnings, "not inside a Git checkout; Git hooks and GitHub handoff are optional/skipped")
	}
	validationAfter := nonNilConfigIssues(validateConfigIssues(proposed))
	issues = nonNilConfigIssues(issues)
	dimensions := map[string]string{
		"core_config":              readinessStatus(len(validationAfter) == 0, state),
		"policy_preservation":      policyPreservationStatus(state, policyDiff),
		"public_core_verification": "not_run",
		"project_preflight":        "not_run",
		"github_actions":           existingPathStatus(filepath.Join(".github", "workflows")),
		"git_hooks":                existingPathStatus(filepath.Join(".git", "hooks")),
	}
	overall := "ready_with_recommendations"
	if len(blockers) > 0 {
		overall = "blocked"
	} else if state == "missing" || strings.HasPrefix(state, "legacy-v") || strings.HasPrefix(state, "partial-v") || state == "malformed" || len(validationAfter) > 0 {
		overall = "changes_required"
	} else if dimensions["github_actions"] == "present" && dimensions["git_hooks"] == "present" {
		overall = "ready"
	}
	proposedMutations := []map[string]any{}
	if len(blockers) == 0 && state != validConfigState() {
		proposedMutations = append(proposedMutations, map[string]any{"id": "write-config", "mutates": true, "requires_confirmation": true, "path": path})
	}
	recommendedActions := setupRecommendedActions(overall)
	executionStatus := "planned"
	if overall == "blocked" {
		executionStatus = "blocked"
	}
	return map[string]any{
		"output_contract_version": "1",
		"status":                  okFail(len(blockers)),
		"overall":                 overall,
		"mode":                    "preview",
		"execution_status":        executionStatus,
		"config_path":             path,
		"config_state":            state,
		"profile": map[string]any{
			"selected":            selectedProfile,
			"requested":           requestedProfile,
			"detected":            detected["primary"],
			"confidence":          detected["confidence"],
			"profile_confidence":  detected["profile_confidence"],
			"evidence":            detected["evidence"],
			"detected_indicators": detected["detected_indicators"],
			"capabilities":        detected["capabilities"],
			"ambiguities":         detected["ambiguities"],
		},
		"readiness":           dimensions,
		"blockers":            blockers,
		"warnings":            warnings,
		"validation":          map[string]any{"current_issues": issues, "proposed_issues": validationAfter},
		"policy_diff":         policyDiff,
		"proposed_mutations":  proposedMutations,
		"recommended_actions": recommendedActions,
		"proposed_config":     proposed,
		"raw_document":        rawDoc,
	}
}

func setupBackupPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "none"
	}
	return path
}

func setupRecommendedActions(overall string) []map[string]any {
	if overall == "blocked" {
		return []map[string]any{
			{"id": "validate", "command": "vigil config:validate --json", "mutates": false, "requires_confirmation": false},
		}
	}
	return []map[string]any{
		{"id": "validate", "command": "vigil config:validate --json", "mutates": false, "requires_confirmation": false},
		{"id": "plan", "command": "vigil plan --json", "mutates": false, "requires_confirmation": false},
		{"id": "verify", "command": "vigil verify --json", "mutates": false, "requires_confirmation": false},
		{"id": "github-actions-helper", "command": "vigil github:init-ci", "mutates": false, "requires_confirmation": false},
		{"id": "git-hooks", "command": "vigil --allow-mutation hooks:install", "mutates": true, "requires_confirmation": true},
	}
}

func nonNilConfigIssues(issues []configIssue) []configIssue {
	if issues == nil {
		return []configIssue{}
	}
	return issues
}

func withSetupError(plan map[string]any, message string) map[string]any {
	next := map[string]any{}
	for key, value := range plan {
		next[key] = value
	}
	next["status"] = "fail"
	next["overall"] = "blocked"
	if _, ok := next["execution_status"]; !ok {
		next["execution_status"] = "failed"
	}
	next["blockers"] = append(stringSliceFromAny(next["blockers"]), message)
	delete(next, "proposed_config")
	delete(next, "raw_document")
	return next
}

func renderSetupPlan(plan map[string]any) {
	fmt.Printf("%s setup overall=%s config=%s\n", statusLabel(plan["status"].(string)), plan["overall"], plan["config_state"])
	if profile, ok := plan["profile"].(map[string]any); ok {
		fmt.Printf("profile=%v confidence=%v evidence=%v\n", profile["selected"], profile["confidence"], profile["evidence"])
	}
	for _, warning := range stringSliceFromAny(plan["warnings"]) {
		fmt.Printf("%s %s\n", statusLabel("warn"), warning)
	}
	for _, blocker := range stringSliceFromAny(plan["blockers"]) {
		fmt.Printf("%s %s\n", statusLabel("fail"), blocker)
	}
}

func detectSetupProfile() map[string]any {
	evidence := []string{}
	capabilities := []string{}
	ambiguities := []string{}
	hasGo := fileExists("go.mod")
	hasComposer := fileExists("composer.json")
	hasPackage := fileExists("package.json")
	hasNative := fileExists("CMakeLists.txt") || fileExists("Package.swift") || fileExists("Makefile") || hasFileWithExtension([]string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".m", ".mm", ".swift", ".qml", ".xaml", ".ps1"})
	hasStatic := hasFileWithExtension([]string{".html", ".css"})
	if hasGo {
		evidence = append(evidence, "go.mod")
		capabilities = append(capabilities, "go")
	}
	if hasComposer {
		evidence = append(evidence, "composer.json")
		capabilities = append(capabilities, "php")
	}
	if hasPackage {
		evidence = append(evidence, "package.json")
		capabilities = append(capabilities, "javascript-assets")
	}
	if hasNative {
		evidence = append(evidence, "native build/source files")
		capabilities = append(capabilities, "native")
	}
	if hasStatic {
		evidence = append(evidence, "html/css files")
		capabilities = append(capabilities, "static-assets")
	}
	primary := "generic"
	confidence := "medium"
	switch {
	case hasComposer:
		primary = "php-app"
		confidence = "high"
		if hasPackage {
			ambiguities = append(ambiguities, "package.json treated as supporting assets for php-app")
		}
	case hasGo:
		primary = "go-tool"
		confidence = "high"
	case hasPackage:
		primary = "js-app"
		confidence = "high"
	case hasNative:
		primary = "native-app"
		confidence = "medium"
	case hasStatic:
		primary = "static-site"
		confidence = "medium"
	default:
		confidence = "low"
	}
	if len(evidence) == 0 {
		evidence = append(evidence, "no known profile files detected")
	}
	profileConfidence := "certain"
	if confidence != "high" || len(ambiguities) > 0 {
		profileConfidence = "ambiguous"
	}
	return map[string]any{
		"primary":             primary,
		"confidence":          confidence,
		"profile_confidence":  profileConfidence,
		"evidence":            evidence,
		"detected_indicators": append([]string{}, evidence...),
		"capabilities":        uniqueStrings(capabilities),
		"ambiguities":         uniqueStrings(ambiguities),
	}
}

func hasFileWithExtension(extensions []string) bool {
	seen := stringSet(extensions)
	found := false
	_ = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if entry.IsDir() {
			if path != "." {
				switch entry.Name() {
				case ".git", "node_modules", "vendor", "bin", "tmp":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if seen[strings.ToLower(filepath.Ext(path))] {
			found = true
		}
		return nil
	})
	return found
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

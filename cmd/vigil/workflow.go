package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigilgit "github.com/PayCal-Technologies/vigil-public/internal/git"
	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"
	vigilpacks "github.com/PayCal-Technologies/vigil-public/internal/packs"
	vigilplan "github.com/PayCal-Technologies/vigil-public/internal/plan"
	vigilplugins "github.com/PayCal-Technologies/vigil-public/internal/plugins"
	"github.com/PayCal-Technologies/vigil-public/internal/runartifact"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
	vigilworkflow "github.com/PayCal-Technologies/vigil-public/internal/workflow"
)

const defaultWorkflowJobs = 4

func selfHealPlan(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
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
		return exitSuccess
	}
	for _, action := range actions {
		fmt.Printf("%s\t%s\n", action["action"], action["command"])
	}
	return exitSuccess
}

func next(configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
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
	return exitSuccess
}

type checkResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	ExitCode int    `json:"exit_code,omitempty"`
}

func doctor(configPath string, args []string) int {
	return doctorContext(context.Background(), configPath, args)
}

func doctorContext(ctx context.Context, configPath string, args []string) int {
	format, err := parseCheckFormat("doctor", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	checks := []checkResult{
		commandCheck("git", "git executable"),
		commandCheck("bash", "bash executable"),
	}
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	if cfgErr != nil {
		checks = append(checks, checkResult{Name: "config", Status: "fail", Detail: cfgErr.Error(), ExitCode: exitUsage})
	} else if err := validateStruct(cfg); err != nil {
		checks = append(checks, checkResult{Name: "config", Status: "fail", Detail: cfgPath + ": " + err.Error(), ExitCode: exitUsage})
	} else {
		checks = append(checks, checkResult{Name: "config", Status: "ok", Detail: cfgPath})
	}
	ext := loadExtensions(extensionRoot())
	extensionExit := exitSuccess
	if ext.Status != "ok" {
		extensionExit = exitUsage
	}
	checks = append(checks, checkResult{Name: "extensions", Status: ext.Status, Detail: fmt.Sprintf("%d extension(s)", ext.Count), ExitCode: extensionExit})
	checks = append(checks, pluginHealthCheck(ctx, configPath, "plugins"))
	return checkResultsOutput(format, "doctor", checks)
}

func statusContext(ctx context.Context, configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	ext := loadExtensions(extensionRoot())
	pluginReport, pluginErr := pluginDiscoveryForConfig(ctx, configPath, pluginReservedCommands(configPath))
	payload := map[string]any{
		"status":           "ok",
		"config_path":      cfgPath,
		"config_loaded":    cfgErr == nil,
		"extension_status": ext.Status,
		"extension_count":  ext.Count,
		"command_count":    len(activeCommandsForConfig(configPath)),
		"git_root":         gitRoot(),
	}
	exitCode := exitSuccess
	if cfgErr != nil {
		payload["status"] = "fail"
		payload["config_error"] = cfgErr.Error()
		exitCode = preferExitCode(exitCode, exitUsage)
	} else {
		payload["project"] = cfg.Project
		payload["profile"] = cfg.Profile
		payload["gate_count"] = len(cfg.Gates)
	}
	if ext.Status != "ok" {
		payload["status"] = "fail"
		payload["extension_issues"] = append([]string{}, ext.Issues...)
		exitCode = preferExitCode(exitCode, exitUsage)
	}
	if pluginErr != nil {
		payload["status"] = "fail"
		payload["plugin_status"] = "fail"
		payload["plugin_error"] = pluginErr.Error()
		exitCode = preferExitCode(exitCode, vigilplugins.ExitCode(pluginErr))
	} else {
		payload["plugin_status"] = pluginReport.Status
		payload["plugin_count"] = len(pluginReport.Plugins)
		payload["plugin_available_count"] = len(pluginReport.Available)
		payload["plugin_issues"] = pluginReport.Issues
		if pluginReport.Status != "ok" {
			payload["status"] = "fail"
			exitCode = preferExitCode(exitCode, vigilplugins.DiscoveryExit(pluginReport))
		}
	}
	if jsonOut {
		return printStatusJSON(payload, exitCode)
	}
	renderStatusSummary(payload)
	return exitCode
}

func renderStatusSummary(payload map[string]any) {
	status, _ := payload["status"].(string)
	attention := "Ready"
	if status != "ok" {
		attention = "Needs attention"
	}
	fmt.Printf("Project status: %s\n", attention)
	fmt.Println()
	fmt.Printf("Configuration       %s\n", statusReady(payload["config_loaded"] == true))
	fmt.Printf("Required tools      %s\n", statusReady(status == "ok"))
	fmt.Printf("Extensions          %s\n", statusReady(payload["extension_status"] == "ok"))
	fmt.Printf("Plugins             %s\n", statusReady(payload["plugin_status"] == "ok"))
	if gitRoot, _ := payload["git_root"].(string); strings.TrimSpace(gitRoot) == "" {
		fmt.Println("Git repository      Not detected")
	} else {
		fmt.Println("Git repository      Ready")
	}
	fmt.Println()
	fmt.Println("Recommended next step:")
	if status == "ok" {
		fmt.Println("Run `vigil check` before publishing or sharing this project.")
		return
	}
	if payload["config_loaded"] != true {
		fmt.Println("Run `vigil setup` or `vigil config:validate` and review the configuration issue.")
		return
	}
	fmt.Println("Run `vigil fix` to see safe next actions.")
}

func statusReady(ready bool) string {
	if ready {
		return "Ready"
	}
	return "Needs attention"
}

func pluginHealthCheck(ctx context.Context, configPath, name string) checkResult {
	report, err := pluginDiscoveryForConfig(ctx, configPath, pluginReservedCommands(configPath))
	if err != nil {
		return checkResult{Name: name, Status: "fail", Detail: err.Error(), ExitCode: vigilplugins.ExitCode(err)}
	}
	return checkResult{
		Name:     name,
		Status:   report.Status,
		Detail:   fmt.Sprintf("%d locked, %d available, %d issue(s)", len(report.Plugins), len(report.Available), len(report.Issues)),
		ExitCode: vigilplugins.DiscoveryExit(report),
	}
}

func plan(configPath string, args []string, allowMutation bool) int {
	finishOutput := ensureCommandOutput("plan")
	defer finishOutput()

	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	outputPath := fs.String("output", "", "write the reviewed plan to a private file")
	force := fs.Bool("force", false, "replace an existing plan file")
	tagFilter := fs.String("tag", "", "include checks matching tag")
	defaultTimeout := fs.Duration("timeout", 10*time.Minute, "default timeout for each check")
	maxParallel := fs.Int("jobs", defaultWorkflowJobs, "maximum checks in an explicit parallel group")
	if err := fs.Parse(args); err != nil {
		return vigilcli.ExitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: vigil plan [--json] [--output PATH] [--force] [--tag TAG] [--timeout DURATION] [--jobs N]")
		return vigilcli.ExitUsage
	}
	if *maxParallel < 1 || *maxParallel > 32 {
		fmt.Fprintln(os.Stderr, "--jobs must be between 1 and 32")
		return vigilcli.ExitUsage
	}
	if strings.TrimSpace(*outputPath) == "" && *force {
		fmt.Fprintln(os.Stderr, "--force requires --output")
		return vigilcli.ExitUsage
	}
	if strings.TrimSpace(*outputPath) != "" && !allowMutation {
		message := "writing a plan requires explicit mutation confirmation"
		if *jsonOut {
			return printJSON(map[string]any{"error": message}, vigilcli.ExitPolicyBlocked)
		}
		fmt.Fprintln(os.Stderr, message)
		return vigilcli.ExitPolicyBlocked
	}

	cfg, cfgPath, err := loadConfig(configPath)
	if err != nil {
		if *jsonOut {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitUsage)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	gates, err := filterGatesByTag(cfg.Gates, *tagFilter)
	if err != nil {
		if *jsonOut {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitUsage)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	document, err := buildWorkflowPlan(cfgPath, gates, *tagFilter, *defaultTimeout, *maxParallel, time.Now().UTC())
	if err != nil {
		if *jsonOut {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitInternal)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitInternal
	}

	writtenPath := ""
	if strings.TrimSpace(*outputPath) != "" {
		writtenPath, err = writeReviewedPlan(*outputPath, document, *force)
		if err != nil {
			if *jsonOut {
				return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitPolicyBlocked)
			}
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitPolicyBlocked
		}
	}
	if *jsonOut {
		payload := map[string]any{"plan": document}
		if writtenPath != "" {
			payload["output_path"] = writtenPath
			payload["artifacts"] = []vigiloutput.Artifact{{
				Kind:      "plan",
				Path:      writtenPath,
				MediaType: "application/vnd.vigil.plan+json;version=1",
				Digest:    document.PlanID,
			}}
		}
		return printJSON(payload)
	}
	fmt.Printf("Plan ID: %s\n", document.PlanID)
	fmt.Printf("Repository: %s @ %s\n", document.Inputs.RepositoryRoot, document.Inputs.RepositoryHead)
	for _, gate := range document.Gates {
		fmt.Printf("%s\tread_only=%t\t%s\n", gate.Name, gate.ReadOnly, gateDisplayCommand(gate))
	}
	if writtenPath != "" {
		fmt.Printf("Wrote private plan: %s\n", writtenPath)
	}
	return vigilcli.ExitSuccess
}

func applyPlanCommand(ctx context.Context, args []string, allowMutation bool) int {
	finishOutput := ensureCommandOutput("apply")
	defer finishOutput()

	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	formatValue := fs.String("format", "", "output format: text, json, jsonl, junit, or github")
	writeArtifacts := fs.Bool("artifacts", false, "write private result, stdout, and stderr artifacts")
	artifactsDir := fs.String("artifacts-dir", "", "artifact root (implies --artifacts)")
	if err := fs.Parse(args); err != nil {
		return vigilcli.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil --allow-mutation apply [--json|--format FORMAT] [--artifacts] [--artifacts-dir PATH] <plan-file>")
		return vigilcli.ExitUsage
	}
	format, err := vigiloutput.ResolveFormat(*jsonOut, *formatValue, vigiloutput.FormatText, vigiloutput.FormatJSON, vigiloutput.FormatJSONL, vigiloutput.FormatJUnit, vigiloutput.FormatGitHub)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	if !allowMutation {
		message := "applying a reviewed plan requires --allow-mutation"
		if format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"error": message}, vigilcli.ExitPolicyBlocked)
		}
		fmt.Fprintln(os.Stderr, message)
		return vigilcli.ExitPolicyBlocked
	}
	document, err := vigilplan.Read(fs.Arg(0))
	if err != nil {
		if format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitUsage)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	current, err := workflowInputs(document.Inputs.ConfigPath)
	if err != nil {
		if format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"plan_id": document.PlanID, "error": err.Error()}, vigilcli.ExitPolicyBlocked)
		}
		fmt.Fprintf(os.Stderr, "cannot verify reviewed plan inputs: %v\n", err)
		return vigilcli.ExitPolicyBlocked
	}
	mismatches := vigilplan.Compare(document.Inputs, current)
	if len(mismatches) > 0 {
		payload := map[string]any{
			"plan_id":    document.PlanID,
			"mismatches": mismatches,
			"error":      "reviewed plan is stale; generate and review a new plan",
		}
		if format == vigiloutput.FormatJSON {
			return printJSON(payload, vigilcli.ExitPolicyBlocked)
		}
		fmt.Fprintln(os.Stderr, "reviewed plan is stale; generate and review a new plan:")
		for _, mismatch := range mismatches {
			fmt.Fprintf(os.Stderr, "  %s: expected %s, actual %s\n", mismatch.Field, mismatch.Expected, mismatch.Actual)
		}
		return vigilcli.ExitPolicyBlocked
	}
	return executeWorkflowPlan(ctx, "apply", document, workflowExecutionOptions{
		Format:         format,
		WriteArtifacts: *writeArtifacts || strings.TrimSpace(*artifactsDir) != "",
		ArtifactsDir:   strings.TrimSpace(*artifactsDir),
	}, true)
}

type gateResult struct {
	Index           int                    `json:"-"`
	Name            string                 `json:"name"`
	Command         string                 `json:"command"`
	Args            []string               `json:"args,omitempty"`
	Shell           bool                   `json:"shell,omitempty"`
	Dependencies    []string               `json:"depends_on,omitempty"`
	ParallelGroup   string                 `json:"parallel_group,omitempty"`
	ContinueOnError bool                   `json:"continue_on_error,omitempty"`
	Required        bool                   `json:"required"`
	CWD             string                 `json:"cwd,omitempty"`
	Status          string                 `json:"status"`
	State           string                 `json:"state"`
	ExitCode        int                    `json:"exit_code"`
	Attempts        int                    `json:"attempts"`
	DurationMS      int64                  `json:"duration_ms"`
	Output          string                 `json:"output,omitempty"`
	OutputTruncated bool                   `json:"output_truncated,omitempty"`
	Warnings        []string               `json:"warnings,omitempty"`
	Artifacts       []vigiloutput.Artifact `json:"artifacts,omitempty"`
	StdoutLog       string                 `json:"stdout_log,omitempty"`
	StderrLog       string                 `json:"stderr_log,omitempty"`
	StdoutTruncated bool                   `json:"stdout_log_truncated,omitempty"`
	StderrTruncated bool                   `json:"stderr_log_truncated,omitempty"`
	MutationDiff    string                 `json:"mutation_diff,omitempty"`
}

func filterGatesByTag(gates []gateConfig, tag string) ([]gateConfig, error) {
	return vigilworkflow.FilterByTag(gates, tag)
}

func workflowLocal(configPath string, args []string, allowMutation bool) int {
	return workflowLocalContext(context.Background(), configPath, args, allowMutation)
}

type workflowExecutionOptions struct {
	Format         vigiloutput.Format
	DryRun         bool
	WriteArtifacts bool
	ArtifactsDir   string
}

func (options workflowExecutionOptions) machineOutput() bool {
	return options.Format != "" && options.Format != vigiloutput.FormatText
}

func workflowLocalContext(ctx context.Context, configPath string, args []string, allowMutation bool) int {
	finishOutput := ensureCommandOutput("workflow:local")
	defer finishOutput()

	fs := flag.NewFlagSet("workflow:local", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	formatValue := fs.String("format", "", "output format: text, json, jsonl, junit, or github")
	dryRun := fs.Bool("dry-run", false, "show checks without running")
	tagFilter := fs.String("tag", "", "run checks matching tag")
	defaultTimeout := fs.Duration("timeout", 10*time.Minute, "default timeout for each check")
	maxParallel := fs.Int("jobs", defaultWorkflowJobs, "maximum checks in an explicit parallel group")
	writeArtifacts := fs.Bool("artifacts", false, "write private plan, result, stdout, and stderr artifacts")
	artifactsDir := fs.String("artifacts-dir", "", "artifact root (implies --artifacts)")
	if err := fs.Parse(args); err != nil {
		return vigilcli.ExitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: vigil workflow:local [--dry-run] [--tag TAG] [--timeout DURATION] [--jobs N] [--json|--format FORMAT] [--artifacts] [--artifacts-dir PATH]")
		return vigilcli.ExitUsage
	}
	if *maxParallel < 1 || *maxParallel > 32 {
		fmt.Fprintln(os.Stderr, "--jobs must be between 1 and 32")
		return vigilcli.ExitUsage
	}
	format, err := vigiloutput.ResolveFormat(*jsonOut, *formatValue, vigiloutput.FormatText, vigiloutput.FormatJSON, vigiloutput.FormatJSONL, vigiloutput.FormatJUnit, vigiloutput.FormatGitHub)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	cfg, cfgPath, err := loadConfig(configPath)
	if err != nil {
		if format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitUsage)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	gates, err := filterGatesByTag(cfg.Gates, *tagFilter)
	if err != nil {
		if format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitUsage)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	document, err := buildWorkflowPlan(cfgPath, gates, *tagFilter, *defaultTimeout, *maxParallel, time.Now().UTC())
	if err != nil {
		if format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitPolicyBlocked)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitPolicyBlocked
	}
	return executeWorkflowPlan(ctx, "workflow:local", document, workflowExecutionOptions{
		Format:         format,
		DryRun:         *dryRun,
		WriteArtifacts: *writeArtifacts || strings.TrimSpace(*artifactsDir) != "",
		ArtifactsDir:   strings.TrimSpace(*artifactsDir),
	}, allowMutation)
}

func executeWorkflowPlan(ctx context.Context, outputCommand string, document vigilplan.Document, options workflowExecutionOptions, allowMutation bool) int {
	if err := vigilplan.Validate(document); err != nil {
		if options.Format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"error": err.Error()}, vigilcli.ExitUsage)
		}
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	defaultTimeout, err := time.ParseDuration(document.Options.DefaultTimeout)
	if err != nil || defaultTimeout <= 0 {
		if options.Format == vigiloutput.FormatJSON {
			return printJSON(map[string]any{"error": "reviewed plan has an invalid default timeout"}, vigilcli.ExitUsage)
		}
		fmt.Fprintln(os.Stderr, "reviewed plan has an invalid default timeout")
		return vigilcli.ExitUsage
	}
	startedAt := time.Now().UTC()
	runID, err := runartifact.NewID(startedAt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitInternal
	}
	eventSequence := 0
	emitEvent := func(eventType string, data any) error {
		if options.Format != vigiloutput.FormatJSONL {
			return nil
		}
		eventSequence++
		return vigiloutput.WriteJSONLEvent(os.Stdout, eventSequence, eventType, outputCommand, time.Now().UTC(), data)
	}
	if err := emitEvent("run_started", map[string]any{
		"run_id":  runID,
		"plan_id": document.PlanID,
		"gates":   len(document.Gates),
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitInternal
	}
	var artifactRun *runartifact.Run
	if options.WriteArtifacts {
		root := options.ArtifactsDir
		if root == "" {
			root = filepath.Join(".vigil", "runs")
		}
		if err := validateArtifactRoot(root); err != nil {
			if options.Format == vigiloutput.FormatJSON {
				return printJSON(map[string]any{"plan_id": document.PlanID, "error": err.Error()}, vigilcli.ExitPolicyBlocked)
			}
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitPolicyBlocked
		}
		artifactRun, err = runartifact.Start(root, runID, document)
		if err != nil {
			if options.Format == vigiloutput.FormatJSON {
				return printJSON(map[string]any{"plan_id": document.PlanID, "error": err.Error()}, vigilcli.ExitInternal)
			}
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	}
	if options.DryRun {
		payload := map[string]any{"status": "ok", "dry_run": true, "run_id": runID, "plan_id": document.PlanID, "gates": document.Gates}
		if artifactRun != nil {
			payload["artifact_dir"] = artifactRun.Dir
			if err := writeWorkflowArtifactResult(artifactRun, outputCommand, vigilcli.ExitSuccess, payload); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return vigilcli.ExitInternal
			}
		}
		if err := emitEvent("plan_ready", map[string]any{"plan_id": document.PlanID, "gates": document.Gates}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
		return writeWorkflowOutput(outputCommand, options.Format, vigilcli.ExitSuccess, payload, dryRunChecks(document.Gates), eventSequence)
	}
	results, workflowExit, err := runWorkflowGraph(ctx, document, options, allowMutation, runID, defaultTimeout, artifactRun, emitEvent)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitInternal
	}
	status := "ok"
	if workflowExit != vigilcli.ExitSuccess {
		status = "fail"
	}
	payload := map[string]any{"status": status, "run_id": runID, "plan_id": document.PlanID, "results": results}
	if artifacts := workflowArtifacts(results); len(artifacts) > 0 {
		payload["artifacts"] = artifacts
	}
	if warnings := workflowWarnings(results); len(warnings) > 0 {
		payload["warnings"] = warnings
	}
	if artifactRun != nil {
		payload["artifact_dir"] = artifactRun.Dir
		if err := writeWorkflowArtifactResult(artifactRun, outputCommand, workflowExit, payload); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	}
	return writeWorkflowOutput(outputCommand, options.Format, workflowExit, payload, workflowChecks(results), eventSequence)
}

func gateDisplayCommand(gate gateConfig) string {
	if gate.Shell {
		return "shell: " + gate.Command
	}
	parts := []string{shellQuote(gate.Command)}
	for _, arg := range gate.Args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func workflowStateExitCode(state runner.State) int {
	switch state {
	case runner.StateOK, runner.StateSkipped:
		return vigilcli.ExitSuccess
	case runner.StateFailed:
		return vigilcli.ExitCheckFailed
	case runner.StateBlocked:
		return vigilcli.ExitPolicyBlocked
	case runner.StateToolMissing:
		return vigilcli.ExitDependencyMissing
	case runner.StateTimedOut, runner.StateCancelled:
		return vigilcli.ExitInterrupted
	case runner.StateMutationDetected:
		return vigilcli.ExitMutationViolation
	default:
		return vigilcli.ExitInternal
	}
}

func buildWorkflowPlan(configPath string, gates []gateConfig, tag string, defaultTimeout time.Duration, maxParallel int, createdAt time.Time) (vigilplan.Document, error) {
	inputs, err := workflowInputs(configPath)
	if err != nil {
		return vigilplan.Document{}, err
	}
	return vigilplan.New("workflow:local", createdAt, inputs, vigilplan.Options{
		TagFilter:      strings.TrimSpace(tag),
		DefaultTimeout: defaultTimeout.String(),
		MaxParallel:    maxParallel,
	}, gates)
}

func workflowInputs(configPath string) (vigilplan.Inputs, error) {
	_, resolvedConfig, err := loadConfig(configPath)
	if err != nil {
		return vigilplan.Inputs{}, err
	}
	resolvedConfig, err = canonicalExistingPath(resolvedConfig)
	if err != nil {
		return vigilplan.Inputs{}, fmt.Errorf("resolve config path: %w", err)
	}
	configDigest, err := vigilplan.DigestFile(resolvedConfig)
	if err != nil {
		return vigilplan.Inputs{}, fmt.Errorf("digest config: %w", err)
	}
	repositoryRoot, rootExit := gitRootResult()
	if gitInvocationFailedInternally(rootExit) {
		return vigilplan.Inputs{}, fmt.Errorf("resolve Git repository root (exit %d)", rootExit)
	}
	if rootExit != 0 {
		repositoryRoot, err = canonicalExistingPath(mustGetwd())
		if err != nil {
			return vigilplan.Inputs{}, fmt.Errorf("resolve working directory: %w", err)
		}
		workspaceDigest, digestErr := vigilplan.DigestJSON(map[string]string{
			"config_digest":   configDigest,
			"repository_root": repositoryRoot,
		})
		if digestErr != nil {
			return vigilplan.Inputs{}, fmt.Errorf("digest non-Git workspace: %w", digestErr)
		}
		return remainingWorkflowInputs(resolvedConfig, configDigest, repositoryRoot, "not-git", workspaceDigest)
	}
	repositoryRoot, err = canonicalExistingPath(repositoryRoot)
	if err != nil {
		return vigilplan.Inputs{}, fmt.Errorf("resolve repository root: %w", err)
	}
	fingerprint, ok := gitMutationFingerprint()
	if !ok {
		return vigilplan.Inputs{}, fmt.Errorf("capture Git-visible workspace digest")
	}
	workspaceDigest := fingerprint.Hash
	if !strings.HasPrefix(workspaceDigest, "sha256:") {
		workspaceDigest = "sha256:" + workspaceDigest
	}
	repositoryHead, headExit := runCommand("git", "rev-parse", "--verify", "HEAD")
	repositoryHead = strings.TrimSpace(repositoryHead)
	if gitInvocationFailedInternally(headExit) {
		return vigilplan.Inputs{}, fmt.Errorf("resolve Git repository head (exit %d)", headExit)
	}
	if headExit != 0 {
		repositoryHead = "unborn"
	}
	return remainingWorkflowInputs(resolvedConfig, configDigest, repositoryRoot, repositoryHead, workspaceDigest)
}

func remainingWorkflowInputs(resolvedConfig, configDigest, repositoryRoot, repositoryHead, workspaceDigest string) (vigilplan.Inputs, error) {
	packReport := loadExtensionsForConfig(resolvedConfig, extensionRootForConfig(resolvedConfig))
	if packReport.Status != "ok" {
		return vigilplan.Inputs{}, fmt.Errorf("pack registry is invalid: %s", strings.Join(packReport.Issues, "; "))
	}
	packDigest, err := vigilplan.DigestJSON(packReport)
	if err != nil {
		return vigilplan.Inputs{}, fmt.Errorf("digest pack registry: %w", err)
	}
	commands := activeCommandsForConfig(resolvedConfig)
	if len(commands) == 0 {
		return vigilplan.Inputs{}, fmt.Errorf("command registry is unavailable")
	}
	registryDigest, err := vigilplan.DigestJSON(commands)
	if err != nil {
		return vigilplan.Inputs{}, fmt.Errorf("digest command registry: %w", err)
	}
	binaryDigest, err := vigilplan.ExecutableDigest()
	if err != nil {
		return vigilplan.Inputs{}, fmt.Errorf("digest Vigil executable: %w", err)
	}
	return vigilplan.Inputs{
		BinaryDigest:          binaryDigest,
		ConfigPath:            resolvedConfig,
		ConfigDigest:          configDigest,
		RepositoryRoot:        repositoryRoot,
		RepositoryHead:        repositoryHead,
		WorkspaceDigest:       workspaceDigest,
		CommandRegistryDigest: registryDigest,
		PackDigest:            packDigest,
	}, nil
}

func writeReviewedPlan(path string, document vigilplan.Document, force bool) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := validateIgnoredRepositoryPath(absolutePath, "plan output"); err != nil {
		return "", err
	}
	if info, err := os.Lstat(absolutePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to replace plan symlink: %s", absolutePath)
		}
		if !force {
			return "", fmt.Errorf("plan already exists: %s (use --force after review)", absolutePath)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return "", err
	}
	if err := vigilplan.Write(absolutePath, document); err != nil {
		return "", err
	}
	return absolutePath, nil
}

func validateArtifactRoot(root string) error {
	return validateIgnoredRepositoryPath(root, "artifact root")
}

func validateIgnoredRepositoryPath(path, label string) error {
	repositoryRoot, rootExit := gitRootResult()
	if gitInvocationFailedInternally(rootExit) {
		return fmt.Errorf("resolve Git repository root for %s (exit %d)", label, rootExit)
	}
	if rootExit != 0 {
		return nil
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(absolutePath); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink: %s", label, absolutePath)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	absoluteRepository, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return err
	}
	resolvedRepository, err := filepath.EvalSymlinks(absoluteRepository)
	if err != nil {
		return fmt.Errorf("resolve Git repository root for %s: %w", label, err)
	}
	resolvedPath, err := prospectiveCanonicalPath(absolutePath)
	if err != nil {
		return fmt.Errorf("resolve %s path: %w", label, err)
	}
	lexicallyInside, err := pathLexicallyInsideRoot(absoluteRepository, absolutePath)
	if err != nil {
		return fmt.Errorf("compare %s path with Git repository: %w", label, err)
	}
	resolvesInside := vigilpacks.PathInside(resolvedRepository, resolvedPath)
	if lexicallyInside && !resolvesInside {
		return fmt.Errorf("%s symlink path escapes repository: %s", label, absolutePath)
	}
	gitDirectory, code := runCommand("git", "rev-parse", "--git-dir")
	if code != 0 {
		return fmt.Errorf("resolve Git metadata directory for %s (exit %d)", label, code)
	}
	gitDirectory = strings.TrimSpace(gitDirectory)
	if gitDirectory == "" {
		return fmt.Errorf("resolve Git metadata directory for %s: empty result", label)
	}
	if !filepath.IsAbs(gitDirectory) {
		gitDirectory = filepath.Join(absoluteRepository, gitDirectory)
	}
	resolvedGitDirectory, err := filepath.EvalSymlinks(gitDirectory)
	if err != nil {
		return fmt.Errorf("resolve Git metadata directory for %s: %w", label, err)
	}
	if vigilpacks.PathInside(resolvedGitDirectory, resolvedPath) {
		return fmt.Errorf("%s must not be inside Git metadata: %s", label, resolvedPath)
	}
	if !resolvesInside {
		return nil
	}
	relative, err := filepath.Rel(resolvedRepository, resolvedPath)
	if err != nil {
		return err
	}
	if _, code := runCommand("git", "check-ignore", "-q", "--no-index", "--", filepath.ToSlash(relative)); code != 0 {
		return fmt.Errorf("%s inside the repository must be Git-ignored: %s", label, relative)
	}
	return nil
}

func pathLexicallyInsideRoot(root, candidate string) (bool, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false, err
	}
	ancestor := candidate
	for {
		info, statErr := os.Lstat(ancestor)
		if statErr == nil {
			if os.SameFile(rootInfo, info) {
				return true, nil
			}
		} else if !os.IsNotExist(statErr) {
			return false, statErr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return false, nil
		}
		ancestor = parent
	}
}

func prospectiveCanonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := absolute
	for {
		info, statErr := os.Lstat(ancestor)
		if statErr == nil {
			relative, err := filepath.Rel(ancestor, absolute)
			if err != nil {
				return "", err
			}
			if relative != "." && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return "", fmt.Errorf("existing ancestor is not a directory: %s", ancestor)
			}
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			if relative != "." {
				resolvedInfo, err := os.Stat(resolved)
				if err != nil {
					return "", err
				}
				if !resolvedInfo.IsDir() {
					return "", fmt.Errorf("existing ancestor is not a directory: %s", ancestor)
				}
			}
			return filepath.Join(resolved, relative), nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("no existing ancestor for %s", absolute)
		}
		ancestor = parent
	}
}

func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func writeWorkflowArtifactResult(run *runartifact.Run, fallbackCommand string, exitCode int, payload any) error {
	command, commandStartedAt := currentCommandOutput(fallbackCommand)
	envelope := vigiloutput.EnvelopeFromPayload(command, exitCode, commandStartedAt, time.Now().UTC(), payload)
	return run.WriteResult(envelope)
}

func writeWorkflowOutput(command string, format vigiloutput.Format, exitCode int, payload map[string]any, checks []vigiloutput.Check, jsonlSequence int) int {
	switch format {
	case "", vigiloutput.FormatText:
		if dryRun, _ := payload["dry_run"].(bool); dryRun {
			if gates, ok := payload["gates"].([]gateConfig); ok {
				for _, gate := range gates {
					fmt.Printf("%s dry-run %s: %s\n", statusLabel("ok"), gate.Name, gateDisplayCommand(gate))
				}
			}
		}
		if artifactDir, _ := payload["artifact_dir"].(string); artifactDir != "" {
			fmt.Printf("Artifacts: %s\n", artifactDir)
		}
	case vigiloutput.FormatJSON:
		return printJSON(payload, exitCode)
	case vigiloutput.FormatJSONL:
		outputCommand, startedAt := currentCommandOutput(command)
		envelope := vigiloutput.EnvelopeFromPayload(outputCommand, exitCode, startedAt, time.Now().UTC(), payload)
		if err := vigiloutput.WriteJSONLEvent(os.Stdout, jsonlSequence+1, "run_finished", outputCommand, time.Now().UTC(), envelope); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	case vigiloutput.FormatJUnit:
		if err := vigiloutput.WriteJUnit(os.Stdout, command, checks); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	case vigiloutput.FormatGitHub:
		findings := workflowFindings(checks)
		if len(findings) == 0 {
			findings = append(findings, vigiloutput.Finding{
				RuleID:  "vigil.workflow",
				Level:   "note",
				Message: fmt.Sprintf("%s completed successfully", command),
			})
		}
		if err := vigiloutput.WriteGitHubAnnotations(os.Stdout, findings); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	default:
		fmt.Fprintf(os.Stderr, "unsupported workflow output format %q\n", format)
		return vigilcli.ExitUsage
	}
	return exitCode
}

func workflowChecks(results []gateResult) []vigiloutput.Check {
	checks := make([]vigiloutput.Check, 0, len(results))
	for _, result := range results {
		message := ""
		if result.Status != "ok" {
			message = firstNonEmpty(result.Output, result.State, "gate failed")
		}
		checks = append(checks, vigiloutput.Check{
			Name:       result.Name,
			Status:     result.Status,
			DurationMS: result.DurationMS,
			Message:    message,
			Output:     result.Output,
		})
	}
	return checks
}

func dryRunChecks(gates []gateConfig) []vigiloutput.Check {
	checks := make([]vigiloutput.Check, 0, len(gates))
	for _, gate := range gates {
		checks = append(checks, vigiloutput.Check{
			Name:    gate.Name,
			Status:  "skipped",
			Message: "dry run",
			Output:  gateDisplayCommand(gate),
		})
	}
	return checks
}

func workflowFindings(checks []vigiloutput.Check) []vigiloutput.Finding {
	var findings []vigiloutput.Finding
	for _, check := range checks {
		switch check.Status {
		case "ok", "passed", "success", "skipped":
			continue
		}
		findings = append(findings, vigiloutput.Finding{
			RuleID:  "vigil.workflow." + strings.ReplaceAll(strings.ToLower(check.Name), " ", "-"),
			Level:   "error",
			Message: firstNonEmpty(check.Message, check.Output, "gate failed"),
		})
	}
	return findings
}

func gitMutationEvidence() []byte {
	return vigilgit.MutationEvidence(runGitCommand)
}

func verifyContext(ctx context.Context, configPath string, args []string) int {
	format, err := parseCheckFormat("verify", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	checks := []checkResult{}
	if _, cfgPath, err := loadConfig(configPath); err != nil {
		checks = append(checks, checkResult{Name: "config:validate", Status: "fail", Detail: cfgPath + ": " + err.Error(), ExitCode: exitUsage})
	} else {
		checks = append(checks, checkResult{Name: "config:validate", Status: "ok", Detail: cfgPath})
	}
	ext := loadExtensions(extensionRoot())
	extensionExit := exitSuccess
	if ext.Status != "ok" {
		extensionExit = exitUsage
	}
	checks = append(checks, checkResult{Name: "extensions:doctor", Status: ext.Status, Detail: fmt.Sprintf("%d extension(s)", ext.Count), ExitCode: extensionExit})
	checks = append(checks, pluginHealthCheck(ctx, configPath, "plugins:doctor"))
	catalogIssues := commandCatalogIssues()
	checks = append(checks, checkResult{Name: "checks:command-catalog", Status: okFail(len(catalogIssues)), Detail: fmt.Sprintf("%d issue(s)", len(catalogIssues)), ExitCode: boolExit(len(catalogIssues) > 0)})
	assumptions, assumptionErr := publicAssumptionFindings(configPath)
	if assumptionErr != nil {
		checks = append(checks, checkResult{Name: "checks:public-assumptions", Status: "fail", Detail: assumptionErr.Error(), ExitCode: exitInternal})
	} else {
		checks = append(checks, checkResult{Name: "checks:public-assumptions", Status: okFail(len(assumptions)), Detail: fmt.Sprintf("%d finding(s)", len(assumptions)), ExitCode: boolExit(len(assumptions) > 0)})
	}
	return checkResultsOutput(format, "verify", checks)
}

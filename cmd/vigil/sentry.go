package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"
	vigilplan "github.com/PayCal-Technologies/vigil-public/internal/plan"
)

func sentryCommand(configPath string, args []string, allowMutation bool) int {
	fs := flag.NewFlagSet("sentry", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outputPath := fs.String("output", ".vigil/plans/sentry-reviewed.json", "write the Sentry-reviewed plan to a private file")
	force := fs.Bool("force", false, "replace an existing Sentry plan")
	tagFilter := fs.String("tag", "", "include checks matching tag")
	defaultTimeout := fs.Duration("timeout", 10*time.Minute, "default timeout for each check")
	maxParallel := fs.Int("jobs", defaultWorkflowJobs, "maximum checks in an explicit parallel group")
	if err := fs.Parse(args); err != nil {
		return vigilcli.ExitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: vigil --allow-mutation sentry [--output PATH] [--force] [--tag TAG] [--timeout DURATION] [--jobs N]")
		return vigilcli.ExitUsage
	}
	if !allowMutation {
		fmt.Fprintln(os.Stderr, "Sentry Mode writes a reviewed plan; rerun with --allow-mutation after review")
		return vigilcli.ExitPolicyBlocked
	}
	if *maxParallel < 1 || *maxParallel > 32 {
		fmt.Fprintln(os.Stderr, "--jobs must be between 1 and 32")
		return vigilcli.ExitUsage
	}
	if !isInteractiveTerminal() {
		fmt.Fprintln(os.Stderr, "Sentry Mode requires an interactive terminal")
		return vigilcli.ExitUsage
	}
	cfg, cfgPath, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	gates, err := filterGatesByTag(cfg.Gates, *tagFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	document, err := buildWorkflowPlan(cfgPath, gates, *tagFilter, *defaultTimeout, *maxParallel, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitPolicyBlocked
	}
	reviewed, err := runSentryReview(document)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitInterrupted
	}
	writtenPath, err := writeReviewedPlan(*outputPath, reviewed, *force)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitPolicyBlocked
	}
	fmt.Printf("%s Sentry-reviewed plan written: %s\n", statusLabel("ok"), writtenPath)
	fmt.Println("Apply it with:")
	fmt.Printf("  vigil sentry:apply %s\n", shellQuote(writtenPath))
	return vigilcli.ExitSuccess
}

func sentryApplyCommand(ctx context.Context, args []string, allowMutation bool) int {
	fs := flag.NewFlagSet("sentry:apply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	formatValue := fs.String("format", "", "output format: text, json, jsonl, junit, or github")
	writeArtifacts := fs.Bool("artifacts", false, "write private result, stdout, and stderr artifacts")
	artifactsDir := fs.String("artifacts-dir", "", "artifact root (implies --artifacts)")
	if err := fs.Parse(args); err != nil {
		return vigilcli.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil sentry:apply [--json|--format FORMAT] [--artifacts] [--artifacts-dir PATH] <plan-file>")
		return vigilcli.ExitUsage
	}
	format, err := vigiloutput.ResolveFormat(*jsonOut, *formatValue, vigiloutput.FormatText, vigiloutput.FormatJSON, vigiloutput.FormatJSONL, vigiloutput.FormatJUnit, vigiloutput.FormatGitHub)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	document, err := vigilplan.Read(fs.Arg(0))
	if err != nil {
		return sentryPlanError(format, map[string]any{"error": err.Error()}, vigilcli.ExitUsage)
	}
	current, err := workflowInputs(document.Inputs.ConfigPath)
	if err != nil {
		return sentryPlanError(format, map[string]any{"plan_id": document.PlanID, "error": err.Error()}, vigilcli.ExitPolicyBlocked)
	}
	if mismatches := vigilplan.Compare(document.Inputs, current); len(mismatches) > 0 {
		return sentryPlanError(format, map[string]any{
			"plan_id":    document.PlanID,
			"mismatches": mismatches,
			"error":      "Sentry-reviewed plan is stale; generate and review a new plan",
		}, vigilcli.ExitPolicyBlocked)
	}
	if !allowMutation {
		if missing := vigilplan.UnapprovedMutationGates(document); len(missing) > 0 {
			return sentryPlanError(format, map[string]any{
				"plan_id":           document.PlanID,
				"unapproved_gates":  missing,
				"approved_gate_map": vigilplan.ApprovedMutationGates(document),
				"error":             "Sentry-reviewed plan has unapproved mutating gates",
			}, vigilcli.ExitPolicyBlocked)
		}
	}
	return executeWorkflowPlan(ctx, "sentry:apply", document, workflowExecutionOptions{
		Format:         format,
		WriteArtifacts: *writeArtifacts || strings.TrimSpace(*artifactsDir) != "",
		ArtifactsDir:   strings.TrimSpace(*artifactsDir),
	}, allowMutation)
}

func sentryPlanError(format vigiloutput.Format, payload map[string]any, exitCode int) int {
	if format == vigiloutput.FormatJSON {
		return printJSON(payload, exitCode)
	}
	if errText, _ := payload["error"].(string); errText != "" {
		fmt.Fprintln(os.Stderr, errText)
	}
	if mismatches, ok := payload["mismatches"].([]vigilplan.Mismatch); ok {
		for _, mismatch := range mismatches {
			fmt.Fprintf(os.Stderr, "  %s: expected %s, actual %s\n", mismatch.Field, mismatch.Expected, mismatch.Actual)
		}
	}
	if gates, ok := payload["unapproved_gates"].([]string); ok {
		for _, gate := range gates {
			fmt.Fprintf(os.Stderr, "  unapproved: %s\n", gate)
		}
	}
	return exitCode
}

func sentryStateCommand(ctx context.Context, configPath string, args []string) int {
	fs := flag.NewFlagSet("sentry:state", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return vigilcli.ExitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: vigil sentry:state [--json]")
		return vigilcli.ExitUsage
	}
	payload := sentryState(ctx, configPath)
	if *jsonOut {
		return printJSON(payload)
	}
	fmt.Print(renderSentryStateMarkdown(payload))
	return vigilcli.ExitSuccess
}

func sentryState(ctx context.Context, configPath string) map[string]any {
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	commands := activeCommandsForConfig(configPath)
	mutatingCommands := make([]string, 0)
	for _, command := range commands {
		if commandInfoCanMutate(command) {
			mutatingCommands = append(mutatingCommands, command.Command)
		}
	}
	sort.Strings(mutatingCommands)
	pluginReport, pluginErr := pluginDiscoveryForConfig(ctx, configPath, pluginReservedCommands(configPath))
	payload := map[string]any{
		"status":            "ok",
		"mode":              "sentry",
		"config_path":       cfgPath,
		"config_loaded":     cfgErr == nil,
		"git_root":          gitRoot(),
		"command_count":     len(commands),
		"mutating_commands": mutatingCommands,
		"recommended_next":  []string{"vigil sentry", "vigil sentry:apply .vigil/plans/sentry-reviewed.json"},
	}
	if cfgErr != nil {
		payload["status"] = "fail"
		payload["config_error"] = cfgErr.Error()
	} else {
		payload["project"] = cfg.Project
		payload["profile"] = cfg.Profile
		payload["gate_count"] = len(cfg.Gates)
		payload["mutating_gates"] = mutatingGateNames(cfg.Gates)
	}
	if pluginErr != nil {
		payload["plugin_status"] = "fail"
		payload["plugin_error"] = pluginErr.Error()
	} else {
		payload["plugin_status"] = pluginReport.Status
		payload["plugin_count"] = len(pluginReport.Plugins)
	}
	return payload
}

func commandInfoCanMutate(command commandInfo) bool {
	switch strings.TrimSpace(command.Access) {
	case string(vigilcli.AccessWrite):
		return true
	case string(vigilcli.AccessConditionalWrite):
		return len(command.WriteFlags) > 0
	default:
		return false
	}
}

func renderSentryStateMarkdown(payload map[string]any) string {
	var b strings.Builder
	b.WriteString("## Vigil Sentry State\n\n")
	b.WriteString("- Status: " + fmt.Sprint(payload["status"]) + "\n")
	b.WriteString("- Project: " + fmt.Sprint(payload["project"]) + "\n")
	b.WriteString("- Config: " + fmt.Sprint(payload["config_path"]) + "\n")
	b.WriteString("- Git root: " + fmt.Sprint(payload["git_root"]) + "\n")
	b.WriteString("- Commands: " + fmt.Sprint(payload["command_count"]) + "\n")
	b.WriteString("- Plugins: " + fmt.Sprint(payload["plugin_status"]) + "\n")
	if gates, ok := payload["mutating_gates"].([]string); ok {
		b.WriteString("- Mutating gates: " + strings.Join(gates, ", ") + "\n")
	}
	if commands, ok := payload["mutating_commands"].([]string); ok {
		b.WriteString("- Mutating commands: " + strings.Join(commands, ", ") + "\n")
	}
	b.WriteString("\n### Recommended Next Commands\n\n")
	if next, ok := payload["recommended_next"].([]string); ok {
		for _, command := range next {
			b.WriteString("- `" + command + "`\n")
		}
	}
	return b.String()
}

func runSentryReview(document vigilplan.Document) (vigilplan.Document, error) {
	approvals := map[string]bool{}
	for {
		renderSentryReview(document, approvals)
		input := strings.TrimSpace(promptString("Sentry command", "continue"))
		switch strings.ToLower(input) {
		case "", "c", "continue":
			document.Review = sentryReview(approvals, document.Gates, time.Now().UTC())
			return document, nil
		case "a", "all":
			for _, gate := range document.Gates {
				if !gate.ReadOnly {
					approvals[gate.Name] = true
				}
			}
		case "n", "none":
			approvals = map[string]bool{}
		case "q", "quit":
			return vigilplan.Document{}, fmt.Errorf("Sentry review cancelled")
		default:
			index, ok := parseSentryGateSelection(input, len(document.Gates))
			if !ok {
				fmt.Println("Enter a gate number, all, none, continue, or quit.")
				continue
			}
			gate := document.Gates[index]
			if gate.ReadOnly {
				fmt.Println("Read-only gates do not need mutation approval.")
				continue
			}
			approvals[gate.Name] = !approvals[gate.Name]
		}
	}
}

func renderSentryReview(document vigilplan.Document, approvals map[string]bool) {
	fmt.Println()
	fmt.Println("Vigil Sentry Mode")
	fmt.Printf("Plan: %s\n", document.PlanID)
	fmt.Printf("Repository: %s @ %s\n", document.Inputs.RepositoryRoot, document.Inputs.RepositoryHead)
	fmt.Println()
	for index, gate := range document.Gates {
		status := "[READ]"
		if !gate.ReadOnly {
			status = "[HOLD]"
			if approvals[gate.Name] {
				status = "[APPROVED]"
			}
		}
		fmt.Printf("%2d. %-10s %s\n", index+1, status, gate.Name)
		fmt.Printf("    %s\n", gateDisplayCommand(gate))
	}
	fmt.Println()
	fmt.Println("Commands: number toggles mutating gate approval, all, none, continue, quit")
}

func sentryReview(approvals map[string]bool, gates []gateConfig, reviewedAt time.Time) *vigilplan.Review {
	review := &vigilplan.Review{
		Mode:       "sentry",
		ReviewedAt: reviewedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, gate := range gates {
		if gate.ReadOnly || !approvals[gate.Name] {
			continue
		}
		review.MutationApprovals = append(review.MutationApprovals, vigilplan.MutationApproval{
			Gate:    gate.Name,
			Command: gateDisplayCommand(gate),
		})
	}
	return review
}

func parseSentryGateSelection(input string, gateCount int) (int, bool) {
	const noSelection = -1
	var selected int
	if _, err := fmt.Sscanf(strings.TrimSpace(input), "%d", &selected); err != nil {
		return noSelection, false
	}
	if selected < 1 || selected > gateCount {
		return noSelection, false
	}
	return selected - 1, true
}

func mutatingGateNames(gates []gateConfig) []string {
	names := make([]string, 0)
	for _, gate := range gates {
		if !gate.ReadOnly {
			names = append(names, gate.Name)
		}
	}
	sort.Strings(names)
	return names
}

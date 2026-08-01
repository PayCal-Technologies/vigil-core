package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"
	vigilpacks "github.com/PayCal-Technologies/vigil-public/internal/packs"
	vigilplan "github.com/PayCal-Technologies/vigil-public/internal/plan"
	"github.com/PayCal-Technologies/vigil-public/internal/runartifact"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
	vigilworkflow "github.com/PayCal-Technologies/vigil-public/internal/workflow"
)

type workflowEventEmitter func(string, any) error

func runWorkflowGraph(
	ctx context.Context,
	document vigilplan.Document,
	options workflowExecutionOptions,
	allowMutation bool,
	runID string,
	defaultTimeout time.Duration,
	artifactRun *runartifact.Run,
	emitEvent workflowEventEmitter,
) ([]gateResult, int, error) {
	maxParallel := document.Options.MaxParallel
	if maxParallel == 0 {
		maxParallel = 1
	}
	scheduler, err := vigilworkflow.NewScheduler(document.Gates, maxParallel)
	if err != nil {
		return nil, vigilcli.ExitUsage, err
	}
	resultSlots := make([]*gateResult, len(document.Gates))
	workflowExit := vigilcli.ExitSuccess

	for {
		batch, skipped, done := scheduler.Next()
		for _, skip := range skipped {
			result := dependencySkipResult(skip.Index, document.Gates[skip.Index], skip.FailedDependencies)
			resultSlots[skip.Index] = &result
			if err := emitEvent("gate_skipped", result); err != nil {
				return nil, vigilcli.ExitInternal, err
			}
			printSkippedGate(options, result)
		}
		if done {
			break
		}
		if len(batch) == 0 {
			return nil, vigilcli.ExitInternal, fmt.Errorf("workflow graph made no progress")
		}
		for _, gateIndex := range batch {
			gate := document.Gates[gateIndex]
			if err := emitEvent("gate_started", map[string]any{
				"index":          gateIndex,
				"name":           gate.Name,
				"command":        gateDisplayCommand(gate),
				"parallel_group": gate.ParallelGroup,
			}); err != nil {
				return nil, vigilcli.ExitInternal, err
			}
			if !options.machineOutput() {
				fmt.Printf("[RUN] %s\n", gate.Name)
			}
		}

		batchResults, err := runGateBatch(ctx, document, options, allowMutation, runID, defaultTimeout, artifactRun, batch)
		if err != nil {
			return nil, vigilcli.ExitInternal, err
		}
		halt := false
		for batchIndex, result := range batchResults {
			gateIndex := batch[batchIndex]
			gate := document.Gates[gateIndex]
			resultSlots[gateIndex] = &result
			if err := emitEvent("gate_finished", result); err != nil {
				return nil, vigilcli.ExitInternal, err
			}
			printFinishedGate(options, len(batch) > 1, result)
			state := runner.State(result.State)
			if state == runner.StateOK || state == runner.StateSkipped {
				if err := scheduler.MarkSucceeded(gateIndex); err != nil {
					return nil, vigilcli.ExitInternal, fmt.Errorf("mark workflow gate %q succeeded: %w", gate.Name, err)
				}
				continue
			}
			if err := scheduler.MarkFailed(gateIndex); err != nil {
				return nil, vigilcli.ExitInternal, fmt.Errorf("mark workflow gate %q failed: %w", gate.Name, err)
			}
			workflowExit = preferExitCode(workflowExit, workflowStateExitCode(state))
			if !gateMayContinue(gate, state) {
				halt = true
			}
		}
		if halt {
			scheduler.Halt()
			break
		}
	}

	results := make([]gateResult, 0, len(document.Gates))
	for _, result := range resultSlots {
		if result != nil {
			results = append(results, *result)
		}
	}
	return results, workflowExit, nil
}

func runGateBatch(
	ctx context.Context,
	document vigilplan.Document,
	options workflowExecutionOptions,
	allowMutation bool,
	runID string,
	defaultTimeout time.Duration,
	artifactRun *runartifact.Run,
	batch []int,
) ([]gateResult, error) {
	results := make([]gateResult, len(batch))
	allReadOnly := true
	for _, gateIndex := range batch {
		if !document.Gates[gateIndex].ReadOnly {
			allReadOnly = false
			break
		}
	}
	if len(batch) > 1 && !allReadOnly {
		return nil, fmt.Errorf("mutating gates cannot execute concurrently")
	}
	if !allReadOnly && !allowMutation {
		for batchIndex, gateIndex := range batch {
			result := newGateResult(gateIndex, document.Gates[gateIndex])
			result.Status = "fail"
			result.State = string(runner.StateBlocked)
			result.ExitCode = vigilcli.ExitPolicyBlocked
			result.Output = "mutation confirmation required for mutating check; generate a plan and apply it with --allow-mutation"
			results[batchIndex] = result
		}
		return results, nil
	}

	var before mutationFingerprint
	if allReadOnly {
		var ok bool
		before, ok = gitMutationFingerprint()
		if !ok {
			for batchIndex, gateIndex := range batch {
				result := newGateResult(gateIndex, document.Gates[gateIndex])
				result.Status = "fail"
				result.State = string(runner.StateBlocked)
				result.ExitCode = vigilcli.ExitPolicyBlocked
				result.Output = "read-only mutation fingerprint unavailable before gate"
				results[batchIndex] = result
			}
			return results, nil
		}
	}

	if len(batch) == 1 {
		gateIndex := batch[0]
		results[0] = runOneGate(ctx, document, options, runID, defaultTimeout, artifactRun, gateIndex, false)
	} else {
		type completed struct {
			batchIndex int
			result     gateResult
		}
		completedResults := make(chan completed, len(batch))
		for batchIndex, gateIndex := range batch {
			go func(batchIndex, gateIndex int) {
				completedResults <- completed{
					batchIndex: batchIndex,
					result:     runOneGate(ctx, document, options, runID, defaultTimeout, artifactRun, gateIndex, true),
				}
			}(batchIndex, gateIndex)
		}
		for range batch {
			completed := <-completedResults
			results[completed.batchIndex] = completed.result
		}
	}

	if !allReadOnly {
		return results, nil
	}
	after, ok := gitMutationFingerprint()
	if !ok {
		for index := range results {
			results[index].Status = "fail"
			results[index].State = string(runner.StateBlocked)
			results[index].ExitCode = vigilcli.ExitPolicyBlocked
			results[index].Output = appendResultOutput(results[index].Output, "read-only mutation fingerprint unavailable after gate")
		}
		return results, nil
	}
	if after.Hash == before.Hash {
		return results, nil
	}

	mutationDiff := ""
	if artifactRun != nil {
		path, err := artifactRun.WriteMutationDiff(gitMutationEvidence())
		if err != nil {
			for index := range results {
				results[index].Status = "fail"
				results[index].State = string(runner.StateInternalError)
				results[index].ExitCode = vigilcli.ExitInternal
				results[index].Output = appendResultOutput(results[index].Output, "write mutation evidence: "+err.Error())
			}
			return results, nil
		}
		mutationDiff = path
	}
	for index := range results {
		results[index].Status = "fail"
		results[index].State = string(runner.StateMutationDetected)
		results[index].ExitCode = vigilcli.ExitMutationViolation
		results[index].MutationDiff = mutationDiff
		message := "read-only check changed git workspace fingerprint"
		if len(batch) > 1 {
			group := strings.TrimSpace(document.Gates[batch[index]].ParallelGroup)
			if group == "" {
				group = "unnamed"
			}
			message = fmt.Sprintf("a command in parallel group %q changed the git workspace fingerprint; individual attribution is unavailable", group)
		}
		results[index].Output = appendResultOutput(results[index].Output, message)
	}
	return results, nil
}

func runOneGate(
	ctx context.Context,
	document vigilplan.Document,
	options workflowExecutionOptions,
	runID string,
	defaultTimeout time.Duration,
	artifactRun *runartifact.Run,
	gateIndex int,
	concurrent bool,
) gateResult {
	gate := document.Gates[gateIndex]
	result := newGateResult(gateIndex, gate)
	gateDir, err := resolveGateDirectory(document.Inputs.RepositoryRoot, gate.CWD)
	if err != nil {
		result.Status = "fail"
		result.State = string(runner.StateBlocked)
		result.ExitCode = vigilcli.ExitPolicyBlocked
		result.Output = err.Error()
		return result
	}
	result.CWD = gate.CWD

	timeout := defaultTimeout
	if strings.TrimSpace(gate.Timeout) != "" {
		timeout, err = time.ParseDuration(gate.Timeout)
		if err != nil || timeout <= 0 {
			result.Status = "fail"
			result.State = string(runner.StateBlocked)
			result.ExitCode = vigilcli.ExitUsage
			result.Output = "invalid gate timeout: " + gate.Timeout
			return result
		}
	}

	var logs *runartifact.GateLogs
	if artifactRun != nil {
		logs, err = artifactRun.OpenGate(gateIndex, gate.Name)
		if err != nil {
			result.Status = "fail"
			result.State = string(runner.StateInternalError)
			result.ExitCode = vigilcli.ExitInternal
			result.Output = err.Error()
			return result
		}
		result.StdoutLog = logs.StdoutPath
		result.StderrLog = logs.StderrPath
	}

	spec := runner.Spec{
		Name:       gate.Name,
		Mode:       runner.ModeArgv,
		Executable: gate.Command,
		Args:       append([]string(nil), gate.Args...),
		Dir:        gateDir,
		Env:        gateEnvironment(gate, runID, document.PlanID, document.Inputs.RepositoryRoot),
		Timeout:    timeout,
	}
	if gate.Shell {
		spec.Mode = runner.ModeShell
		spec.Shell = "bash"
		spec.ShellCommand = gate.Command
		spec.Executable = ""
		spec.Args = nil
	}
	live := !options.machineOutput() && !concurrent
	switch {
	case logs != nil && live:
		spec.Stdout = io.MultiWriter(os.Stdout, logs.Stdout)
		spec.Stderr = io.MultiWriter(os.Stderr, logs.Stderr)
	case logs != nil:
		spec.Stdout = logs.Stdout
		spec.Stderr = logs.Stderr
	case live:
		spec.Stdout = os.Stdout
		spec.Stderr = os.Stderr
	}

	execution, attempts, output := runGateAttempts(ctx, spec, gate)
	result.Attempts = attempts
	result.DurationMS = execution.DurationMS
	result.Output = trimOutput(output)
	result.OutputTruncated = execution.Truncated || len(strings.TrimSpace(output)) > outputSummaryByteLimit
	if result.OutputTruncated {
		result.Warnings = append(result.Warnings, "structured output summary was truncated; use private run artifacts for larger bounded logs")
	}
	result.State = string(execution.State)
	result.ExitCode = execution.ExitCode
	if execution.State == runner.StateOK {
		result.Status = "ok"
	}
	if execution.State == runner.StateToolMissing && !gate.IsRequired() {
		result.Status = "skipped"
		result.State = string(runner.StateSkipped)
		result.ExitCode = vigilcli.ExitSuccess
		result.Output = appendResultOutput(result.Output, "optional gate skipped because its executable is unavailable")
	}

	if logs != nil {
		if err := logs.Close(); err != nil {
			result.Status = "fail"
			result.State = string(runner.StateInternalError)
			result.ExitCode = vigilcli.ExitInternal
			result.Output = appendResultOutput(result.Output, "close run logs: "+err.Error())
		}
		result.StdoutTruncated, result.StderrTruncated = logs.Truncated()
		if result.StdoutTruncated || result.StderrTruncated {
			result.Warnings = append(result.Warnings, "private gate logs reached the 64 MiB per-stream limit")
		}
	}

	if runner.State(result.State) != runner.StateSkipped {
		artifacts, warnings, artifactErr := collectGateArtifacts(document.Inputs.RepositoryRoot, gateDir, gate, runner.State(result.State) == runner.StateOK)
		result.Artifacts = artifacts
		result.Warnings = append(result.Warnings, warnings...)
		if artifactErr != nil {
			result.Output = appendResultOutput(result.Output, artifactErr.Error())
			if runner.State(result.State) == runner.StateOK {
				result.Status = "fail"
				result.State = string(runner.StateFailed)
				result.ExitCode = vigilcli.ExitCheckFailed
			}
		}
	}
	return result
}

func runGateAttempts(ctx context.Context, spec runner.Spec, gate gateConfig) (runner.Result, int, string) {
	startedAt := time.Now()
	maxAttempts := 1
	delay := time.Duration(0)
	retryOn := map[runner.State]bool{runner.StateFailed: true}
	if gate.Retry != nil {
		maxAttempts = gate.Retry.MaxAttempts
		if strings.TrimSpace(gate.Retry.Delay) != "" {
			delay, _ = time.ParseDuration(gate.Retry.Delay)
		}
		if len(gate.Retry.On) > 0 {
			retryOn = map[runner.State]bool{}
			for _, state := range gate.Retry.On {
				retryOn[runner.State(state)] = true
			}
		}
	}
	var execution runner.Result
	var totalDurationMS int64
	outputs := make([]string, 0, maxAttempts)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		execution = runner.Run(ctx, spec)
		totalDurationMS += execution.DurationMS
		attemptOutput := execution.Output
		if execution.Error != "" {
			attemptOutput = appendResultOutput(attemptOutput, execution.Error)
		}
		if maxAttempts > 1 && strings.TrimSpace(attemptOutput) != "" {
			attemptOutput = fmt.Sprintf("attempt %d (%s):\n%s", attempt, execution.State, attemptOutput)
		}
		if strings.TrimSpace(attemptOutput) != "" {
			outputs = append(outputs, attemptOutput)
		}
		if attempt == maxAttempts || !retryOn[execution.State] {
			execution.DurationMS = maxInt64(totalDurationMS, time.Since(startedAt).Milliseconds())
			return execution, attempt, strings.Join(outputs, "\n")
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				execution.State = runner.StateCancelled
				execution.ExitCode = vigilcli.ExitInterrupted
				execution.Error = ctx.Err().Error()
				execution.DurationMS = maxInt64(totalDurationMS, time.Since(startedAt).Milliseconds())
				return execution, attempt, appendResultOutput(strings.Join(outputs, "\n"), execution.Error)
			case <-timer.C:
			}
		}
	}
	execution.DurationMS = maxInt64(totalDurationMS, time.Since(startedAt).Milliseconds())
	return execution, maxAttempts, strings.Join(outputs, "\n")
}

func collectGateArtifacts(repositoryRoot, gateDir string, gate gateConfig, enforceRequired bool) ([]vigiloutput.Artifact, []string, error) {
	artifacts := make([]vigiloutput.Artifact, 0, len(gate.Artifacts))
	warnings := make([]string, 0)
	failures := make([]string, 0)
	for _, declaration := range gate.Artifacts {
		path, err := resolveGateArtifact(repositoryRoot, gateDir, declaration.Path)
		if err != nil {
			if declaration.IsRequired() && enforceRequired {
				failures = append(failures, err.Error())
			} else {
				warnings = append(warnings, err.Error())
			}
			continue
		}
		digest, err := vigilplan.DigestFile(path)
		if err != nil {
			message := fmt.Sprintf("inspect declared artifact %s: %v", declaration.Path, err)
			if declaration.IsRequired() && enforceRequired {
				failures = append(failures, message)
			} else {
				warnings = append(warnings, message)
			}
			continue
		}
		kind := declaration.Kind
		if kind == "" {
			kind = "workflow-output"
		}
		artifacts = append(artifacts, vigiloutput.Artifact{
			Kind:      kind,
			Path:      path,
			MediaType: declaration.MediaType,
			Digest:    digest,
		})
	}
	if len(failures) > 0 {
		return artifacts, warnings, fmt.Errorf("declared artifact verification failed: %s", strings.Join(failures, "; "))
	}
	return artifacts, warnings, nil
}

func resolveGateDirectory(repositoryRoot, cwd string) (string, error) {
	root, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	candidate := root
	if strings.TrimSpace(cwd) != "" {
		candidate = filepath.Join(root, cwd)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve gate cwd: %w", err)
	}
	if !vigilpacks.PathInside(root, candidate) {
		return "", fmt.Errorf("gate cwd escapes repository root: %s", cwd)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve gate cwd %q: %w", cwd, err)
	}
	if !vigilpacks.PathInside(root, resolved) {
		return "", fmt.Errorf("gate cwd symlink escapes repository root: %s", cwd)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect gate cwd %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("gate cwd is not a directory: %s", cwd)
	}
	return resolved, nil
}

func resolveGateArtifact(repositoryRoot, gateDir, declaredPath string) (string, error) {
	candidate, err := filepath.Abs(filepath.Join(gateDir, declaredPath))
	if err != nil {
		return "", fmt.Errorf("resolve declared artifact %q: %w", declaredPath, err)
	}
	if !vigilpacks.PathInside(repositoryRoot, candidate) {
		return "", fmt.Errorf("declared artifact escapes repository root: %s", declaredPath)
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return "", fmt.Errorf("declared artifact %q is unavailable: %w", declaredPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("declared artifact must not be a symlink: %s", declaredPath)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("declared artifact is not a regular file: %s", declaredPath)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve declared artifact %q: %w", declaredPath, err)
	}
	if !vigilpacks.PathInside(repositoryRoot, resolved) {
		return "", fmt.Errorf("declared artifact symlink path escapes repository root: %s", declaredPath)
	}
	return resolved, nil
}

func gateEnvironment(gate gateConfig, runID, planID, repositoryRoot string) []string {
	keys := make([]string, 0, len(gate.Environment))
	for key := range gate.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys)+4)
	for _, key := range keys {
		environment = append(environment, key+"="+gate.Environment[key])
	}
	environment = append(environment,
		"VIGIL_GATE_NAME="+gate.Name,
		"VIGIL_PLAN_ID="+planID,
		"VIGIL_REPOSITORY_ROOT="+repositoryRoot,
		"VIGIL_RUN_ID="+runID,
	)
	return environment
}

func newGateResult(index int, gate gateConfig) gateResult {
	return gateResult{
		Index:           index,
		Name:            gate.Name,
		Command:         gate.Command,
		Args:            append([]string(nil), gate.Args...),
		Shell:           gate.Shell,
		Dependencies:    append([]string(nil), gate.DependsOn...),
		ParallelGroup:   gate.ParallelGroup,
		ContinueOnError: gate.ContinueOnError,
		Required:        gate.IsRequired(),
		Status:          "fail",
		State:           string(runner.StateInternalError),
		ExitCode:        vigilcli.ExitInternal,
	}
}

func dependencySkipResult(index int, gate gateConfig, failedDependencies []string) gateResult {
	result := newGateResult(index, gate)
	result.Status = "skipped"
	result.State = string(runner.StateSkipped)
	result.ExitCode = vigilcli.ExitSuccess
	result.Output = "dependency failed: " + strings.Join(failedDependencies, ", ")
	return result
}

func gateMayContinue(gate gateConfig, state runner.State) bool {
	if !gate.ContinueOnError {
		return false
	}
	switch state {
	case runner.StateFailed, runner.StateToolMissing, runner.StateTimedOut:
		return true
	default:
		return false
	}
}

func printSkippedGate(options workflowExecutionOptions, result gateResult) {
	if options.machineOutput() {
		return
	}
	fmt.Printf("%s %s: %s\n", statusLabel("skipped"), result.Name, result.Output)
}

func printFinishedGate(options workflowExecutionOptions, concurrent bool, result gateResult) {
	if options.machineOutput() {
		return
	}
	if concurrent && result.Output != "" {
		fmt.Printf("--- %s output ---\n%s\n", result.Name, result.Output)
	}
	fmt.Printf("%s %s (%d ms, %d attempt(s))\n", statusLabel(result.Status), result.Name, result.DurationMS, result.Attempts)
	for _, warning := range result.Warnings {
		fmt.Fprintf(os.Stderr, "%s %s: %s\n", statusLabel("warning"), result.Name, warning)
	}
	state := runner.State(result.State)
	if !concurrent && (state == runner.StateMutationDetected || state == runner.StateBlocked || state == runner.StateInternalError || state == runner.StateToolMissing || state == runner.StateTimedOut || state == runner.StateCancelled) && result.Output != "" {
		fmt.Fprintln(os.Stderr, result.Output)
	}
}

func appendResultOutput(output, message string) string {
	output = strings.TrimSpace(output)
	message = strings.TrimSpace(message)
	switch {
	case output == "":
		return message
	case message == "":
		return output
	default:
		return trimOutput(output + "\n" + message)
	}
}

func workflowArtifacts(results []gateResult) []vigiloutput.Artifact {
	var artifacts []vigiloutput.Artifact
	for _, result := range results {
		artifacts = append(artifacts, result.Artifacts...)
	}
	return artifacts
}

func workflowWarnings(results []gateResult) []string {
	var warnings []string
	for _, result := range results {
		for _, warning := range result.Warnings {
			warnings = append(warnings, result.Name+": "+warning)
		}
	}
	return warnings
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

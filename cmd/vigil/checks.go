package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/buildinfo"
	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"
)

const (
	maxPublicAssumptionFiles     = 100_000
	maxPublicAssumptionFileBytes = int64(8 * 1024 * 1024)
	maxPublicAssumptionTotal     = int64(256 * 1024 * 1024)
)

func checkStagedSensitive(args []string) int {
	format, err := parseFindingFormat("checks:staged-sensitive", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	files, ok := gitPathsChecked("diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR")
	if !ok {
		return findingSourceFailure(format, "staged_sensitive", "could not enumerate staged files")
	}
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
	return findingsOutput(format, "staged_sensitive", findings)
}

func checkWorkspaceHygiene(args []string) int {
	format, err := parseFindingFormat("checks:workspace-hygiene", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
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
	return findingsOutput(format, "workspace_hygiene", findings)
}

func checkTrackedAssistantArtifacts(args []string) int {
	format, err := parseFindingFormat("checks:tracked-assistant-artifacts", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
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
	files, ok := gitPathsChecked("ls-files", "-z")
	if !ok {
		return findingSourceFailure(format, "tracked_assistant_artifacts", "could not enumerate tracked files")
	}
	for _, file := range files {
		for _, pattern := range patterns {
			if pattern.MatchString(file) {
				findings = append(findings, file)
				break
			}
		}
	}
	return findingsOutput(format, "tracked_assistant_artifacts", findings)
}

func checkCommandCatalog(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	issues := commandCatalogIssues()
	if jsonOut {
		return printStatusJSON(map[string]any{"status": okFail(len(issues)), "command_count": len(activeCommands()), "issues": issues}, boolExit(len(issues) > 0))
	}
	if len(issues) == 0 {
		fmt.Printf("%s command-catalog: %d commands\n", statusLabel("ok"), len(activeCommands()))
		return exitSuccess
	}
	for _, issue := range issues {
		fmt.Println(issue)
	}
	return exitCheckFailed
}

func commandCatalogIssues() []string {
	seen := map[string]bool{}
	issues := make([]string, 0)
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
	format, err := parseFindingFormat("checks:public-assumptions", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	findings, err := publicAssumptionFindings(configPath)
	if err != nil {
		return findingSourceFailure(format, "public_assumptions", err.Error())
	}
	return findingsOutput(format, "public_assumptions", findings)
}

func checkPublicParity(configPath string, args []string) int {
	format, err := parseFindingFormat("checks:public-parity", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	findings, err := publicAssumptionFindings(configPath)
	if err != nil {
		return findingSourceFailure(format, "public_parity", err.Error())
	}
	for _, ext := range loadExtensions(extensionRoot()).Extensions {
		if ext.Private {
			findings = append(findings, ext.Path+": public extension manifest cannot be private")
		}
		if !ext.PublicCore {
			findings = append(findings, ext.Path+": public extension manifest must set public_core=true")
		}
	}
	return findingsOutput(format, "public_parity", findings)
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
	root, paths, err := publicAssumptionPaths()
	if err != nil {
		return nil, err
	}
	if len(paths) > maxPublicAssumptionFiles {
		return nil, fmt.Errorf("public assumption scan exceeds %d files", maxPublicAssumptionFiles)
	}
	findings := make([]string, 0)
	var totalBytes int64
	for _, path := range paths {
		fullPath, err := confinedRepositoryPath(root, path)
		if err != nil {
			return nil, err
		}
		if samePath(fullPath, cfgPath) {
			continue
		}
		data, err := readPublicAssumptionCandidate(fullPath)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", filepath.ToSlash(path), err)
		}
		totalBytes += int64(len(data))
		if totalBytes > maxPublicAssumptionTotal {
			return nil, fmt.Errorf("public assumption scan exceeds %d bytes", maxPublicAssumptionTotal)
		}
		for _, re := range compiled {
			if re.Match(data) {
				findings = append(findings, filepath.ToSlash(path))
				break
			}
		}
	}
	sort.Strings(findings)
	return findings, nil
}

func publicAssumptionPaths() (string, []string, error) {
	root, rootExit := gitRootResult()
	if rootExit == 0 {
		paths, ok := gitPathsChecked("-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
		if !ok {
			return "", nil, fmt.Errorf("could not enumerate complete Git-visible paths")
		}
		return root, paths, nil
	}
	if gitInvocationFailedInternally(rootExit) {
		return "", nil, fmt.Errorf("resolve Git repository root (exit %d)", rootExit)
	}

	root, err := filepath.Abs(mustGetwd())
	if err != nil {
		return "", nil, err
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if name == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			switch name {
			case "bin", "dist", "tmp", ".vigil", ".code-review-graph":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, relative)
		if len(paths) > maxPublicAssumptionFiles {
			return fmt.Errorf("public assumption scan exceeds %d files", maxPublicAssumptionFiles)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return root, paths, nil
}

func confinedRepositoryPath(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("git returned an absolute path: %q", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("git returned an unconfined path: %q", path)
	}
	fullPath := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, fullPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("git path escapes repository: %q", path)
	}
	return fullPath, nil
}

func readPublicAssumptionCandidate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		if int64(len(target)) > maxPublicAssumptionFileBytes {
			return nil, fmt.Errorf("symlink target exceeds %d bytes", maxPublicAssumptionFileBytes)
		}
		return []byte(target), nil
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file or symlink")
	}
	if info.Size() > maxPublicAssumptionFileBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxPublicAssumptionFileBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxPublicAssumptionFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > maxPublicAssumptionFileBytes {
		return nil, fmt.Errorf("file grew beyond %d bytes", maxPublicAssumptionFileBytes)
	}
	return data, nil
}

func depsInventory(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
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
	return exitSuccess
}

func commandCheck(command, label string) checkResult {
	if path, err := exec.LookPath(command); err == nil {
		return checkResult{Name: label, Status: "ok", Detail: path}
	}
	return checkResult{Name: label, Status: "fail", Detail: command + " not found", ExitCode: exitDependencyMissing}
}

func summarizeChecks(checks []checkResult) (string, int) {
	exitCode := exitSuccess
	for _, check := range checks {
		if check.Status == "fail" {
			checkExit := check.ExitCode
			if checkExit == exitSuccess {
				checkExit = exitCheckFailed
			}
			exitCode = preferExitCode(exitCode, checkExit)
		}
	}
	if exitCode != exitSuccess {
		return "fail", exitCode
	}
	return "ok", exitSuccess
}

func parseCheckFormat(command string, args []string) (vigiloutput.Format, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	formatValue := fs.String("format", "", "output format: text, json, jsonl, junit, or github")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return vigiloutput.ResolveFormat(
		*jsonOut,
		*formatValue,
		vigiloutput.FormatText,
		vigiloutput.FormatJSON,
		vigiloutput.FormatJSONL,
		vigiloutput.FormatJUnit,
		vigiloutput.FormatGitHub,
	)
}

func checkResultsOutput(format vigiloutput.Format, command string, checks []checkResult) int {
	status, exitCode := summarizeChecks(checks)
	normalized := make([]vigiloutput.Check, 0, len(checks))
	for _, check := range checks {
		normalized = append(normalized, vigiloutput.Check{
			Name:    check.Name,
			Status:  check.Status,
			Message: check.Detail,
			Output:  check.Detail,
		})
	}
	payload := map[string]any{"status": status, "checks": checks}
	switch format {
	case "", vigiloutput.FormatText:
		for _, check := range checks {
			fmt.Printf("%s %s: %s\n", statusLabel(check.Status), check.Name, check.Detail)
		}
	case vigiloutput.FormatJSON:
		return printJSON(payload, exitCode)
	case vigiloutput.FormatJSONL:
		outputCommand, startedAt := currentCommandOutput(command)
		for index, check := range normalized {
			if err := vigiloutput.WriteJSONLEvent(os.Stdout, index+1, "check_finished", outputCommand, time.Now().UTC(), check); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return vigilcli.ExitInternal
			}
		}
		envelope := vigiloutput.EnvelopeFromPayload(outputCommand, exitCode, startedAt, time.Now().UTC(), payload)
		if err := vigiloutput.WriteJSONLEvent(os.Stdout, len(normalized)+1, "run_finished", outputCommand, time.Now().UTC(), envelope); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	case vigiloutput.FormatJUnit:
		if err := vigiloutput.WriteJUnit(os.Stdout, command, normalized); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	case vigiloutput.FormatGitHub:
		var findings []vigiloutput.Finding
		for _, check := range checks {
			if check.Status == "ok" {
				continue
			}
			findings = append(findings, vigiloutput.Finding{
				RuleID:  "vigil." + strings.ReplaceAll(command, ":", ".") + "." + strings.ReplaceAll(strings.ToLower(check.Name), " ", "-"),
				Level:   "error",
				Message: check.Detail,
			})
		}
		if len(findings) == 0 {
			findings = append(findings, vigiloutput.Finding{RuleID: "vigil." + strings.ReplaceAll(command, ":", "."), Level: "note", Message: command + " passed"})
		}
		if err := vigiloutput.WriteGitHubAnnotations(os.Stdout, findings); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	default:
		fmt.Fprintf(os.Stderr, "unsupported check output format %q\n", format)
		return vigilcli.ExitUsage
	}
	return exitCode
}

func printStatusJSON(payload map[string]any, exit int) int {
	if _, ok := payload["status"]; !ok {
		payload["status"] = okFail(exit)
	}
	exitCode := vigilcli.ClassifyExit(exit).Code
	if writeExit := printJSON(payload, exitCode); writeExit != 0 {
		return writeExit
	}
	return exitCode
}

func okFail(count int) string {
	if count > 0 {
		return "fail"
	}
	return "ok"
}

func boolExit(failed bool) int {
	if failed {
		return exitCheckFailed
	}
	return exitSuccess
}

func parseFindingFormat(command string, args []string) (vigiloutput.Format, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	formatValue := fs.String("format", "", "output format: text, json, jsonl, junit, sarif, or github")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return vigiloutput.ResolveFormat(
		*jsonOut,
		*formatValue,
		vigiloutput.FormatText,
		vigiloutput.FormatJSON,
		vigiloutput.FormatJSONL,
		vigiloutput.FormatJUnit,
		vigiloutput.FormatSARIF,
		vigiloutput.FormatGitHub,
	)
}

func findingsOutput(format vigiloutput.Format, name string, findings []string) int {
	if findings == nil {
		findings = []string{}
	}
	sort.Strings(findings)
	status := okFail(len(findings))
	exitCode := vigilcli.ExitSuccess
	if len(findings) > 0 {
		exitCode = vigilcli.ExitCheckFailed
	}
	payload := map[string]any{"status": status, "check": name, "findings": findings, "count": len(findings)}
	normalized := normalizedFindings(name, findings)
	switch format {
	case "", vigiloutput.FormatText:
		if len(findings) == 0 {
			fmt.Printf("%s %s\n", statusLabel("ok"), name)
			return vigilcli.ExitSuccess
		}
		for _, finding := range findings {
			fmt.Println(finding)
		}
	case vigiloutput.FormatJSON:
		return printJSON(payload, exitCode)
	case vigiloutput.FormatJSONL:
		command, startedAt := currentCommandOutput(name)
		sequence := 0
		for _, finding := range normalized {
			sequence++
			if err := vigiloutput.WriteJSONLEvent(os.Stdout, sequence, "finding", command, time.Now().UTC(), finding); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return vigilcli.ExitInternal
			}
		}
		envelope := vigiloutput.EnvelopeFromPayload(command, exitCode, startedAt, time.Now().UTC(), payload)
		if err := vigiloutput.WriteJSONLEvent(os.Stdout, sequence+1, "check_finished", command, time.Now().UTC(), envelope); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	case vigiloutput.FormatJUnit:
		checks := []vigiloutput.Check{{Name: name, Status: status}}
		if len(findings) > 0 {
			checks[0].Message = fmt.Sprintf("%d finding(s)", len(findings))
			checks[0].Output = strings.Join(findings, "\n")
		}
		if err := vigiloutput.WriteJUnit(os.Stdout, name, checks); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	case vigiloutput.FormatSARIF:
		if err := vigiloutput.WriteSARIF(os.Stdout, "Vigil", buildinfo.Current().Version, normalized); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	case vigiloutput.FormatGitHub:
		if err := vigiloutput.WriteGitHubAnnotations(os.Stdout, normalized); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	default:
		fmt.Fprintf(os.Stderr, "unsupported findings output format %q\n", format)
		return vigilcli.ExitUsage
	}
	return exitCode
}

func findingSourceFailure(format vigiloutput.Format, name, detail string) int {
	payload := map[string]any{"status": "fail", "check": name, "error": detail}
	finding := vigiloutput.Finding{
		RuleID:  "vigil." + strings.ReplaceAll(name, "_", ".") + ".input",
		Level:   "error",
		Message: detail,
	}
	switch format {
	case "", vigiloutput.FormatText:
		fmt.Fprintln(os.Stderr, detail)
	case vigiloutput.FormatJSON:
		return printJSON(payload, vigilcli.ExitInternal)
	case vigiloutput.FormatJSONL:
		command, startedAt := currentCommandOutput(name)
		envelope := vigiloutput.EnvelopeFromPayload(command, vigilcli.ExitInternal, startedAt, time.Now().UTC(), payload)
		if err := vigiloutput.WriteJSONLEvent(os.Stdout, 1, "check_finished", command, time.Now().UTC(), envelope); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	case vigiloutput.FormatJUnit:
		if err := vigiloutput.WriteJUnit(os.Stdout, name, []vigiloutput.Check{{
			Name: name, Status: "failed", Message: detail, Output: detail,
		}}); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	case vigiloutput.FormatSARIF:
		if err := vigiloutput.WriteSARIF(os.Stdout, "Vigil", buildinfo.Current().Version, []vigiloutput.Finding{finding}); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	case vigiloutput.FormatGitHub:
		if err := vigiloutput.WriteGitHubAnnotations(os.Stdout, []vigiloutput.Finding{finding}); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	default:
		fmt.Fprintf(os.Stderr, "unsupported findings output format %q\n", format)
		return vigilcli.ExitUsage
	}
	return vigilcli.ExitInternal
}

func normalizedFindings(name string, findings []string) []vigiloutput.Finding {
	normalized := make([]vigiloutput.Finding, 0, len(findings))
	for _, value := range findings {
		path := value
		if separator := strings.Index(value, ": "); separator > 0 {
			path = value[:separator]
		}
		if _, err := os.Lstat(path); err != nil {
			path = ""
		}
		normalized = append(normalized, vigiloutput.Finding{
			RuleID:  "vigil." + strings.ReplaceAll(name, "_", "."),
			Level:   "error",
			Message: value,
			Path:    path,
		})
	}
	return normalized
}

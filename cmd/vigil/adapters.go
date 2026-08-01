package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
	"os"
	"os/exec"

	"path/filepath"

	"sort"

	"strings"
	"time"
)

func filesIterate(args []string) int {
	fs := flag.NewFlagSet("files:iterate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "root directory")
	glob := fs.String("glob", "*", "file glob")
	jsonOut := fs.Bool("json", false, "json output")
	jsonl := fs.Bool("jsonl", false, "emit versioned JSONL events")
	formatValue := fs.String("format", "", "output format: text, json, or jsonl")
	if err := fs.Parse(args); err != nil {
		return vigilcli.ExitUsage
	}
	if *jsonl {
		if strings.TrimSpace(*formatValue) != "" && *formatValue != "jsonl" {
			fmt.Fprintln(os.Stderr, "--jsonl conflicts with --format")
			return vigilcli.ExitUsage
		}
		*formatValue = "jsonl"
	}
	format, err := vigiloutput.ResolveFormat(*jsonOut, *formatValue, vigiloutput.FormatText, vigiloutput.FormatJSON, vigiloutput.FormatJSONL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return vigilcli.ExitUsage
	}
	type fileEntry struct {
		Path      string `json:"path"`
		SizeBytes int64  `json:"size_bytes"`
	}
	matches := make([]fileEntry, 0)
	err = filepath.WalkDir(*root, func(path string, entry os.DirEntry, err error) error {
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
			info, err := entry.Info()
			if err != nil {
				return err
			}
			matches = append(matches, fileEntry{Path: rel, SizeBytes: info.Size()})
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitCheckFailed
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Path < matches[j].Path
	})
	payload := map[string]any{"files": matches, "count": len(matches), "root": *root, "glob": *glob}
	switch format {
	case vigiloutput.FormatJSON:
		return printJSON(payload)
	case vigiloutput.FormatJSONL:
		command, startedAt := currentCommandOutput("files:iterate")
		for index, match := range matches {
			if err := vigiloutput.WriteJSONLEvent(os.Stdout, index+1, "file", command, time.Now().UTC(), match); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return vigilcli.ExitInternal
			}
		}
		envelope := vigiloutput.EnvelopeFromPayload(command, vigilcli.ExitSuccess, startedAt, time.Now().UTC(), payload)
		if err := vigiloutput.WriteJSONLEvent(os.Stdout, len(matches)+1, "iteration_finished", command, time.Now().UTC(), envelope); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return vigilcli.ExitInternal
		}
	default:
		for _, match := range matches {
			fmt.Printf("%s\t%d\n", match.Path, match.SizeBytes)
		}
	}
	return vigilcli.ExitSuccess
}

func scribeCommand(command string, args []string) int {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("path", "README.md", "README path")
	dryRun := fs.Bool("dry-run", false, "preview changes")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	current, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitCheckFailed
	}
	next, changed, err := renderScribeReadme(string(current))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if command == "readme:check" {
		if *jsonOut {
			return printStatusJSON(map[string]any{"status": okFail(boolExit(changed)), "path": *path, "changed": changed}, boolExit(changed))
		}
		if changed {
			fmt.Fprintf(os.Stderr, "%s Scribe README block is stale; run: vigil readme:generate\n", statusLabel("fail"))
			return exitCheckFailed
		}
		fmt.Printf("%s Scribe README block is current\n", statusLabel("ok"))
		return exitSuccess
	}
	if *dryRun {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": *path, "changed": changed, "content": next})
		}
		fmt.Print(next)
		return exitSuccess
	}
	if !changed {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "path": *path, "changed": false})
		}
		fmt.Printf("%s README already current: %s\n", statusLabel("ok"), *path)
		return exitSuccess
	}
	if _, err := atomicfile.Write(*path, []byte(next), atomicfile.Options{
		DefaultMode:          0o644,
		PreserveExistingMode: true,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "path": *path, "changed": true})
	}
	fmt.Printf("%s updated README: %s\n", statusLabel("ok"), *path)
	return exitSuccess
}

func renderScribeReadme(current string) (string, bool, error) {
	block, err := scribeBlock()
	if err != nil {
		return "", false, err
	}
	begin := "<!-- scribe:begin -->"
	end := "<!-- scribe:end -->"
	start := strings.Index(current, begin)
	stop := strings.Index(current, end)
	if start >= 0 && stop > start {
		stop += len(end)
		next := current[:start] + strings.TrimRight(block, "\n") + current[stop:]
		return next, next != current, nil
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
	return next, next != current, nil
}

func scribeBlock() (string, error) {
	testPaths, ok := existingTestPathsChecked()
	if !ok {
		return "", fmt.Errorf("could not enumerate complete repository test paths")
	}
	var b strings.Builder
	b.WriteString("<!-- scribe:begin -->\n")
	b.WriteString("## Repository Snapshot\n\n")
	b.WriteString("| Signal | Value |\n")
	b.WriteString("| --- | --- |\n")
	b.WriteString("| Repository | " + tableValue(repositoryDisplayName()) + " |\n")
	b.WriteString("| Dependency manifests | " + tableValue(strings.Join(existingFiles(dependencyManifestFiles()), ", ")) + " |\n")
	b.WriteString("| Test paths | " + tableValue(strings.Join(testPaths, ", ")) + " |\n")
	b.WriteString("| Vigil commands | " + fmt.Sprintf("%d", len(activeCommands())) + " |\n")
	b.WriteString("\nGenerated by Vigil Scribe from local repository facts.\n")
	b.WriteString("<!-- scribe:end -->\n")
	return b.String(), nil
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
	paths, _ := existingTestPathsChecked()
	return paths
}

func existingTestPathsChecked() ([]string, bool) {
	out := existingTestDirectories()
	goTests, ok := repositoryFilesBySuffixChecked("_test.go")
	if !ok {
		return nil, false
	}
	if len(goTests) > 0 {
		out = append(out, fmt.Sprintf("Go *_test.go (%d)", len(goTests)))
	}
	return out, true
}

func existingTestDirectories() []string {
	candidates := []string{"test", "tests", "__tests__", "spec", "specs"}
	var out []string
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			out = append(out, path)
		}
	}
	return out
}

func repositoryDisplayName() string {
	if cfg, _, err := loadConfig(""); err == nil && strings.TrimSpace(cfg.Project) != "" {
		return cfg.Project
	}
	return filepath.Base(gitRoot())
}

func repositoryFilesBySuffixChecked(suffix string) ([]string, bool) {
	var out []string
	files, ok := gitPathsChecked("ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if !ok {
		return nil, false
	}
	for _, file := range files {
		if strings.HasSuffix(file, suffix) {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return uniqueStrings(out), true
}

func accessibilityCommand(command string, args []string) int {
	switch command {
	case "a11y:inventory":
		jsonOut, err := parseJSONOnly(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
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
		return runAdapterCommand("playwright", "npx", append([]string{"--no-install", "playwright", "test"}, args...)...)
	default:
		return exitUsage
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
		return exitUsage
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
		return exitUsage
	}
}

func javascriptQuality(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
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
		return exitUsage
	}
	files, ok := trackedFilesBySuffixChecked(".php")
	if !ok {
		return adapterInputFailure(jsonOut, "php_lint", "could not enumerate tracked PHP files")
	}
	if len(files) == 0 {
		return checksOutput(jsonOut, "php_lint", []checkResult{{Name: "php:lint", Status: "warn", Detail: "no tracked PHP files"}})
	}
	if _, err := exec.LookPath("php"); err != nil {
		return requiredToolMissingOutput(jsonOut, "php_lint", "php")
	}
	var checks []checkResult
	for _, file := range files {
		checks = append(checks, runExternalCheck(file, "php", "-l", file))
	}
	return checksOutput(jsonOut, "php_lint", checks)
}

func dependencySecurity(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
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
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil deps:why [--json] <package>")
		return exitUsage
	}
	name := fs.Arg(0)
	type whyAdapter struct {
		Name   string
		Binary string
		Args   []string
	}
	var adapters []whyAdapter
	if fileExists("package.json") && jsonDependencyListed("package.json", name, "dependencies", "devDependencies", "peerDependencies", "optionalDependencies") {
		adapters = append(adapters, whyAdapter{Name: "npm", Binary: "npm", Args: []string{"explain", name}})
	}
	if fileExists("composer.json") && jsonDependencyListed("composer.json", name, "require", "require-dev") {
		adapters = append(adapters, whyAdapter{Name: "composer", Binary: "composer", Args: []string{"why", name}})
	}
	if fileExists("go.mod") && strings.Contains(name, ".") {
		adapters = append(adapters, whyAdapter{Name: "go", Binary: "go", Args: []string{"mod", "why", "-m", name}})
	}
	if len(adapters) == 0 && fileExists("Cargo.toml") {
		adapters = append(adapters, whyAdapter{Name: "cargo", Binary: "cargo", Args: []string{"tree", "--invert", name}})
	}
	if len(adapters) == 0 {
		message := "package is not an exact dependency in a supported manifest: " + name
		if *jsonOut {
			_ = printJSON(map[string]any{"status": "fail", "package": name, "error": message, "adapters": []any{}}, exitCheckFailed)
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", statusLabel("fail"), message)
		}
		return exitCheckFailed
	}
	checks := make([]checkResult, 0, len(adapters))
	for _, adapter := range adapters {
		if _, err := exec.LookPath(adapter.Binary); err != nil {
			checks = append(checks, checkResult{Name: adapter.Name, Status: "fail", Detail: adapter.Binary + " not found (required)", ExitCode: exitDependencyMissing})
			continue
		}
		checks = append(checks, runExternalCheck(adapter.Name, adapter.Binary, adapter.Args...))
	}
	status, exitCode := summarizeChecks(checks)
	if *jsonOut {
		return printStatusJSON(map[string]any{"status": status, "package": name, "checks": checks}, exitCode)
	}
	for _, check := range checks {
		fmt.Printf("%s %s: %s\n", statusLabel(check.Status), check.Name, check.Detail)
	}
	return exitCode
}

func jsonDependencyListed(path, name string, fields ...string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return false
	}
	for _, field := range fields {
		var dependencies map[string]json.RawMessage
		if err := json.Unmarshal(document[field], &dependencies); err == nil {
			if _, exists := dependencies[name]; exists {
				return true
			}
		}
	}
	return false
}

func dependencyManifestFiles() []string {
	return []string{"go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "composer.json", "composer.lock", "Cargo.toml", "Cargo.lock", "requirements.txt", "pyproject.toml"}
}

func runAdapterCommand(name, binary string, args ...string) int {
	if _, err := exec.LookPath(binary); err != nil {
		fmt.Fprintf(os.Stderr, "%s %s not found\n", statusLabel("fail"), binary)
		return exitDependencyMissing
	}
	result := runner.Run(context.Background(), runner.Spec{
		Name:         name,
		Mode:         runner.ModeArgv,
		Executable:   binary,
		Args:         args,
		Timeout:      10 * time.Minute,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		CaptureLimit: 1024 * 1024,
	})
	switch result.State {
	case runner.StateOK:
		fmt.Printf("%s %s\n", statusLabel("ok"), name)
		return exitSuccess
	case runner.StateToolMissing:
		return exitDependencyMissing
	case runner.StateCancelled, runner.StateTimedOut:
		return exitInterrupted
	case runner.StateFailed:
		return exitCheckFailed
	default:
		if result.Error != "" {
			fmt.Fprintln(os.Stderr, result.Error)
		}
		return exitInternal
	}
}

func requiredToolMissingOutput(jsonOut bool, check, binary string) int {
	if jsonOut {
		_ = printJSON(map[string]any{
			"status": "fail",
			"check":  check,
			"state":  runner.StateToolMissing,
			"error":  binary + " not found",
		}, exitDependencyMissing)
	} else {
		fmt.Fprintf(os.Stderr, "%s %s not found (required for %s)\n", statusLabel("fail"), binary, check)
	}
	return exitDependencyMissing
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
	result := runner.Run(context.Background(), runner.Spec{
		Name:         name,
		Mode:         runner.ModeArgv,
		Executable:   binary,
		Args:         args,
		Timeout:      2 * time.Minute,
		CaptureLimit: 32 * 1024 * 1024,
	})
	return externalCheckResult(name, result)
}

func externalCheckResult(name string, result runner.Result) checkResult {
	detail := trimOneLine(runnerOutput(result))
	if result.Truncated {
		return checkResult{Name: name, Status: "fail", Detail: detail, ExitCode: exitInternal}
	}
	switch result.State {
	case runner.StateOK:
		return checkResult{Name: name, Status: "ok", Detail: detail}
	case runner.StateToolMissing:
		return checkResult{Name: name, Status: "fail", Detail: detail, ExitCode: exitDependencyMissing}
	case runner.StateCancelled, runner.StateTimedOut:
		return checkResult{Name: name, Status: "fail", Detail: detail, ExitCode: exitInterrupted}
	case runner.StateFailed:
		return checkResult{Name: name, Status: "fail", Detail: detail, ExitCode: exitCheckFailed}
	default:
		return checkResult{Name: name, Status: "fail", Detail: detail, ExitCode: exitInternal}
	}
}

func checksOutput(jsonOut bool, name string, checks []checkResult) int {
	status, exitCode := summarizeChecks(checks)
	if jsonOut {
		return printStatusJSON(map[string]any{"status": status, "check": name, "checks": checks}, exitCode)
	}
	for _, check := range checks {
		fmt.Printf("%s %s: %s\n", statusLabel(check.Status), check.Name, check.Detail)
	}
	return exitCode
}

func repoHealth(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if gitRoot() == "" {
		fmt.Fprintln(os.Stderr, "not inside a git repository")
		return exitCheckFailed
	}
	commitLines, commitsOK := gitLinesChecked("rev-list", "--count", "HEAD")
	contributors, contributorsOK := gitLinesChecked("shortlog", "-sn", "--all")
	churn, churnOK := gitPathsChecked("log", "--name-only", "-z", "--pretty=format:")
	if !commitsOK || !contributorsOK || !churnOK {
		return adapterInputFailure(jsonOut, "repo_health", "could not read complete Git history")
	}
	commits := strings.TrimSpace(firstLine(commitLines))
	counts := map[string]int{}
	for _, file := range churn {
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
	return exitSuccess
}

func releasePolicy(args []string) int {
	fs := flag.NewFlagSet("checks:release-policy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	requireClean := fs.Bool("require-clean", false, "fail when the working tree is dirty")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	checks := []checkResult{}
	if gitRoot() == "" {
		checks = append(checks, checkResult{Name: "git", Status: "fail", Detail: "not inside git repository"})
	} else if statusEntries, ok := gitStatusChecked(); !ok {
		checks = append(checks, checkResult{Name: "clean_tree", Status: "fail", Detail: "could not read complete Git status", ExitCode: exitInternal})
	} else if len(statusEntries) > 0 {
		status := "warn"
		if *requireClean {
			status = "fail"
		}
		checks = append(checks, checkResult{Name: "clean_tree", Status: status, Detail: "working tree has changes"})
	} else {
		checks = append(checks, checkResult{Name: "clean_tree", Status: "ok", Detail: "clean"})
	}
	versionSources := existingFiles([]string{"VERSION"})
	for _, manifest := range []string{"package.json", "composer.json"} {
		if manifestHasVersion(manifest) {
			versionSources = append(versionSources, manifest)
		}
	}
	if tags, ok := gitLinesChecked("tag", "--points-at", "HEAD"); !ok {
		checks = append(checks, checkResult{Name: "version_source", Status: "fail", Detail: "could not read tags at HEAD", ExitCode: exitInternal})
	} else {
		if len(tags) > 0 {
			versionSources = append(versionSources, "git-tag:"+tags[0])
		}
		if len(versionSources) == 0 {
			checks = append(checks, checkResult{Name: "version_source", Status: "warn", Detail: "no common version source found"})
		} else {
			checks = append(checks, checkResult{Name: "version_source", Status: "ok", Detail: strings.Join(versionSources, ", ")})
		}
	}
	return checksOutput(*jsonOut, "release_policy", checks)
}

func manifestHasVersion(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	var version string
	return json.Unmarshal(manifest["version"], &version) == nil && strings.TrimSpace(version) != ""
}

func deployVerify(args []string) int {
	fs := flag.NewFlagSet("deploy:verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", "", "endpoint URL")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
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
		return exitUsage
	}
}

func testsHistory(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
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
	return exitSuccess
}

func testsAffected(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	unstaged, unstagedOK := gitPathsChecked("diff", "--name-only", "-z", "HEAD")
	staged, stagedOK := gitPathsChecked("diff", "--cached", "--name-only", "-z")
	if !unstagedOK || !stagedOK {
		return adapterInputFailure(jsonOut, "tests_affected", "could not read complete Git changes")
	}
	changed := append(unstaged, staged...)
	goTests, goTestsOK := repositoryFilesBySuffixChecked("_test.go")
	if !goTestsOK {
		return adapterInputFailure(jsonOut, "tests_affected", "could not enumerate repository tests")
	}
	testDirectories := existingTestDirectories()
	tests := map[string]bool{}
	for _, file := range changed {
		base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		for _, dir := range testDirectories {
			_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
				if err == nil && !entry.IsDir() && strings.Contains(strings.ToLower(filepath.Base(path)), strings.ToLower(base)) {
					tests[path] = true
				}
				return nil
			})
		}
		for _, testPath := range goTests {
			if strings.Contains(strings.ToLower(filepath.Base(testPath)), strings.ToLower(base)) {
				tests[testPath] = true
			}
		}
	}
	out := mapKeys(tests)
	if jsonOut {
		return printJSON(map[string]any{"status": "ok", "tests": out, "count": len(out)})
	}
	for _, file := range out {
		fmt.Println(file)
	}
	return exitSuccess
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

func trackedFilesBySuffixChecked(suffix string) ([]string, bool) {
	var out []string
	files, ok := gitPathsChecked("ls-files", "-z")
	if !ok {
		return nil, false
	}
	for _, file := range files {
		if strings.HasSuffix(file, suffix) {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return out, true
}

func adapterInputFailure(jsonOut bool, check, detail string) int {
	if jsonOut {
		return printStatusJSON(map[string]any{
			"status": "fail",
			"check":  check,
			"error":  detail,
		}, exitInternal)
	}
	fmt.Fprintln(os.Stderr, detail)
	return exitInternal
}

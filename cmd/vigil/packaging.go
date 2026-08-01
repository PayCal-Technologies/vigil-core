package main

import (
	"flag"
	"fmt"

	"github.com/PayCal-Technologies/vigil-public/internal/buildinfo"
	vigilcompletion "github.com/PayCal-Technologies/vigil-public/internal/completion"

	vigilsupport "github.com/PayCal-Technologies/vigil-public/internal/support"
	"os"

	"path/filepath"

	"sort"
	"strings"

	"time"
)

func supportBundle(configPath string, args []string) int {
	fs := flag.NewFlagSet("support:bundle", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	dryRun := fs.Bool("dry-run", false, "preview only")
	includeConfig := fs.Bool("include-config", false, "include the complete config by explicit request")
	includeGitStatus := fs.Bool("include-git-status", false, "include redacted Git status by explicit request")
	outputPath := fs.String("output", "", "output path")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	cfg, cfgPath, cfgErr := loadConfig(configPath)
	input := vigilsupport.Input{
		GeneratedAt:      time.Now(),
		ConfigPath:       cfgPath,
		IncludeConfig:    *includeConfig,
		IncludeGitStatus: *includeGitStatus,
		Build:            buildinfo.Current(),
		Commands:         activeCommands(),
		Packs:            loadExtensions(extensionRoot()),
		DiagnosticPaths:  []string{extensionRoot(), userExtensionRoot()},
	}
	if cfgErr != nil {
		input.ConfigError = cfgErr.Error()
	} else {
		input.ConfigSummary = map[string]any{
			"schema_version":     cfg.SchemaVersion,
			"profile":            cfg.Profile,
			"gate_count":         len(cfg.Gates),
			"extensions_enabled": cfg.Extensions.Enabled,
		}
		input.Config = cfg
	}
	if *includeGitStatus {
		statusEntries, ok := gitStatusChecked()
		if !ok {
			fmt.Fprintln(os.Stderr, "could not read complete Git status")
			return exitInternal
		}
		input.GitStatus = make([]vigilsupport.GitStatusEntry, 0, len(statusEntries))
		for _, entry := range statusEntries {
			input.GitStatus = append(input.GitStatus, vigilsupport.GitStatusEntry{
				Status:       entry.Status,
				Path:         entry.Path,
				OriginalPath: entry.OriginalPath,
			})
		}
	}
	bundle := vigilsupport.Build(input)
	bundleID, _ := bundle["bundle_id"].(string)
	proposedPath := strings.TrimSpace(*outputPath)
	if proposedPath == "" {
		proposedPath = vigilsupport.DefaultPath(bundleID)
	}
	vigilsupport.AddOutput(bundle, proposedPath)
	if *dryRun {
		return printJSON(bundle)
	}
	if err := vigilsupport.Write(proposedPath, bundle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if *jsonOut {
		return printJSON(map[string]any{
			"status":           "ok",
			"bundle_id":        bundleID,
			"path":             proposedPath,
			"permissions":      "0600",
			"uploaded":         false,
			"redaction_report": bundle["redaction_report"],
		})
	}
	fmt.Println(proposedPath)
	return exitSuccess
}

func completion(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil completion bash|zsh|fish")
		return exitUsage
	}
	registry, err := newCommandRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	var tags []string
	if cfg, _, err := loadConfig(""); err == nil {
		for _, gate := range cfg.Gates {
			tags = append(tags, gate.Tags...)
		}
	}
	var packIDs []string
	for _, pack := range loadExtensions(extensionRoot()).Extensions {
		packIDs = append(packIDs, pack.ID)
	}
	content, err := vigilcompletion.Generate(args[0], registry.Commands(), globalFlagSpecs(), vigilcompletion.DynamicValues{
		Profiles: profileNames(),
		GateTags: uniqueSortedStrings(tags),
		PackIDs:  uniqueSortedStrings(packIDs),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Usage: vigil completion bash|zsh|fish")
		return exitUsage
	}
	fmt.Print(content)
	return exitSuccess
}

func uniqueSortedStrings(values []string) []string {
	values = uniqueStrings(values)
	sort.Strings(values)
	return values
}

func manpageGenerate(args []string) int {
	fs := flag.NewFlagSet("manpage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	output := fs.String("output", "", "write manpage to path instead of stdout")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	content := generateManpage()
	if strings.TrimSpace(*output) == "" {
		fmt.Print(content)
		return exitSuccess
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if _, err := atomicWriteFile(*output, []byte(content), fileExists(*output)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	fmt.Printf("wrote %s\n", *output)
	return exitSuccess
}

func manpageInstall(args []string) int {
	fs := flag.NewFlagSet("manpage:install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	prefix := fs.String("prefix", "/usr/local", "installation prefix")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	path := filepath.Join(*prefix, "share", "man", "man1", "vigil.1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if _, err := atomicWriteFile(path, []byte(generateManpage()), fileExists(path)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	fmt.Printf("installed %s\n", path)
	return exitSuccess
}

func generateManpage() string {
	var b strings.Builder
	info := buildinfo.Current()
	b.WriteString(".TH VIGIL 1 \"" + buildinfo.ReproducibleDate() + "\" \"vigil " + info.Version + "\" \"User Commands\"\n")
	b.WriteString(".SH NAME\nvigil \\- policy-aware repository preflight engine\n")
	b.WriteString(".SH SYNOPSIS\n.B vigil\n[--config PATH] [--allow-mutation|--auto] <command> [args]\n")
	b.WriteString(".SH DESCRIPTION\nVigil lets humans and coding agents inspect, approve, run, and verify repository automation before that automation changes a project. Read-only verification covers the Git-visible workspace and is not an operating-system sandbox.\n")
	b.WriteString(".SH COMMANDS\n")
	for _, command := range activeCommands() {
		b.WriteString(".TP\n.B " + roffEscape(command.Command) + "\n")
		b.WriteString(roffEscape(command.Description) + "\n")
		if command.Usage != "" {
			b.WriteString(".br\nUsage: " + roffEscape(command.Usage) + "\n")
		}
		b.WriteString(".br\nAccess: " + roffEscape(command.Access) + "; stability: " + roffEscape(command.Stability) + "\n")
		for _, option := range command.Flags {
			display := option.Long
			if option.Short != "" {
				display = option.Short + ", " + option.Long
			}
			if option.ValueName != "" {
				display += " " + option.ValueName
			}
			b.WriteString(".br\n" + roffEscape(display) + " \\- " + roffEscape(option.Description) + "\n")
		}
	}
	b.WriteString(".SH REVIEWED PLANS\nUse .B vigil plan --json to inspect a digest-bound plan. Write one with .B vigil --allow-mutation plan --output .vigil/plans/reviewed.json and execute it with .B vigil --allow-mutation apply .vigil/plans/reviewed.json. Apply fails when reviewed inputs changed.\n")
	b.WriteString(".SH MACHINE OUTPUT\nThe --json compatibility flag emits envelope schema 1. Commands advertise JSONL, JUnit, SARIF, and GitHub annotation support in .B vigil list --json.\n")
	b.WriteString(".SH SETUP\nRun .B vigil setup:wizard or .B vigil init for guided configuration. Use .B vigil --allow-mutation setup:wizard to permit confirmed writes.\n")
	b.WriteString(".SH LICENSE\n0BSD\n")
	return b.String()
}

func roffEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "-", `\-`)
	if strings.HasPrefix(value, ".") || strings.HasPrefix(value, "'") {
		value = `\&` + value
	}
	return value
}

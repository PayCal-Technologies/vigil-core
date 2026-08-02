package main

import (
	"context"

	"flag"
	"fmt"

	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigilconfig "github.com/PayCal-Technologies/vigil-public/internal/config"

	vigilpacks "github.com/PayCal-Technologies/vigil-public/internal/packs"
	vigilplugins "github.com/PayCal-Technologies/vigil-public/internal/plugins"
	"os"

	"strings"
)

const (
	configSchemaVersion    = vigilconfig.SchemaVersion
	extensionSchemaVersion = vigilpacks.SchemaVersion
	defaultConfigName      = "vigil.config.json"
	vigilCoreGoVersion     = "1.26.5"
	vigilCoreModulePath    = "github.com/PayCal-Technologies/vigil-public/cmd/vigil"
)

func runContext(ctx context.Context, args []string) int {
	if ctx == nil {
		ctx = context.Background()
	}
	args, confirmation := extractConfirmationArgs(args)
	global := flag.NewFlagSet("vigil", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	configPath := global.String("config", "", "config file path")
	help := global.Bool("help", false, "show help")
	helpShort := global.Bool("h", false, "show help")
	plain := global.Bool("plain", false, "minimal plain text output")
	noColor := global.Bool("no-color", false, "disable ANSI color")
	showVersion := global.Bool("version", false, "show version")
	if err := global.Parse(args); err != nil {
		return exitUsage
	}
	if *plain {
		_ = os.Setenv("VIGIL_PLAIN", "1")
		_ = os.Setenv("NO_COLOR", "1")
	}
	if *noColor {
		_ = os.Setenv("NO_COLOR", "1")
	}
	if strings.TrimSpace(*configPath) != "" {
		_ = os.Setenv("VIGIL_CONFIG", *configPath)
	}
	rest := append([]string(nil), global.Args()...)
	if *showVersion {
		rest = append([]string{"version"}, rest...)
	} else if *help || *helpShort {
		rest = append([]string{"help"}, rest...)
	}
	if len(rest) == 0 {
		if !fileExists(resolvedConfigPath(*configPath)) {
			printBeginnerWelcome()
		} else {
			printHelp()
		}
		if isInteractiveTerminal() {
			runSetup, control := wizardBool("Run setup wizard now?", !fileExists(resolvedConfigPath(*configPath)))
			if control == "quit" {
				return exitInterrupted
			}
			if runSetup {
				return setupWizard(*configPath, []string{})
			}
		}
		return exitSuccess
	}
	if exitCode, handled := runFastReadCommand(rest[0], rest[1:]); handled {
		return exitCode
	}
	registry, err := newCommandRegistryForConfigContext(ctx, *configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s command registry invalid: %v\n", statusLabel("fail"), err)
		return exitInternal
	}
	invokedAs := rest[0]
	command, ok := registry.Resolve(invokedAs)
	if !ok {
		if plugin, issues, exitCode, found := unavailablePluginCommand(ctx, *configPath, invokedAs); found {
			if wantsJSONEnvelope(rest[1:]) {
				finishOutput := beginCommandOutput(invokedAs)
				defer finishOutput()
				return printJSON(map[string]any{
					"error":          "locked plugin command is unavailable",
					"plugin_command": invokedAs,
					"plugin":         plugin,
					"issues":         issues,
				}, exitCode)
			}
			fmt.Fprintf(os.Stderr, "%s plugin command %q is unavailable (%s@%s)\n", statusLabel("fail"), invokedAs, plugin.ID, plugin.Version)
			for _, issue := range issues {
				fmt.Fprintf(os.Stderr, "%s %s\n", statusLabel("fail"), vigilplugins.FormatIssue(issue))
			}
			return exitCode
		}
		if wantsJSONEnvelope(rest[1:]) {
			finishOutput := beginCommandOutput(invokedAs)
			defer finishOutput()
			return printJSON(map[string]any{"error": fmt.Sprintf("unknown command %q", invokedAs)}, vigilcli.ExitUsage)
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", invokedAs)
		printHelp()
		return exitUsage
	}
	finishOutput := beginCommandOutput(command.Name)
	defer finishOutput()
	commandArgs := prepareCommandArgs(command.Name, rest[1:], confirmation)
	mutationArgs := append([]string(nil), commandArgs...)
	if confirmation.AllowMutation {
		mutationArgs = append(mutationArgs, "--allow-mutation")
	}
	requiresMutation := command.RequiresMutation(mutationArgs)
	if requiresMutation && !confirmation.Allowed(command.Name) {
		if wantsJSONEnvelope(commandArgs) {
			return printJSON(map[string]any{
				"error":   "permission to write files is required",
				"command": command.Name,
			}, vigilcli.ExitPolicyBlocked)
		}
		return mutationConfirmationError(command.Name, confirmation)
	}
	if requiresMutation && !mutationRequirementsSatisfied(*configPath, command.Name, confirmation) {
		return vigilcli.ExitPolicyBlocked
	}
	invocationContext, cancel := context.WithTimeout(ctx, command.Timeout)
	defer cancel()
	exitCode := command.Handler(vigilcli.Invocation{
		Context:       invocationContext,
		Command:       command.Name,
		InvokedAs:     invokedAs,
		Args:          commandArgs,
		ConfigPath:    *configPath,
		AllowMutation: confirmation.AllowMutation,
		Auto:          confirmation.Auto,
	})
	return vigilcli.ClassifyExit(exitCode).Code
}

func runFastReadCommand(command string, args []string) (int, bool) {
	var handler func() int
	switch command {
	case "version":
		handler = func() int { return versionCommand(args) }
	case "help":
		if len(args) == 0 {
			handler = func() int {
				printHelp()
				return exitSuccess
			}
		} else {
			handler = func() int { return commandHelp(args) }
		}
	case "list":
		handler = func() int { return listCommands(args) }
	default:
		return exitSuccess, false
	}
	finishOutput := beginCommandOutput(command)
	defer finishOutput()
	return vigilcli.ClassifyExit(handler()).Code, true
}

func wantsJSONEnvelope(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "--format=json" {
			return true
		}
	}
	return false
}

func prepareCommandArgs(command string, args []string, confirmation confirmationArgs) []string {
	prepared := append([]string(nil), args...)
	switch command {
	case "init", "setup", "setup:wizard":
		if confirmation.AllowMutation && !hasFlag(prepared, "dry-run") && !hasFlag(prepared, "json") && !hasFlag(prepared, "write") {
			prepared = append(prepared, "--write")
		}
	}
	return prepared
}

func printHelp() {
	commands := activeCommands()
	width := 0
	for _, command := range commands {
		if len(command.Command) > width {
			width = len(command.Command)
		}
	}
	fmt.Println("Vigil")
	fmt.Println("Policy-aware repository preflight engine")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  vigil [--config PATH] [--allow-mutation|--auto] <command> [args]")
	fmt.Println()
	order := []string{"Core", "Checks", "Config", "Packs", "Plugins", "Automation", "Packaging", "Setup"}
	for _, category := range order {
		printed := false
		for _, command := range commands {
			if commandCategory(command) != category {
				continue
			}
			if !printed {
				fmt.Println(strings.ToLower(category))
				printed = true
			}
			fmt.Printf("  %-3s %-*s   %s\n", compactAccessMarker(command.Access), width, command.Command, compactHelpDescription(command.Description))
		}
	}
}

func printBeginnerWelcome() {
	fmt.Println("Welcome to Vigil")
	fmt.Println()
	fmt.Println("Vigil checks your project before you publish or share it.")
	fmt.Println()
	fmt.Println("It can:")
	fmt.Println("  [OK] detect the kind of project you have")
	fmt.Println("  [OK] find common tests and quality checks")
	fmt.Println("  [OK] show what each check will do")
	fmt.Println("  [OK] warn if a check changes files unexpectedly")
	fmt.Println("  [OK] explain failures in plain language")
	fmt.Println()
	fmt.Println("What would you like to do?")
	fmt.Println()
	fmt.Println("  1. Check this project       vigil check")
	fmt.Println("  2. Set up Vigil             vigil setup")
	fmt.Println("  3. Learn what Vigil does    vigil learn")
	fmt.Println("  4. Show advanced commands   vigil advanced")
}

func printBeginnerHelp() {
	fmt.Println("Vigil")
	fmt.Println("Checks a software project before you publish, push, or release it.")
	fmt.Println()
	fmt.Println("Common commands:")
	fmt.Println("  vigil check      Run the normal project checks")
	fmt.Println("  vigil setup      Set up Vigil with guided questions")
	fmt.Println("  vigil explain    Show what Vigil will do")
	fmt.Println("  vigil status     Show whether the project appears ready")
	fmt.Println("  vigil fix        Suggest safe next actions")
	fmt.Println("  vigil learn      Learn one feature at a time")
	fmt.Println("  vigil advanced   Show the complete command interface")
	fmt.Println()
	fmt.Println("Most commands do not change files. Commands that write files ask for explicit permission.")
}

type commandInfo struct {
	Command        string              `json:"command"`
	Aliases        []string            `json:"aliases,omitempty"`
	Source         string              `json:"source"`
	Pack           string              `json:"pack,omitempty"`
	Binding        string              `json:"binding"`
	Category       string              `json:"category,omitempty"`
	Description    string              `json:"description"`
	Access         string              `json:"access"`
	Capabilities   []string            `json:"capabilities"`
	Args           string              `json:"args"`
	Flags          []vigilcli.Flag     `json:"flags"`
	Arguments      []vigilcli.Argument `json:"arguments"`
	Stability      string              `json:"stability"`
	HostAPIVersion string              `json:"host_api_version"`
	Timeout        string              `json:"timeout"`
	Network        string              `json:"network"`
	RequiredTools  []string            `json:"required_tools"`
	OutputFormats  []string            `json:"output_formats"`
	Interactive    bool                `json:"interactive"`
	WriteFlags     []string            `json:"write_flags,omitempty"`
	ReadOnlyFlags  []string            `json:"read_only_flags,omitempty"`
	AutoEnabled    bool                `json:"auto_enabled,omitempty"`
	AutoReason     string              `json:"auto_reason,omitempty"`
	Usage          string              `json:"usage,omitempty"`
	InstallHint    string              `json:"install_hint,omitempty"`
	Examples       []string            `json:"examples,omitempty"`
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

type confirmationArgs struct {
	AllowMutation bool
	Auto          bool
}

func extractConfirmationArgs(args []string) ([]string, confirmationArgs) {
	var confirmation confirmationArgs
	clean := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--allow-mutation":
			confirmation.AllowMutation = true
		case "--auto":
			confirmation.Auto = true
		default:
			clean = append(clean, arg)
		}
	}
	return clean, confirmation
}

func (a confirmationArgs) Allowed(command string) bool {
	if a.AllowMutation {
		return true
	}
	return a.Auto && autoEnabledCommand(command)
}

func autoEnabledCommand(command string) bool {
	registry, err := newCommandRegistry()
	if err != nil {
		return false
	}
	spec, ok := registry.Resolve(command)
	return ok && spec.AutoEnabled
}

func requiresMutationConfirmation(command string, args []string) bool {
	if registry, err := newCommandRegistry(); err == nil {
		if spec, ok := registry.Resolve(command); ok {
			return spec.RequiresMutation(args)
		}
	}
	return extensionCommandRequiresMutation(command, args)
}

func extensionCommandRequiresMutation(command string, args []string) bool {
	contract, ok := extensionCommandContractFor(command)
	if !ok {
		return false
	}
	switch strings.TrimSpace(contract.Access) {
	case "write":
		return true
	case "conditional-write":
		for _, flag := range contract.WriteFlags {
			if hasFlag(args, strings.TrimPrefix(flag, "--")) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func extensionCommandAccess(command string) string {
	contract, ok := extensionCommandContractFor(command)
	if !ok {
		return ""
	}
	return strings.TrimSpace(contract.Access)
}

func extensionCommandContractFor(command string) (extensionCommandContract, bool) {
	for _, ext := range loadExtensions(extensionRoot()).Extensions {
		for _, contract := range ext.CommandContracts {
			if contract.Command == command {
				return contract, true
			}
		}
	}
	return extensionCommandContract{}, false
}

func mutationRequirementsSatisfied(configPath string, command string, confirmation confirmationArgs) bool {
	switch command {
	case "config:init", "config:repair", "config:migrate", "hooks:uninstall", "init", "setup", "setup:wizard":
		return true
	}
	cfg, path, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s configuration must be valid before this command can write files: %s: %v\n", statusLabel("fail"), path, err)
		return false
	}
	requirements := cfg.Coordination.MutationRequires
	if len(requirements) == 0 {
		requirements = []string{"explicit-confirmation", "clean-config"}
	}
	for _, requirement := range requirements {
		switch strings.TrimSpace(requirement) {
		case "", "explicit-confirmation":
			if !confirmation.AllowMutation && !confirmation.Auto {
				fmt.Fprintf(os.Stderr, "%s explicit approval is required before this command can write files\n", statusLabel("fail"))
				return false
			}
		case "clean-config":

		case "clean-tree":
			if snapshot, ok := gitMutationFingerprint(); !ok {
				fmt.Fprintf(os.Stderr, "%s a clean Git tree is required, but Vigil could not inspect Git state\n", statusLabel("fail"))
				return false
			} else if !snapshot.Clean {
				fmt.Fprintf(os.Stderr, "%s a clean Git tree is required before this command can write files\n", statusLabel("fail"))
				return false
			}
		default:
			fmt.Fprintf(os.Stderr, "%s unsupported write-safety requirement: %s\n", statusLabel("fail"), requirement)
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

func mutationConfirmationError(command string, confirmation confirmationArgs) int {
	fmt.Fprintf(os.Stderr, "%s write approval required for %s\n", statusLabel("fail"), command)
	if autoEnabledCommand(command) {
		fmt.Fprintf(os.Stderr, "rerun with --auto for deterministic repair or --allow-mutation for explicit write approval\n")
	} else if confirmation.Auto {
		fmt.Fprintf(os.Stderr, "--auto is not available for %s; rerun with --allow-mutation after review\n", command)
	} else {
		fmt.Fprintf(os.Stderr, "rerun with --allow-mutation after review\n")
	}
	return vigilcli.ExitPolicyBlocked
}

func commandHelp(args []string) int {
	fs := flag.NewFlagSet("help", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	beginner := fs.Bool("beginner", false, "show beginner help")
	standard := fs.Bool("standard", false, "show standard help")
	expert := fs.Bool("expert", false, "show expert help")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	modes := boolCount(*beginner, *standard, *expert)
	if modes > 1 {
		fmt.Fprintln(os.Stderr, "choose only one of --beginner, --standard, or --expert")
		return exitUsage
	}
	if fs.NArg() == 0 {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "commands": activeCommands()})
		}
		switch {
		case *beginner:
			printBeginnerHelp()
		default:
			printHelp()
		}
		return exitSuccess
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil help [--json] [--beginner|--standard|--expert] [command]")
		return exitUsage
	}
	commandName := fs.Arg(0)
	if *beginner {
		return beginnerCommandHelp(commandName, *jsonOut)
	}
	manual, ok := manualForCommand(commandName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", commandName)
		return exitUsage
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
	return exitSuccess
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
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
			access = commandAccess(info.Command)
		}
		return commandManual{Command: info.Command, Source: info.Source, Access: access, Usage: usage, Description: info.Description, Examples: info.Examples, InstallHint: info.InstallHint, Related: relatedCommands(info.Command)}, true
	}
	return commandManual{}, false
}

func listCommands(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	commands := activeCommands()
	if jsonOut {
		return printJSON(commands)
	}
	for _, command := range commands {
		fmt.Printf("%-22s %-10s %s\n", command.Command, command.Source, command.Description)
	}
	return exitSuccess
}

func explain(args []string) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		return explainProject(*jsonOut)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil explain [--json] [command]")
		return exitUsage
	}
	name := fs.Arg(0)
	if manual, ok := manualForCommand(name); ok {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "manual": manual})
		}
		fmt.Printf("%s\nsource=%s\naccess=%s\nusage=%s\n%s\n", manual.Command, manual.Source, manual.Access, manual.Usage, manual.Description)
		return exitSuccess
	}
	fmt.Fprintf(os.Stderr, "unknown command: %s\n", name)
	return exitUsage
}

func catalogCommand(command string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
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
		return exitSuccess
	}
	for _, resource := range payload["resources"].([]map[string]string) {
		fmt.Printf("%s\t%s\n", resource["uri"], resource["description"])
	}
	return exitSuccess
}

func guardsSummary(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	readOnly := []string{}
	mutating := []string{}
	for _, command := range activeCommands() {
		if command.Access == "read" {
			readOnly = append(readOnly, command.Command)
		} else {
			mutating = append(mutating, command.Command)
		}
	}
	payload := map[string]any{"status": "ok", "read_only": readOnly, "mutating": mutating, "read_only_count": len(readOnly), "mutating_count": len(mutating), "confirmation": "commands that write files require explicit human or CI approval"}
	if jsonOut {
		return printJSON(payload)
	}
	fmt.Printf("%s read=%d write=%d\n", statusLabel("ok"), len(readOnly), len(mutating))
	return exitSuccess
}

func extensionCommand(command string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	report := loadExtensions(extensionRoot())
	if jsonOut {
		exit := exitSuccess
		if command == "extensions:doctor" && report.Status == "fail" {
			exit = exitUsage
		}
		_ = printJSON(report, exit)
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
		return exitUsage
	}
	return exitSuccess
}

func activeCommands() []commandInfo {
	return activeCommandsForConfig("")
}

func activeCommandsForConfig(configPath string) []commandInfo {
	registry, err := newCommandRegistryForConfig(configPath)
	if err != nil {
		return nil
	}
	specs := registry.Commands()
	commands := make([]commandInfo, 0, len(specs))
	for _, spec := range specs {
		capabilities := make([]string, 0, len(spec.Capabilities))
		for _, capability := range spec.Capabilities {
			capabilities = append(capabilities, string(capability))
		}
		commands = append(commands, commandInfo{
			Command:        spec.Name,
			Aliases:        append([]string(nil), spec.Aliases...),
			Source:         spec.Source,
			Pack:           spec.Pack,
			Binding:        spec.Binding,
			Category:       spec.Category,
			Description:    spec.Summary,
			Access:         string(spec.Access),
			Capabilities:   capabilities,
			Args:           spec.Args,
			Flags:          append([]vigilcli.Flag{}, spec.Flags...),
			Arguments:      append([]vigilcli.Argument{}, spec.Arguments...),
			Stability:      string(spec.Stability),
			HostAPIVersion: spec.HostAPIVersion,
			Timeout:        spec.Timeout.String(),
			Network:        spec.Network,
			RequiredTools:  append([]string{}, spec.RequiredTools...),
			OutputFormats:  append([]string{}, spec.OutputFormats...),
			Interactive:    spec.Interactive,
			WriteFlags:     append([]string(nil), spec.WriteFlags...),
			ReadOnlyFlags:  append([]string(nil), spec.ReadOnlyFlags...),
			AutoEnabled:    spec.AutoEnabled,
			AutoReason:     spec.AutoReason,
			Usage:          spec.Usage,
			InstallHint:    spec.InstallHint,
			Examples:       append([]string(nil), spec.Examples...),
		})
	}
	return commands
}

func commandCategory(command commandInfo) string {
	if command.Category != "" {
		return command.Category
	}
	switch {
	case command.Command == "init" || command.Command == "setup" || command.Command == "setup:wizard":
		return "Setup"
	case strings.HasPrefix(command.Command, "checks:") || strings.HasPrefix(command.Command, "deps:") || command.Command == "verify":
		return "Checks"
	case strings.HasPrefix(command.Command, "config:") || command.Command == "settings:show":
		return "Config"
	case strings.HasPrefix(command.Command, "extensions:"):
		return "Extensions"
	case command.Command == "init:ci" || command.Command == "github:init-ci" || strings.HasPrefix(command.Command, "hooks:"):
		return "Automation"
	case command.Command == "completion" || strings.HasPrefix(command.Command, "manpage"):
		return "Packaging"
	default:
		return "Core"
	}
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
		"github:init-ci":             "Generate GitHub Actions helper workflow from Vigil checks.",
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

func commandAccess(command string) string {
	if registry, err := newCommandRegistry(); err == nil {
		if spec, ok := registry.Resolve(command); ok {
			return string(spec.Access)
		}
	}
	return extensionCommandAccess(command)
}

func compactAccessMarker(access string) string {
	switch access {
	case "read":
		return "r"
	case "write":
		return "w"
	case "conditional-write":
		return "r/w"
	default:
		return access
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

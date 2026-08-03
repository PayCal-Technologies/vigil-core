package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/buildinfo"
	vigilcache "github.com/PayCal-Technologies/vigil-public/internal/cache"
	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
)

const maxCachedCommandRegistries = 16

var commandRegistries = vigilcache.NewLRU[string, *vigilcli.Registry](maxCachedCommandRegistries)

func newCommandRegistry() (*vigilcli.Registry, error) {
	return newCommandRegistryForConfig("")
}

func newCommandRegistryForConfig(configPath string) (*vigilcli.Registry, error) {
	return newCommandRegistryForConfigContext(context.Background(), configPath)
}

func newCommandRegistryForConfigContext(ctx context.Context, configPath string) (*vigilcli.Registry, error) {
	commands := append(coreCommandSpecs(), packCommandSpecsForConfig(configPath)...)
	commands = append(commands, pluginCommandSpecsForConfig(ctx, configPath, commands)...)
	cacheKey, cacheable := commandRegistryCacheKey(configPath, commands)
	if cacheable {
		if registry, ok := commandRegistries.Get(cacheKey); ok {
			return registry, nil
		}
	}
	registry, err := vigilcli.New(commands)
	if err != nil {
		return nil, err
	}
	if cacheable {
		commandRegistries.Put(cacheKey, registry)
	}
	return registry, nil
}

func commandRegistryCacheKey(configPath string, commands []vigilcli.Command) (string, bool) {
	commandMetadata := make([]map[string]any, 0, len(commands))
	commandType := reflect.TypeFor[vigilcli.Command]()
	for _, command := range commands {
		value := reflect.ValueOf(command)
		fields := make(map[string]any, commandType.NumField()-1)
		for fieldIndex := 0; fieldIndex < commandType.NumField(); fieldIndex++ {
			field := commandType.Field(fieldIndex)
			if field.Name == "Handler" {
				continue
			}
			fields[field.Name] = value.Field(fieldIndex).Interface()
		}
		commandMetadata = append(commandMetadata, fields)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", false
	}
	data, err := json.Marshal(map[string]any{
		"commands":       commandMetadata,
		"config_path":    resolvedConfigPath(configPath),
		"home":           os.Getenv("HOME"),
		"plugin_root":    os.Getenv("VIGIL_PLUGIN_ROOT"),
		"user_pack_root": os.Getenv("VIGIL_USER_PACK_ROOT"),
		"working_dir":    workingDirectory,
		"xdg_config":     os.Getenv("XDG_CONFIG_HOME"),
	})
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:]), true
}

func coreCommandSpecs() []vigilcli.Command {
	fsRead := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead}
	fsWrite := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityFilesystemWrite}
	gitRead := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityGitRead, vigilcli.CapabilityProcess}
	gitWrite := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityFilesystemWrite, vigilcli.CapabilityGitRead, vigilcli.CapabilityGitWrite, vigilcli.CapabilityProcess}
	process := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityGitRead, vigilcli.CapabilityProcess}
	planCapabilities := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityFilesystemWrite, vigilcli.CapabilityGitRead, vigilcli.CapabilityProcess}
	workflowCapabilities := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityFilesystemWrite, vigilcli.CapabilityGitRead, vigilcli.CapabilityGitWrite, vigilcli.CapabilityNetwork, vigilcli.CapabilityProcess, vigilcli.CapabilityEnvironment, vigilcli.CapabilitySecrets}
	interactiveWrite := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityFilesystemWrite, vigilcli.CapabilityInteractive}
	pluginRead := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityProcess}
	pluginStateWrite := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityFilesystemWrite}
	pluginIndexRead := []vigilcli.Capability{vigilcli.CapabilityFilesystemRead, vigilcli.CapabilityNetwork}
	pluginIndexWrite := []vigilcli.Capability{
		vigilcli.CapabilityFilesystemRead,
		vigilcli.CapabilityFilesystemWrite,
		vigilcli.CapabilityNetwork,
		vigilcli.CapabilityProcess,
	}
	pluginConformance := []vigilcli.Capability{
		vigilcli.CapabilityFilesystemRead,
		vigilcli.CapabilityFilesystemWrite,
		vigilcli.CapabilityNetwork,
		vigilcli.CapabilityProcess,
	}

	command := func(name, category, summary string, access vigilcli.Access, capabilities []vigilcli.Capability, handler vigilcli.Handler) vigilcli.Command {
		return vigilcli.Command{
			Name:           name,
			Summary:        summary,
			Handler:        handler,
			Access:         access,
			Capabilities:   capabilities,
			Args:           "[args]",
			Source:         "core",
			Binding:        "builtin:" + name,
			Category:       category,
			Stability:      vigilcli.StabilityStable,
			HostAPIVersion: "v1",
			Timeout:        2 * time.Minute,
			Network:        "none",
			RequiredTools:  []string{},
			OutputFormats:  commandOutputFormats(name),
			Usage:          "vigil " + name + " [args]",
		}
	}

	commands := []vigilcli.Command{
		command("help", "Core", "Show command help and contracts.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			if len(inv.Args) > 0 {
				return commandHelp(inv.Args)
			}
			printHelp()
			return exitSuccess
		}),
		command("list", "Core", "List core and loaded pack commands.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return listCommands(inv.Args)
		}),
		command("version", "Core", "Print Vigil version and build metadata.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return versionCommand(inv.Args)
		}),
		command("doctor", "Core", "Check local readiness for using Vigil.", vigilcli.AccessRead, process, func(inv vigilcli.Invocation) int {
			return doctorContext(inv.Context, inv.ConfigPath, inv.Args)
		}),
		command("status", "Core", "Summarize config, pack, Git, and command readiness.", vigilcli.AccessRead, process, func(inv vigilcli.Invocation) int {
			return statusContext(inv.Context, inv.ConfigPath, inv.Args)
		}),
		command("next", "Core", "Prioritize next local setup and verification actions.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return next(inv.ConfigPath, inv.Args)
		}),
		command("plan", "Core", "Create a digest-bound plan for configured local checks.", vigilcli.AccessConditionalWrite, planCapabilities, func(inv vigilcli.Invocation) int {
			return plan(inv.ConfigPath, inv.Args, inv.AllowMutation)
		}),
		command("apply", "Core", "Verify and execute an unchanged reviewed plan.", vigilcli.AccessWrite, workflowCapabilities, func(inv vigilcli.Invocation) int {
			return applyPlanCommand(inv.Context, inv.Args, inv.AllowMutation)
		}),
		command("explain", "Core", "Explain a command's source, access, and usage.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return explain(inv.Args)
		}),
		command("workflow:local", "Core", "Preview or run configured local preflight checks.", vigilcli.AccessConditionalWrite, workflowCapabilities, func(inv vigilcli.Invocation) int {
			return workflowLocalContext(inv.Context, inv.ConfigPath, inv.Args, inv.AllowMutation)
		}),
		command("verify", "Checks", "Run the public readiness proof set.", vigilcli.AccessRead, process, func(inv vigilcli.Invocation) int {
			return verifyContext(inv.Context, inv.ConfigPath, inv.Args)
		}),
		command("hooks:install", "Automation", "Install Vigil Git hook shims into the current repository.", vigilcli.AccessWrite, gitWrite, func(inv vigilcli.Invocation) int {
			return hooksInstall(inv.Args)
		}),
		command("hooks:doctor", "Automation", "Inspect Vigil hook paths, ownership, and backups.", vigilcli.AccessRead, gitRead, func(inv vigilcli.Invocation) int {
			return hooksDoctor(inv.Args)
		}),
		command("hooks:uninstall", "Automation", "Restore chained hooks or remove Vigil-managed hooks.", vigilcli.AccessWrite, gitWrite, func(inv vigilcli.Invocation) int {
			return hooksUninstall(inv.Args)
		}),
		command("hooks:pre-commit", "Automation", "Run pre-commit checks from Vigil config.", vigilcli.AccessRead, process, func(inv vigilcli.Invocation) int {
			return hookRunContext(inv.Context, inv.ConfigPath, "pre-commit", inv.Args)
		}),
		command("hooks:pre-push", "Automation", "Run pre-push checks from Vigil config.", vigilcli.AccessRead, process, func(inv vigilcli.Invocation) int {
			return hookRunContext(inv.Context, inv.ConfigPath, "pre-push", inv.Args)
		}),
		command("checks:staged-sensitive", "Checks", "Scan staged files for common secret patterns before commit.", vigilcli.AccessRead, gitRead, func(inv vigilcli.Invocation) int {
			return checkStagedSensitive(inv.Args)
		}),
		command("checks:workspace-hygiene", "Checks", "Detect local OS, editor, backup, and temporary artifacts.", vigilcli.AccessRead, gitRead, func(inv vigilcli.Invocation) int {
			return checkWorkspaceHygiene(inv.Args)
		}),
		command("checks:command-catalog", "Checks", "Audit the active command catalog for malformed entries.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return checkCommandCatalog(inv.Args)
		}),
		command("checks:public-assumptions", "Checks", "Scan public Vigil source for product-specific assumptions.", vigilcli.AccessRead, gitRead, func(inv vigilcli.Invocation) int {
			return checkPublicAssumptions(inv.ConfigPath, inv.Args)
		}),
		command("checks:public-parity", "Checks", "Check public source stays inside configured public boundaries.", vigilcli.AccessRead, gitRead, func(inv vigilcli.Invocation) int {
			return checkPublicParity(inv.ConfigPath, inv.Args)
		}),
		command("checks:tracked-assistant-artifacts", "Checks", "Detect tracked local AI and editor instruction artifacts.", vigilcli.AccessRead, gitRead, func(inv vigilcli.Invocation) int {
			return checkTrackedAssistantArtifacts(inv.Args)
		}),
		command("deps:inventory", "Checks", "Inventory common dependency manifests and lockfiles.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return depsInventory(inv.Args)
		}),
		command("support:bundle", "Core", "Write or preview a redacted local diagnostic bundle.", vigilcli.AccessWrite, fsWrite, func(inv vigilcli.Invocation) int {
			return supportBundle(inv.ConfigPath, inv.Args)
		}),
		command("completion", "Packaging", "Generate shell completion for Bash, Zsh, or Fish.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return completion(inv.Args)
		}),
		command("manpage", "Packaging", "Generate the vigil(1) manual page.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return manpageGenerate(inv.Args)
		}),
		command("manpage:generate", "Packaging", "Generate the vigil(1) manual page.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return manpageGenerate(inv.Args)
		}),
		command("manpage:install", "Packaging", "Install the vigil(1) manual page.", vigilcli.AccessWrite, fsWrite, func(inv vigilcli.Invocation) int {
			return manpageInstall(inv.Args)
		}),
		command("init:ci", "Automation", "Generate GitHub Actions helper workflows from loaded checks.", vigilcli.AccessConditionalWrite, fsWrite, func(inv vigilcli.Invocation) int {
			return initCI(inv.ConfigPath, inv.Args)
		}),
		command("config:schema", "Config", "Print the supported JSON config schema summary.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return printConfigSchema(inv.Args)
		}),
		command("config:init", "Config", "Generate or write a starter JSON config.", vigilcli.AccessConditionalWrite, fsWrite, func(inv vigilcli.Invocation) int {
			return initConfig(inv.ConfigPath, inv.Args)
		}),
		command("config:validate", "Config", "Validate the effective JSON config.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return validateConfig(inv.ConfigPath, inv.Args)
		}),
		command("config:repair", "Config", "Interactively repair missing or broken config fields.", vigilcli.AccessWrite, interactiveWrite, func(inv vigilcli.Invocation) int {
			return repairConfig(inv.ConfigPath, inv.Args)
		}),
		command("config:migrate", "Config", "Migrate config files to the current schema.", vigilcli.AccessConditionalWrite, fsWrite, func(inv vigilcli.Invocation) int {
			return configMigrate(inv.ConfigPath, inv.Args)
		}),
		command("config:report", "Config", "Report effective redacted configuration and discovery details.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return configReport(inv.ConfigPath, inv.Args)
		}),
		command("settings:show", "Config", "Alias for config:report.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return configReport(inv.ConfigPath, inv.Args)
		}),
		command("config:template", "Config", "Print a versioned starter config for a selected profile.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return configTemplate(inv.Args)
		}),
		command("init", "Setup", "Alias for setup:wizard.", vigilcli.AccessConditionalWrite, interactiveWrite, setupCommand),
		command("setup", "Setup", "Plan or apply first-run Vigil configuration setup.", vigilcli.AccessConditionalWrite, interactiveWrite, setupCommand),
		command("setup:wizard", "Setup", "Run the interactive setup wizard.", vigilcli.AccessConditionalWrite, interactiveWrite, setupCommand),
		command("guards:summary", "Core", "Summarize read-only and mutating command coverage.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return guardsSummary(inv.Args)
		}),
		command("self-heal:plan", "Core", "Suggest safe repairs for local configuration and setup.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return selfHealPlan(inv.ConfigPath, inv.Args)
		}),
		command("tools:catalog", "Core", "List public tools and command contracts.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return catalogCommand(inv.Command, inv.Args)
		}),
		command("resources:catalog", "Core", "List public local diagnostic resources.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return catalogCommand(inv.Command, inv.Args)
		}),
		command("extensions:list", "Packs", "List loaded pack manifests.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return extensionCommand(inv.Command, inv.Args)
		}),
		command("extensions:doctor", "Packs", "Validate loaded pack manifests.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return extensionCommand(inv.Command, inv.Args)
		}),
		command("plugins:list", "Plugins", "List locked plugins and local trust state.", vigilcli.AccessRead, pluginRead, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:doctor", "Plugins", "Verify plugin digests, trust, compatibility, and handshakes.", vigilcli.AccessRead, pluginRead, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:conformance", "Plugins", "Validate an external executable against the plugin protocol.", vigilcli.AccessConditionalWrite, pluginConformance, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:publishers", "Plugins", "List trusted and revoked plugin publisher keys.", vigilcli.AccessRead, fsRead, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:index:verify", "Plugins", "Verify a signed plugin index against local publisher trust.", vigilcli.AccessRead, pluginIndexRead, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:install", "Plugins", "Install and trust a local or signed-index plugin executable.", vigilcli.AccessWrite, pluginIndexWrite, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:update", "Plugins", "Update a locked plugin from a local file or signed index.", vigilcli.AccessWrite, pluginIndexWrite, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:remove", "Plugins", "Remove and locally revoke a locked plugin.", vigilcli.AccessWrite, pluginStateWrite, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:trust-publisher", "Plugins", "Trust an Ed25519 plugin publisher key.", vigilcli.AccessWrite, pluginStateWrite, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
		command("plugins:revoke-publisher", "Plugins", "Locally revoke a trusted plugin publisher key.", vigilcli.AccessWrite, pluginStateWrite, func(inv vigilcli.Invocation) int {
			return pluginLifecycleCommand(inv.Context, inv.Command, inv.ConfigPath, inv.Args)
		}),
	}

	for i := range commands {
		switch commands[i].Name {
		case "help":
			commands[i].Aliases = []string{"--help", "-h"}
		case "list":
			commands[i].Aliases = []string{"commands"}
		case "version":
			commands[i].Aliases = []string{"--version"}
		case "workflow:local":
			commands[i].WriteFlags = []string{"--allow-mutation", "--artifacts", "--artifacts-dir"}
			commands[i].Timeout = 30 * time.Minute
			commands[i].Network = "optional"
		case "plan":
			commands[i].WriteFlags = []string{"--output"}
			commands[i].RequiredTools = []string{"git"}
		case "apply":
			commands[i].Timeout = 30 * time.Minute
			commands[i].Network = "optional"
			commands[i].RequiredTools = []string{"git"}
		case "init:ci", "config:migrate":
			commands[i].WriteFlags = []string{"--write"}
		case "config:init", "init", "setup", "setup:wizard":
			commands[i].WriteFlags = []string{"--write", "--force"}
		case "support:bundle":
			commands[i].ReadOnlyFlags = []string{"--dry-run"}
		case "hooks:install", "hooks:uninstall":
			commands[i].ReadOnlyFlags = []string{"--dry-run"}
		}
		switch commands[i].Name {
		case "config:repair", "init", "setup", "setup:wizard":
			commands[i].Interactive = true
		}
		switch commands[i].Name {
		case "plugins:list":
			commands[i].Usage = "vigil plugins:list [--json]"
			commands[i].Examples = []string{"vigil plugins:list --json"}
		case "plugins:doctor":
			commands[i].Usage = "vigil plugins:doctor [--json]"
			commands[i].Examples = []string{"vigil plugins:doctor --json"}
		case "plugins:conformance":
			commands[i].Usage = "vigil plugins:conformance --file PATH [--execute --timeout DURATION] [--json]"
			commands[i].Examples = []string{
				"vigil plugins:conformance --file ./vigil-plugin-example --json",
				"vigil --allow-mutation plugins:conformance --file ./vigil-plugin-example --execute --json",
			}
		case "plugins:install":
			commands[i].Usage = "vigil --allow-mutation plugins:install (--file PATH|--index SOURCE --id ID --version VERSION) [--approve CAPABILITY ...|--approve-all]"
			commands[i].Examples = []string{"vigil --allow-mutation plugins:install --file ./vigil-plugin-example --approve filesystem:read"}
		case "plugins:update":
			commands[i].Usage = "vigil --allow-mutation plugins:update (--file PATH|--index SOURCE --id ID --version VERSION) [--approve CAPABILITY ...|--approve-all]"
			commands[i].Examples = []string{"vigil --allow-mutation plugins:update --file ./vigil-plugin-example --approve-all"}
		case "plugins:remove":
			commands[i].Usage = "vigil --allow-mutation plugins:remove [--version VERSION] [--keep-trust] PLUGIN_ID"
			commands[i].Examples = []string{"vigil --allow-mutation plugins:remove example"}
		case "plugins:publishers":
			commands[i].Usage = "vigil plugins:publishers [--json]"
			commands[i].Examples = []string{"vigil plugins:publishers --json"}
		case "plugins:index:verify":
			commands[i].Usage = "vigil plugins:index:verify --index PATH_OR_HTTPS_URL [--json]"
			commands[i].Examples = []string{"vigil plugins:index:verify --index ./index-v1.json --json"}
		case "plugins:trust-publisher":
			commands[i].Usage = "vigil --allow-mutation plugins:trust-publisher --key PATH --name NAME [--restore-trust]"
			commands[i].Examples = []string{"vigil --allow-mutation plugins:trust-publisher --key ./publisher.pub --name Example"}
		case "plugins:revoke-publisher":
			commands[i].Usage = "vigil --allow-mutation plugins:revoke-publisher KEY_ID"
			commands[i].Examples = []string{"vigil --allow-mutation plugins:revoke-publisher sha256:<digest>"}
		}
		switch commands[i].Name {
		case "plugins:conformance", "plugins:index:verify", "plugins:install", "plugins:update":
			commands[i].Network = "optional"
		}
		if commands[i].Name == "plugins:conformance" {
			commands[i].WriteFlags = []string{"--execute"}
			commands[i].Timeout = 30 * time.Minute
		}
		commands[i].Flags = commandFlagSpecs(commands[i])
		commands[i].Arguments = commandArgumentSpecs(commands[i].Name)
	}
	return commands
}

func packCommandSpecsForConfig(configPath string) []vigilcli.Command {
	handlers := packCommandHandlers()
	report := loadExtensionsForConfig(configPath, extensionRootForConfig(configPath))
	commands := make([]vigilcli.Command, 0)
	for _, pack := range report.Extensions {
		contracts := make(map[string]extensionCommandContract, len(pack.CommandContracts))
		for _, contract := range pack.CommandContracts {
			contracts[contract.Command] = contract
		}
		for _, name := range pack.Commands {
			handler, executable := handlers[name]
			if !executable {
				continue
			}
			contract, contracted := contracts[name]
			if !contracted {
				continue
			}
			summary := strings.TrimSpace(contract.Description)
			if summary == "" {
				summary = extensionCommandDescription(name, pack.Description)
			}
			timeout, err := time.ParseDuration(contract.Timeout)
			if err != nil || timeout <= 0 {
				continue
			}
			capabilities := make([]vigilcli.Capability, 0, len(contract.Capabilities))
			for _, capability := range contract.Capabilities {
				capabilities = append(capabilities, vigilcli.Capability(capability))
			}
			command := vigilcli.Command{
				Name:           name,
				Summary:        summary,
				Handler:        handler,
				Access:         vigilcli.Access(contract.Access),
				Capabilities:   capabilities,
				Args:           "[args]",
				Source:         "pack:" + pack.ID,
				Pack:           pack.ID,
				Binding:        contract.Binding,
				Category:       "Packs",
				Stability:      vigilcli.Stability(contract.Stability),
				HostAPIVersion: pack.HostAPIVersion,
				Timeout:        timeout,
				Network:        contract.Network,
				RequiredTools:  append([]string{}, contract.RequiredTools...),
				OutputFormats:  append([]string{}, contract.OutputFormats...),
				WriteFlags:     append([]string(nil), contract.WriteFlags...),
				ReadOnlyFlags:  append([]string(nil), contract.ReadOnlyFlags...),
				Usage:          contract.Usage,
				InstallHint:    contract.InstallHint,
				Examples:       append([]string(nil), contract.Examples...),
			}
			command.Flags = packCommandFlagSpecs(command, contract)
			command.Arguments = packCommandArgumentSpecs(name)
			if name == "readme:generate" {
				command.AutoEnabled = true
				command.AutoReason = "deterministic idempotent repair"
			}
			commands = append(commands, command)
		}
	}
	return commands
}

func packCommandHandlers() map[string]vigilcli.Handler {
	return map[string]vigilcli.Handler{
		"checks:release-policy": func(inv vigilcli.Invocation) int { return releasePolicy(inv.Args) },
		"github:init-ci": func(inv vigilcli.Invocation) int {
			return initCI(inv.ConfigPath, append([]string{"--provider=github"}, inv.Args...))
		},
		"files:iterate":   func(inv vigilcli.Invocation) int { return filesIterate(inv.Args) },
		"readme:generate": func(inv vigilcli.Invocation) int { return scribeCommand(inv.Command, inv.Args) },
		"readme:check":    func(inv vigilcli.Invocation) int { return scribeCommand(inv.Command, inv.Args) },
		"a11y:inventory":  func(inv vigilcli.Invocation) int { return accessibilityCommand(inv.Command, inv.Args) },
		"a11y:smoke":      func(inv vigilcli.Invocation) int { return accessibilityCommand(inv.Command, inv.Args) },
		"a11y:ci":         func(inv vigilcli.Invocation) int { return accessibilityCommand(inv.Command, inv.Args) },
		"a11y:pa11y":      func(inv vigilcli.Invocation) int { return accessibilityCommand(inv.Command, inv.Args) },
		"a11y:lighthouse": func(inv vigilcli.Invocation) int { return accessibilityCommand(inv.Command, inv.Args) },
		"a11y:playwright": func(inv vigilcli.Invocation) int { return accessibilityCommand(inv.Command, inv.Args) },
		"checks:dependency-security": func(inv vigilcli.Invocation) int {
			return adapterCommand(inv.Command, inv.Args)
		},
		"deps:why":          func(inv vigilcli.Invocation) int { return adapterCommand(inv.Command, inv.Args) },
		"npm:audit":         func(inv vigilcli.Invocation) int { return adapterCommand(inv.Command, inv.Args) },
		"composer:validate": func(inv vigilcli.Invocation) int { return adapterCommand(inv.Command, inv.Args) },
		"php:lint":          func(inv vigilcli.Invocation) int { return adapterCommand(inv.Command, inv.Args) },
		"phpstan:analyse":   func(inv vigilcli.Invocation) int { return adapterCommand(inv.Command, inv.Args) },
		"security:gitleaks": func(inv vigilcli.Invocation) int { return adapterCommand(inv.Command, inv.Args) },
		"javascript:quality": func(inv vigilcli.Invocation) int {
			return adapterCommand(inv.Command, inv.Args)
		},
		"repo:health":      func(inv vigilcli.Invocation) int { return repoHealth(inv.Args) },
		"history:diagnose": func(inv vigilcli.Invocation) int { return repoHealth(inv.Args) },
		"deploy:verify":    func(inv vigilcli.Invocation) int { return deployVerify(inv.Args) },
		"tests:history":    func(inv vigilcli.Invocation) int { return testsCommand(inv.Command, inv.Args) },
		"tests:affected":   func(inv vigilcli.Invocation) int { return testsCommand(inv.Command, inv.Args) },
	}
}

func setupCommand(inv vigilcli.Invocation) int {
	args := append([]string(nil), inv.Args...)
	if inv.AllowMutation && !hasFlag(args, "dry-run") && !hasFlag(args, "json") && !hasFlag(args, "write") {
		args = append(args, "--write")
	}
	return setupWizard(inv.ConfigPath, args)
}

func versionCommand(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	info := buildinfo.Current()
	if jsonOut {
		return printJSON(map[string]any{
			"status":        "ok",
			"build":         info,
			"config_schema": configSchemaVersion,
		})
	}
	fmt.Printf(
		"vigil %s commit=%s built=%s dirty=%s go=%s os=%s arch=%s config_schema=%s\n",
		info.Version,
		info.Commit,
		info.BuildDate,
		info.Dirty,
		info.GoVersion,
		info.OS,
		info.Arch,
		configSchemaVersion,
	)
	return exitSuccess
}

func commandOutputFormats(name string) []string {
	switch name {
	case "completion":
		return []string{"shell"}
	case "manpage", "manpage:generate":
		return []string{"roff"}
	case "hooks:install", "hooks:pre-commit", "hooks:pre-push", "manpage:install":
		return []string{"text"}
	case "workflow:local", "apply":
		return []string{"text", "json", "jsonl", "junit", "github"}
	case "doctor", "verify":
		return []string{"text", "json", "jsonl", "junit", "github"}
	case "checks:staged-sensitive", "checks:workspace-hygiene", "checks:tracked-assistant-artifacts", "checks:public-assumptions", "checks:public-parity":
		return []string{"text", "json", "jsonl", "junit", "sarif", "github"}
	default:
		return []string{"text", "json"}
	}
}

func commandFlagSpecs(command vigilcli.Command) []vigilcli.Flag {
	flags := outputFlagSpecs(command.OutputFormats)
	add := func(flag vigilcli.Flag) {
		for _, existing := range flags {
			if existing.Long == flag.Long {
				return
			}
		}
		flags = append(flags, flag)
	}
	switch command.Name {
	case "plan":
		add(vigilcli.Flag{Long: "--output", Description: "Write a private reviewed plan.", ValueName: "PATH", File: true})
		add(vigilcli.Flag{Long: "--force", Description: "Replace an existing plan after review."})
		add(vigilcli.Flag{Long: "--tag", Description: "Include gates matching a tag.", ValueName: "TAG"})
		add(vigilcli.Flag{Long: "--timeout", Description: "Set the default gate timeout.", ValueName: "DURATION"})
		add(vigilcli.Flag{Long: "--jobs", Description: "Bound explicit parallel groups.", ValueName: "N"})
	case "apply":
		add(vigilcli.Flag{Long: "--artifacts", Description: "Write private run artifacts."})
		add(vigilcli.Flag{Long: "--artifacts-dir", Description: "Choose the private artifact root.", ValueName: "PATH", File: true})
	case "workflow:local":
		add(vigilcli.Flag{Long: "--dry-run", Description: "Show planned gates without executing them."})
		add(vigilcli.Flag{Long: "--tag", Description: "Run gates matching a tag.", ValueName: "TAG"})
		add(vigilcli.Flag{Long: "--timeout", Description: "Set the default gate timeout.", ValueName: "DURATION"})
		add(vigilcli.Flag{Long: "--jobs", Description: "Bound explicit parallel groups.", ValueName: "N"})
		add(vigilcli.Flag{Long: "--artifacts", Description: "Write private run artifacts."})
		add(vigilcli.Flag{Long: "--artifacts-dir", Description: "Choose the private artifact root.", ValueName: "PATH", File: true})
	case "config:init":
		add(vigilcli.Flag{Long: "--profile", Description: "Select the starter profile.", ValueName: "PROFILE", Values: profileNames()})
		add(vigilcli.Flag{Long: "--write", Description: "Write the generated configuration."})
		add(vigilcli.Flag{Long: "--force", Description: "Replace an existing configuration."})
	case "config:template":
		add(vigilcli.Flag{Long: "--profile", Description: "Select the starter profile.", ValueName: "PROFILE", Values: profileNames()})
	case "config:migrate":
		add(vigilcli.Flag{Long: "--write", Description: "Write the migrated configuration."})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "config:repair":
		add(vigilcli.Flag{Long: "--yes", Description: "Accept deterministic repair defaults."})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "init", "setup", "setup:wizard":
		add(vigilcli.Flag{Long: "--profile", Description: "Select or override the detected profile.", ValueName: "PROFILE", Values: append([]string{"auto"}, profileNames()...)})
		add(vigilcli.Flag{Long: "--write", Description: "Apply reviewed setup changes."})
		add(vigilcli.Flag{Long: "--force", Description: "Replace an existing configuration after review."})
		add(vigilcli.Flag{Long: "--yes", Description: "Accept deterministic setup defaults."})
		add(vigilcli.Flag{Long: "--dry-run", Description: "Preview setup without writing."})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL in non-interactive setup.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "hooks:install":
		add(vigilcli.Flag{Long: "--dry-run", Description: "Preview hook changes."})
		add(vigilcli.Flag{Long: "--chain", Description: "Preserve and chain existing hooks."})
	case "hooks:uninstall":
		add(vigilcli.Flag{Long: "--dry-run", Description: "Preview hook restoration."})
	case "support:bundle":
		add(vigilcli.Flag{Long: "--dry-run", Description: "Preview the support bundle."})
		add(vigilcli.Flag{Long: "--include-config", Description: "Include complete redacted configuration."})
		add(vigilcli.Flag{Long: "--include-git-status", Description: "Include redacted Git status."})
		add(vigilcli.Flag{Long: "--output", Description: "Choose the bundle output path.", ValueName: "PATH", File: true})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "manpage", "manpage:generate":
		add(vigilcli.Flag{Long: "--output", Description: "Write the manpage to a file.", ValueName: "PATH", File: true})
	case "manpage:install":
		add(vigilcli.Flag{Long: "--prefix", Description: "Choose the installation prefix.", ValueName: "PATH", File: true})
	case "github:init-ci", "init:ci":
		add(vigilcli.Flag{Long: "--provider", Description: "Select the CI provider.", ValueName: "PROVIDER", Values: []string{"github"}})
		add(vigilcli.Flag{Long: "--write", Description: "Write the generated workflow."})
	case "plugins:install", "plugins:update":
		add(vigilcli.Flag{Long: "--file", Description: "Choose a local plugin executable.", ValueName: "PATH", File: true})
		add(vigilcli.Flag{Long: "--index", Description: "Choose a signed index path or HTTPS URL.", ValueName: "SOURCE", File: true})
		add(vigilcli.Flag{Long: "--id", Description: "Require an exact plugin id.", ValueName: "PLUGIN_ID"})
		add(vigilcli.Flag{Long: "--version", Description: "Require an exact semantic version.", ValueName: "VERSION"})
		add(vigilcli.Flag{Long: "--digest", Description: "Require an exact SHA-256 executable digest.", ValueName: "SHA256"})
		add(vigilcli.Flag{Long: "--approve", Description: "Approve one declared capability.", ValueName: "CAPABILITY", Repeatable: true})
		add(vigilcli.Flag{Long: "--approve-all", Description: "Approve every declared capability."})
		add(vigilcli.Flag{Long: "--restore-trust", Description: "Remove a matching local digest revocation."})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "plugins:conformance":
		add(vigilcli.Flag{Long: "--file", Description: "Choose a local plugin executable.", ValueName: "PATH", File: true})
		add(vigilcli.Flag{Long: "--execute", Description: "Exercise every declared command in an isolated temporary repository."})
		add(vigilcli.Flag{Long: "--timeout", Description: "Cap each conformance command execution.", ValueName: "DURATION"})
	case "plugins:remove":
		add(vigilcli.Flag{Long: "--version", Description: "Require an exact locked version.", ValueName: "VERSION"})
		add(vigilcli.Flag{Long: "--keep-trust", Description: "Remove without revoking the digest."})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "plugins:index:verify":
		add(vigilcli.Flag{Long: "--index", Description: "Choose a signed index path or HTTPS URL.", ValueName: "SOURCE", File: true})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "plugins:trust-publisher":
		add(vigilcli.Flag{Long: "--key", Description: "Choose a base64 Ed25519 public key file.", ValueName: "PATH", File: true})
		add(vigilcli.Flag{Long: "--name", Description: "Set the publisher display name.", ValueName: "NAME"})
		add(vigilcli.Flag{Long: "--restore-trust", Description: "Remove a matching publisher-key revocation."})
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	case "plugins:revoke-publisher":
		add(vigilcli.Flag{Long: "--stream", Description: "Stream phase status as text or JSONL.", ValueName: "FORMAT", Values: []string{"text", "jsonl"}})
		add(vigilcli.Flag{Long: "--verbose", Description: "Stream additional phase details."})
	}
	return flags
}

func commandArgumentSpecs(name string) []vigilcli.Argument {
	switch name {
	case "help", "explain":
		return []vigilcli.Argument{{Name: "COMMAND", Description: "Registered command name.", Required: true}}
	case "apply":
		return []vigilcli.Argument{{Name: "PLAN_FILE", Description: "Reviewed Vigil plan file.", File: true, Required: true}}
	case "completion":
		return []vigilcli.Argument{{Name: "SHELL", Description: "Target shell.", Values: []string{"bash", "zsh", "fish"}, Required: true}}
	case "plugins:remove":
		return []vigilcli.Argument{{Name: "PLUGIN_ID", Description: "Locked plugin identifier.", Required: true}}
	case "plugins:revoke-publisher":
		return []vigilcli.Argument{{Name: "KEY_ID", Description: "Trusted publisher key identifier.", Required: true}}
	default:
		return []vigilcli.Argument{}
	}
}

func packCommandFlagSpecs(command vigilcli.Command, contract extensionCommandContract) []vigilcli.Flag {
	flags := outputFlagSpecs(command.OutputFormats)
	seen := map[string]bool{}
	for _, flag := range flags {
		seen[flag.Long] = true
	}
	for _, name := range append(append([]string(nil), contract.WriteFlags...), contract.ReadOnlyFlags...) {
		if seen[name] || name == "--json" || name == "--jsonl" {
			continue
		}
		flags = append(flags, vigilcli.Flag{Long: name, Description: "Command option declared by the pack contract."})
		seen[name] = true
	}
	add := func(flag vigilcli.Flag) {
		if !seen[flag.Long] {
			flags = append(flags, flag)
			seen[flag.Long] = true
		}
	}
	switch command.Name {
	case "files:iterate":
		add(vigilcli.Flag{Long: "--root", Description: "Choose the iteration root.", ValueName: "PATH", File: true})
		add(vigilcli.Flag{Long: "--glob", Description: "Filter files with a glob.", ValueName: "PATTERN"})
		add(vigilcli.Flag{Long: "--jsonl", Description: "Emit versioned JSONL events."})
	case "readme:generate", "readme:check":
		add(vigilcli.Flag{Long: "--path", Description: "Choose the README path.", ValueName: "PATH", File: true})
	case "deploy:verify":
		add(vigilcli.Flag{Long: "--url", Description: "Endpoint to verify.", ValueName: "URL"})
	}
	return flags
}

func packCommandArgumentSpecs(name string) []vigilcli.Argument {
	switch name {
	case "deps:why":
		return []vigilcli.Argument{{Name: "PACKAGE", Description: "Exact dependency package name.", Required: true}}
	default:
		return []vigilcli.Argument{}
	}
}

func outputFlagSpecs(formats []string) []vigilcli.Flag {
	var flags []vigilcli.Flag
	if sliceContains(formats, "json") {
		flags = append(flags, vigilcli.Flag{Long: "--json", Description: "Emit the versioned JSON envelope."})
	}
	if len(formats) > 2 || (len(formats) == 2 && !sliceContains(formats, "json")) {
		flags = append(flags, vigilcli.Flag{
			Long:        "--format",
			Description: "Select a machine or human output format.",
			ValueName:   "FORMAT",
			Values:      append([]string(nil), formats...),
		})
	}
	return flags
}

func profileNames() []string {
	return []string{"generic", "go-tool", "static-site", "js-app", "php-app", "native-app", "mixed", "custom"}
}

func globalFlagSpecs() []vigilcli.Flag {
	return []vigilcli.Flag{
		{Long: "--config", Description: "Use a specific Vigil configuration file.", ValueName: "PATH", File: true},
		{Long: "--help", Short: "-h", Description: "Show command help."},
		{Long: "--version", Description: "Show build and compatibility metadata."},
		{Long: "--allow-mutation", Description: "Authorize the reviewed mutation path."},
		{Long: "--auto", Description: "Authorize supported deterministic repairs."},
	}
}

func sliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

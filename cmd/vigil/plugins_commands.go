package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigilplugins "github.com/PayCal-Technologies/vigil-public/internal/plugins"
)

type pluginCapabilityFlags []string

func (values *pluginCapabilityFlags) String() string {
	return strings.Join(*values, ",")
}

func (values *pluginCapabilityFlags) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("capability must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func pluginLifecycleCommand(ctx context.Context, command, configPath string, args []string) int {
	switch command {
	case "plugins:list", "plugins:doctor":
		return pluginListOrDoctor(ctx, command, configPath, args)
	case "plugins:conformance":
		return pluginConformanceCommand(ctx, configPath, args)
	case "plugins:publishers", "plugins:index:verify":
		return pluginPublisherReadCommand(ctx, command, configPath, args)
	case "plugins:install", "plugins:update":
		return pluginInstallOrUpdate(ctx, command, configPath, args)
	case "plugins:remove":
		return pluginRemove(configPath, args)
	case "plugins:trust-publisher", "plugins:revoke-publisher":
		return pluginPublisherMutationCommand(command, configPath, args)
	default:
		return exitInternal
	}
}

func pluginConformanceCommand(ctx context.Context, configPath string, args []string) int {
	fs := flag.NewFlagSet("plugins:conformance", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	candidate := fs.String("file", "", "local plugin executable")
	executeCommands := fs.Bool("execute", false, "execute each declared command in an isolated temporary repository")
	commandTimeout := fs.Duration("timeout", 10*time.Second, "maximum duration for each conformance command")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(*candidate) == "" {
		fmt.Fprintln(os.Stderr, "Usage: vigil plugins:conformance --file PATH [--execute --timeout DURATION] [--json]")
		return exitUsage
	}
	policy, err := pluginPolicyForConfig(configPath)
	if err != nil {
		return pluginErrorOutput(*jsonOut, "plugins:conformance", err)
	}
	report := vigilplugins.Conform(ctx, strings.TrimSpace(*candidate), vigilplugins.ConformanceOptions{
		ExecuteCommands: *executeCommands,
		CommandTimeout:  *commandTimeout,
		Policy:          &policy,
	})
	exitCode := vigilplugins.ConformanceExit(report)
	if *jsonOut {
		return printJSON(report, exitCode)
	}
	plugin := "unresolved"
	if report.Plugin != nil {
		plugin = report.Plugin.ID + "@" + report.Plugin.Version
	}
	fmt.Printf("%s plugin=%s checks=%d execute=%t\n", statusLabel(report.Status), plugin, len(report.Checks), report.ExecuteCommands)
	for _, check := range report.Checks {
		name := check.Name
		if check.Command != "" {
			name += " " + check.Command
		}
		fmt.Printf("- %s %s: %s\n", statusLabel(check.Status), name, check.Detail)
	}
	return exitCode
}

func pluginListOrDoctor(ctx context.Context, command, configPath string, args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	report, err := pluginDiscoveryForConfig(ctx, configPath, pluginReservedCommands(configPath))
	if err != nil {
		return pluginErrorOutput(jsonOut, command, err)
	}
	exitCode := vigilplugins.DiscoveryExit(report)
	if jsonOut {
		return printJSON(report, exitCode)
	}
	fmt.Printf("%s plugins=%d lock=%s\n", statusLabel(report.Status), len(report.Plugins), report.Layout.LockPath)
	for _, plugin := range report.Plugins {
		fmt.Printf("- %s@%s %s commands=%d\n", plugin.ID, plugin.Version, plugin.Status, len(plugin.Commands))
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(os.Stderr, "%s %s\n", statusLabel("fail"), vigilplugins.FormatIssue(issue))
	}
	return exitCode
}

func pluginInstallOrUpdate(ctx context.Context, command, configPath string, args []string) int {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	candidate := fs.String("file", "", "local plugin executable")
	indexSource := fs.String("index", "", "signed plugin index path or HTTPS URL")
	approveAll := fs.Bool("approve-all", false, "approve every declared capability")
	restoreTrust := fs.Bool("restore-trust", false, "remove a matching local digest revocation")
	expectedID := fs.String("id", "", "expected plugin id")
	expectedVersion := fs.String("version", "", "expected semantic version")
	expectedDigestValue := fs.String("digest", "", "expected SHA-256 executable digest")
	jsonOut := fs.Bool("json", false, "json output")
	stream := fs.String("stream", "", "stream phase status: text or jsonl")
	verbose := fs.Bool("verbose", false, "stream text phase status")
	var approved pluginCapabilityFlags
	fs.Var(&approved, "approve", "approve one declared capability (repeatable)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	reporter, err := commandStreamReporter(command, *stream, *verbose, *jsonOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	localCandidate := strings.TrimSpace(*candidate)
	indexCandidate := strings.TrimSpace(*indexSource)
	if fs.NArg() != 0 || (localCandidate == "") == (indexCandidate == "") {
		fmt.Fprintf(os.Stderr, "Usage: vigil %s (--file PATH|--index PATH_OR_HTTPS_URL --id ID --version VERSION) [--approve CAPABILITY ...|--approve-all]\n", command)
		return exitUsage
	}
	layoutStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("resolve plugin layout", configPath)
	}
	layout, err := pluginLayoutForConfig(configPath)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("resolve plugin layout", exitInternal, time.Since(layoutStarted), err.Error())
		}
		return pluginErrorOutput(*jsonOut, command, err)
	}
	if reporter != nil {
		_ = reporter.OK("resolve plugin layout", time.Since(layoutStarted), layout.Root)
	}
	policyStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("load plugin policy", configPath)
	}
	policy, err := pluginPolicyForConfig(configPath)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("load plugin policy", exitInternal, time.Since(policyStarted), err.Error())
		}
		return pluginErrorOutput(*jsonOut, command, err)
	}
	if reporter != nil {
		_ = reporter.OK("load plugin policy", time.Since(policyStarted), policy.Mode)
	}
	expectedDigest := ""
	if strings.TrimSpace(*expectedDigestValue) != "" {
		expectedDigest, err = vigilplugins.NormalizeDigest(*expectedDigestValue)
		if err != nil {
			return pluginErrorOutput(*jsonOut, command, err)
		}
	}
	installOptions := vigilplugins.InstallOptions{
		Layout:           layout,
		Candidate:        localCandidate,
		Approved:         append([]string{}, approved...),
		ApproveAll:       *approveAll,
		RestoreTrust:     *restoreTrust,
		Update:           command == "plugins:update",
		ApprovalTime:     time.Now().UTC(),
		ExpectedPluginID: strings.TrimSpace(*expectedID),
		ExpectedVersion:  strings.TrimSpace(*expectedVersion),
		ExpectedDigest:   expectedDigest,
		Policy:           &policy,
	}
	var acquisition map[string]any
	var acquired vigilplugins.AcquiredPlugin
	if indexCandidate != "" {
		if strings.TrimSpace(*expectedID) == "" || strings.TrimSpace(*expectedVersion) == "" || expectedDigest != "" {
			fmt.Fprintln(os.Stderr, "--index requires --id and --version and does not accept --digest")
			return exitUsage
		}
		loadStarted := time.Now()
		if reporter != nil {
			_ = reporter.Start("load signed index", indexCandidate)
		}
		loaded, loadErr := vigilplugins.LoadVerifiedIndex(ctx, layout, indexCandidate, vigilplugins.IndexLoadOptions{})
		if loadErr != nil {
			if reporter != nil {
				_ = reporter.Fail("load signed index", exitPolicyBlocked, time.Since(loadStarted), loadErr.Error())
			}
			return pluginErrorOutput(*jsonOut, command, loadErr)
		}
		if reporter != nil {
			_ = reporter.OK("load signed index", time.Since(loadStarted), loaded.Source)
		}
		selectStarted := time.Now()
		if reporter != nil {
			_ = reporter.Start("select indexed release", strings.TrimSpace(*expectedID)+"@"+strings.TrimSpace(*expectedVersion))
		}
		selected, selectErr := vigilplugins.SelectIndexRelease(
			loaded.Verified,
			strings.TrimSpace(*expectedID),
			strings.TrimSpace(*expectedVersion),
			runtime.GOOS,
			runtime.GOARCH,
		)
		if selectErr != nil {
			if reporter != nil {
				_ = reporter.Fail("select indexed release", exitPolicyBlocked, time.Since(selectStarted), selectErr.Error())
			}
			return pluginErrorOutput(*jsonOut, command, selectErr)
		}
		if reporter != nil {
			_ = reporter.OK("select indexed release", time.Since(selectStarted), selected.Release.ID+"@"+selected.Release.Version)
		}
		checkStarted := time.Now()
		if reporter != nil {
			_ = reporter.Start("check plugin policy", selected.Release.ID)
		}
		if policyErr := vigilplugins.CheckPolicy(
			policy,
			selected.Release.ID,
			selected.Release.Capabilities,
			"signed-index",
			loaded.Verified.SignerIDs,
			loaded.Verified.Document.Signed.SignatureThreshold,
		); policyErr != nil {
			if reporter != nil {
				_ = reporter.Fail("check plugin policy", exitPolicyBlocked, time.Since(checkStarted), policyErr.Error())
			}
			return pluginErrorOutput(*jsonOut, command, policyErr)
		}
		if reporter != nil {
			_ = reporter.OK("check plugin policy", time.Since(checkStarted), selected.Release.ID)
		}
		acquireStarted := time.Now()
		if reporter != nil {
			_ = reporter.Start("acquire plugin artifact", selected.Release.ID)
		}
		acquired, err = vigilplugins.AcquireIndexedPlugin(ctx, layout, loaded, selected, nil)
		if err != nil {
			if reporter != nil {
				_ = reporter.Fail("acquire plugin artifact", exitInternal, time.Since(acquireStarted), err.Error())
			}
			return pluginErrorOutput(*jsonOut, command, err)
		}
		if reporter != nil {
			_ = reporter.OK("acquire plugin artifact", time.Since(acquireStarted), acquired.Path)
		}
		defer func() { _ = vigilplugins.RemoveAcquiredPlugin(acquired) }()
		installOptions.Candidate = acquired.Path
		installOptions.ExpectedDigest = acquired.Artifact.Digest
		installOptions.ExpectedMetadataDigest = acquired.Release.MetadataDigest
		installOptions.ExpectedCapabilities = append([]string{}, acquired.Release.Capabilities...)
		installOptions.Acquisition = "signed-index"
		installOptions.IndexDigest = acquired.IndexDigest
		installOptions.PublisherKeyIDs = append([]string{}, acquired.SignerIDs...)
		installOptions.SignatureThreshold = acquired.SignatureThreshold
		acquisition = map[string]any{
			"index":               acquired.Source,
			"index_digest":        acquired.IndexDigest,
			"signature_threshold": acquired.SignatureThreshold,
			"signer_ids":          acquired.SignerIDs,
			"artifact":            acquired.Artifact,
		}
	}
	installStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("install plugin", installOptions.Candidate)
	}
	result, err := vigilplugins.Install(ctx, installOptions)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("install plugin", exitPolicyBlocked, time.Since(installStarted), err.Error())
		}
		return pluginErrorOutput(*jsonOut, command, err)
	}
	if reporter != nil {
		_ = reporter.OK("install plugin", time.Since(installStarted), result.Plugin.ID+"@"+result.Plugin.Version)
	}
	if *jsonOut {
		payload := map[string]any{"status": "ok", "result": result}
		if acquisition != nil {
			payload["acquisition"] = acquisition
		}
		return printJSON(payload)
	}
	fmt.Printf("%s %s %s@%s\n", statusLabel("ok"), result.Action, result.Plugin.ID, result.Plugin.Version)
	fmt.Printf("binding: %s\n", result.Plugin.Binding)
	if acquisition != nil {
		fmt.Printf("index: %s\n", acquisition["index"])
		fmt.Printf("signers: %s\n", strings.Join(acquired.SignerIDs, ", "))
	}
	return exitSuccess
}

func pluginPublisherReadCommand(ctx context.Context, command, configPath string, args []string) int {
	switch command {
	case "plugins:publishers":
		jsonOut, err := parseJSONOnly(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		layout, err := pluginLayoutForConfig(configPath)
		if err != nil {
			return pluginErrorOutput(jsonOut, command, err)
		}
		store, err := vigilplugins.ReadPublishers(layout.PublisherPath)
		if err != nil {
			return pluginErrorOutput(jsonOut, command, err)
		}
		if jsonOut {
			return printJSON(map[string]any{
				"status": "ok", "publisher_path": layout.PublisherPath, "publishers": store.Keys,
				"revoked_key_ids": store.RevokedKeyIDs,
			})
		}
		fmt.Printf("%s publishers=%d revoked=%d path=%s\n", statusLabel("ok"), len(store.Keys), len(store.RevokedKeyIDs), layout.PublisherPath)
		for _, publisher := range store.Keys {
			status := "trusted"
			if slices.Contains(store.RevokedKeyIDs, publisher.KeyID) {
				status = "revoked"
			}
			fmt.Printf("- %s %s %s (%s)\n", publisher.KeyID, publisher.Name, publisher.Algorithm, status)
		}
		return exitSuccess
	case "plugins:index:verify":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		indexSource := fs.String("index", "", "signed plugin index path or HTTPS URL")
		jsonOut := fs.Bool("json", false, "json output")
		stream := fs.String("stream", "", "stream phase status: text or jsonl")
		verbose := fs.Bool("verbose", false, "stream text phase status")
		if err := fs.Parse(args); err != nil {
			return exitUsage
		}
		reporter, err := commandStreamReporter(command, *stream, *verbose, *jsonOut)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		if fs.NArg() != 0 || strings.TrimSpace(*indexSource) == "" {
			fmt.Fprintln(os.Stderr, "Usage: vigil plugins:index:verify --index PATH_OR_HTTPS_URL [--json]")
			return exitUsage
		}
		layoutStarted := time.Now()
		if reporter != nil {
			_ = reporter.Start("resolve plugin layout", configPath)
		}
		layout, err := pluginLayoutForConfig(configPath)
		if err != nil {
			if reporter != nil {
				_ = reporter.Fail("resolve plugin layout", exitInternal, time.Since(layoutStarted), err.Error())
			}
			return pluginErrorOutput(*jsonOut, command, err)
		}
		if reporter != nil {
			_ = reporter.OK("resolve plugin layout", time.Since(layoutStarted), layout.Root)
		}
		verifyStarted := time.Now()
		if reporter != nil {
			_ = reporter.Start("verify plugin index", strings.TrimSpace(*indexSource))
		}
		loaded, err := vigilplugins.LoadVerifiedIndex(ctx, layout, strings.TrimSpace(*indexSource), vigilplugins.IndexLoadOptions{})
		if err != nil {
			if reporter != nil {
				_ = reporter.Fail("verify plugin index", exitPolicyBlocked, time.Since(verifyStarted), err.Error())
			}
			return pluginErrorOutput(*jsonOut, command, err)
		}
		if reporter != nil {
			_ = reporter.OK("verify plugin index", time.Since(verifyStarted), loaded.Source)
		}
		payload := map[string]any{
			"status": "ok", "source": loaded.Source, "signer_ids": loaded.Verified.SignerIDs,
			"signed": loaded.Verified.Document.Signed,
		}
		if *jsonOut {
			return printJSON(payload)
		}
		fmt.Printf("%s source=%s releases=%d threshold=%d\n",
			statusLabel("ok"),
			loaded.Source,
			len(loaded.Verified.Document.Signed.Plugins),
			loaded.Verified.Document.Signed.SignatureThreshold,
		)
		fmt.Printf("signers: %s\n", strings.Join(loaded.Verified.SignerIDs, ", "))
		return exitSuccess
	default:
		return exitInternal
	}
}

func pluginPublisherMutationCommand(command, configPath string, args []string) int {
	layout, err := pluginLayoutForConfig(configPath)
	if err != nil {
		return pluginErrorOutput(wantsJSONEnvelope(args), command, err)
	}
	switch command {
	case "plugins:trust-publisher":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		keyPath := fs.String("key", "", "base64 Ed25519 public key file")
		name := fs.String("name", "", "publisher display name")
		restoreTrust := fs.Bool("restore-trust", false, "remove a matching key revocation")
		jsonOut := fs.Bool("json", false, "json output")
		stream := fs.String("stream", "", "stream phase status: text or jsonl")
		verbose := fs.Bool("verbose", false, "stream text phase status")
		if err := fs.Parse(args); err != nil {
			return exitUsage
		}
		reporter, err := commandStreamReporter(command, *stream, *verbose, *jsonOut)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		if fs.NArg() != 0 || strings.TrimSpace(*keyPath) == "" || strings.TrimSpace(*name) == "" {
			fmt.Fprintln(os.Stderr, "Usage: vigil --allow-mutation plugins:trust-publisher --key PATH --name NAME [--restore-trust] [--json]")
			return exitUsage
		}
		started := time.Now()
		if reporter != nil {
			_ = reporter.Start("trust publisher", strings.TrimSpace(*name))
		}
		result, err := vigilplugins.TrustPublisherFile(
			layout,
			strings.TrimSpace(*name),
			strings.TrimSpace(*keyPath),
			time.Now().UTC(),
			*restoreTrust,
		)
		if err != nil {
			if reporter != nil {
				_ = reporter.Fail("trust publisher", exitPolicyBlocked, time.Since(started), err.Error())
			}
			return pluginErrorOutput(*jsonOut, command, err)
		}
		if reporter != nil {
			_ = reporter.OK("trust publisher", time.Since(started), result.Key.KeyID)
		}
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "result": result})
		}
		fmt.Printf("%s %s publisher %s (%s)\n", statusLabel("ok"), result.Action, result.Key.Name, result.Key.KeyID)
		return exitSuccess
	case "plugins:revoke-publisher":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		jsonOut := fs.Bool("json", false, "json output")
		stream := fs.String("stream", "", "stream phase status: text or jsonl")
		verbose := fs.Bool("verbose", false, "stream text phase status")
		if err := fs.Parse(args); err != nil {
			return exitUsage
		}
		reporter, err := commandStreamReporter(command, *stream, *verbose, *jsonOut)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "Usage: vigil --allow-mutation plugins:revoke-publisher [--json] KEY_ID")
			return exitUsage
		}
		started := time.Now()
		if reporter != nil {
			_ = reporter.Start("revoke publisher", fs.Arg(0))
		}
		result, err := vigilplugins.RevokePublisher(layout, fs.Arg(0))
		if err != nil {
			if reporter != nil {
				_ = reporter.Fail("revoke publisher", exitPolicyBlocked, time.Since(started), err.Error())
			}
			return pluginErrorOutput(*jsonOut, command, err)
		}
		if reporter != nil {
			_ = reporter.OK("revoke publisher", time.Since(started), result.KeyID)
		}
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "result": result})
		}
		fmt.Printf("%s revoked publisher %s\n", statusLabel("ok"), result.KeyID)
		return exitSuccess
	default:
		return exitInternal
	}
}

func pluginRemove(configPath string, args []string) int {
	fs := flag.NewFlagSet("plugins:remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	version := fs.String("version", "", "require an exact locked version")
	keepTrust := fs.Bool("keep-trust", false, "remove without revoking the digest")
	jsonOut := fs.Bool("json", false, "json output")
	stream := fs.String("stream", "", "stream phase status: text or jsonl")
	verbose := fs.Bool("verbose", false, "stream text phase status")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	reporter, err := commandStreamReporter("plugins:remove", *stream, *verbose, *jsonOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil plugins:remove [--version VERSION] [--keep-trust] <plugin-id>")
		return exitUsage
	}
	layoutStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("resolve plugin layout", configPath)
	}
	layout, err := pluginLayoutForConfig(configPath)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("resolve plugin layout", exitInternal, time.Since(layoutStarted), err.Error())
		}
		return pluginErrorOutput(*jsonOut, "plugins:remove", err)
	}
	if reporter != nil {
		_ = reporter.OK("resolve plugin layout", time.Since(layoutStarted), layout.Root)
	}
	removeStarted := time.Now()
	if reporter != nil {
		_ = reporter.Start("remove plugin", fs.Arg(0))
	}
	result, err := vigilplugins.Remove(vigilplugins.RemoveOptions{
		Layout: layout, ID: fs.Arg(0), Version: strings.TrimSpace(*version), Revoke: !*keepTrust,
	})
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("remove plugin", exitPolicyBlocked, time.Since(removeStarted), err.Error())
		}
		return pluginErrorOutput(*jsonOut, "plugins:remove", err)
	}
	if reporter != nil {
		_ = reporter.OK("remove plugin", time.Since(removeStarted), result.ID+"@"+result.Version)
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "result": result})
	}
	fmt.Printf("%s removed %s@%s revoked=%t\n", statusLabel("ok"), result.ID, result.Version, result.Revoked)
	return exitSuccess
}

func pluginLayoutForConfig(configPath string) (vigilplugins.Layout, error) {
	resolved := resolvedConfigPath(configPath)
	repositoryRoot := filepath.Dir(resolved)
	if strings.TrimSpace(repositoryRoot) == "" {
		repositoryRoot = mustGetwd()
	}
	return vigilplugins.NewLayout(vigilplugins.DefaultUserRoot(), repositoryRoot)
}

func pluginReservedCommands(configPath string) []vigilcli.Command {
	return append(coreCommandSpecs(), packCommandSpecsForConfig(configPath)...)
}

func pluginDiscoveryForConfig(ctx context.Context, configPath string, reserved []vigilcli.Command) (vigilplugins.Discovery, error) {
	layout, err := pluginLayoutForConfig(configPath)
	if err != nil {
		return vigilplugins.Discovery{}, err
	}
	policy, err := pluginPolicyForConfig(configPath)
	if err != nil {
		return vigilplugins.Discovery{}, err
	}
	report := vigilplugins.DiscoverWithPolicy(ctx, layout, policy)
	reservedNames := map[string]bool{}
	for _, command := range reserved {
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			reservedNames[name] = true
		}
	}
	available := make([]vigilplugins.InstalledPlugin, 0, len(report.Available))
	for _, plugin := range report.Available {
		conflict := ""
		for _, command := range plugin.Metadata.Commands {
			for _, name := range append([]string{command.Name}, command.Aliases...) {
				if reservedNames[name] {
					conflict = name
					break
				}
			}
			if conflict != "" {
				break
			}
		}
		if conflict != "" {
			report.AddIssue(
				plugin.Metadata.ID,
				"VIGIL_PLUGIN_COMMAND_CONFLICT",
				vigilplugins.BlockedError("register plugin", fmt.Sprintf("command or alias %q is already registered", conflict)),
			)
			for index := range report.Plugins {
				if report.Plugins[index].ID == plugin.Metadata.ID {
					report.Plugins[index].Status = "blocked"
				}
			}
			continue
		}
		for _, command := range plugin.Metadata.Commands {
			for _, name := range append([]string{command.Name}, command.Aliases...) {
				reservedNames[name] = true
			}
		}
		available = append(available, plugin)
	}
	report.Available = available
	return report, nil
}

func pluginPolicyForConfig(configPath string) (vigilplugins.Policy, error) {
	resolved := resolvedConfigPath(configPath)
	if !fileExists(resolved) {
		return vigilplugins.DefaultPolicy(), nil
	}
	cfg, _, err := loadConfig(configPath)
	if err != nil {
		return vigilplugins.Policy{}, err
	}
	return vigilplugins.NormalizePolicy(cfg.Plugins)
}

func unavailablePluginCommand(ctx context.Context, configPath, name string) (vigilplugins.PluginStatus, []vigilplugins.Issue, int, bool) {
	report, err := pluginDiscoveryForConfig(ctx, configPath, pluginReservedCommands(configPath))
	if err != nil {
		return vigilplugins.PluginStatus{}, nil, exitSuccess, false
	}
	for _, plugin := range report.Plugins {
		if plugin.Status == "ok" || !slices.Contains(plugin.Commands, name) {
			continue
		}
		issues := make([]vigilplugins.Issue, 0)
		exitCode := exitPolicyBlocked
		for _, issue := range report.Issues {
			if issue.PluginID != plugin.ID {
				continue
			}
			issues = append(issues, issue)
			exitCode = preferExitCode(exitCode, issue.ExitCode)
		}
		return plugin, issues, exitCode, true
	}
	return vigilplugins.PluginStatus{}, nil, exitSuccess, false
}

func pluginCommandSpecsForConfig(ctx context.Context, configPath string, reserved []vigilcli.Command) []vigilcli.Command {
	report, err := pluginDiscoveryForConfig(ctx, configPath, reserved)
	if err != nil {
		return []vigilcli.Command{}
	}
	var commands []vigilcli.Command
	for _, plugin := range report.Available {
		for _, contract := range plugin.Metadata.Commands {
			timeout, err := time.ParseDuration(contract.Timeout)
			if err != nil {
				continue
			}
			capabilities := make([]vigilcli.Capability, 0, len(contract.Capabilities))
			for _, capability := range contract.Capabilities {
				capabilities = append(capabilities, vigilcli.Capability(capability))
			}
			flags := outputFlagSpecs(contract.OutputFormats)
			seenFlags := map[string]bool{}
			for _, flag := range flags {
				seenFlags[flag.Long] = true
			}
			for _, flag := range contract.Flags {
				if !seenFlags[flag.Long] {
					flags = append(flags, flag)
					seenFlags[flag.Long] = true
				}
			}
			installed := plugin
			metadata := contract
			commands = append(commands, vigilcli.Command{
				Name:           contract.Name,
				Aliases:        append([]string{}, contract.Aliases...),
				Summary:        contract.Summary,
				Handler:        func(inv vigilcli.Invocation) int { return executePluginCommand(inv, installed, metadata) },
				Access:         vigilcli.Access(contract.Access),
				Capabilities:   capabilities,
				Args:           contract.Args,
				Flags:          flags,
				Arguments:      append([]vigilcli.Argument{}, contract.Arguments...),
				Source:         "plugin:" + plugin.Metadata.ID + "@" + plugin.Metadata.Version,
				Binding:        vigilplugins.BindingFor(plugin),
				Category:       "Plugins",
				Stability:      vigilcli.Stability(contract.Stability),
				HostAPIVersion: plugin.Metadata.HostAPIVersion,
				Timeout:        timeout,
				Network:        contract.Network,
				RequiredTools:  append([]string{}, contract.RequiredTools...),
				OutputFormats:  append([]string{}, contract.OutputFormats...),
				Interactive:    contract.Interactive,
				WriteFlags:     append([]string{}, contract.WriteFlags...),
				ReadOnlyFlags:  append([]string{}, contract.ReadOnlyFlags...),
				Usage:          contract.Usage,
				Examples:       append([]string{}, contract.Examples...),
			})
		}
	}
	return commands
}

func executePluginCommand(inv vigilcli.Invocation, plugin vigilplugins.InstalledPlugin, command vigilplugins.Command) int {
	root := gitRoot()
	if root == "" {
		root = mustGetwd()
	}
	format := requestedPluginFormat(inv.Args)
	response, err := vigilplugins.Execute(inv.Context, plugin, command, inv.Args, vigilplugins.ExecuteOptions{
		RepositoryRoot: root,
		ConfigPath:     resolvedConfigPath(inv.ConfigPath),
		OutputFormat:   format,
		AllowMutation:  inv.AllowMutation,
	})
	jsonOut := format == "json"
	if err != nil {
		return pluginErrorOutput(jsonOut, inv.Command, err)
	}
	if jsonOut {
		var data any
		if err := json.Unmarshal(response.Data, &data); err != nil {
			return pluginErrorOutput(true, inv.Command, err)
		}
		payload := map[string]any{
			"status":    vigilcli.ClassifyExit(response.ExitCode).Status,
			"plugin":    plugin.Metadata.ID,
			"version":   plugin.Metadata.Version,
			"result":    data,
			"warnings":  response.Warnings,
			"errors":    response.Errors,
			"artifacts": response.Artifacts,
		}
		if response.Output != "" {
			payload["output"] = response.Output
		}
		return printJSON(payload, response.ExitCode)
	}
	for _, warning := range response.Warnings {
		fmt.Fprintf(os.Stderr, "%s %s: %s\n", statusLabel("warn"), warning.Code, warning.Message)
	}
	for _, diagnostic := range response.Errors {
		fmt.Fprintf(os.Stderr, "%s %s: %s\n", statusLabel("fail"), diagnostic.Code, diagnostic.Message)
	}
	if response.Output != "" {
		fmt.Print(response.Output)
		if !strings.HasSuffix(response.Output, "\n") {
			fmt.Println()
		}
	} else if string(response.Data) != "null" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, response.Data, "", "  "); err == nil {
			fmt.Println(pretty.String())
		}
	}
	return response.ExitCode
}

func requestedPluginFormat(args []string) string {
	for index, arg := range args {
		switch {
		case arg == "--json":
			return "json"
		case strings.HasPrefix(arg, "--format="):
			return strings.TrimPrefix(arg, "--format=")
		case arg == "--format" && index+1 < len(args):
			return args[index+1]
		}
	}
	return "text"
}

func pluginErrorOutput(jsonOut bool, command string, err error) int {
	exitCode := vigilplugins.ExitCode(err)
	if jsonOut {
		return printJSON(map[string]any{"status": "fail", "plugin_command": command, "error": err.Error()}, exitCode)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", statusLabel("fail"), err)
	return exitCode
}

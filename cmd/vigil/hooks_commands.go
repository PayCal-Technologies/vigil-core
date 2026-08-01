package main

import (
	"context"

	"flag"
	"fmt"

	vigilhooks "github.com/PayCal-Technologies/vigil-public/internal/hooks"
	"os"
	"os/exec"
)

func hooksInstall(args []string) int {
	fs := flag.NewFlagSet("hooks:install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	dryRun := fs.Bool("dry-run", false, "preview hook changes")
	chain := fs.Bool("chain", false, "preserve and run existing hooks before Vigil")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unknown argument: %s\n", fs.Arg(0))
		return exitUsage
	}
	hookDir, err := gitHooksDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return gitContextExit()
	}
	plans, err := vigilhooks.PlanInstall(hookDir, *chain)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", statusLabel("fail"), err)
		if !*chain {
			fmt.Fprintln(os.Stderr, "rerun hooks:install --dry-run --chain to preview a preserving chain")
		}
		return exitPolicyBlocked
	}

	if *dryRun {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "dry_run": true, "hook_dir": hookDir, "plans": plans})
		}
		for _, plan := range plans {
			fmt.Printf("%s %s %s\n", statusLabel("ok"), plan.Action, plan.Path)
		}
		return exitSuccess
	}
	if err := vigilhooks.ApplyInstall(hookDir, plans); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "hook_dir": hookDir, "plans": plans})
	}
	for _, plan := range plans {
		fmt.Printf("%s %s\n", plan.Action, plan.Path)
	}
	return exitSuccess
}

type hookInspection = vigilhooks.Inspection

func hooksDoctor(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	hookDir, err := gitHooksDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return gitContextExit()
	}
	inspections := inspectVigilHooks(hookDir)
	if jsonOut {
		return printJSON(map[string]any{"status": "ok", "hook_dir": hookDir, "hooks": inspections})
	}
	for _, inspection := range inspections {
		fmt.Printf("%s %s: %s\n", statusLabel("ok"), inspection.Hook, inspection.State)
	}
	return exitSuccess
}

func hooksUninstall(args []string) int {
	fs := flag.NewFlagSet("hooks:uninstall", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	dryRun := fs.Bool("dry-run", false, "preview hook restoration")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unknown argument: %s\n", fs.Arg(0))
		return exitUsage
	}
	hookDir, err := gitHooksDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return gitContextExit()
	}
	inspections := inspectVigilHooks(hookDir)
	for _, inspection := range inspections {
		if inspection.State == "foreign" || inspection.State == "unreadable" {
			fmt.Fprintf(os.Stderr, "%s refusing to remove foreign hook: %s\n", statusLabel("fail"), inspection.Path)
			return exitPolicyBlocked
		}
	}
	if *dryRun {
		if *jsonOut {
			return printJSON(map[string]any{"status": "ok", "dry_run": true, "hook_dir": hookDir, "hooks": inspections})
		}
		for _, inspection := range inspections {
			fmt.Printf("%s uninstall %s: %s\n", statusLabel("ok"), inspection.Hook, inspection.State)
		}
		return exitSuccess
	}
	if err := vigilhooks.ApplyUninstall(inspections); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "hook_dir": hookDir, "hooks": inspections})
	}
	fmt.Println("Vigil hooks uninstalled")
	return exitSuccess
}

func gitContextExit() int {
	if _, err := exec.LookPath("git"); err != nil {
		return exitDependencyMissing
	}
	return exitUsage
}

func gitHooksDir() (string, error) {
	return vigilhooks.ResolveDir(mustGetwd(), func(args ...string) (string, int) {
		return runCommand("git", args...)
	})
}

func isVigilManagedHook(data []byte) bool {
	return vigilhooks.IsManaged(data)
}

func inspectVigilHooks(hookDir string) []hookInspection {
	return vigilhooks.Inspect(hookDir)
}

func hookRun(configPath, hook string, args []string) int {
	return hookRunContext(context.Background(), configPath, hook, args)
}

func hookRunContext(ctx context.Context, configPath, hook string, args []string) int {
	_ = args
	switch hook {
	case "pre-commit":
		return workflowLocalContext(ctx, configPath, []string{"--tag=pre-commit"}, false)
	case "pre-push":
		return workflowLocalContext(ctx, configPath, []string{"--tag=pre-push"}, false)
	default:
		return workflowLocalContext(ctx, configPath, nil, false)
	}
}

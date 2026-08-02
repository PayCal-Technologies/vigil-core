package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
)

func beginnerCheckContext(ctx context.Context, configPath string, args []string, allowMutation bool) int {
	if _, _, err := loadConfig(configPath); err != nil {
		if wantsJSONEnvelope(args) {
			return printJSON(map[string]any{
				"status":          "fail",
				"what_happened":   "Vigil is not set up for this project yet.",
				"what_it_means":   "There is no readable Vigil configuration file.",
				"next_step":       "Run vigil setup to review the file Vigil wants to create.",
				"technical_error": err.Error(),
			}, vigilcli.ExitUsage)
		}
		fmt.Println("Vigil is not set up for this project yet.")
		fmt.Println()
		fmt.Println("What this means:")
		fmt.Println("Vigil needs a configuration file before it can run project checks.")
		fmt.Println()
		fmt.Println("Suggested next step:")
		fmt.Println("Run `vigil setup` to review the file Vigil wants to create.")
		return exitUsage
	}
	if !wantsMachineOutput(args) {
		fmt.Println("Checking project...")
	}
	return workflowLocalContext(ctx, configPath, args, allowMutation)
}

func beginnerFix(configPath string, args []string) int {
	return selfHealPlan(configPath, args)
}

func beginnerLearn(args []string) int {
	fs := flag.NewFlagSet("learn", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	topic := "overview"
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "Usage: vigil learn [--json] [hooks|plans|ci|ai]")
		return exitUsage
	}
	if fs.NArg() == 1 {
		topic = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	lesson, ok := beginnerLessons()[topic]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown learn topic: %s\n", topic)
		return exitUsage
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "topic": topic, "lesson": lesson})
	}
	fmt.Println(lesson.Title)
	fmt.Println()
	for _, line := range lesson.Lines {
		fmt.Println(line)
	}
	if lesson.Try != "" {
		fmt.Println()
		fmt.Println("Try:")
		fmt.Println("  " + lesson.Try)
	}
	return exitSuccess
}

func advancedCommand(args []string) int {
	jsonOut, err := parseJSONOnly(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if jsonOut {
		return printJSON(activeCommands())
	}
	printHelp()
	return exitSuccess
}

func beginnerCommandHelp(commandName string, jsonOut bool) int {
	info, ok := commandInfoByName(commandName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", commandName)
		return exitUsage
	}
	payload := map[string]any{
		"status":       "ok",
		"command":      info.Command,
		"purpose":      beginnerPurpose(info),
		"changes":      beginnerAccessDescription(info.Access),
		"network":      beginnerNetworkDescription(info.Network),
		"common_usage": commandUsage(info),
		"next_command": beginnerNextCommand(info.Command),
	}
	if jsonOut {
		return printJSON(payload)
	}
	fmt.Println(info.Command)
	fmt.Printf("  for: %s\n", payload["purpose"])
	fmt.Printf("  changes: %s\n", payload["changes"])
	fmt.Printf("  network: %s\n", payload["network"])
	fmt.Printf("  example: %s\n", payload["common_usage"])
	fmt.Printf("  next: %s\n", payload["next_command"])
	return exitSuccess
}

func explainProject(jsonOut bool) int {
	cfg, cfgPath, err := loadConfig("")
	if err != nil {
		payload := map[string]any{
			"status":          "fail",
			"what_happened":   "Vigil configuration is not ready.",
			"what_it_means":   "Vigil cannot explain project checks until it can read the configuration.",
			"next_step":       "Run vigil setup or vigil config:validate.",
			"technical_error": err.Error(),
		}
		if jsonOut {
			return printJSON(payload, exitUsage)
		}
		fmt.Println(payload["what_happened"])
		fmt.Println()
		fmt.Println("What this means:")
		fmt.Println(payload["what_it_means"])
		fmt.Println()
		fmt.Println("Suggested next step:")
		fmt.Println(payload["next_step"])
		return exitUsage
	}
	checks := make([]map[string]any, 0, len(cfg.Gates))
	for _, gate := range cfg.Gates {
		checks = append(checks, map[string]any{
			"name":      gate.Name,
			"command":   gateDisplayCommand(gate),
			"changes":   gateChangeDescription(gate),
			"network":   gateNetworkDescription(gate),
			"required":  !gate.ContinueOnError,
			"directory": firstNonEmpty(gate.CWD, "."),
		})
	}
	payload := map[string]any{"status": "ok", "config_path": cfgPath, "checks": checks}
	if jsonOut {
		return printJSON(payload)
	}
	fmt.Println("Vigil will run these project checks:")
	fmt.Println()
	for index, check := range checks {
		fmt.Printf("  %d. %s\n", index+1, check["name"])
		fmt.Printf("     Changes: %s\n", check["changes"])
		fmt.Printf("     Network: %s\n", check["network"])
		fmt.Printf("     Command: %s\n", check["command"])
	}
	return exitSuccess
}

type beginnerLesson struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
	Try   string   `json:"try,omitempty"`
}

func beginnerLessons() map[string]beginnerLesson {
	return map[string]beginnerLesson{
		"overview": {
			Title: "What Vigil Does",
			Lines: []string{
				"Vigil checks your project before you publish or share it.",
				"It runs checks your project already uses and reports what passed or failed.",
				"It warns when a check changes files unexpectedly.",
			},
			Try: "vigil explain",
		},
		"hooks": {
			Title: "Git Hooks",
			Lines: []string{
				"A Git hook can run Vigil before you push code.",
				"This helps catch failures before they reach GitHub.",
				"Installing hooks writes files inside .git/hooks only after you approve it.",
			},
			Try: "vigil --allow-mutation hooks:install --dry-run",
		},
		"plans": {
			Title: "Reviewed Plans",
			Lines: []string{
				"A reviewed plan records exactly which checks Vigil intends to run.",
				"Vigil refuses to run that plan if the project or setup changed after review.",
			},
			Try: "vigil plan --json",
		},
		"ci": {
			Title: "CI Output",
			Lines: []string{
				"Vigil can produce JSON, JSONL, JUnit, and GitHub-friendly output.",
				"These formats let CI systems read results without parsing human text.",
			},
			Try: "vigil check --json",
		},
		"ai": {
			Title: "AI-Assisted Development",
			Lines: []string{
				"Vigil does not replace code review.",
				"It helps verify that selected checks ran and that commands did not change files beyond what they said they would do.",
				"Run Vigil before and after agent work when you want a clear project check record.",
			},
			Try: "vigil check",
		},
	}
}

func commandInfoByName(name string) (commandInfo, bool) {
	for _, info := range activeCommands() {
		if info.Command == name {
			return info, true
		}
		for _, alias := range info.Aliases {
			if alias == name {
				return info, true
			}
		}
	}
	return commandInfo{}, false
}

func beginnerPurpose(info commandInfo) string {
	description := strings.TrimSuffix(strings.TrimSpace(info.Description), ".")
	if description == "" {
		return "Run " + info.Command
	}
	return plainTerminology(description)
}

func commandUsage(info commandInfo) string {
	if strings.TrimSpace(info.Usage) != "" {
		return info.Usage
	}
	return "vigil " + info.Command
}

func beginnerNextCommand(command string) string {
	switch command {
	case "setup", "setup:wizard", "init":
		return "vigil check"
	case "check", "workflow:local":
		return "vigil status"
	case "status":
		return "vigil check"
	case "learn":
		return "vigil learn hooks"
	default:
		return "vigil status"
	}
}

func beginnerAccessDescription(access string) string {
	switch access {
	case "read":
		return "Does not change project files."
	case "conditional-write":
		return "Can change files only when you choose a write option and approve it."
	case "write":
		return "Can change files after explicit approval."
	default:
		return "Vigil does not know whether this changes files, so it stops rather than guessing."
	}
}

func beginnerNetworkDescription(network string) string {
	switch network {
	case "none":
		return "Does not use the network."
	case "optional":
		return "May use the network depending on configuration or selected tools."
	case "required":
		return "Uses the network."
	default:
		return "Network behavior is not declared."
	}
}

func gateChangeDescription(gate gateConfig) string {
	if gate.ReadOnly {
		return "Should not change tracked project files."
	}
	return "May change files; review before running."
}

func gateNetworkDescription(gate gateConfig) string {
	if gateTagsContain(gate.Tags, "network") {
		return "May use the network."
	}
	return "No network use is declared."
}

func gateTagsContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func wantsMachineOutput(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--format=") && !strings.HasPrefix(arg, "--format=text") {
			return true
		}
	}
	return false
}

package completion

import (
	"strings"
	"testing"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

func completionFixtures() ([]cli.Command, []cli.Flag, DynamicValues) {
	commands := []cli.Command{
		{
			Name:    "apply",
			Summary: "Verify and execute a reviewed plan.",
			Flags: []cli.Flag{
				{Long: "--format", Description: "Output format.", ValueName: "FORMAT", Values: []string{"text", "json", "junit"}},
				{Long: "--artifacts-dir", Description: "Artifact directory.", ValueName: "PATH", File: true},
			},
			Arguments: []cli.Argument{{Name: "PLAN_FILE", Description: "Reviewed plan.", File: true, Required: true}},
		},
		{
			Name:    "plan",
			Summary: "Create a digest-bound plan.",
			Flags: []cli.Flag{
				{Long: "--profile", Description: "Profile.", ValueName: "PROFILE"},
				{Long: "--tag", Description: "Gate tag.", ValueName: "TAG"},
			},
		},
	}
	globals := []cli.Flag{{Long: "--config", Description: "Config file.", ValueName: "PATH", File: true}}
	dynamic := DynamicValues{
		Profiles: []string{"generic", "go-tool"},
		GateTags: []string{"pre-commit", "pre-push"},
		PackIDs:  []string{"repo-health"},
	}
	return commands, globals, dynamic
}

func TestGenerateRichCompletions(t *testing.T) {
	commands, globals, dynamic := completionFixtures()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			result, err := Generate(shell, commands, globals, dynamic)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"apply", "Verify and execute a reviewed plan.", "config", "json", "junit", "generic", "pre-push"} {
				if !strings.Contains(result, want) {
					t.Fatalf("%s completion missing %q:\n%s", shell, want, result)
				}
			}
		})
	}
}

func TestGenerateRejectsUnknownShell(t *testing.T) {
	if _, err := Generate("powershell", nil, nil, DynamicValues{}); err == nil {
		t.Fatal("Generate accepted an unsupported shell")
	}
}

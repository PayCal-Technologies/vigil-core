package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/PayCal-Technologies/vigil-public/internal/runner"
	"os"

	"path/filepath"

	"runtime/debug"

	"strings"
	"time"
)

func initCI(configPath string, args []string) int {
	fs := flag.NewFlagSet("init:ci", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	provider := fs.String("provider", "github", "ci provider")
	write := fs.Bool("write", false, "write workflow file")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *provider != "github" {
		fmt.Fprintln(os.Stderr, "only --provider=github is supported")
		return exitUsage
	}
	cfg, _, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	content := githubWorkflow(cfg)
	path := filepath.Join(".github", "workflows", "vigil.yml")
	if *write {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitInternal
		}
		if _, err := atomicWriteFile(path, []byte(content), fileExists(path)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitInternal
		}
	}
	if *jsonOut {
		return printJSON(map[string]any{"status": "ok", "provider": *provider, "path": path, "written": *write, "content": content})
	}
	if *write {
		fmt.Printf("%s wrote %s\n", statusLabel("ok"), path)
		return exitSuccess
	}
	fmt.Print(content)
	return exitSuccess
}

func githubWorkflow(cfg config) string {
	_ = cfg
	var b strings.Builder
	b.WriteString("name: Vigil\n\n")
	b.WriteString("on:\n  pull_request:\n  push:\n    branches: [main]\n\n")
	b.WriteString("jobs:\n  vigil:\n    runs-on: ubuntu-latest\n    steps:\n")
	b.WriteString("      - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5\n")
	b.WriteString("      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6\n        with:\n          go-version: '" + goVersionForWorkflow() + "'\n          cache: false\n")
	b.WriteString("      - name: Install Vigil\n        run: |\n          mkdir -p bin\n          GOBIN=\"$PWD/bin\" go install " + vigilCoreModulePath + "@" + vigilCoreInstallRef() + "\n          echo \"$PWD/bin\" >> \"$GITHUB_PATH\"\n")
	b.WriteString("      - name: Verify Vigil\n        run: vigil verify --json\n")
	b.WriteString("      - name: Run Vigil Preflight\n        run: vigil workflow:local --json\n")
	return b.String()
}

func goVersionForWorkflow() string {
	return vigilCoreGoVersion
}

func vigilCoreInstallRef() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) == 40 {
				return setting.Value
			}
		}
	}
	if root := vigilCoreSourceRoot(); root != "" {
		if ref := gitHeadRef(root); ref != "" {
			return ref
		}
	}
	return "main"
}

func vigilCoreSourceRoot() string {
	if root := findFileUpward(mustGetwd(), "go.mod"); root != "" {
		data, err := os.ReadFile(root)
		if err == nil && strings.Contains(string(data), "module github.com/PayCal-Technologies/vigil-public") {
			return filepath.Dir(root)
		}
	}
	return ""
}

func gitHeadRef(repoRoot string) string {
	result := runner.Run(context.Background(), runner.Spec{
		Name:         "git-head",
		Mode:         runner.ModeArgv,
		Executable:   "git",
		Args:         []string{"rev-parse", "HEAD"},
		Dir:          repoRoot,
		Timeout:      10 * time.Second,
		CaptureLimit: 4096,
	})
	if result.ExitCode != exitSuccess {
		return ""
	}
	ref := strings.TrimSpace(result.Output)
	if len(ref) != 40 {
		return ""
	}
	return ref
}

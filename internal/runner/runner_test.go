package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRunCanClearEnvironmentAndProvideStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	t.Setenv("VIGIL_RUNNER_SECRET_FIXTURE", "must-not-leak")
	result := Run(context.Background(), Spec{
		Name:       "sanitized",
		Mode:       ModeArgv,
		Executable: "sh",
		Args:       []string{"-c", `read value; printf '%s|%s' "$value" "${VIGIL_RUNNER_SECRET_FIXTURE-unset}"`},
		Stdin:      strings.NewReader("request\n"),
		ClearEnv:   true,
		Env:        []string{"PATH=" + os.Getenv("PATH")},
		Timeout:    time.Second,
	})
	if result.State != StateOK || result.Output != "request|unset" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunArgvStreamsAndCapturesOutput(t *testing.T) {
	var stream bytes.Buffer
	result := Run(context.Background(), Spec{
		Name:       "printf",
		Mode:       ModeArgv,
		Executable: "printf",
		Args:       []string{"hello"},
		Stdout:     &stream,
		Timeout:    time.Second,
	})
	if result.State != StateOK || result.ExitCode != 0 {
		t.Fatalf("result=%+v", result)
	}
	if result.Output != "hello" || stream.String() != "hello" {
		t.Fatalf("capture=%q stream=%q", result.Output, stream.String())
	}
}

func TestRunPreservesCapturedBoundaryWhitespace(t *testing.T) {
	result := Run(context.Background(), Spec{
		Name:         "raw-output",
		Mode:         ModeShell,
		Shell:        "sh",
		ShellCommand: `printf ' leading and trailing \n'`,
		Timeout:      time.Second,
	})
	if result.State != StateOK || result.Output != " leading and trailing \n" {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestRunReportsMissingTool(t *testing.T) {
	result := Run(context.Background(), Spec{
		Name:       "missing",
		Mode:       ModeArgv,
		Executable: "vigil-test-tool-that-does-not-exist",
		Timeout:    time.Second,
	})
	if result.State != StateToolMissing || result.ExitCode != 4 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunReportsMissingExplicitShell(t *testing.T) {
	result := Run(context.Background(), Spec{
		Name:         "missing-shell",
		Mode:         ModeShell,
		Shell:        "vigil-test-shell-that-does-not-exist",
		ShellCommand: "true",
		Timeout:      time.Second,
	})
	if result.State != StateToolMissing || result.ExitCode != 4 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunTimesOutProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	start := time.Now()
	result := Run(context.Background(), Spec{
		Name:         "timeout",
		Mode:         ModeShell,
		Shell:        "sh",
		ShellCommand: "sleep 30",
		Timeout:      100 * time.Millisecond,
	})
	if result.State != StateTimedOut || result.ExitCode != 5 {
		t.Fatalf("result=%+v", result)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestRunTimeoutTerminatesSpawnedChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	marker := filepath.Join(t.TempDir(), "child-survived")
	result := Run(context.Background(), Spec{
		Name:         "timeout-child",
		Mode:         ModeShell,
		Shell:        "sh",
		ShellCommand: "(sleep 0.4; printf child-survived > " + shellQuote(marker) + ") & sleep 30",
		Timeout:      100 * time.Millisecond,
	})
	if result.State != StateTimedOut || result.ExitCode != 5 {
		t.Fatalf("result=%+v", result)
	}
	time.Sleep(700 * time.Millisecond)
	if data, err := os.ReadFile(marker); err == nil {
		t.Fatalf("spawned child survived timeout and wrote %q", data)
	} else if !os.IsNotExist(err) {
		t.Fatalf("reading marker: %v", err)
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	result := Run(ctx, Spec{
		Name:         "cancel",
		Mode:         ModeShell,
		Shell:        "sh",
		ShellCommand: "sleep 30",
		Timeout:      time.Minute,
	})
	if result.State != StateCancelled || result.ExitCode != 5 {
		t.Fatalf("result=%+v", result)
	}
	if !strings.Contains(result.Error, "canceled") {
		t.Fatalf("error=%q", result.Error)
	}
}

func TestRunTruncatesCapturedOutput(t *testing.T) {
	result := Run(context.Background(), Spec{
		Name:         "truncate",
		Mode:         ModeShell,
		Shell:        "sh",
		ShellCommand: "printf 123456789",
		CaptureLimit: 4,
		Timeout:      time.Second,
	})
	if result.State != StateOK || result.Output != "1234\n[truncated]" || !result.Truncated {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunTruncatedCapturedOutputKeepsValidUTF8(t *testing.T) {
	result := Run(context.Background(), Spec{
		Name:         "truncate-utf8",
		Mode:         ModeArgv,
		Executable:   "printf",
		Args:         []string{"abc€def"},
		CaptureLimit: 5,
		Timeout:      time.Second,
	})
	if result.State != StateOK || !result.Truncated {
		t.Fatalf("result=%+v", result)
	}
	if !utf8.ValidString(result.Output) {
		t.Fatalf("output is invalid UTF-8: %q", result.Output)
	}
	if result.Output != "abc\n[truncated]" {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestParseCommandLine(t *testing.T) {
	args, err := ParseCommandLine(`go test "./pkg with space" 'literal value' escaped\ value`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go", "test", "./pkg with space", "literal value", "escaped value"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args=%q want=%q", args, want)
	}
}

func TestParseCommandLineRejectsIncompleteSyntax(t *testing.T) {
	for _, input := range []string{`go test "`, `go test \`} {
		if _, err := ParseCommandLine(input); err == nil {
			t.Fatalf("expected %q to fail", input)
		}
	}
}

func TestRequiresShell(t *testing.T) {
	for _, input := range []string{"find . -name '*.php' | xargs php -l", "printf value > file", "MODE=test go test ./..."} {
		if !RequiresShell(input) {
			t.Fatalf("expected shell syntax: %s", input)
		}
	}
	for _, input := range []string{`go test ./...`, `git commit -m "message value"`, `vigil verify --json`} {
		if RequiresShell(input) {
			t.Fatalf("unexpected shell syntax: %s", input)
		}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func FuzzCommandLineParsing(f *testing.F) {
	for _, seed := range []string{
		`go test ./...`,
		`printf "%s\n" "hello world"`,
		`command 'single quoted' escaped\ value`,
		`unterminated "quote`,
		`go test ./... | tee result.log`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64*1024 {
			return
		}
		args, err := ParseCommandLine(input)
		if err == nil && len(args) == 0 {
			t.Fatal("successful command-line parse returned no arguments")
		}
		_ = RequiresShell(input)
	})
}

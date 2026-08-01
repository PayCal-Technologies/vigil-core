package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Mode string

const (
	ModeArgv  Mode = "argv"
	ModeShell Mode = "shell"
)

type State string

const (
	StateOK               State = "ok"
	StateFailed           State = "failed"
	StateSkipped          State = "skipped"
	StateCancelled        State = "cancelled"
	StateTimedOut         State = "timed_out"
	StateMutationDetected State = "mutation_detected"
	StateBlocked          State = "blocked"
	StateToolMissing      State = "tool_missing"
	StateInternalError    State = "internal_error"
)

type Spec struct {
	Name         string
	Mode         Mode
	Executable   string
	Args         []string
	ShellCommand string
	Shell        string
	Dir          string
	Env          []string
	ClearEnv     bool
	Timeout      time.Duration
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	CaptureLimit int
}

type Result struct {
	Name       string        `json:"name"`
	State      State         `json:"state"`
	ExitCode   int           `json:"exit_code"`
	StartedAt  time.Time     `json:"started_at"`
	Duration   time.Duration `json:"-"`
	DurationMS int64         `json:"duration_ms"`
	Output     string        `json:"output,omitempty"`
	Error      string        `json:"error,omitempty"`
	Truncated  bool          `json:"truncated,omitempty"`
}

func Run(parent context.Context, spec Spec) Result {
	startedAt := time.Now().UTC()
	result := Result{Name: spec.Name, State: StateInternalError, ExitCode: 7, StartedAt: startedAt}
	if parent == nil {
		parent = context.Background()
	}
	if spec.CaptureLimit <= 0 {
		spec.CaptureLimit = 64 * 1024
	}
	executable, args, err := resolveCommand(spec)
	if err != nil {
		result.Error = err.Error()
		return finish(result, startedAt)
	}
	if _, err := exec.LookPath(executable); err != nil {
		result.State = StateToolMissing
		result.ExitCode = 4
		result.Error = err.Error()
		return finish(result, startedAt)
	}

	ctx := parent
	cancel := func() {}
	if spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, spec.Timeout)
	}
	defer cancel()
	if err := ctx.Err(); err != nil {
		return contextResult(result, err, startedAt)
	}

	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = spec.Dir
	command.Stdin = spec.Stdin
	if spec.ClearEnv {
		command.Env = append([]string{}, spec.Env...)
	} else if len(spec.Env) > 0 {
		command.Env = append(os.Environ(), spec.Env...)
	}
	configureProcess(command)

	capture := &boundedBuffer{limit: spec.CaptureLimit}
	command.Stdout = outputWriter(spec.Stdout, capture)
	command.Stderr = outputWriter(spec.Stderr, capture)
	err = command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		result = contextResult(result, ctxErr, startedAt)
		result.Output = capture.String()
		result.Truncated = capture.Truncated()
		return result
	}
	result.Output = capture.String()
	result.Truncated = capture.Truncated()
	if err == nil {
		result.State = StateOK
		result.ExitCode = 0
		return finish(result, startedAt)
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.State = StateFailed
		result.ExitCode = exitError.ExitCode()
		if result.ExitCode < 0 {
			result.ExitCode = 1
		}
		return finish(result, startedAt)
	}
	result.Error = err.Error()
	return finish(result, startedAt)
}

func resolveCommand(spec Spec) (string, []string, error) {
	switch spec.Mode {
	case "", ModeArgv:
		if strings.TrimSpace(spec.Executable) == "" {
			return "", nil, errors.New("argv execution requires an executable")
		}
		return spec.Executable, append([]string(nil), spec.Args...), nil
	case ModeShell:
		if strings.TrimSpace(spec.ShellCommand) == "" {
			return "", nil, errors.New("shell execution requires a command")
		}
		shell := strings.TrimSpace(spec.Shell)
		if shell == "" {
			shell = "sh"
		}
		return shell, []string{"-c", spec.ShellCommand}, nil
	default:
		return "", nil, fmt.Errorf("unsupported execution mode %q", spec.Mode)
	}
}

func contextResult(result Result, err error, startedAt time.Time) Result {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.State = StateTimedOut
	case errors.Is(err, context.Canceled):
		result.State = StateCancelled
	default:
		result.State = StateInternalError
	}
	result.ExitCode = 5
	result.Error = err.Error()
	return finish(result, startedAt)
}

func finish(result Result, startedAt time.Time) Result {
	result.Duration = time.Since(startedAt)
	result.DurationMS = result.Duration.Milliseconds()
	return result
}

func outputWriter(stream io.Writer, capture io.Writer) io.Writer {
	if stream == nil {
		return capture
	}
	return io.MultiWriter(stream, capture)
}

type boundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	output := safeCapturedText(b.buffer.Bytes(), b.truncated)
	if b.truncated {
		if output != "" && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}
		output += "[truncated]"
	}
	return output
}

func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func safeCapturedText(data []byte, truncated bool) string {
	if truncated {
		for len(data) > 0 {
			r, size := utf8.DecodeLastRune(data)
			if r != utf8.RuneError || size != 1 {
				break
			}
			data = data[:len(data)-1]
		}
	}
	return strings.ToValidUTF8(string(data), "\uFFFD")
}

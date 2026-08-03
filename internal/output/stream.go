package output

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type StreamReporter struct {
	writer   io.Writer
	command  string
	format   Format
	sequence int
	verbose  bool
	mu       sync.Mutex
}

type StreamOptions struct {
	Writer  io.Writer
	Command string
	Format  Format
	Verbose bool
}

func NewStreamReporter(options StreamOptions) *StreamReporter {
	format := options.Format
	if format == "" {
		format = FormatText
	}
	return &StreamReporter{
		writer:  options.Writer,
		command: strings.TrimSpace(options.Command),
		format:  format,
		verbose: options.Verbose,
	}
}

func (reporter *StreamReporter) Start(phase string, detail any) error {
	return reporter.emit("phase_started", "info", phase, detail, 0, 0)
}

func (reporter *StreamReporter) OK(phase string, duration time.Duration, detail any) error {
	return reporter.emit("phase_finished", "ok", phase, detail, 0, duration)
}

func (reporter *StreamReporter) Warn(phase string, detail any) error {
	return reporter.emit("phase_warning", "warn", phase, detail, 0, 0)
}

func (reporter *StreamReporter) Fail(phase string, exitCode int, duration time.Duration, detail any) error {
	if exitCode == 0 {
		exitCode = 1
	}
	return reporter.emit("phase_failed", "fail", phase, detail, exitCode, duration)
}

func (reporter *StreamReporter) Info(phase string, detail any) error {
	if !reporter.verbose {
		return nil
	}
	return reporter.emit("phase_info", "info", phase, detail, 0, 0)
}

func (reporter *StreamReporter) emit(eventType, status, phase string, detail any, exitCode int, duration time.Duration) error {
	if reporter == nil || reporter.writer == nil {
		return nil
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "phase"
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	switch reporter.format {
	case FormatJSONL:
		reporter.sequence++
		return WriteJSONLEvent(reporter.writer, reporter.sequence, eventType, reporter.command, time.Now().UTC(), map[string]any{
			"phase":       phase,
			"status":      status,
			"exit_code":   exitCode,
			"duration_ms": duration.Milliseconds(),
			"detail":      detail,
		})
	default:
		label := "INFO"
		switch status {
		case "ok":
			label = "OK"
		case "fail":
			label = "FAIL"
		case "warn":
			label = "WARN"
		}
		line := fmt.Sprintf("[%s] %s", label, phase)
		switch status {
		case "ok":
			line += " passed"
		case "fail":
			line += fmt.Sprintf(" failed exit=%d", exitCode)
		case "warn":
			line += " warning"
		default:
			line += " started"
		}
		if duration > 0 {
			line += fmt.Sprintf(" (%s)", duration.Round(time.Millisecond))
		}
		if text := strings.TrimSpace(fmt.Sprint(detail)); text != "" && text != "<nil>" {
			line += ": " + text
		}
		_, err := fmt.Fprintln(reporter.writer, line)
		return err
	}
}

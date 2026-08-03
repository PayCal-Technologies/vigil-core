package main

import (
	"fmt"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"

	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"

	vigilsupport "github.com/PayCal-Technologies/vigil-public/internal/support"
	"os"

	"path/filepath"
	"regexp"

	"sort"

	"strings"
	"sync"
	"time"
)

const (
	exitSuccess           = vigilcli.ExitSuccess
	exitCheckFailed       = vigilcli.ExitCheckFailed
	exitUsage             = vigilcli.ExitUsage
	exitPolicyBlocked     = vigilcli.ExitPolicyBlocked
	exitDependencyMissing = vigilcli.ExitDependencyMissing
	exitInterrupted       = vigilcli.ExitInterrupted
	exitMutationViolation = vigilcli.ExitMutationViolation
	exitInternal          = vigilcli.ExitInternal
)

func preferExitCode(current, candidate int) int {
	current = vigilcli.ClassifyExit(current).Code
	candidate = vigilcli.ClassifyExit(candidate).Code
	if candidate > current {
		return candidate
	}
	return current
}

var commandOutputTiming = struct {
	sync.Mutex
	command   string
	startedAt time.Time
}{}

func atomicWriteFile(path string, data []byte, backup bool) (atomicWriteResult, error) {
	return atomicWriteFileMode(path, data, backup, 0o644, true)
}

func atomicWriteFileMode(path string, data []byte, backup bool, defaultMode os.FileMode, preserveExistingMode bool) (atomicWriteResult, error) {
	return atomicfile.Write(path, data, atomicfile.Options{
		Backup:               backup,
		DefaultMode:          defaultMode,
		PreserveExistingMode: preserveExistingMode,
	})
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func redactedPath(path string) string {
	return vigilsupport.RedactedPath(path)
}

func matchFileGlob(pattern, rel string) (bool, error) {
	if strings.HasPrefix(pattern, "**/") {
		if ok, err := filepath.Match(strings.TrimPrefix(pattern, "**/"), filepath.Base(rel)); err != nil || ok {
			return ok, err
		}
		return filepath.Match(strings.TrimPrefix(pattern, "**/"), rel)
	}
	return filepath.Match(pattern, rel)
}

const outputSummaryByteLimit = 8000

func trimOutput(output string) string {
	return vigiloutput.TrimSummary(output, outputSummaryByteLimit)
}

func promptString(label, def string) string {
	fmt.Printf("%s [default: %s]: ", label, def)
	value, _ := promptReader.ReadString('\n')
	value = strings.TrimSpace(value)
	if value == "" {
		return def
	}
	return value
}

func promptBool(label string, def bool) bool {
	defText := "y"
	if !def {
		defText = "n"
	}
	for {
		value := strings.ToLower(promptString(label+" (y/n)", defText))
		switch value {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Println("Please answer y or n.")
	}
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func repairPatternPrompts(patterns []string) []string {
	var repaired []string
	for i, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err == nil {
			repaired = append(repaired, pattern)
			continue
		}
		if promptBool(fmt.Sprintf("Remove invalid public_assumption_patterns[%d]?", i), true) {
			continue
		}
		repaired = append(repaired, promptString(fmt.Sprintf("Replacement regex for public_assumption_patterns[%d]", i), ""))
	}
	return validPatternsOnly(repaired)
}

func validPatternsOnly(patterns []string) []string {
	var out []string
	for _, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err == nil {
			out = append(out, pattern)
		}
	}
	return out
}

func statusLabel(status string) string {
	return vigiloutput.StatusLabel(status, colorEnabled())
}

func commandStreamReporter(command, stream string, verbose bool, jsonOut bool) (*vigiloutput.StreamReporter, error) {
	stream = strings.ToLower(strings.TrimSpace(stream))
	if stream == "" && !verbose {
		return nil, nil
	}
	if jsonOut {
		return nil, fmt.Errorf("--stream/--verbose cannot be combined with --json")
	}
	format := vigiloutput.FormatText
	switch stream {
	case "", "text":
	case "jsonl":
		format = vigiloutput.FormatJSONL
	default:
		return nil, fmt.Errorf("--stream must be text or jsonl")
	}
	return vigiloutput.NewStreamReporter(vigiloutput.StreamOptions{
		Writer:  os.Stderr,
		Command: command,
		Format:  format,
		Verbose: verbose,
	}), nil
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("CI") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func samePath(path, other string) bool {
	pathInfo, pathErr := os.Stat(path)
	otherInfo, otherErr := os.Stat(other)
	if pathErr == nil && otherErr == nil {
		return os.SameFile(pathInfo, otherInfo)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path == other
	}
	absOther, err := filepath.Abs(other)
	if err != nil {
		return path == other
	}
	return absPath == absOther
}

func parseJSONOnly(args []string) (bool, error) {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			return false, fmt.Errorf("unknown option: %s", arg)
		}
	}
	return jsonOut, nil
}

func resolvedConfigPath(path string) string {
	if strings.TrimSpace(path) != "" {
		return path
	}
	if envPath := strings.TrimSpace(os.Getenv("VIGIL_CONFIG")); envPath != "" {
		return envPath
	}
	if found := findFileUpward(mustGetwd(), defaultConfigName); found != "" {
		return found
	}
	return defaultConfigName
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findFileUpward(start, name string) string {
	dir := filepath.Clean(start)
	for {
		candidate := filepath.Join(dir, name)
		if fileExists(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "project"
	}
	return wd
}

func beginCommandOutput(command string) func() {
	commandOutputTiming.Lock()
	previousCommand := commandOutputTiming.command
	previousStartedAt := commandOutputTiming.startedAt
	commandOutputTiming.command = command
	commandOutputTiming.startedAt = time.Now().UTC()
	commandOutputTiming.Unlock()
	return func() {
		commandOutputTiming.Lock()
		commandOutputTiming.command = previousCommand
		commandOutputTiming.startedAt = previousStartedAt
		commandOutputTiming.Unlock()
	}
}

func ensureCommandOutput(command string) func() {
	commandOutputTiming.Lock()
	active := commandOutputTiming.command != ""
	commandOutputTiming.Unlock()
	if active {
		return func() {}
	}
	return beginCommandOutput(command)
}

func currentCommandOutput(fallback string) (string, time.Time) {
	commandOutputTiming.Lock()
	command := commandOutputTiming.command
	startedAt := commandOutputTiming.startedAt
	commandOutputTiming.Unlock()
	if strings.TrimSpace(command) == "" {
		command = fallback
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return command, startedAt
}

func printJSON(v any, exitCodes ...int) int {
	exitCode := exitSuccess
	if len(exitCodes) > 0 {
		exitCode = exitCodes[0]
	}
	exitCode = vigilcli.ClassifyExit(exitCode).Code
	command, startedAt := currentCommandOutput("unknown")
	envelope := vigiloutput.EnvelopeFromPayload(command, exitCode, startedAt, time.Now().UTC(), v)
	if err := vigiloutput.WriteEnvelope(os.Stdout, envelope); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	return exitCode
}

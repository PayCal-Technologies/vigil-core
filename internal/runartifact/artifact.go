package runartifact

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
)

const SchemaVersion = "1"

const ManifestSchemaVersion = SchemaVersion

const MaxGateLogBytes int64 = 64 * 1024 * 1024

const MaxRunLogBytes int64 = 512 * 1024 * 1024

const truncationMarker = "\n[truncated]\n"

type Run struct {
	ID        string `json:"run_id"`
	Dir       string `json:"artifact_dir"`
	logBudget *byteBudget
}

type GateLogs struct {
	Stdout     io.Writer
	Stderr     io.Writer
	StdoutPath string
	StderrPath string
	stdout     *boundedLog
	stderr     *boundedLog
}

type Manifest struct {
	SchemaVersion   string `json:"schema_version"`
	RunID           string `json:"run_id"`
	CreatedAt       string `json:"created_at"`
	ArtifactDir     string `json:"artifact_dir"`
	PlanPath        string `json:"plan_path"`
	ResultPath      string `json:"result_path"`
	GatesDir        string `json:"gates_dir"`
	MaxGateLogBytes int64  `json:"max_gate_log_bytes"`
	MaxRunLogBytes  int64  `json:"max_run_log_bytes"`
}

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type boundedLog struct {
	mu        sync.Mutex
	file      *os.File
	limit     int64
	written   int64
	truncated bool
	budget    *byteBudget
	closed    bool
	closeErr  error
}

type byteBudget struct {
	mu        sync.Mutex
	remaining int64
}

func NewID(now time.Time) (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random), nil
}

func Start(baseDir, id string, plan any) (*Run, error) {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = filepath.Join(".vigil", "runs")
	}
	if err := validateRunID(id); err != nil {
		return nil, err
	}
	runDir := filepath.Join(baseDir, id)
	gatesDir := filepath.Join(runDir, "gates")
	if err := os.MkdirAll(gatesDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(runDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(gatesDir, 0o700); err != nil {
		return nil, err
	}
	run := &Run{
		ID:        id,
		Dir:       runDir,
		logBudget: &byteBudget{remaining: MaxRunLogBytes},
	}
	if err := run.writeJSON("manifest.json", NewManifest(id, runDir, time.Now().UTC())); err != nil {
		return nil, err
	}
	if err := run.writeJSON("plan.json", plan); err != nil {
		return nil, err
	}
	return run, nil
}

func NewManifest(id, dir string, createdAt time.Time) Manifest {
	return Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		RunID:           id,
		CreatedAt:       createdAt.UTC().Format(time.RFC3339Nano),
		ArtifactDir:     dir,
		PlanPath:        "plan.json",
		ResultPath:      "result.json",
		GatesDir:        "gates",
		MaxGateLogBytes: MaxGateLogBytes,
		MaxRunLogBytes:  MaxRunLogBytes,
	}
}

func ValidateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported run artifact manifest schema_version %q", manifest.SchemaVersion)
	}
	if err := validateRunID(manifest.RunID); err != nil {
		return fmt.Errorf("run artifact manifest run_id is invalid: %w", err)
	}
	if strings.TrimSpace(manifest.ArtifactDir) == "" {
		return fmt.Errorf("run artifact manifest artifact_dir is required")
	}
	if manifest.PlanPath != "plan.json" || manifest.ResultPath != "result.json" || manifest.GatesDir != "gates" {
		return fmt.Errorf("run artifact manifest standard paths are invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		return fmt.Errorf("invalid run artifact manifest created_at: %w", err)
	}
	if manifest.MaxGateLogBytes != MaxGateLogBytes || manifest.MaxRunLogBytes != MaxRunLogBytes {
		return fmt.Errorf("run artifact manifest log budgets are invalid")
	}
	return nil
}

func validateRunID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("run_id is required")
	}
	if id != strings.TrimSpace(id) {
		return fmt.Errorf("run_id must not have leading or trailing whitespace")
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("run_id must be a single path segment")
	}
	if !runIDPattern.MatchString(id) {
		return fmt.Errorf("run_id contains unsupported characters")
	}
	return nil
}

func (run *Run) OpenGate(index int, name string) (*GateLogs, error) {
	if run.logBudget == nil {
		run.logBudget = &byteBudget{remaining: MaxRunLogBytes}
	}
	safeName := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(name), "-"), "-.")
	if safeName == "" {
		safeName = "gate"
	}
	prefix := fmt.Sprintf("%03d-%s", index+1, safeName)
	stdoutPath := filepath.Join(run.Dir, "gates", prefix+".stdout.log")
	stderrPath := filepath.Join(run.Dir, "gates", prefix+".stderr.log")
	stdout, err := openBoundedLog(stdoutPath, MaxGateLogBytes, run.logBudget)
	if err != nil {
		return nil, err
	}
	stderr, err := openBoundedLog(stderrPath, MaxGateLogBytes, run.logBudget)
	if err != nil {
		_ = stdout.Close()
		_ = os.Remove(stdoutPath)
		return nil, err
	}
	return &GateLogs{
		Stdout:     stdout,
		Stderr:     stderr,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		stdout:     stdout,
		stderr:     stderr,
	}, nil
}

func (logs *GateLogs) Close() error {
	return errors.Join(logs.stdout.Close(), logs.stderr.Close())
}

func (logs *GateLogs) Truncated() (stdout, stderr bool) {
	return logs.stdout.Truncated(), logs.stderr.Truncated()
}

func openBoundedLog(path string, limit int64, budget *byteBudget) (*boundedLog, error) {
	if limit < int64(len(truncationMarker)) {
		return nil, fmt.Errorf("log limit must be at least %d bytes", len(truncationMarker))
	}
	if budget == nil {
		return nil, fmt.Errorf("run log budget is required")
	}
	markerBytes := int64(len(truncationMarker))
	if reserved := budget.take(markerBytes); reserved != markerBytes {
		budget.release(reserved)
		return nil, fmt.Errorf("run log budget exhausted")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		budget.release(markerBytes)
		return nil, err
	}
	return &boundedLog{file: file, limit: limit, budget: budget}, nil
}

func (log *boundedLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return 0, os.ErrClosed
	}

	originalLength := len(data)
	payloadLimit := log.limit - int64(len(truncationMarker))
	remaining := payloadLimit - log.written
	if remaining <= 0 {
		if originalLength > 0 {
			log.truncated = true
		}
		return originalLength, nil
	}
	toWrite := data
	if int64(len(toWrite)) > remaining {
		toWrite = toWrite[:remaining]
		log.truncated = true
	}
	allowed := log.budget.take(int64(len(toWrite)))
	if allowed < int64(len(toWrite)) {
		toWrite = toWrite[:allowed]
		log.truncated = true
	}
	if len(toWrite) == 0 {
		if originalLength > 0 {
			log.truncated = true
		}
		return originalLength, nil
	}
	written, err := log.file.Write(toWrite)
	log.written += int64(written)
	if err != nil || written != len(toWrite) {
		log.budget.release(int64(len(toWrite) - written))
		return written, firstNonNil(err, io.ErrShortWrite)
	}
	return originalLength, nil
}

func (log *boundedLog) Truncated() bool {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.truncated
}

func (log *boundedLog) Close() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return log.closeErr
	}
	log.closed = true
	var markerErr error
	if log.truncated {
		written, err := io.WriteString(log.file, truncationMarker)
		if err != nil {
			markerErr = err
		} else if written != len(truncationMarker) {
			markerErr = io.ErrShortWrite
		}
	} else {
		log.budget.release(int64(len(truncationMarker)))
	}
	log.closeErr = errors.Join(markerErr, log.file.Close())
	return log.closeErr
}

func (budget *byteBudget) take(requested int64) int64 {
	if requested <= 0 {
		return 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if requested > budget.remaining {
		requested = budget.remaining
	}
	budget.remaining -= requested
	return requested
}

func (budget *byteBudget) release(count int64) {
	if count <= 0 {
		return
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	budget.remaining += count
}

func firstNonNil(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (run *Run) WriteResult(result any) error {
	return run.writeJSON("result.json", result)
}

func (run *Run) WriteMutationDiff(data []byte) (string, error) {
	path := filepath.Join(run.Dir, "mutation-diff.patch")
	_, err := atomicfile.Write(path, data, atomicfile.Options{DefaultMode: 0o600})
	return path, err
}

func (run *Run) writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = atomicfile.Write(filepath.Join(run.Dir, name), data, atomicfile.Options{DefaultMode: 0o600})
	return err
}

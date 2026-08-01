package runartifact

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunWritesPrivatePlanLogsAndResult(t *testing.T) {
	id, err := NewID(time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	run, err := Start(t.TempDir(), id, map[string]any{"schema_version": SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	logs, err := run.OpenGate(0, "Go test / package")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(logs.Stdout, "stdout"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(logs.Stderr, "stderr"); err != nil {
		t.Fatal(err)
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run.WriteResult(map[string]any{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	manifestData, err := os.ReadFile(filepath.Join(run.Dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.RunID != id || manifest.ArtifactDir != run.Dir {
		t.Fatalf("manifest = %#v, run = %#v", manifest, run)
	}
	for _, path := range []string{
		filepath.Join(run.Dir, "manifest.json"),
		filepath.Join(run.Dir, "plan.json"),
		filepath.Join(run.Dir, "result.json"),
		logs.StdoutPath,
		logs.StderrPath,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %#o", path, info.Mode().Perm())
		}
	}
	for _, path := range []string{
		run.Dir,
		filepath.Join(run.Dir, "gates"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %#o", path, info.Mode().Perm())
		}
	}
}

func TestValidateManifestRejectsInvalidContractFields(t *testing.T) {
	valid := NewManifest("fixture", ".vigil/runs/fixture", time.Unix(0, 0))
	tests := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string
	}{
		{"schema", func(manifest *Manifest) { manifest.SchemaVersion = "2" }, "schema_version"},
		{"run id", func(manifest *Manifest) { manifest.RunID = "" }, "run_id"},
		{"run id whitespace", func(manifest *Manifest) { manifest.RunID = " fixture" }, "run_id"},
		{"run id traversal", func(manifest *Manifest) { manifest.RunID = "../fixture" }, "run_id"},
		{"run id separator", func(manifest *Manifest) { manifest.RunID = "nested/fixture" }, "run_id"},
		{"run id unsupported", func(manifest *Manifest) { manifest.RunID = "fixture:1" }, "run_id"},
		{"artifact dir", func(manifest *Manifest) { manifest.ArtifactDir = " " }, "artifact_dir"},
		{"plan path", func(manifest *Manifest) { manifest.PlanPath = "other.json" }, "standard paths"},
		{"result path", func(manifest *Manifest) { manifest.ResultPath = "other.json" }, "standard paths"},
		{"gates dir", func(manifest *Manifest) { manifest.GatesDir = "other" }, "standard paths"},
		{"created at", func(manifest *Manifest) { manifest.CreatedAt = "not-time" }, "created_at"},
		{"gate budget", func(manifest *Manifest) { manifest.MaxGateLogBytes = 1 }, "log budgets"},
		{"run budget", func(manifest *Manifest) { manifest.MaxRunLogBytes = 1 }, "log budgets"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := valid
			test.mutate(&manifest)
			if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateManifest error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestStartRejectsUnsafeRunIDs(t *testing.T) {
	for _, id := range []string{
		"",
		" ",
		" fixture",
		"fixture ",
		".",
		"..",
		"../fixture",
		"nested/fixture",
		`nested\fixture`,
		"fixture:1",
		"fixture?",
		"fixture-",
	} {
		t.Run(id, func(t *testing.T) {
			if _, err := Start(t.TempDir(), id, map[string]any{"status": "planned"}); err == nil {
				t.Fatal("expected unsafe run ID to be rejected")
			}
		})
	}
}

func TestBoundedLogMarksAndLimitsTruncatedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounded.log")
	log, err := openBoundedLog(path, 32, &byteBudget{remaining: 64})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("x", 64)
	if written, err := io.WriteString(log, input); err != nil || written != len(input) {
		t.Fatalf("write = %d, %v", written, err)
	}
	if !log.Truncated() {
		t.Fatal("oversized log was not marked truncated")
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 32 || !strings.HasSuffix(string(data), truncationMarker) {
		t.Fatalf("bounded log = %q (%d bytes)", data, len(data))
	}
}

func TestRunLogBudgetIsSharedAndFailsClosed(t *testing.T) {
	budget := &byteBudget{remaining: 40}
	first, err := openBoundedLog(filepath.Join(t.TempDir(), "first.log"), 32, budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(first, strings.Repeat("x", 64)); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoundedLog(filepath.Join(t.TempDir(), "second.log"), 32, budget); err == nil {
		t.Fatal("expected shared run log budget exhaustion")
	}
}

func TestOpenGateRefusesToReplaceExistingLog(t *testing.T) {
	run, err := Start(t.TempDir(), "fixture", map[string]any{"status": "planned"})
	if err != nil {
		t.Fatal(err)
	}
	logs, err := run.OpenGate(0, "gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := run.OpenGate(0, "gate"); err == nil {
		t.Fatal("expected existing log refusal")
	}
}

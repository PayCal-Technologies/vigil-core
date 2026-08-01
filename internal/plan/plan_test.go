package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/config"
)

func fixtureDocument(t *testing.T) Document {
	t.Helper()
	digest := DigestBytes([]byte("fixture"))
	document, err := New(
		"workflow:local",
		time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Inputs{
			BinaryDigest:          digest,
			ConfigPath:            "/repo/vigil.config.json",
			ConfigDigest:          digest,
			RepositoryRoot:        "/repo",
			RepositoryHead:        "abc123",
			WorkspaceDigest:       digest,
			CommandRegistryDigest: digest,
			PackDigest:            digest,
		},
		Options{TagFilter: "pre-push", DefaultTimeout: "10m0s"},
		[]config.Gate{{Name: "test", Command: "go", Args: []string{"test", "./..."}, ReadOnly: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestPlanIDDetectsAnyReviewedActionChange(t *testing.T) {
	document := fixtureDocument(t)
	document.Gates[0].Args[1] = "./internal/..."
	if err := Validate(document); err == nil || !strings.Contains(err.Error(), "plan_id mismatch") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPlanDeepCopiesWorkflowGraphControls(t *testing.T) {
	digest := DigestBytes([]byte("fixture"))
	required := false
	gates := []config.Gate{{
		Name:          "network",
		Command:       "true",
		ReadOnly:      true,
		Tags:          []string{"network"},
		DependsOn:     []string{},
		ParallelGroup: "analysis",
		Required:      &required,
		Retry:         &config.GateRetry{MaxAttempts: 2, On: []string{"failed"}},
		Environment:   map[string]string{"FIXTURE": "original"},
		Artifacts:     []config.GateArtifact{{Path: "report.xml", Kind: "junit", Required: &required}},
	}}
	document, err := New(
		"workflow:local",
		time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Inputs{
			BinaryDigest:          digest,
			ConfigPath:            "/repo/vigil.config.json",
			ConfigDigest:          digest,
			RepositoryRoot:        "/repo",
			RepositoryHead:        "abc123",
			WorkspaceDigest:       digest,
			CommandRegistryDigest: digest,
			PackDigest:            digest,
		},
		Options{DefaultTimeout: "10m0s", MaxParallel: 4},
		gates,
	)
	if err != nil {
		t.Fatal(err)
	}
	gates[0].Retry.On[0] = "timed_out"
	gates[0].Environment["FIXTURE"] = "changed"
	*gates[0].Required = true
	*gates[0].Artifacts[0].Required = true
	if document.Gates[0].Retry.On[0] != "failed" ||
		document.Gates[0].Environment["FIXTURE"] != "original" ||
		document.Gates[0].IsRequired() ||
		document.Gates[0].Artifacts[0].IsRequired() {
		t.Fatalf("plan retained mutable gate input: %#v", document.Gates[0])
	}
}

func TestReadWriteRoundTripIsPrivateAndStrict(t *testing.T) {
	document := fixtureDocument(t)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := Write(path, document); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %#o", info.Mode().Perm())
	}
	decoded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.PlanID != document.PlanID || decoded.Gates[0].Command != "go" {
		t.Fatalf("decoded = %#v", decoded)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	tampered, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Read() error = %v", err)
	}
}

func TestReadRejectsSymlinkAndOversizedPlan(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := Write(target, fixtureDocument(t)); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Read(symlink) error = %v", err)
	}

	large := filepath.Join(root, "large.json")
	if err := os.WriteFile(large, make([]byte, MaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(large); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Read(large) error = %v", err)
	}
}

func TestCompareReportsSortedInputMismatches(t *testing.T) {
	expected := fixtureDocument(t).Inputs
	actual := expected
	actual.PackDigest = DigestBytes([]byte("other pack"))
	actual.BinaryDigest = DigestBytes([]byte("other binary"))
	mismatches := Compare(expected, actual)
	if len(mismatches) != 2 || mismatches[0].Field != "binary_digest" || mismatches[1].Field != "pack_digest" {
		t.Fatalf("mismatches = %#v", mismatches)
	}
}

func TestValidateRejectsInvalidDurationInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Document)
		want   string
	}{
		{
			name: "zero default timeout",
			mutate: func(document *Document) {
				document.Options.DefaultTimeout = "0s"
			},
			want: "invalid plan default_timeout",
		},
		{
			name: "malformed default timeout",
			mutate: func(document *Document) {
				document.Options.DefaultTimeout = "10mx"
			},
			want: "invalid plan default_timeout",
		},
		{
			name: "malformed gate timeout",
			mutate: func(document *Document) {
				document.Gates[0].Timeout = "10mx"
			},
			want: "gates[0].timeout must be a positive Go duration",
		},
		{
			name: "malformed retry delay",
			mutate: func(document *Document) {
				document.Gates[0].Tags = []string{"network"}
				document.Gates[0].Retry = &config.GateRetry{MaxAttempts: 2, Delay: "10mx"}
			},
			want: "gates[0].retry.delay must be a duration from 0s through 5m",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := fixtureDocument(t)
			test.mutate(&document)
			if err := Validate(document); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDigestFileStreamsRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != DigestBytes([]byte("fixture")) {
		t.Fatalf("digest = %s", got)
	}
}

func TestPlanV1GoldenCompatibilityFixture(t *testing.T) {
	path := filepath.Join("testdata", "plan-v1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document Document
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if err := Validate(document); err != nil {
		t.Fatalf("golden fixture is incompatible: %v", err)
	}
}

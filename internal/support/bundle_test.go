package support

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/packs"
)

func TestBuildIsContentStableAndRedactsPathsByDefault(t *testing.T) {
	root := t.TempDir()
	input := Input{
		GeneratedAt:   time.Unix(100, 0),
		ConfigPath:    filepath.Join(root, "vigil.config.json"),
		ConfigSummary: map[string]any{"schema_version": "3"},
		Build:         map[string]any{"version": "test"},
		Commands:      []string{"verify"},
		Packs: packs.Report{
			Root:  filepath.Join(root, "extensions"),
			Roots: []string{filepath.Join(root, "extensions")},
			Extensions: []packs.Manifest{{
				ID:     "fixture",
				Origin: "repository",
				Path:   filepath.Join(root, "extensions", "fixture", "extension.json"),
			}},
			Issues: []string{filepath.Join(root, "extensions") + ": invalid"},
		},
		DiagnosticPaths: []string{filepath.Join(root, "extensions")},
	}
	first := Build(input)
	input.GeneratedAt = time.Unix(200, 0)
	second := Build(input)
	if first["bundle_id"] != second["bundle_id"] {
		t.Fatalf("ids differ: %v %v", first["bundle_id"], second["bundle_id"])
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), root) {
		t.Fatalf("bundle leaked root: %s", encoded)
	}
	if _, ok := first["config"]; ok {
		t.Fatal("bundle included config without opt-in")
	}
	if _, ok := first["git_status"]; ok {
		t.Fatal("bundle included git status without opt-in")
	}
}

func TestWriteUsesMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "bundle.json")
	bundle := map[string]any{"schema_version": SchemaVersion}
	if err := Write(path, bundle); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %#o", info.Mode().Perm())
	}
}

func TestIncludedEmptyGitStatusIsAnArray(t *testing.T) {
	bundle := Build(Input{
		GeneratedAt:      time.Unix(100, 0),
		IncludeGitStatus: true,
		GitStatus:        []GitStatusEntry{},
	})
	status, ok := bundle["git_status"].([]map[string]string)
	if !ok || status == nil || len(status) != 0 {
		t.Fatalf("empty Git status = %#v", bundle["git_status"])
	}
}

func TestStructuredGitStatusRedactionPreservesRecordBoundaries(t *testing.T) {
	status := RedactGitStatusEntries([]GitStatusEntry{
		{Status: "??", Path: "nested/line\nbreak.txt"},
		{Status: "R ", Path: "new/location.txt", OriginalPath: "old/location.txt"},
	})
	if len(status) != 2 ||
		status[0]["status"] != "??" ||
		status[0]["path"] != "line\nbreak.txt" ||
		status[1]["status"] != "R" ||
		status[1]["path"] != "location.txt -> location.txt" {
		t.Fatalf("status = %#v", status)
	}
}

func TestDiagnosticRedactionMasksCommonSecretValues(t *testing.T) {
	diagnostic := strings.Join([]string{
		"open /private/repo/vigil.config.json failed",
		"api_token=ghp_1234567890abcdef",
		"password: 'correcthorsebattery'",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
		"exit_code=123",
	}, "\n")
	redacted := RedactDiagnosticText(diagnostic, "/private/repo/vigil.config.json")
	for _, leaked := range []string{
		"/private/repo/vigil.config.json",
		"ghp_1234567890abcdef",
		"correcthorsebattery",
		"abcdefghijklmnopqrstuvwxyz012345",
	} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("diagnostic retained sensitive value %q:\n%s", leaked, redacted)
		}
	}
	for _, want := range []string{
		"vigil.config.json",
		"api_token=[redacted]",
		"password: '[redacted]'",
		"Authorization: Bearer [redacted]",
		"exit_code=123",
	} {
		if !strings.Contains(redacted, want) {
			t.Fatalf("diagnostic missing %q:\n%s", want, redacted)
		}
	}
}

func TestBuildReportsDiagnosticSecretRedaction(t *testing.T) {
	bundle := Build(Input{GeneratedAt: time.Unix(100, 0)})
	report, ok := bundle["redaction_report"].(map[string]any)
	if !ok {
		t.Fatalf("redaction report = %#v", bundle["redaction_report"])
	}
	if report["diagnostics"] != "paths-and-common-secret-patterns" {
		t.Fatalf("diagnostic redaction report = %#v", report["diagnostics"])
	}
}

func FuzzSupportRedaction(f *testing.F) {
	f.Add("open /private/project/config.json failed", "/private/project/config.json", "R  old/name -> new/name")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, diagnostic, path, statusLine string) {
		if len(diagnostic) > 64*1024 || len(path) > 4096 || len(statusLine) > 64*1024 {
			return
		}
		first := RedactDiagnosticText(diagnostic, path)
		second := RedactDiagnosticText(diagnostic, path)
		if first != second {
			t.Fatal("diagnostic redaction is nondeterministic")
		}
		trimmedPath := strings.TrimSpace(path)
		if trimmedPath != "" && strings.Contains(trimmedPath, string(filepath.Separator)) &&
			RedactedPath(trimmedPath) != trimmedPath &&
			strings.Contains(diagnostic, trimmedPath) && strings.Contains(first, trimmedPath) {
			t.Fatalf("diagnostic retained full path %q", trimmedPath)
		}
		redacted := RedactGitStatus([]string{statusLine})
		if _, err := json.Marshal(redacted); err != nil {
			t.Fatalf("redacted Git status is not JSON-safe: %v", err)
		}
	})
}

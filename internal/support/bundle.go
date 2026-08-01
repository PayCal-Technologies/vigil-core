package support

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
	"github.com/PayCal-Technologies/vigil-public/internal/packs"
)

const SchemaVersion = "1"

var secretAssignmentPattern = regexp.MustCompile(`(?i)\b(api[_-]?(?:key|token)|access[_-]?token|auth[_-]?token|authorization|credential|client[_-]?secret|private[_-]?key|secret[_-]?key|password|passwd|secret|token)\b\s*[:=]\s*(['"]?)[^\s,'" ;]{8,}['"]?`)

var bearerTokenPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`)

type Input struct {
	GeneratedAt      time.Time
	ConfigPath       string
	ConfigError      string
	ConfigSummary    map[string]any
	Config           any
	IncludeConfig    bool
	IncludeGitStatus bool
	GitStatus        []GitStatusEntry
	Build            any
	Commands         any
	Packs            packs.Report
	DiagnosticPaths  []string
}

type GitStatusEntry struct {
	Status       string
	Path         string
	OriginalPath string
}

func Build(input Input) map[string]any {
	generatedAt := input.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	bundle := map[string]any{
		"schema_version": SchemaVersion,
		"generated_at":   generatedAt.UTC().Format(time.RFC3339),
		"config_path":    RedactedPath(input.ConfigPath),
		"config_error":   "",
		"build":          input.Build,
		"commands":       input.Commands,
		"packs":          RedactPackReport(input.Packs, input.DiagnosticPaths...),
		"redaction_report": map[string]any{
			"paths":       "basename-only",
			"diagnostics": "paths-and-common-secret-patterns",
			"environment": "excluded",
			"secrets":     "not-collected",
			"config":      "summary-only",
			"git_status":  "excluded",
			"uploads":     "disabled",
		},
	}
	if strings.TrimSpace(input.ConfigError) != "" {
		bundle["config_error"] = RedactDiagnosticText(input.ConfigError, append(input.DiagnosticPaths, input.ConfigPath)...)
	} else if input.ConfigSummary != nil {
		bundle["config_summary"] = input.ConfigSummary
		if input.IncludeConfig {
			bundle["config"] = input.Config
			report := bundle["redaction_report"].(map[string]any)
			report["config"] = "included-by-explicit-request"
			report["config_warning"] = "user-provided config values are not content-scanned for secrets"
		}
	}
	if input.IncludeGitStatus {
		bundle["git_status"] = RedactGitStatusEntries(input.GitStatus)
		bundle["redaction_report"].(map[string]any)["git_status"] = "included-with-path-redaction"
	}
	bundleID := ID(bundle)
	bundle["bundle_id"] = bundleID
	return bundle
}

func ID(bundle map[string]any) string {
	stable := map[string]any{}
	for key, value := range bundle {
		if key == "generated_at" || key == "bundle_id" || key == "output" {
			continue
		}
		stable[key] = value
	}
	data, err := json.Marshal(stable)
	if err != nil {
		return "invalid"
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:12])
}

func DefaultPath(bundleID string) string {
	return filepath.Join(".vigil", "support", "support-bundle-"+bundleID+".json")
}

func AddOutput(bundle map[string]any, path string) {
	bundle["output"] = map[string]any{
		"path":        RedactedPath(path),
		"permissions": "0600",
		"atomic":      true,
	}
}

func Write(path string, bundle map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = atomicfile.Write(path, data, atomicfile.Options{DefaultMode: 0o600})
	return err
}

func RedactPackReport(report packs.Report, diagnosticPaths ...string) packs.Report {
	report.Root = redactPackRoot(report.Root)
	for i := range report.Roots {
		report.Roots[i] = redactPackRoot(report.Roots[i])
	}
	for i := range report.Extensions {
		pack := &report.Extensions[i]
		pack.Path = pack.Origin + ":" + pack.ID + "/extension.json"
	}
	for i := range report.Issues {
		report.Issues[i] = RedactDiagnosticText(report.Issues[i], diagnosticPaths...)
	}
	return report
}

func RedactDiagnosticText(value string, paths ...string) string {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		value = strings.ReplaceAll(value, path, RedactedPath(path))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		value = strings.ReplaceAll(value, home, "$HOME")
	}
	value = secretAssignmentPattern.ReplaceAllStringFunc(value, redactSecretAssignment)
	value = bearerTokenPattern.ReplaceAllString(value, "Bearer [redacted]")
	return value
}

func redactSecretAssignment(value string) string {
	separator := strings.IndexAny(value, ":=")
	if separator < 0 {
		return "[redacted]"
	}
	prefix := value[:separator+1]
	suffix := value[separator+1:]
	leadingSpace := len(suffix) - len(strings.TrimLeft(suffix, " \t"))
	spacing := suffix[:leadingSpace]
	token := suffix[leadingSpace:]
	quote := ""
	if strings.HasPrefix(token, `"`) || strings.HasPrefix(token, `'`) {
		quote = token[:1]
	}
	return prefix + spacing + quote + "[redacted]" + quote
}

func RedactGitStatus(lines []string) []map[string]string {
	status := make([]map[string]string, 0)
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		path := strings.TrimSpace(line[2:])
		if parts := strings.Split(path, " -> "); len(parts) == 2 {
			path = RedactedPath(parts[0]) + " -> " + RedactedPath(parts[1])
		} else {
			path = RedactedPath(path)
		}
		status = append(status, map[string]string{
			"status": strings.TrimSpace(line[:2]),
			"path":   path,
		})
	}
	return status
}

func RedactGitStatusEntries(entries []GitStatusEntry) []map[string]string {
	status := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		path := RedactedPath(entry.Path)
		if entry.OriginalPath != "" {
			path = RedactedPath(entry.OriginalPath) + " -> " + path
		}
		status = append(status, map[string]string{
			"status": strings.TrimSpace(entry.Status),
			"path":   path,
		})
	}
	return status
}

func RedactedPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

func redactPackRoot(root string) string {
	if strings.HasPrefix(root, "embedded:") {
		return root
	}
	return RedactedPath(root)
}

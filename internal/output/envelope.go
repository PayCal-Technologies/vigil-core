package output

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

const EnvelopeSchemaVersion = "1"

var diagnosticCodePattern = regexp.MustCompile(`^VIGIL_[EW]_[A-Z0-9_]+$`)

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Artifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type Envelope struct {
	SchemaVersion string       `json:"schema_version"`
	Command       string       `json:"command"`
	Status        string       `json:"status"`
	ExitCode      int          `json:"exit_code"`
	StartedAt     string       `json:"started_at"`
	FinishedAt    string       `json:"finished_at"`
	DurationMS    int64        `json:"duration_ms"`
	Warnings      []Diagnostic `json:"warnings"`
	Errors        []Diagnostic `json:"errors"`
	Data          any          `json:"data"`
	Artifacts     []Artifact   `json:"artifacts"`
}

func NewEnvelope(command string, exitCode int, startedAt, finishedAt time.Time, data any, warnings, errors []Diagnostic, artifacts []Artifact) Envelope {
	if finishedAt.Before(startedAt) {
		finishedAt = startedAt
	}
	class := cli.ClassifyExit(exitCode)
	if exitCode != class.Code {
		exitCode = class.Code
	}
	warnings = nonNilDiagnostics(warnings)
	errors = nonNilDiagnostics(errors)
	artifacts = nonNilArtifacts(artifacts)
	if exitCode != cli.ExitSuccess && len(errors) == 0 {
		errors = append(errors, Diagnostic{
			Code:    "VIGIL_E_" + strings.ToUpper(class.Name),
			Message: fmt.Sprintf("%s exited with %s", command, class.Name),
		})
	}
	return Envelope{
		SchemaVersion: EnvelopeSchemaVersion,
		Command:       command,
		Status:        class.Status,
		ExitCode:      exitCode,
		StartedAt:     startedAt.UTC().Format(time.RFC3339Nano),
		FinishedAt:    finishedAt.UTC().Format(time.RFC3339Nano),
		DurationMS:    finishedAt.Sub(startedAt).Milliseconds(),
		Warnings:      warnings,
		Errors:        errors,
		Data:          data,
		Artifacts:     artifacts,
	}
}

func EnvelopeFromPayload(command string, exitCode int, startedAt, finishedAt time.Time, payload any) Envelope {
	data, warnings, errors, artifacts := normalizePayload(payload)
	return NewEnvelope(command, exitCode, startedAt, finishedAt, data, warnings, errors, artifacts)
}

func WriteEnvelope(writer interface{ Write([]byte) (int, error) }, envelope Envelope) error {
	return WriteJSON(writer, envelope)
}

func ValidateEnvelope(envelope Envelope) error {
	if envelope.SchemaVersion != EnvelopeSchemaVersion {
		return fmt.Errorf("unsupported output schema_version %q", envelope.SchemaVersion)
	}
	if strings.TrimSpace(envelope.Command) == "" {
		return fmt.Errorf("output command is required")
	}
	class := cli.ClassifyExit(envelope.ExitCode)
	if class.Code != envelope.ExitCode {
		return fmt.Errorf("unsupported output exit_code %d", envelope.ExitCode)
	}
	if envelope.Status != class.Status {
		return fmt.Errorf("status %q does not match exit_code %d", envelope.Status, envelope.ExitCode)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, envelope.StartedAt)
	if err != nil {
		return fmt.Errorf("invalid started_at: %w", err)
	}
	finishedAt, err := time.Parse(time.RFC3339Nano, envelope.FinishedAt)
	if err != nil {
		return fmt.Errorf("invalid finished_at: %w", err)
	}
	if finishedAt.Before(startedAt) {
		return fmt.Errorf("finished_at precedes started_at")
	}
	if envelope.DurationMS < 0 {
		return fmt.Errorf("duration_ms must not be negative")
	}
	if expectedDurationMS := finishedAt.Sub(startedAt).Milliseconds(); envelope.DurationMS != expectedDurationMS {
		return fmt.Errorf("duration_ms %d does not match started_at/finished_at delta %d", envelope.DurationMS, expectedDurationMS)
	}
	if envelope.Warnings == nil || envelope.Errors == nil || envelope.Artifacts == nil {
		return fmt.Errorf("warnings, errors, and artifacts must be arrays")
	}
	for _, diagnostic := range append(append([]Diagnostic(nil), envelope.Warnings...), envelope.Errors...) {
		if !diagnosticCodePattern.MatchString(diagnostic.Code) {
			return fmt.Errorf("invalid diagnostic code %q", diagnostic.Code)
		}
		if strings.TrimSpace(diagnostic.Message) == "" {
			return fmt.Errorf("diagnostic message is required")
		}
	}
	for _, artifact := range envelope.Artifacts {
		if strings.TrimSpace(artifact.Kind) == "" || strings.TrimSpace(artifact.Path) == "" {
			return fmt.Errorf("artifact kind and path are required")
		}
	}
	return nil
}

func normalizePayload(payload any) (any, []Diagnostic, []Diagnostic, []Artifact) {
	source, ok := payload.(map[string]any)
	if !ok {
		return payload, []Diagnostic{}, []Diagnostic{}, []Artifact{}
	}
	data := make(map[string]any, len(source))
	var warnings []Diagnostic
	var errors []Diagnostic
	var artifacts []Artifact
	for key, value := range source {
		switch key {
		case "status":
			continue
		case "warning":
			warnings = append(warnings, diagnosticValues("VIGIL_W_COMMAND", value)...)
		case "warnings":
			warnings = append(warnings, diagnosticValues("VIGIL_W_COMMAND", value)...)
		case "error":
			errors = append(errors, diagnosticValues("VIGIL_E_COMMAND", value)...)
		case "errors":
			errors = append(errors, diagnosticValues("VIGIL_E_COMMAND", value)...)
		case "artifacts":
			artifacts = append(artifacts, artifactValues(value)...)
		case "artifact_dir":
			if path, ok := value.(string); ok && strings.TrimSpace(path) != "" {
				artifacts = append(artifacts, Artifact{Kind: "run_directory", Path: path})
			}
			data[key] = value
		default:
			data[key] = value
		}
	}
	return data, nonNilDiagnostics(warnings), nonNilDiagnostics(errors), nonNilArtifacts(artifacts)
}

func diagnosticValues(defaultCode string, value any) []Diagnostic {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []Diagnostic{{Code: defaultCode, Message: typed}}
	case []string:
		out := make([]Diagnostic, 0, len(typed))
		for _, message := range typed {
			if strings.TrimSpace(message) != "" {
				out = append(out, Diagnostic{Code: defaultCode, Message: message})
			}
		}
		return out
	case []Diagnostic:
		return append([]Diagnostic(nil), typed...)
	case Diagnostic:
		return []Diagnostic{typed}
	default:
		return nil
	}
}

func artifactValues(value any) []Artifact {
	switch typed := value.(type) {
	case []Artifact:
		return append([]Artifact(nil), typed...)
	case Artifact:
		return []Artifact{typed}
	case []string:
		out := make([]Artifact, 0, len(typed))
		for _, path := range typed {
			if strings.TrimSpace(path) != "" {
				out = append(out, Artifact{Kind: "file", Path: path})
			}
		}
		return out
	default:
		return nil
	}
}

func nonNilDiagnostics(values []Diagnostic) []Diagnostic {
	if values == nil {
		return []Diagnostic{}
	}
	return values
}

func nonNilArtifacts(values []Artifact) []Artifact {
	if values == nil {
		return []Artifact{}
	}
	return values
}

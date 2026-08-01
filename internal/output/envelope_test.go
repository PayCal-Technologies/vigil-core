package output

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

func TestEnvelopeFromPayloadHasRequiredContract(t *testing.T) {
	started := time.Date(2026, 7, 31, 12, 0, 0, 0, time.FixedZone("fixture", -6*60*60))
	finished := started.Add(1250 * time.Millisecond)
	envelope := EnvelopeFromPayload("verify", cli.ExitCheckFailed, started, finished, map[string]any{
		"status":       "fail",
		"error":        "verification failed",
		"warning":      "partial evidence",
		"artifact_dir": ".vigil/runs/fixture",
		"checks":       []string{"config"},
	})
	if envelope.SchemaVersion != EnvelopeSchemaVersion || envelope.Command != "verify" {
		t.Fatalf("identity = %#v", envelope)
	}
	if envelope.Status != "failed" || envelope.ExitCode != cli.ExitCheckFailed {
		t.Fatalf("outcome = %#v", envelope)
	}
	if envelope.DurationMS != 1250 || envelope.StartedAt != "2026-07-31T18:00:00Z" {
		t.Fatalf("timing = %#v", envelope)
	}
	if len(envelope.Warnings) != 1 || len(envelope.Errors) != 1 || len(envelope.Artifacts) != 1 {
		t.Fatalf("diagnostics/artifacts = %#v", envelope)
	}
	data := envelope.Data.(map[string]any)
	if _, exists := data["status"]; exists {
		t.Fatal("legacy status leaked into envelope data")
	}
	if data["artifact_dir"] != ".vigil/runs/fixture" {
		t.Fatalf("data = %#v", data)
	}
}

func TestEnvelopeAlwaysWritesArraysAndNormalizesUnknownExit(t *testing.T) {
	now := time.Unix(0, 0)
	envelope := NewEnvelope("fixture", 99, now, now, nil, nil, nil, nil)
	if envelope.ExitCode != cli.ExitInternal || len(envelope.Errors) != 1 {
		t.Fatalf("envelope = %#v", envelope)
	}
	var encoded bytes.Buffer
	if err := WriteEnvelope(&encoded, envelope); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"warnings", "errors", "artifacts"} {
		if _, ok := decoded[field].([]any); !ok {
			t.Fatalf("%s is not an array: %#v", field, decoded[field])
		}
	}
}

func TestValidateEnvelopeRejectsStatusExitMismatch(t *testing.T) {
	now := time.Unix(0, 0)
	envelope := NewEnvelope("fixture", cli.ExitSuccess, now, now, map[string]any{}, nil, nil, nil)
	envelope.Status = "failed"
	if err := ValidateEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "does not match exit_code") {
		t.Fatalf("ValidateEnvelope mismatch error = %v", err)
	}
}

func TestValidateEnvelopeRejectsWhitespaceOnlyRequiredStrings(t *testing.T) {
	now := time.Unix(0, 0)
	valid := NewEnvelope("fixture", cli.ExitSuccess, now, now, map[string]any{}, nil, nil, nil)
	tests := []struct {
		name    string
		mutate  func(*Envelope)
		wantErr string
	}{
		{"command", func(envelope *Envelope) { envelope.Command = " " }, "command"},
		{"diagnostic message", func(envelope *Envelope) {
			envelope.Warnings = []Diagnostic{{Code: "VIGIL_W_FIXTURE", Message: " "}}
		}, "diagnostic message"},
		{"artifact kind", func(envelope *Envelope) {
			envelope.Artifacts = []Artifact{{Kind: " ", Path: "report.json"}}
		}, "artifact kind and path"},
		{"artifact path", func(envelope *Envelope) {
			envelope.Artifacts = []Artifact{{Kind: "report", Path: " "}}
		}, "artifact kind and path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := valid
			test.mutate(&envelope)
			if err := ValidateEnvelope(envelope); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateEnvelope error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateEnvelopeRejectsInconsistentDuration(t *testing.T) {
	started := time.Unix(0, 0)
	finished := started.Add(125 * time.Millisecond)
	envelope := NewEnvelope("fixture", cli.ExitSuccess, started, finished, map[string]any{}, nil, nil, nil)
	envelope.DurationMS = 124
	if err := ValidateEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "does not match started_at/finished_at") {
		t.Fatalf("ValidateEnvelope duration error = %v", err)
	}
}

func TestEnvelopeV1GoldenCompatibilityFixtures(t *testing.T) {
	for _, name := range []string{"envelope-v1-success.json", "envelope-v1-failure.json"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			var envelope Envelope
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			if err := ValidateEnvelope(envelope); err != nil {
				t.Fatalf("golden fixture is incompatible: %v", err)
			}
		})
	}
}

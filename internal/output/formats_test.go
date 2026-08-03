package output

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestResolveFormatSupportsJSONAliasAndRejectsConflicts(t *testing.T) {
	format, err := ResolveFormat(true, "", FormatText, FormatJSON)
	if err != nil || format != FormatJSON {
		t.Fatalf("ResolveFormat() = %q, %v", format, err)
	}
	if _, err := ResolveFormat(true, "junit", FormatJSON, FormatJUnit); err == nil {
		t.Fatal("ResolveFormat accepted conflicting flags")
	}
	if _, err := ResolveFormat(false, "sarif", FormatJSON); err == nil {
		t.Fatal("ResolveFormat accepted a disallowed format")
	}
}

func TestWriteJSONLEventIsOneCompactObject(t *testing.T) {
	var output bytes.Buffer
	if err := WriteJSONLEvent(&output, 1, "gate_started", "workflow:local", time.Unix(0, 0), map[string]string{"name": "test"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("JSONL output is not one line: %q", output.String())
	}
	var event Event
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 1 || event.Type != "gate_started" || event.Command != "workflow:local" {
		t.Fatalf("event = %#v", event)
	}
	if event.SchemaVersion != EventSchemaVersion {
		t.Fatalf("event schema_version = %q", event.SchemaVersion)
	}
	if err := ValidateEvent(event); err != nil {
		t.Fatalf("encoded event failed validation: %v", err)
	}
}

func TestStreamReporterWritesTextStatus(t *testing.T) {
	var output bytes.Buffer
	reporter := NewStreamReporter(StreamOptions{Writer: &output, Command: "fixture", Format: FormatText, Verbose: true})

	if err := reporter.Start("setup", "loading config"); err != nil {
		t.Fatal(err)
	}
	if err := reporter.OK("setup", 125*time.Millisecond, "ready"); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "[INFO] setup started: loading config") || !strings.Contains(text, "[OK] setup passed (125ms): ready") {
		t.Fatalf("stream output = %q", text)
	}
}

func TestStreamReporterWritesJSONLEvents(t *testing.T) {
	var output bytes.Buffer
	reporter := NewStreamReporter(StreamOptions{Writer: &output, Command: "fixture", Format: FormatJSONL})

	if err := reporter.OK("bundle", time.Second, map[string]string{"path": "bundle.json"}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("JSONL line count = %d: %q", len(lines), output.String())
	}
	var event Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "phase_finished" || event.Command != "fixture" || event.Sequence != 1 {
		t.Fatalf("event = %#v", event)
	}
}

func TestValidateEventRejectsInvalidContractFields(t *testing.T) {
	valid := Event{
		SchemaVersion: EventSchemaVersion,
		Sequence:      1,
		Type:          "run_started",
		Command:       "workflow:local",
		Timestamp:     time.Unix(0, 0).UTC().Format(time.RFC3339Nano),
		Data:          map[string]any{},
	}
	tests := []struct {
		name    string
		mutate  func(*Event)
		wantErr string
	}{
		{"schema", func(event *Event) { event.SchemaVersion = "2" }, "schema_version"},
		{"sequence", func(event *Event) { event.Sequence = 0 }, "sequence"},
		{"type", func(event *Event) { event.Type = " " }, "type and command"},
		{"command", func(event *Event) { event.Command = "" }, "type and command"},
		{"timestamp", func(event *Event) { event.Timestamp = "not-time" }, "timestamp"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := valid
			test.mutate(&event)
			if err := ValidateEvent(event); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateEvent error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestWriteJUnitEscapesContentAndClassifiesChecks(t *testing.T) {
	var output bytes.Buffer
	checks := []Check{
		{Name: "pass <one>", Status: "ok", DurationMS: 125},
		{Name: "fail", Status: "failed", Message: `bad "result"`, Output: "details"},
		{Name: "skip", Status: "skipped"},
	}
	if err := WriteJUnit(&output, "verify", checks); err != nil {
		t.Fatal(err)
	}
	var decoded junitSuites
	if err := xml.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Tests != 3 || decoded.Failures != 1 || decoded.Skipped != 1 {
		t.Fatalf("JUnit totals = %#v", decoded)
	}
	if decoded.Suites[0].Cases[1].Failure == nil || decoded.Suites[0].Cases[1].Failure.Body != "details" {
		t.Fatalf("failure = %#v", decoded.Suites[0].Cases[1])
	}
}

func TestWriteSARIFProducesVersionedLocationsAndRules(t *testing.T) {
	var output bytes.Buffer
	findings := []Finding{{
		RuleID:  "vigil.secret",
		Level:   "error",
		Message: "possible secret",
		Path:    "config/example.env",
		Line:    4,
		Column:  2,
	}}
	if err := WriteSARIF(&output, "Vigil", "0.3.0", findings); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["version"] != "2.1.0" {
		t.Fatalf("SARIF = %#v", decoded)
	}
	if !strings.Contains(output.String(), `"startLine": 4`) || !strings.Contains(output.String(), `"vigil.secret"`) {
		t.Fatalf("SARIF missing location or rule:\n%s", output.String())
	}
}

func TestWriteGitHubAnnotationsEscapesCommands(t *testing.T) {
	var output bytes.Buffer
	if err := WriteGitHubAnnotations(&output, []Finding{{
		RuleID:  "vigil,test",
		Level:   "warning",
		Message: "line one\nline two%",
		Path:    "a,b.go",
		Line:    3,
	}}); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{"::warning ", "file=a%2Cb.go", "title=vigil%2Ctest", "line one%0Aline two%25"} {
		if !strings.Contains(got, want) {
			t.Fatalf("annotation missing %q: %s", want, got)
		}
	}
}

package output

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Format string

const (
	FormatText   Format = "text"
	FormatJSON   Format = "json"
	FormatJSONL  Format = "jsonl"
	FormatJUnit  Format = "junit"
	FormatSARIF  Format = "sarif"
	FormatGitHub Format = "github"
)

const EventSchemaVersion = EnvelopeSchemaVersion

type Event struct {
	SchemaVersion string `json:"schema_version"`
	Sequence      int    `json:"sequence"`
	Type          string `json:"type"`
	Command       string `json:"command"`
	Timestamp     string `json:"timestamp"`
	Data          any    `json:"data"`
}

type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Message    string `json:"message,omitempty"`
	Output     string `json:"output,omitempty"`
}

type Finding struct {
	RuleID  string `json:"rule_id"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

func ParseFormat(value string) (Format, error) {
	format := Format(strings.ToLower(strings.TrimSpace(value)))
	if format == "" {
		return FormatText, nil
	}
	switch format {
	case FormatText, FormatJSON, FormatJSONL, FormatJUnit, FormatSARIF, FormatGitHub:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported output format %q", value)
	}
}

func ResolveFormat(jsonAlias bool, value string, allowed ...Format) (Format, error) {
	format, err := ParseFormat(value)
	if err != nil {
		return "", err
	}
	if jsonAlias {
		if format != FormatText && format != FormatJSON {
			return "", fmt.Errorf("--json conflicts with --format=%s", format)
		}
		format = FormatJSON
	}
	if len(allowed) == 0 {
		return format, nil
	}
	for _, candidate := range allowed {
		if format == candidate {
			return format, nil
		}
	}
	return "", fmt.Errorf("output format %q is not supported for this command", format)
}

func WriteJSONLEvent(writer io.Writer, sequence int, eventType, command string, timestamp time.Time, data any) error {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		Sequence:      sequence,
		Type:          strings.TrimSpace(eventType),
		Command:       strings.TrimSpace(command),
		Timestamp:     timestamp.UTC().Format(time.RFC3339Nano),
		Data:          data,
	}
	if err := ValidateEvent(event); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(event)
}

func ValidateEvent(event Event) error {
	if event.SchemaVersion != EventSchemaVersion {
		return fmt.Errorf("unsupported JSONL event schema_version %q", event.SchemaVersion)
	}
	if event.Sequence < 1 {
		return fmt.Errorf("JSONL sequence must be positive")
	}
	if strings.TrimSpace(event.Type) == "" || strings.TrimSpace(event.Command) == "" {
		return fmt.Errorf("JSONL event type and command are required")
	}
	if _, err := time.Parse(time.RFC3339Nano, event.Timestamp); err != nil {
		return fmt.Errorf("invalid JSONL event timestamp: %w", err)
	}
	return nil
}

func WriteJUnit(writer io.Writer, command string, checks []Check) error {
	suite := junitSuite{
		Name:  command,
		Tests: len(checks),
	}
	for _, check := range checks {
		testCase := junitCase{
			Name:      check.Name,
			ClassName: command,
			Time:      seconds(check.DurationMS),
		}
		switch strings.ToLower(check.Status) {
		case "ok", "passed", "success":
		case "skipped":
			suite.Skipped++
			testCase.Skipped = &junitSkipped{Message: firstNonEmpty(check.Message, "skipped")}
		default:
			suite.Failures++
			testCase.Failure = &junitFailure{
				Message: firstNonEmpty(check.Message, check.Status, "failed"),
				Body:    check.Output,
			}
		}
		if check.Output != "" && testCase.Failure == nil {
			testCase.SystemOut = check.Output
		}
		suite.Cases = append(suite.Cases, testCase)
	}
	document := junitSuites{
		Name:     "Vigil",
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Skipped:  suite.Skipped,
		Suites:   []junitSuite{suite},
	}
	data, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(writer, xml.Header); err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	_, err = io.WriteString(writer, "\n")
	return err
}

func WriteSARIF(writer io.Writer, toolName, toolVersion string, findings []Finding) error {
	ruleDescriptions := map[string]string{}
	for _, finding := range findings {
		ruleID := firstNonEmpty(strings.TrimSpace(finding.RuleID), "vigil.finding")
		if _, exists := ruleDescriptions[ruleID]; !exists {
			ruleDescriptions[ruleID] = firstNonEmpty(strings.TrimSpace(finding.Message), ruleID)
		}
	}
	ruleIDs := make([]string, 0, len(ruleDescriptions))
	for ruleID := range ruleDescriptions {
		ruleIDs = append(ruleIDs, ruleID)
	}
	sort.Strings(ruleIDs)
	rules := make([]sarifRule, 0, len(ruleIDs))
	for _, ruleID := range ruleIDs {
		rules = append(rules, sarifRule{
			ID: ruleID,
			ShortDescription: sarifMessage{
				Text: ruleDescriptions[ruleID],
			},
		})
	}
	results := make([]sarifResult, 0, len(findings))
	for _, finding := range findings {
		result := sarifResult{
			RuleID:  firstNonEmpty(strings.TrimSpace(finding.RuleID), "vigil.finding"),
			Level:   sarifLevel(finding.Level),
			Message: sarifMessage{Text: finding.Message},
		}
		if strings.TrimSpace(finding.Path) != "" {
			region := sarifRegion{}
			if finding.Line > 0 {
				region.StartLine = finding.Line
			}
			if finding.Column > 0 {
				region.StartColumn = finding.Column
			}
			result.Locations = []sarifLocation{{
				PhysicalLocation: sarifPhysicalLocation{
					ArtifactLocation: sarifArtifactLocation{URI: filepathURI(finding.Path)},
					Region:           region,
				},
			}}
		}
		results = append(results, result)
	}
	document := sarifLog{
		Version: "2.1.0",
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           firstNonEmpty(toolName, "Vigil"),
				Version:        toolVersion,
				InformationURI: "https://github.com/PayCal-Technologies/vigil-public",
				Rules:          rules,
			}},
			Results: results,
		}},
	}
	return WriteJSON(writer, document)
}

func WriteGitHubAnnotations(writer io.Writer, findings []Finding) error {
	for _, finding := range findings {
		level := "error"
		switch strings.ToLower(finding.Level) {
		case "note", "notice":
			level = "notice"
		case "warning", "warn":
			level = "warning"
		}
		var properties []string
		if finding.Path != "" {
			properties = append(properties, "file="+githubProperty(finding.Path))
		}
		if finding.Line > 0 {
			properties = append(properties, "line="+strconv.Itoa(finding.Line))
		}
		if finding.Column > 0 {
			properties = append(properties, "col="+strconv.Itoa(finding.Column))
		}
		if finding.RuleID != "" {
			properties = append(properties, "title="+githubProperty(finding.RuleID))
		}
		propertyText := ""
		if len(properties) > 0 {
			propertyText = " " + strings.Join(properties, ",")
		}
		if _, err := fmt.Fprintf(writer, "::%s%s::%s\n", level, propertyText, githubMessage(finding.Message)); err != nil {
			return err
		}
	}
	return nil
}

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

type sarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifMessage `json:"shortDescription"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
}

func seconds(milliseconds int64) string {
	return strconv.FormatFloat(float64(milliseconds)/1000, 'f', 3, 64)
}

func sarifLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "note", "warning", "error":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "error"
	}
}

func filepathURI(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

func githubMessage(value string) string {
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, "\r", "%0D")
	return strings.ReplaceAll(value, "\n", "%0A")
}

func githubProperty(value string) string {
	value = githubMessage(value)
	value = strings.ReplaceAll(value, ":", "%3A")
	return strings.ReplaceAll(value, ",", "%2C")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

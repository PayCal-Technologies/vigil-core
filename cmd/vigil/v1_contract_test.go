package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	vigilcli "github.com/PayCal-Technologies/vigil-public/internal/cli"
	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
)

type v1CommandContractDocument struct {
	SchemaVersion string              `json:"schema_version"`
	Commands      []v1CommandContract `json:"commands"`
}

type v1CommandContract struct {
	Name           string                `json:"name"`
	Aliases        []string              `json:"aliases"`
	Access         vigilcli.Access       `json:"access"`
	Capabilities   []vigilcli.Capability `json:"capabilities"`
	Args           string                `json:"args"`
	Flags          []v1FlagContract      `json:"flags"`
	Arguments      []v1ArgumentContract  `json:"arguments"`
	Source         string                `json:"source"`
	Pack           string                `json:"pack,omitempty"`
	Binding        string                `json:"binding"`
	Stability      vigilcli.Stability    `json:"stability"`
	HostAPIVersion string                `json:"host_api_version"`
	Timeout        string                `json:"timeout"`
	Network        string                `json:"network"`
	RequiredTools  []string              `json:"required_tools"`
	OutputFormats  []string              `json:"output_formats"`
	Interactive    bool                  `json:"interactive"`
	WriteFlags     []string              `json:"write_flags"`
	ReadOnlyFlags  []string              `json:"read_only_flags"`
	Usage          string                `json:"usage"`
	AutoEnabled    bool                  `json:"auto_enabled"`
}

type v1FlagContract struct {
	Long       string   `json:"long"`
	Short      string   `json:"short,omitempty"`
	ValueName  string   `json:"value_name,omitempty"`
	Values     []string `json:"values,omitempty"`
	File       bool     `json:"file,omitempty"`
	Repeatable bool     `json:"repeatable,omitempty"`
}

type v1ArgumentContract struct {
	Name       string   `json:"name"`
	Values     []string `json:"values,omitempty"`
	File       bool     `json:"file,omitempty"`
	Required   bool     `json:"required,omitempty"`
	Repeatable bool     `json:"repeatable,omitempty"`
}

func TestV1CommandContractGolden(t *testing.T) {
	packageDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(packageDirectory, "testdata", "v1-command-contract.json")

	empty := t.TempDir()
	t.Setenv("HOME", filepath.Join(empty, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(empty, "xdg"))
	t.Setenv("VIGIL_PLUGIN_ROOT", filepath.Join(empty, "plugins"))
	t.Setenv("VIGIL_USER_PACK_ROOT", filepath.Join(empty, "packs"))
	t.Chdir(empty)

	registry, err := newCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	document := v1CommandContractDocument{
		SchemaVersion: "1",
		Commands:      projectV1CommandContracts(registry.Commands()),
	}
	got, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	if os.Getenv("UPDATE_VIGIL_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read v1 command fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("embedded command contract differs from the reviewed v1 fixture; update it only with compatibility review")
	}
}

func TestV1StableCommandCollectionsEncodeAsArrays(t *testing.T) {
	if issues := commandCatalogIssues(); issues == nil {
		t.Fatal("command catalogue issues must be an array when empty")
	}

	var exitCode int
	encoded := captureStdout(t, func() {
		exitCode = findingsOutput(vigiloutput.FormatJSON, "v1-empty-findings", nil)
	})
	if exitCode != vigilcli.ExitSuccess {
		t.Fatalf("empty findings exit = %d", exitCode)
	}
	var data struct {
		Findings []string `json:"findings"`
	}
	decodeEnvelopeData(t, []byte(encoded), &data)
	if data.Findings == nil || len(data.Findings) != 0 {
		t.Fatalf("empty findings must encode as [], got %#v", data.Findings)
	}

	encoded = captureStdout(t, func() {
		exitCode = filesIterate([]string{"--root", t.TempDir(), "--json"})
	})
	if exitCode != vigilcli.ExitSuccess {
		t.Fatalf("empty file iteration exit = %d", exitCode)
	}
	var iteration struct {
		Files []json.RawMessage `json:"files"`
	}
	decodeEnvelopeData(t, []byte(encoded), &iteration)
	if iteration.Files == nil || len(iteration.Files) != 0 {
		t.Fatalf("empty file iteration must encode as [], got %#v", iteration.Files)
	}
}

func TestExternalCheckPreservesInternalFailureClass(t *testing.T) {
	result := externalCheckResult("bounded-output", runner.Result{
		State:     runner.StateOK,
		ExitCode:  0,
		Output:    "[truncated]",
		Truncated: true,
	})
	if result.ExitCode != vigilcli.ExitInternal || result.Status != "fail" {
		t.Fatalf("internal adapter failure was reclassified: %#v", result)
	}

	result = externalCheckResult("child-exit-seven", runner.Result{
		State:    runner.StateFailed,
		ExitCode: 7,
		Output:   "tool-specific failure",
	})
	if result.ExitCode != vigilcli.ExitCheckFailed {
		t.Fatalf("child exit 7 was treated as Vigil internal failure: %#v", result)
	}
}

func TestFindingInputFailurePreservesInternalExitClass(t *testing.T) {
	var exitCode int
	encoded := captureStdout(t, func() {
		exitCode = findingSourceFailure(vigiloutput.FormatJSON, "staged_sensitive", "Git output was truncated")
	})
	if exitCode != vigilcli.ExitInternal {
		t.Fatalf("input failure exit = %d", exitCode)
	}
	var data struct {
		Check string `json:"check"`
	}
	envelope := decodeEnvelopeData(t, []byte(encoded), &data)
	if envelope.ExitCode != vigilcli.ExitInternal || envelope.Status != "internal_error" {
		t.Fatalf("input failure envelope = %#v", envelope)
	}
	if data.Check != "staged_sensitive" || len(envelope.Errors) == 0 {
		t.Fatalf("input failure data = %#v, errors = %#v", data, envelope.Errors)
	}
}

func projectV1CommandContracts(commands []vigilcli.Command) []v1CommandContract {
	contracts := make([]v1CommandContract, 0, len(commands))
	for _, command := range commands {
		flags := make([]v1FlagContract, 0, len(command.Flags))
		for _, flag := range command.Flags {
			flags = append(flags, v1FlagContract{
				Long:       flag.Long,
				Short:      flag.Short,
				ValueName:  flag.ValueName,
				Values:     append([]string{}, flag.Values...),
				File:       flag.File,
				Repeatable: flag.Repeatable,
			})
		}
		arguments := make([]v1ArgumentContract, 0, len(command.Arguments))
		for _, argument := range command.Arguments {
			arguments = append(arguments, v1ArgumentContract{
				Name:       argument.Name,
				Values:     append([]string{}, argument.Values...),
				File:       argument.File,
				Required:   argument.Required,
				Repeatable: argument.Repeatable,
			})
		}
		contracts = append(contracts, v1CommandContract{
			Name:           command.Name,
			Aliases:        append([]string{}, command.Aliases...),
			Access:         command.Access,
			Capabilities:   append([]vigilcli.Capability{}, command.Capabilities...),
			Args:           command.Args,
			Flags:          flags,
			Arguments:      arguments,
			Source:         command.Source,
			Pack:           command.Pack,
			Binding:        command.Binding,
			Stability:      command.Stability,
			HostAPIVersion: command.HostAPIVersion,
			Timeout:        command.Timeout.String(),
			Network:        command.Network,
			RequiredTools:  append([]string{}, command.RequiredTools...),
			OutputFormats:  append([]string{}, command.OutputFormats...),
			Interactive:    command.Interactive,
			WriteFlags:     append([]string{}, command.WriteFlags...),
			ReadOnlyFlags:  append([]string{}, command.ReadOnlyFlags...),
			Usage:          command.Usage,
			AutoEnabled:    command.AutoEnabled,
		})
	}
	return contracts
}

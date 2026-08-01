package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func validFixtureCommand() Command {
	return Command{
		Name:           "fixture",
		Summary:        "Fixture command.",
		Handler:        func(Invocation) int { return 0 },
		Access:         AccessRead,
		Capabilities:   []Capability{CapabilityFilesystemRead},
		Args:           "[FILE]",
		Flags:          []Flag{{Long: "--format", Short: "-f", Description: "Output format.", ValueName: "FORMAT", Values: []string{"text", "json"}}},
		Arguments:      []Argument{{Name: "FILE", Description: "Input file.", File: true}},
		Source:         "core",
		Binding:        "builtin:fixture",
		Stability:      StabilityStable,
		HostAPIVersion: "v1",
		Timeout:        time.Second,
		Network:        "none",
		RequiredTools:  []string{},
		OutputFormats:  []string{"text", "json"},
		Usage:          "vigil fixture [FILE]",
	}
}

func TestRegistryRejectsIncompleteOrContradictoryExecutionMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Command)
		want   string
	}{
		{name: "binding", mutate: func(command *Command) { command.Binding = "plugin:fixture" }, want: "unsupported binding"},
		{name: "capability", mutate: func(command *Command) { command.Capabilities = append(command.Capabilities, Capability("unknown")) }, want: "unsupported capability"},
		{name: "network", mutate: func(command *Command) { command.Network = "optional" }, want: "network behavior and network capability disagree"},
		{name: "read with write capability", mutate: func(command *Command) {
			command.Capabilities = append(command.Capabilities, CapabilityFilesystemWrite)
		}, want: "read access declares a write capability"},
		{name: "interactive mismatch", mutate: func(command *Command) {
			command.Capabilities = append(command.Capabilities, CapabilityInteractive)
		}, want: "interactive field and capability disagree"},
		{name: "tools", mutate: func(command *Command) { command.RequiredTools = nil }, want: "missing required-tools declaration"},
		{name: "format", mutate: func(command *Command) { command.OutputFormats = []string{"yaml"} }, want: "unsupported output format"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := validFixtureCommand()
			test.mutate(&command)
			if _, err := New([]Command{command}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRegistryAcceptsDigestBoundPluginBinding(t *testing.T) {
	command := validFixtureCommand()
	command.Name = "example:fixture"
	command.Source = "plugin:example@1.2.3"
	command.Binding = "plugin:example@1.2.3#sha256:" + strings.Repeat("a", 64)
	if _, err := New([]Command{command}); err != nil {
		t.Fatal(err)
	}
	command.Binding = "plugin:example@1.2.3#sha256:" + strings.Repeat("z", 64)
	if _, err := New([]Command{command}); err == nil || !strings.Contains(err.Error(), "unsupported binding") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistryValidatesStructuredFlagsAndArguments(t *testing.T) {
	command := validFixtureCommand()
	registry, err := New([]Command{command})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := registry.Resolve("fixture")
	if !ok || resolved.Flags[0].Long != "--format" || resolved.Arguments[0].Name != "FILE" {
		t.Fatalf("resolved = %#v", resolved)
	}

	command.Flags = append(command.Flags, command.Flags[0])
	if _, err := New([]Command{command}); err == nil || !strings.Contains(err.Error(), "duplicate flag") {
		t.Fatalf("duplicate flag error = %v", err)
	}
}

func TestInvocationRetainsContext(t *testing.T) {
	command := validFixtureCommand()
	called := false
	command.Handler = func(inv Invocation) int {
		called = inv.Context == context.Background()
		return 0
	}
	registry, err := New([]Command{command})
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := registry.Resolve("fixture")
	resolved.Handler(Invocation{Context: context.Background()})
	if !called {
		t.Fatal("handler did not receive invocation context")
	}
}

package cli

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Access string

const (
	AccessRead             Access = "read"
	AccessWrite            Access = "write"
	AccessConditionalWrite Access = "conditional-write"
)

type Stability string

const (
	StabilityExperimental Stability = "experimental"
	StabilityStable       Stability = "stable"
	StabilityDeprecated   Stability = "deprecated"
)

type Capability string

const (
	CapabilityFilesystemRead  Capability = "filesystem:read"
	CapabilityFilesystemWrite Capability = "filesystem:write"
	CapabilityGitRead         Capability = "git:read"
	CapabilityGitWrite        Capability = "git:write"
	CapabilityNetwork         Capability = "network"
	CapabilityProcess         Capability = "process"
	CapabilityEnvironment     Capability = "environment"
	CapabilitySecrets         Capability = "secrets"
	CapabilityInteractive     Capability = "interactive"
)

type Invocation struct {
	Context       context.Context
	Command       string
	InvokedAs     string
	Args          []string
	ConfigPath    string
	AllowMutation bool
	Auto          bool
}

type Handler func(Invocation) int

type Flag struct {
	Long        string   `json:"long"`
	Short       string   `json:"short,omitempty"`
	Description string   `json:"description"`
	ValueName   string   `json:"value_name,omitempty"`
	Values      []string `json:"values,omitempty"`
	File        bool     `json:"file,omitempty"`
	Repeatable  bool     `json:"repeatable,omitempty"`
}

type Argument struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Values      []string `json:"values,omitempty"`
	File        bool     `json:"file,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Repeatable  bool     `json:"repeatable,omitempty"`
}

type Command struct {
	Name           string
	Aliases        []string
	Summary        string
	Handler        Handler
	Access         Access
	Capabilities   []Capability
	Args           string
	Flags          []Flag
	Arguments      []Argument
	Source         string
	Pack           string
	Binding        string
	Category       string
	Stability      Stability
	HostAPIVersion string
	Timeout        time.Duration
	Network        string
	RequiredTools  []string
	OutputFormats  []string
	Interactive    bool
	WriteFlags     []string
	ReadOnlyFlags  []string
	AutoEnabled    bool
	AutoReason     string
	Usage          string
	InstallHint    string
	Examples       []string
}

type Registry struct {
	commands []Command
	byName   map[string]int
}

var commandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(?::[a-z][a-z0-9-]*)*$`)
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
var pluginBindingPattern = regexp.MustCompile(`^plugin:[a-z][a-z0-9-]*@[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?#sha256:[0-9a-f]{64}$`)

func New(commands []Command) (*Registry, error) {
	registry := &Registry{
		commands: append([]Command(nil), commands...),
		byName:   make(map[string]int, len(commands)),
	}
	sort.SliceStable(registry.commands, func(i, j int) bool {
		return registry.commands[i].Name < registry.commands[j].Name
	})
	for i := range registry.commands {
		command := &registry.commands[i]
		if err := validateCommand(*command); err != nil {
			return nil, err
		}
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			if previous, exists := registry.byName[name]; exists {
				return nil, fmt.Errorf("command name %q is registered by both %q and %q", name, registry.commands[previous].Name, command.Name)
			}
			registry.byName[name] = i
		}
	}
	return registry, nil
}

func (r *Registry) Resolve(name string) (Command, bool) {
	index, ok := r.byName[name]
	if !ok {
		return Command{}, false
	}
	return r.commands[index], true
}

func (r *Registry) Commands() []Command {
	return append([]Command(nil), r.commands...)
}

func (c Command) RequiresMutation(args []string) bool {
	switch c.Access {
	case AccessRead:
		return false
	case AccessWrite:
		return !containsFlag(args, c.ReadOnlyFlags)
	case AccessConditionalWrite:
		return containsFlag(args, c.WriteFlags)
	default:
		return true
	}
}

func validateCommand(command Command) error {
	if !commandNamePattern.MatchString(command.Name) {
		return fmt.Errorf("command %q has an invalid canonical name", command.Name)
	}
	if strings.TrimSpace(command.Summary) == "" {
		return fmt.Errorf("command %q is missing a summary", command.Name)
	}
	if command.Handler == nil {
		return fmt.Errorf("command %q is missing a handler", command.Name)
	}
	switch command.Access {
	case AccessRead, AccessWrite, AccessConditionalWrite:
	default:
		return fmt.Errorf("command %q has unsupported access %q", command.Name, command.Access)
	}
	if command.Access == AccessConditionalWrite && len(command.WriteFlags) == 0 {
		return fmt.Errorf("conditional-write command %q is missing write flags", command.Name)
	}
	switch command.Stability {
	case StabilityExperimental, StabilityStable, StabilityDeprecated:
	default:
		return fmt.Errorf("command %q has unsupported stability %q", command.Name, command.Stability)
	}
	if strings.TrimSpace(command.Source) == "" {
		return fmt.Errorf("command %q is missing a source", command.Name)
	}
	if strings.TrimSpace(command.Binding) == "" {
		return fmt.Errorf("command %q is missing a binding", command.Name)
	}
	builtinBinding := command.Binding == "builtin:"+command.Name && command.Source != "" && !strings.HasPrefix(command.Source, "plugin:")
	pluginBinding := strings.HasPrefix(command.Source, "plugin:") && pluginBindingPattern.MatchString(command.Binding)
	if !builtinBinding && !pluginBinding {
		return fmt.Errorf("command %q has unsupported binding %q", command.Name, command.Binding)
	}
	if strings.TrimSpace(command.Args) == "" {
		return fmt.Errorf("command %q is missing an argument contract", command.Name)
	}
	if strings.TrimSpace(command.HostAPIVersion) == "" {
		return fmt.Errorf("command %q is missing a host API version", command.Name)
	}
	if command.Timeout <= 0 {
		return fmt.Errorf("command %q is missing a positive timeout", command.Name)
	}
	switch command.Network {
	case "none", "optional", "required":
	default:
		return fmt.Errorf("command %q has unsupported network behavior %q", command.Name, command.Network)
	}
	if len(command.Capabilities) == 0 {
		return fmt.Errorf("command %q is missing capabilities", command.Name)
	}
	allowedCapabilities := map[Capability]bool{
		CapabilityFilesystemRead: true, CapabilityFilesystemWrite: true,
		CapabilityGitRead: true, CapabilityGitWrite: true, CapabilityNetwork: true,
		CapabilityProcess: true, CapabilityEnvironment: true, CapabilitySecrets: true,
		CapabilityInteractive: true,
	}
	seenCapabilities := map[Capability]bool{}
	for _, capability := range command.Capabilities {
		if !allowedCapabilities[capability] {
			return fmt.Errorf("command %q has unsupported capability %q", command.Name, capability)
		}
		if seenCapabilities[capability] {
			return fmt.Errorf("command %q has duplicate capability %q", command.Name, capability)
		}
		seenCapabilities[capability] = true
	}
	hasNetwork := seenCapabilities[CapabilityNetwork]
	if (command.Network == "none") == hasNetwork {
		return fmt.Errorf("command %q network behavior and network capability disagree", command.Name)
	}
	if (command.Access == AccessWrite || command.Access == AccessConditionalWrite) &&
		!seenCapabilities[CapabilityFilesystemWrite] && !seenCapabilities[CapabilityGitWrite] {
		return fmt.Errorf("command %q mutating access is missing a write capability", command.Name)
	}
	if command.Access == AccessRead &&
		(seenCapabilities[CapabilityFilesystemWrite] || seenCapabilities[CapabilityGitWrite]) {
		return fmt.Errorf("command %q read access declares a write capability", command.Name)
	}
	if command.Interactive != seenCapabilities[CapabilityInteractive] {
		return fmt.Errorf("command %q interactive field and capability disagree", command.Name)
	}
	if command.RequiredTools == nil {
		return fmt.Errorf("command %q is missing required-tools declaration", command.Name)
	}
	seenTools := map[string]bool{}
	for _, tool := range command.RequiredTools {
		if !toolNamePattern.MatchString(tool) {
			return fmt.Errorf("command %q has invalid required tool %q", command.Name, tool)
		}
		if seenTools[tool] {
			return fmt.Errorf("command %q has duplicate required tool %q", command.Name, tool)
		}
		seenTools[tool] = true
	}
	if len(command.OutputFormats) == 0 {
		return fmt.Errorf("command %q is missing output formats", command.Name)
	}
	allowedFormats := map[string]bool{
		"text": true, "json": true, "jsonl": true, "junit": true,
		"sarif": true, "github": true, "shell": true, "roff": true,
	}
	seenFormats := map[string]bool{}
	for _, format := range command.OutputFormats {
		if !allowedFormats[format] {
			return fmt.Errorf("command %q has unsupported output format %q", command.Name, format)
		}
		if seenFormats[format] {
			return fmt.Errorf("command %q has duplicate output format %q", command.Name, format)
		}
		seenFormats[format] = true
	}
	for _, alias := range command.Aliases {
		if strings.TrimSpace(alias) == "" || strings.ContainsAny(alias, " \t\r\n") {
			return fmt.Errorf("command %q has invalid alias %q", command.Name, alias)
		}
	}
	for _, flag := range append(append([]string(nil), command.WriteFlags...), command.ReadOnlyFlags...) {
		if !strings.HasPrefix(flag, "--") {
			return fmt.Errorf("command %q flag %q must use --flag form", command.Name, flag)
		}
	}
	seenFlags := map[string]bool{}
	for _, flag := range command.Flags {
		if !strings.HasPrefix(flag.Long, "--") || strings.ContainsAny(flag.Long, " \t\r\n=") {
			return fmt.Errorf("command %q has invalid long flag %q", command.Name, flag.Long)
		}
		if seenFlags[flag.Long] {
			return fmt.Errorf("command %q has duplicate flag %q", command.Name, flag.Long)
		}
		seenFlags[flag.Long] = true
		if flag.Short != "" && (!strings.HasPrefix(flag.Short, "-") || strings.HasPrefix(flag.Short, "--") || len(flag.Short) != 2) {
			return fmt.Errorf("command %q has invalid short flag %q", command.Name, flag.Short)
		}
		if strings.TrimSpace(flag.Description) == "" {
			return fmt.Errorf("command %q flag %q is missing a description", command.Name, flag.Long)
		}
		if len(flag.Values) > 0 && strings.TrimSpace(flag.ValueName) == "" {
			return fmt.Errorf("command %q flag %q has values but no value name", command.Name, flag.Long)
		}
	}
	seenArguments := map[string]bool{}
	for _, argument := range command.Arguments {
		if strings.TrimSpace(argument.Name) == "" || strings.ContainsAny(argument.Name, " \t\r\n") {
			return fmt.Errorf("command %q has invalid positional argument %q", command.Name, argument.Name)
		}
		if seenArguments[argument.Name] {
			return fmt.Errorf("command %q has duplicate positional argument %q", command.Name, argument.Name)
		}
		seenArguments[argument.Name] = true
		if strings.TrimSpace(argument.Description) == "" {
			return fmt.Errorf("command %q argument %q is missing a description", command.Name, argument.Name)
		}
	}
	return nil
}

func containsFlag(args []string, flags []string) bool {
	for _, arg := range args {
		for _, candidate := range flags {
			if arg == candidate || strings.HasPrefix(arg, candidate+"=") {
				return true
			}
		}
	}
	return false
}

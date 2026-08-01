package plugins

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

const (
	ProtocolVersion    = "1"
	HandshakeSchema    = "1"
	LockSchemaVersion  = "1"
	TrustSchemaVersion = "1"
	HostAPIVersion     = "v1"
)

var (
	pluginIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	commandPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*(?::[a-z][a-z0-9-]*)+$`)
	semverPattern     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	diagnosticPattern = regexp.MustCompile(`^VIGIL_[EW]_[A-Z0-9_]+$`)
	requestIDPattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	toolPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
)

type Handshake struct {
	SchemaVersion   string   `json:"schema_version"`
	ProtocolVersion string   `json:"protocol_version"`
	Plugin          Metadata `json:"plugin"`
}

type Metadata struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Version        string    `json:"version"`
	Description    string    `json:"description"`
	HostAPIVersion string    `json:"host_api_version"`
	Commands       []Command `json:"commands"`
}

type Command struct {
	Name          string         `json:"name"`
	Aliases       []string       `json:"aliases"`
	Summary       string         `json:"summary"`
	Access        string         `json:"access"`
	Capabilities  []string       `json:"capabilities"`
	Args          string         `json:"args"`
	Flags         []cli.Flag     `json:"flags"`
	Arguments     []cli.Argument `json:"arguments"`
	Stability     string         `json:"stability"`
	Timeout       string         `json:"timeout"`
	Network       string         `json:"network"`
	RequiredTools []string       `json:"required_tools"`
	OutputFormats []string       `json:"output_formats"`
	Interactive   bool           `json:"interactive"`
	WriteFlags    []string       `json:"write_flags"`
	ReadOnlyFlags []string       `json:"read_only_flags"`
	Usage         string         `json:"usage"`
	Examples      []string       `json:"examples"`
}

func ValidateHandshake(handshake Handshake) error {
	if handshake.SchemaVersion != HandshakeSchema {
		return pluginError(ErrorInvalid, "validate handshake", "unsupported schema_version %q", handshake.SchemaVersion)
	}
	if handshake.ProtocolVersion != ProtocolVersion {
		return pluginError(ErrorInvalid, "validate handshake", "unsupported protocol_version %q", handshake.ProtocolVersion)
	}
	metadata := handshake.Plugin
	if !pluginIDPattern.MatchString(metadata.ID) {
		return pluginError(ErrorInvalid, "validate handshake", "plugin id must be lowercase kebab-case")
	}
	if strings.TrimSpace(metadata.Name) == "" || strings.TrimSpace(metadata.Description) == "" {
		return pluginError(ErrorInvalid, "validate handshake", "plugin name and description are required")
	}
	if !semverPattern.MatchString(metadata.Version) {
		return pluginError(ErrorInvalid, "validate handshake", "plugin version must be semantic version without a v prefix")
	}
	if metadata.HostAPIVersion != HostAPIVersion {
		return pluginError(ErrorInvalid, "validate handshake", "unsupported host_api_version %q", metadata.HostAPIVersion)
	}
	if len(metadata.Commands) == 0 {
		return pluginError(ErrorInvalid, "validate handshake", "plugin must contribute at least one command")
	}
	seen := map[string]bool{}
	for index, command := range metadata.Commands {
		if err := validateCommand(metadata.ID, command); err != nil {
			return pluginError(ErrorInvalid, "validate handshake", "commands[%d]: %v", index, err)
		}
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			if seen[name] {
				return pluginError(ErrorInvalid, "validate handshake", "duplicate command or alias %q", name)
			}
			seen[name] = true
		}
	}
	return nil
}

func validateCommand(pluginID string, command Command) error {
	if !commandPattern.MatchString(command.Name) || !strings.HasPrefix(command.Name, pluginID+":") {
		return fmt.Errorf("name %q must be namespaced as %s:<command>", command.Name, pluginID)
	}
	if strings.TrimSpace(command.Summary) == "" || strings.TrimSpace(command.Usage) == "" || strings.TrimSpace(command.Args) == "" {
		return fmt.Errorf("summary, usage, and args are required")
	}
	switch command.Access {
	case string(cli.AccessRead), string(cli.AccessWrite), string(cli.AccessConditionalWrite):
	default:
		return fmt.Errorf("unsupported access %q", command.Access)
	}
	switch command.Stability {
	case string(cli.StabilityExperimental), string(cli.StabilityStable), string(cli.StabilityDeprecated):
	default:
		return fmt.Errorf("unsupported stability %q", command.Stability)
	}
	timeout, err := time.ParseDuration(command.Timeout)
	if err != nil || timeout <= 0 || timeout > 30*time.Minute {
		return fmt.Errorf("timeout must be positive and no greater than 30m")
	}
	switch command.Network {
	case "none", "optional", "required":
	default:
		return fmt.Errorf("unsupported network behavior %q", command.Network)
	}
	capabilities, err := validateCapabilities(command.Capabilities)
	if err != nil {
		return err
	}
	if (command.Network == "none") == capabilities[string(cli.CapabilityNetwork)] {
		return fmt.Errorf("network behavior and network capability disagree")
	}
	if command.Interactive != capabilities[string(cli.CapabilityInteractive)] {
		return fmt.Errorf("interactive field and interactive capability disagree")
	}
	if (command.Access == string(cli.AccessWrite) || command.Access == string(cli.AccessConditionalWrite)) &&
		!capabilities[string(cli.CapabilityFilesystemWrite)] && !capabilities[string(cli.CapabilityGitWrite)] {
		return fmt.Errorf("mutating access requires a write capability")
	}
	if command.Access == string(cli.AccessRead) &&
		(capabilities[string(cli.CapabilityFilesystemWrite)] || capabilities[string(cli.CapabilityGitWrite)]) {
		return fmt.Errorf("read access cannot declare a write capability")
	}
	if command.RequiredTools == nil {
		return fmt.Errorf("required_tools must be an array")
	}
	if len(command.OutputFormats) == 0 {
		return fmt.Errorf("output_formats must be a non-empty array")
	}
	if command.Flags == nil || command.Arguments == nil || command.Aliases == nil || command.Examples == nil ||
		command.WriteFlags == nil || command.ReadOnlyFlags == nil {
		return fmt.Errorf("aliases, flags, arguments, write_flags, read_only_flags, and examples must be arrays")
	}
	if command.Access == string(cli.AccessConditionalWrite) && len(command.WriteFlags) == 0 {
		return fmt.Errorf("conditional-write command requires write_flags")
	}
	if command.Access == string(cli.AccessRead) && (len(command.WriteFlags) > 0 || len(command.ReadOnlyFlags) > 0) {
		return fmt.Errorf("read command cannot declare mutation flags")
	}
	declaredFlags := map[string]bool{}
	reservedFlags := map[string]bool{
		"--allow-mutation": true,
		"--auto":           true,
		"--config":         true,
		"--format":         true,
		"--help":           true,
		"--json":           true,
		"--version":        true,
	}
	for _, flag := range command.Flags {
		if reservedFlags[flag.Long] {
			return fmt.Errorf("flag %q is reserved by the Vigil host", flag.Long)
		}
		declaredFlags[flag.Long] = true
	}
	for _, flag := range append(append([]string{}, command.WriteFlags...), command.ReadOnlyFlags...) {
		if !declaredFlags[flag] {
			return fmt.Errorf("mutation flag %q is not declared in flags", flag)
		}
	}
	for _, alias := range command.Aliases {
		if !commandPattern.MatchString(alias) || !strings.HasPrefix(alias, pluginID+":") {
			return fmt.Errorf("alias %q must remain in plugin namespace", alias)
		}
	}
	if err := validateUniqueStrings("required_tools", command.RequiredTools, func(value string) bool {
		return toolPattern.MatchString(value)
	}); err != nil {
		return err
	}
	allowedFormats := map[string]bool{
		"text": true, "json": true, "jsonl": true, "junit": true, "sarif": true, "github": true,
	}
	if err := validateUniqueStrings("output_formats", command.OutputFormats, func(value string) bool {
		return allowedFormats[value]
	}); err != nil {
		return err
	}
	if err := validateFlags(command.Flags); err != nil {
		return err
	}
	if err := validateArguments(command.Arguments); err != nil {
		return err
	}
	return nil
}

func validateCapabilities(values []string) (map[string]bool, error) {
	allowed := map[string]bool{
		string(cli.CapabilityFilesystemRead): true, string(cli.CapabilityFilesystemWrite): true,
		string(cli.CapabilityGitRead): true, string(cli.CapabilityGitWrite): true,
		string(cli.CapabilityNetwork): true, string(cli.CapabilityProcess): true,
		string(cli.CapabilityEnvironment): true, string(cli.CapabilitySecrets): true,
		string(cli.CapabilityInteractive): true,
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("capabilities must be a non-empty array")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !allowed[value] {
			return nil, fmt.Errorf("unsupported capability %q", value)
		}
		if seen[value] {
			return nil, fmt.Errorf("duplicate capability %q", value)
		}
		seen[value] = true
	}
	return seen, nil
}

func validateUniqueStrings(field string, values []string, valid func(string) bool) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !valid(value) {
			return fmt.Errorf("%s contains invalid value %q", field, value)
		}
		if seen[value] {
			return fmt.Errorf("%s contains duplicate %q", field, value)
		}
		seen[value] = true
	}
	return nil
}

func validateFlags(flags []cli.Flag) error {
	seen := map[string]bool{}
	for _, flag := range flags {
		if !strings.HasPrefix(flag.Long, "--") || strings.ContainsAny(flag.Long, " \t\r\n=") || strings.TrimSpace(flag.Description) == "" {
			return fmt.Errorf("invalid flag contract %q", flag.Long)
		}
		if seen[flag.Long] {
			return fmt.Errorf("duplicate flag %q", flag.Long)
		}
		seen[flag.Long] = true
	}
	return nil
}

func validateArguments(arguments []cli.Argument) error {
	seen := map[string]bool{}
	for _, argument := range arguments {
		if strings.TrimSpace(argument.Name) == "" || strings.ContainsAny(argument.Name, " \t\r\n") || strings.TrimSpace(argument.Description) == "" {
			return fmt.Errorf("invalid argument contract %q", argument.Name)
		}
		if seen[argument.Name] {
			return fmt.Errorf("duplicate argument %q", argument.Name)
		}
		seen[argument.Name] = true
	}
	return nil
}

func MetadataDigest(metadata Metadata) (string, error) {
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}

func MetadataCapabilities(metadata Metadata) []string {
	set := map[string]bool{}
	for _, command := range metadata.Commands {
		for _, capability := range command.Capabilities {
			set[capability] = true
		}
	}
	values := make([]string, 0, len(set))
	for capability := range set {
		values = append(values, capability)
	}
	sort.Strings(values)
	return values
}

func MetadataCommandNames(metadata Metadata) []string {
	var names []string
	for _, command := range metadata.Commands {
		names = append(names, command.Name)
		names = append(names, command.Aliases...)
	}
	sort.Strings(names)
	return names
}

func validDigest(value string) bool {
	return digestPattern.MatchString(value)
}

func NormalizeDigest(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) == 64 {
		value = "sha256:" + value
	}
	if !validDigest(value) {
		return "", pluginError(ErrorInvalid, "parse digest", "expected sha256:<64 lowercase hex characters>")
	}
	return value, nil
}

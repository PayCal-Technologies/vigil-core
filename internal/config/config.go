package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/packs"
	"github.com/PayCal-Technologies/vigil-public/internal/plugins"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
)

const SchemaVersion = "3"

type Config struct {
	SchemaVersion            string            `json:"schema_version"`
	Profile                  string            `json:"profile"`
	Project                  string            `json:"project"`
	Coordination             Coordination      `json:"coordination"`
	Gates                    []Gate            `json:"gates"`
	Extensions               packs.Settings    `json:"extensions"`
	Plugins                  *plugins.Policy   `json:"plugins,omitempty"`
	PublicAssumptionPatterns []string          `json:"public_assumption_patterns,omitempty"`
	Metadata                 map[string]string `json:"metadata,omitempty"`
}

type Coordination struct {
	Mode                  string   `json:"mode"`
	AuthoritativeSurfaces []string `json:"authoritative_surfaces"`
	MutationRequires      []string `json:"mutation_requires"`
}

type LegacyAuthority struct {
	LocalFirst       bool     `json:"local_first"`
	MutationRequires []string `json:"mutation_requires"`
}

type Gate struct {
	Name            string            `json:"name"`
	Command         string            `json:"command"`
	Args            []string          `json:"args,omitempty"`
	Shell           bool              `json:"shell,omitempty"`
	ReadOnly        bool              `json:"read_only"`
	Tags            []string          `json:"tags,omitempty"`
	DependsOn       []string          `json:"depends_on,omitempty"`
	ParallelGroup   string            `json:"parallel_group,omitempty"`
	ContinueOnError bool              `json:"continue_on_error,omitempty"`
	Required        *bool             `json:"required,omitempty"`
	Retry           *GateRetry        `json:"retry,omitempty"`
	Timeout         string            `json:"timeout,omitempty"`
	CWD             string            `json:"cwd,omitempty"`
	Environment     map[string]string `json:"environment,omitempty"`
	Artifacts       []GateArtifact    `json:"artifacts,omitempty"`
}

type GateRetry struct {
	MaxAttempts int      `json:"max_attempts"`
	Delay       string   `json:"delay,omitempty"`
	On          []string `json:"on,omitempty"`
}

type GateArtifact struct {
	Path      string `json:"path"`
	Kind      string `json:"kind,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Required  *bool  `json:"required,omitempty"`
}

type Issue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Document struct {
	Config          Config
	Raw             map[string]json.RawMessage
	LegacyAuthority *LegacyAuthority
	SchemaVersion   string
}

func Template(profile, project string) Config {
	cfg := Config{
		SchemaVersion: SchemaVersion,
		Profile:       profile,
		Project:       project,
		Coordination: Coordination{
			Mode:                  "policy-aware-preflight",
			AuthoritativeSurfaces: []string{"GitHub", "reviewed repository configuration", "active workflow files"},
			MutationRequires:      []string{"explicit-confirmation", "clean-config"},
		},
		Gates: []Gate{
			{Name: "go test", Command: "go", Args: []string{"test", "./..."}, ReadOnly: true, Tags: []string{"test", "pre-push"}},
		},
		Extensions: packs.Settings{
			Enabled:        true,
			ManifestRoot:   "extensions",
			AllowedKinds:   []string{"custom"},
			RequirePrivate: false,
		},
		Plugins: pointerTo(plugins.DefaultPolicy()),
	}
	switch profile {
	case "generic":
		cfg.Gates = []Gate{{Name: "status", Command: "git", Args: []string{"status", "--short"}, ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	case "go-tool":
		cfg.Gates = []Gate{
			{Name: "go test", Command: "go", Args: []string{"test", "./..."}, ReadOnly: true, Tags: []string{"test", "pre-push"}},
			{Name: "go build", Command: "go", Args: []string{"build", "./..."}, ReadOnly: true, Tags: []string{"build", "pre-push"}},
		}
	case "static-site":
		cfg.Gates = []Gate{{Name: "workspace hygiene", Command: "vigil", Args: []string{"checks:workspace-hygiene"}, ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	case "js-app":
		cfg.Gates = []Gate{{Name: "npm test", Command: "npm", Args: []string{"test"}, ReadOnly: true, Tags: []string{"test", "pre-push"}}}
	case "php-app":
		cfg.Gates = []Gate{{Name: "php lint", Command: "find . -name '*.php' -print0 | xargs -0 -n1 php -l", Shell: true, ReadOnly: true, Tags: []string{"lint", "pre-commit"}}}
	case "native-app":
		cfg.Gates = []Gate{{Name: "native project status", Command: "git", Args: []string{"status", "--short"}, ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	case "mixed":
		cfg.Gates = []Gate{{Name: "workspace hygiene", Command: "vigil", Args: []string{"checks:workspace-hygiene"}, ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	case "custom":
		cfg.Gates = []Gate{{Name: "status", Command: "git", Args: []string{"status", "--short"}, ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	default:
		cfg.Profile = "generic"
		cfg.Gates = []Gate{{Name: "status", Command: "git", Args: []string{"status", "--short"}, ReadOnly: true, Tags: []string{"diagnostic", "pre-commit"}}}
	}
	return cfg
}

func ParseDocument(data []byte) (Document, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Document{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Document{}, err
	}
	var legacy *LegacyAuthority
	if authorityRaw, ok := raw["authority"]; ok {
		var parsed LegacyAuthority
		if err := json.Unmarshal(authorityRaw, &parsed); err == nil {
			legacy = &parsed
		}
	}
	if legacy != nil && len(cfg.Coordination.MutationRequires) == 0 {
		cfg.Coordination.MutationRequires = append([]string{}, legacy.MutationRequires...)
	}
	if legacy != nil && strings.TrimSpace(cfg.Coordination.Mode) == "" {
		if legacy.LocalFirst {
			cfg.Coordination.Mode = "github-adjacent-helper"
		} else {
			cfg.Coordination.Mode = "custom"
		}
	}
	before := strings.TrimSpace(cfg.SchemaVersion)
	if before == "" {
		before = "unknown"
	}
	return Document{
		Config:          cfg,
		Raw:             raw,
		LegacyAuthority: legacy,
		SchemaVersion:   before,
	}, nil
}

func MigrateData(data []byte) (Config, string, error) {
	doc, err := ParseDocument(data)
	if err != nil {
		return Config{}, "", err
	}
	return doc.Config, doc.SchemaVersion, nil
}

func MarshalDocument(raw map[string]json.RawMessage, cfg Config) ([]byte, error) {
	doc := map[string]json.RawMessage{}
	for key, value := range raw {
		doc[key] = value
	}
	setRaw := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		doc[key] = encoded
		return nil
	}
	if err := setRaw("schema_version", cfg.SchemaVersion); err != nil {
		return nil, err
	}
	if err := setRaw("profile", cfg.Profile); err != nil {
		return nil, err
	}
	if err := setRaw("project", cfg.Project); err != nil {
		return nil, err
	}
	if err := setRaw("coordination", mergeObjectRaw(raw["coordination"], map[string]any{
		"mode":                   cfg.Coordination.Mode,
		"authoritative_surfaces": cfg.Coordination.AuthoritativeSurfaces,
		"mutation_requires":      cfg.Coordination.MutationRequires,
	})); err != nil {
		return nil, err
	}
	if err := setRaw("gates", cfg.Gates); err != nil {
		return nil, err
	}
	if err := setRaw("extensions", mergeObjectRaw(raw["extensions"], map[string]any{
		"enabled":         cfg.Extensions.Enabled,
		"manifest_root":   cfg.Extensions.ManifestRoot,
		"allowed_kinds":   cfg.Extensions.AllowedKinds,
		"enabled_ids":     cfg.Extensions.EnabledIDs,
		"disabled_ids":    cfg.Extensions.DisabledIDs,
		"require_private": cfg.Extensions.RequirePrivate,
	})); err != nil {
		return nil, err
	}
	if len(cfg.PublicAssumptionPatterns) > 0 {
		if err := setRaw("public_assumption_patterns", cfg.PublicAssumptionPatterns); err != nil {
			return nil, err
		}
	}
	if len(cfg.Metadata) > 0 {
		if err := setRaw("metadata", cfg.Metadata); err != nil {
			return nil, err
		}
	}
	delete(doc, "authority")
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func ApplyDefaults(cfg Config, profile, project string) Config {
	defaults := Template(profile, project)
	sourceSchema := strings.TrimSpace(cfg.SchemaVersion)
	if cfg.SchemaVersion != SchemaVersion {
		cfg.SchemaVersion = SchemaVersion
	}
	if strings.TrimSpace(cfg.Profile) == "" {
		cfg.Profile = defaults.Profile
	}
	if strings.TrimSpace(cfg.Project) == "" {
		cfg.Project = defaults.Project
	}
	if strings.TrimSpace(cfg.Coordination.Mode) == "" {
		cfg.Coordination.Mode = defaults.Coordination.Mode
	}
	if len(cfg.Coordination.AuthoritativeSurfaces) == 0 {
		cfg.Coordination.AuthoritativeSurfaces = append([]string{}, defaults.Coordination.AuthoritativeSurfaces...)
	}
	if len(cfg.Coordination.MutationRequires) == 0 {
		cfg.Coordination.MutationRequires = append([]string{}, defaults.Coordination.MutationRequires...)
	}
	if len(cfg.Gates) == 0 {
		cfg.Gates = defaults.Gates
	}
	for i := range cfg.Gates {
		if strings.TrimSpace(cfg.Gates[i].Name) == "" {
			cfg.Gates[i].Name = "gate"
		}
		if strings.TrimSpace(cfg.Gates[i].Command) == "" {
			cfg.Gates[i].Command = "git"
			cfg.Gates[i].Args = []string{"status", "--short"}
		}
	}
	cfg.Gates = MigrateGateExecutions(sourceSchema, cfg.Gates)
	if cfg.Extensions.Enabled && strings.TrimSpace(cfg.Extensions.ManifestRoot) == "" {
		cfg.Extensions.ManifestRoot = defaults.Extensions.ManifestRoot
	}
	if len(cfg.Extensions.AllowedKinds) == 0 {
		cfg.Extensions.AllowedKinds = defaults.Extensions.AllowedKinds
	}
	if len(cfg.Extensions.EnabledIDs) > 0 && len(cfg.Extensions.DisabledIDs) > 0 {
		enabled := stringSet(cfg.Extensions.EnabledIDs)
		var disabled []string
		for _, id := range cfg.Extensions.DisabledIDs {
			if !enabled[id] {
				disabled = append(disabled, id)
			}
		}
		cfg.Extensions.DisabledIDs = disabled
	}
	if cfg.Plugins == nil {
		policy := plugins.DefaultPolicy()
		cfg.Plugins = &policy
	}
	cfg.PublicAssumptionPatterns = validPatternsOnly(cfg.PublicAssumptionPatterns)
	return cfg
}

func ApplyDocumentDefaults(doc Document, profile, project string) Config {
	cfg := ApplyDefaults(doc.Config, profile, project)
	defaults := Template(firstNonEmpty(profile, cfg.Profile, "generic"), project)
	if !rawObjectHasKey(doc.Raw["extensions"], "enabled") {
		cfg.Extensions.Enabled = defaults.Extensions.Enabled
		if cfg.Extensions.Enabled && strings.TrimSpace(cfg.Extensions.ManifestRoot) == "" {
			cfg.Extensions.ManifestRoot = defaults.Extensions.ManifestRoot
		}
	}
	return cfg
}

func MigrateGateExecutions(sourceSchema string, gates []Gate) []Gate {
	if sourceSchema != "" && sourceSchema != "unknown" && CompareSchemaVersion(sourceSchema, SchemaVersion) >= 0 {
		return gates
	}
	migrated := append([]Gate(nil), gates...)
	for i := range migrated {
		gate := &migrated[i]
		if gate.Shell || len(gate.Args) > 0 || strings.TrimSpace(gate.Command) == "" {
			continue
		}
		if runner.RequiresShell(gate.Command) {
			gate.Shell = true
			continue
		}
		parts, err := runner.ParseCommandLine(gate.Command)
		if err != nil {
			gate.Shell = true
			continue
		}
		gate.Command = parts[0]
		gate.Args = append([]string(nil), parts[1:]...)
	}
	return migrated
}

func (gate Gate) IsRequired() bool {
	return gate.Required == nil || *gate.Required
}

func (artifact GateArtifact) IsRequired() bool {
	return artifact.Required == nil || *artifact.Required
}

func Validate(cfg Config) error {
	issues := IssueMessages(ValidateIssues(cfg))
	if len(issues) > 0 {
		return errors.New(strings.Join(issues, "; "))
	}
	return nil
}

func ValidateIssues(cfg Config) []Issue {
	var issues []Issue
	add := func(field, code, message string) {
		issues = append(issues, Issue{Field: field, Code: code, Message: message})
	}
	if cfg.SchemaVersion != SchemaVersion {
		add("schema_version", "schema_version.invalid", "schema_version must be "+SchemaVersion)
	}
	if strings.TrimSpace(cfg.Profile) == "" {
		add("profile", "profile.required", "profile is required")
	}
	if strings.TrimSpace(cfg.Project) == "" {
		add("project", "project.required", "project is required")
	}
	if strings.TrimSpace(cfg.Coordination.Mode) == "" {
		add("coordination.mode", "coordination.mode.required", "coordination.mode is required")
	}
	if len(cfg.Coordination.AuthoritativeSurfaces) == 0 {
		add("coordination.authoritative_surfaces", "coordination.authoritative_surfaces.required", "coordination.authoritative_surfaces must name at least one authoritative surface")
	}
	if len(cfg.Coordination.MutationRequires) == 0 {
		add("coordination.mutation_requires", "coordination.mutation_requires.required", "coordination.mutation_requires must name at least one confirmation requirement")
	}
	if len(cfg.Gates) == 0 {
		add("gates", "gates.required", "gates must include at least one read-only diagnostic check")
	}
	issues = append(issues, GateIssues(cfg.Gates)...)
	if cfg.Extensions.Enabled {
		manifestRoot := strings.TrimSpace(cfg.Extensions.ManifestRoot)
		if manifestRoot == "" {
			add("extensions.manifest_root", "extensions.manifest_root.required", "extensions.manifest_root is required when extensions are enabled")
		} else if filepath.IsAbs(manifestRoot) || !packs.PathInside(".", filepath.Clean(manifestRoot)) {
			add("extensions.manifest_root", "extensions.manifest_root.escape", "extensions.manifest_root must stay inside the repository config directory")
		}
		if len(cfg.Extensions.AllowedKinds) == 0 {
			add("extensions.allowed_kinds", "extensions.allowed_kinds.required", "extensions.allowed_kinds must name at least one allowed pack kind")
		}
	}
	if cfg.Plugins != nil {
		if err := plugins.ValidatePolicy(*cfg.Plugins); err != nil {
			add("plugins", "plugins.policy.invalid", "plugins policy is invalid: "+err.Error())
		}
	}
	validateUniqueValues := func(field string, values []string) {
		seen := map[string]bool{}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				add(field, field+".empty", field+" cannot contain an empty value")
			} else if seen[value] {
				add(field, field+".duplicate", field+" contains duplicate value: "+value)
			}
			seen[value] = true
		}
	}
	validateUniqueValues("extensions.allowed_kinds", cfg.Extensions.AllowedKinds)
	validateUniqueValues("extensions.enabled_ids", cfg.Extensions.EnabledIDs)
	validateUniqueValues("extensions.disabled_ids", cfg.Extensions.DisabledIDs)
	enabled := stringSet(cfg.Extensions.EnabledIDs)
	for _, id := range cfg.Extensions.DisabledIDs {
		if enabled[id] {
			add("extensions.disabled_ids", "extensions.selection.conflict", "extension cannot be both enabled and disabled: "+id)
		}
	}
	for i, pattern := range cfg.PublicAssumptionPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			add(fmt.Sprintf("public_assumption_patterns[%d]", i), "public_assumption_patterns.invalid", fmt.Sprintf("public_assumption_patterns[%d] is invalid: %v", i, err))
		}
	}
	return issues
}

func GateIssues(gates []Gate) []Issue {
	var issues []Issue
	add := func(field, code, message string) {
		issues = append(issues, Issue{Field: field, Code: code, Message: message})
	}
	gateNames := make(map[string]int, len(gates))
	for i, gate := range gates {
		name := strings.TrimSpace(gate.Name)
		if strings.TrimSpace(gate.Name) == "" || strings.TrimSpace(gate.Command) == "" {
			add(fmt.Sprintf("gates[%d]", i), "gates.entry.invalid", fmt.Sprintf("gates[%d] requires name and command", i))
			continue
		}
		if gate.Name != name {
			add(fmt.Sprintf("gates[%d].name", i), "gates.name.normalized", fmt.Sprintf("gates[%d].name must not have leading or trailing whitespace", i))
		}
		if previous, exists := gateNames[name]; exists {
			add(fmt.Sprintf("gates[%d].name", i), "gates.name.duplicate", fmt.Sprintf("gates[%d].name duplicates gates[%d].name: %s", i, previous, name))
		} else {
			gateNames[name] = i
		}
		if gate.Shell && len(gate.Args) > 0 {
			add(fmt.Sprintf("gates[%d].args", i), "gates.shell.args", fmt.Sprintf("gates[%d] cannot declare args when shell is true", i))
		}
		if !gate.Shell {
			parts, err := runner.ParseCommandLine(gate.Command)
			if err != nil || len(parts) != 1 || parts[0] != gate.Command || runner.RequiresShell(gate.Command) {
				add(fmt.Sprintf("gates[%d].command", i), "gates.argv.command", fmt.Sprintf("gates[%d].command must be one executable; put arguments in args or set shell=true explicitly", i))
			}
		}
		if strings.TrimSpace(gate.Timeout) != "" {
			timeout, err := time.ParseDuration(gate.Timeout)
			if err != nil || timeout <= 0 {
				add(fmt.Sprintf("gates[%d].timeout", i), "gates.timeout.invalid", fmt.Sprintf("gates[%d].timeout must be a positive Go duration", i))
			}
		}
		for argIndex, arg := range gate.Args {
			if strings.ContainsRune(arg, '\x00') {
				add(fmt.Sprintf("gates[%d].args[%d]", i, argIndex), "gates.args.invalid", fmt.Sprintf("gates[%d].args[%d] contains a NUL byte", i, argIndex))
			}
		}
		validateGateValues(add, i, gate)
	}
	for i, gate := range gates {
		for dependencyIndex, dependency := range gate.DependsOn {
			dependency = strings.TrimSpace(dependency)
			if dependency == strings.TrimSpace(gate.Name) {
				add(fmt.Sprintf("gates[%d].depends_on[%d]", i, dependencyIndex), "gates.dependency.self", fmt.Sprintf("gates[%d] cannot depend on itself", i))
			} else if _, exists := gateNames[dependency]; !exists && dependency != "" {
				add(fmt.Sprintf("gates[%d].depends_on[%d]", i, dependencyIndex), "gates.dependency.unknown", fmt.Sprintf("gates[%d] depends on unknown gate %q", i, dependency))
			}
		}
	}
	if cycle := gateDependencyCycle(gates, gateNames); len(cycle) > 0 {
		add("gates", "gates.dependency.cycle", "gate dependency cycle: "+strings.Join(cycle, " -> "))
	}
	return issues
}

func pointerTo[T any](value T) *T {
	return &value
}

func IssueMessages(issues []Issue) []string {
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		messages = append(messages, issue.Message)
	}
	return messages
}

func CompareSchemaVersion(a, b string) int {
	if a == b {
		return 0
	}
	aInt, aErr := strconv.Atoi(strings.TrimSpace(a))
	bInt, bErr := strconv.Atoi(strings.TrimSpace(b))
	if aErr == nil && bErr == nil {
		switch {
		case aInt > bInt:
			return 1
		case aInt < bInt:
			return -1
		default:
			return 0
		}
	}
	if a > b {
		return 1
	}
	return -1
}

func mergeObjectRaw(original json.RawMessage, updates map[string]any) map[string]any {
	merged := map[string]any{}
	if len(original) > 0 {
		_ = json.Unmarshal(original, &merged)
	}
	for key, value := range updates {
		merged[key] = value
	}
	return merged
}

func rawObjectHasKey(raw json.RawMessage, key string) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	_, ok := object[key]
	return ok
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func validPatternsOnly(patterns []string) []string {
	var valid []string
	for _, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err == nil {
			valid = append(valid, pattern)
		}
	}
	return valid
}

var (
	gateGroupPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	environmentName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	artifactKindToken = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
)

func validateGateValues(add func(string, string, string), index int, gate Gate) {
	field := func(name string) string {
		return fmt.Sprintf("gates[%d].%s", index, name)
	}
	validateUniqueTrimmed := func(name, code string, values []string) {
		seen := map[string]bool{}
		for valueIndex, value := range values {
			trimmed := strings.TrimSpace(value)
			valueField := fmt.Sprintf("%s[%d]", field(name), valueIndex)
			switch {
			case trimmed == "":
				add(valueField, code+".empty", valueField+" cannot be empty")
			case trimmed != value:
				add(valueField, code+".normalized", valueField+" must not have leading or trailing whitespace")
			case seen[trimmed]:
				add(valueField, code+".duplicate", field(name)+" contains duplicate value: "+trimmed)
			}
			seen[trimmed] = true
		}
	}
	validateUniqueTrimmed("tags", "gates.tags", gate.Tags)
	validateUniqueTrimmed("depends_on", "gates.dependency", gate.DependsOn)

	group := strings.TrimSpace(gate.ParallelGroup)
	if gate.ParallelGroup != group {
		add(field("parallel_group"), "gates.parallel_group.normalized", field("parallel_group")+" must not have leading or trailing whitespace")
	}
	if group != "" && !gateGroupPattern.MatchString(group) {
		add(field("parallel_group"), "gates.parallel_group.invalid", field("parallel_group")+" must be a stable alphanumeric token")
	}
	if group != "" && !gate.ReadOnly {
		add(field("parallel_group"), "gates.parallel_group.mutation", field("parallel_group")+" is allowed only for read-only gates")
	}

	if gate.Retry != nil {
		retry := gate.Retry
		if retry.MaxAttempts < 2 || retry.MaxAttempts > 5 {
			add(field("retry.max_attempts"), "gates.retry.attempts", field("retry.max_attempts")+" must be between 2 and 5")
		}
		if !gate.ReadOnly {
			add(field("retry"), "gates.retry.mutation", field("retry")+" is allowed only for read-only gates")
		}
		if !containsExact(gate.Tags, "network") {
			add(field("retry"), "gates.retry.network", field("retry")+" requires the explicit network tag")
		}
		if strings.TrimSpace(retry.Delay) != "" {
			delay, err := time.ParseDuration(retry.Delay)
			if err != nil || delay < 0 || delay > 5*time.Minute {
				add(field("retry.delay"), "gates.retry.delay", field("retry.delay")+" must be a duration from 0s through 5m")
			}
		}
		validateUniqueTrimmed("retry.on", "gates.retry.on", retry.On)
		for valueIndex, state := range retry.On {
			if state != "failed" && state != "timed_out" {
				add(fmt.Sprintf("%s[%d]", field("retry.on"), valueIndex), "gates.retry.state", field("retry.on")+" may contain only failed or timed_out")
			}
		}
	}

	if cwd := strings.TrimSpace(gate.CWD); cwd != "" {
		if cwd != gate.CWD {
			add(field("cwd"), "gates.cwd.normalized", field("cwd")+" must not have leading or trailing whitespace")
		}
		if strings.ContainsRune(cwd, '\x00') || filepath.IsAbs(cwd) || !packs.PathInside(".", filepath.Clean(cwd)) {
			add(field("cwd"), "gates.cwd.escape", field("cwd")+" must stay inside the repository root")
		}
	}

	environmentKeys := make([]string, 0, len(gate.Environment))
	for key := range gate.Environment {
		environmentKeys = append(environmentKeys, key)
	}
	sort.Strings(environmentKeys)
	for _, key := range environmentKeys {
		value := gate.Environment[key]
		switch {
		case !environmentName.MatchString(key):
			add(field("environment."+key), "gates.environment.name", fmt.Sprintf("%s contains invalid environment variable name %q", field("environment"), key))
		case strings.HasPrefix(key, "VIGIL_"):
			add(field("environment."+key), "gates.environment.reserved", fmt.Sprintf("%s cannot override reserved variable %q", field("environment"), key))
		}
		if strings.ContainsRune(value, '\x00') {
			add(field("environment."+key), "gates.environment.value", fmt.Sprintf("%s[%q] contains a NUL byte", field("environment"), key))
		}
	}

	artifactPaths := map[string]bool{}
	for artifactIndex, artifact := range gate.Artifacts {
		artifactField := fmt.Sprintf("%s[%d]", field("artifacts"), artifactIndex)
		path := strings.TrimSpace(artifact.Path)
		if path == "" {
			add(artifactField+".path", "gates.artifact.path.required", artifactField+".path is required")
		} else {
			cleaned := filepath.Clean(path)
			if path != artifact.Path || strings.ContainsRune(path, '\x00') || filepath.IsAbs(path) || !packs.PathInside(".", cleaned) {
				add(artifactField+".path", "gates.artifact.path.escape", artifactField+".path must stay inside the repository root")
			}
			if artifactPaths[cleaned] {
				add(artifactField+".path", "gates.artifact.path.duplicate", field("artifacts")+" contains duplicate path: "+cleaned)
			}
			artifactPaths[cleaned] = true
		}
		if artifact.Kind != "" && !artifactKindToken.MatchString(artifact.Kind) {
			add(artifactField+".kind", "gates.artifact.kind", artifactField+".kind must be a lowercase token")
		}
		if strings.ContainsRune(artifact.MediaType, '\x00') {
			add(artifactField+".media_type", "gates.artifact.media_type", artifactField+".media_type contains a NUL byte")
		}
	}
}

func gateDependencyCycle(gates []Gate, names map[string]int) []string {
	state := make([]uint8, len(gates))
	stack := make([]int, 0, len(gates))
	stackPosition := map[int]int{}
	var visit func(int) []string
	visit = func(index int) []string {
		state[index] = 1
		stackPosition[index] = len(stack)
		stack = append(stack, index)
		for _, dependencyName := range gates[index].DependsOn {
			dependencyIndex, exists := names[strings.TrimSpace(dependencyName)]
			if !exists || dependencyIndex == index {
				continue
			}
			switch state[dependencyIndex] {
			case 0:
				if cycle := visit(dependencyIndex); len(cycle) > 0 {
					return cycle
				}
			case 1:
				start := stackPosition[dependencyIndex]
				cycle := make([]string, 0, len(stack)-start+1)
				for _, cycleIndex := range stack[start:] {
					cycle = append(cycle, strings.TrimSpace(gates[cycleIndex].Name))
				}
				return append(cycle, strings.TrimSpace(gates[dependencyIndex].Name))
			}
		}
		stack = stack[:len(stack)-1]
		delete(stackPosition, index)
		state[index] = 2
		return nil
	}
	for index := range gates {
		if state[index] == 0 {
			if cycle := visit(index); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

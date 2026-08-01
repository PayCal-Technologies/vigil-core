package packs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	officialpacks "github.com/PayCal-Technologies/vigil-public/extensions"
)

const SchemaVersion = "1"
const HostAPIVersion = "v1"

const (
	maxLayerEntries = 1024
	maxManifestSize = 1 << 20
)

var (
	commandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(?::[a-z][a-z0-9-]*)*$`)
	toolNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
)

type Settings struct {
	Enabled        bool     `json:"enabled"`
	ManifestRoot   string   `json:"manifest_root"`
	AllowedKinds   []string `json:"allowed_kinds"`
	EnabledIDs     []string `json:"enabled_ids,omitempty"`
	DisabledIDs    []string `json:"disabled_ids,omitempty"`
	RequirePrivate bool     `json:"require_private"`
}

type Manifest struct {
	SchemaVersion    string            `json:"schema_version"`
	HostAPIVersion   string            `json:"host_api_version"`
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Kind             string            `json:"kind"`
	Status           string            `json:"status"`
	Private          bool              `json:"private"`
	PublicCore       bool              `json:"public_core"`
	Description      string            `json:"description"`
	SourceRoot       string            `json:"source_root"`
	Packages         []string          `json:"packages"`
	Commands         []string          `json:"commands"`
	CommandContracts []CommandContract `json:"command_contracts,omitempty"`
	Path             string            `json:"path,omitempty"`
	Origin           string            `json:"origin,omitempty"`
}

type CommandContract struct {
	Command       string   `json:"command"`
	Access        string   `json:"access"`
	Capabilities  []string `json:"capabilities"`
	Binding       string   `json:"binding"`
	Timeout       string   `json:"timeout"`
	Stability     string   `json:"stability"`
	RequiredTools []string `json:"required_tools"`
	Network       string   `json:"network"`
	OutputFormats []string `json:"output_formats"`
	WriteFlags    []string `json:"write_flags,omitempty"`
	ReadOnlyFlags []string `json:"read_only_flags,omitempty"`
	Usage         string   `json:"usage"`
	Description   string   `json:"description"`
	Examples      []string `json:"examples,omitempty"`
	InstallHint   string   `json:"install_hint,omitempty"`
}

type Report struct {
	SchemaVersion string     `json:"schema_version"`
	Status        string     `json:"status"`
	Root          string     `json:"root"`
	Roots         []string   `json:"roots,omitempty"`
	Count         int        `json:"count"`
	Extensions    []Manifest `json:"extensions"`
	Overrides     []string   `json:"overrides,omitempty"`
	Issues        []string   `json:"issues,omitempty"`
}

type Options struct {
	RepositoryRoot     string
	RepositoryBoundary string
	UserRoot           string
	Settings           Settings
	OfficialFS         fs.FS
}

type layer struct {
	name       string
	root       string
	embeddedFS fs.FS
}

func Load(options Options) Report {
	report := Report{
		SchemaVersion: SchemaVersion,
		Status:        "ok",
		Root:          options.RepositoryRoot,
		Roots:         []string{"embedded:official"},
		Extensions:    []Manifest{},
	}
	if !options.Settings.Enabled {
		return report
	}
	if options.OfficialFS == nil {
		options.OfficialFS = officialpacks.OfficialManifests()
	}
	if strings.TrimSpace(options.UserRoot) == "" {
		options.UserRoot = UserRoot()
	}

	layers := []layer{{name: "embedded-official", root: "embedded:official", embeddedFS: options.OfficialFS}}
	if directoryExists(options.UserRoot) {
		layers = append(layers, layer{name: "user", root: options.UserRoot})
		report.Roots = append(report.Roots, options.UserRoot)
	}
	repositoryRoot, err := ConfineRepositoryRoot(options.RepositoryRoot, options.RepositoryBoundary)
	if err != nil {
		report.Issues = append(report.Issues, err.Error())
	} else if directoryExists(repositoryRoot) {
		layers = append(layers, layer{name: "repository", root: repositoryRoot})
		report.Roots = append(report.Roots, repositoryRoot)
	}

	enabledIDs := stringSet(options.Settings.EnabledIDs)
	disabledIDs := stringSet(options.Settings.DisabledIDs)
	selected := map[string]Manifest{}
	for _, currentLayer := range layers {
		manifests, issues := readLayer(currentLayer)
		report.Issues = append(report.Issues, issues...)
		seenInLayer := map[string]string{}
		for _, manifest := range manifests {
			if previousPath, duplicate := seenInLayer[manifest.ID]; duplicate {
				report.Issues = append(report.Issues, fmt.Sprintf("%s: duplicate pack id %q also declared by %s", manifest.Path, manifest.ID, previousPath))
				continue
			}
			seenInLayer[manifest.ID] = manifest.Path
			if len(enabledIDs) > 0 && !enabledIDs[manifest.ID] {
				continue
			}
			if disabledIDs[manifest.ID] {
				continue
			}
			issueCount := len(report.Issues)
			report.Issues = append(report.Issues, Validate(manifest)...)
			if !allowedByPolicy(manifest, options.Settings, &report) {
				continue
			}
			if len(report.Issues) > issueCount {
				continue
			}
			if previous, overridden := selected[manifest.ID]; overridden {
				report.Overrides = append(report.Overrides, fmt.Sprintf("%s: %s -> %s", manifest.ID, previous.Origin, manifest.Origin))
			}
			selected[manifest.ID] = manifest
		}
	}

	for _, manifest := range selected {
		report.Extensions = append(report.Extensions, manifest)
	}
	sort.Slice(report.Extensions, func(i, j int) bool {
		return report.Extensions[i].ID < report.Extensions[j].ID
	})
	commandOwners := map[string]string{}
	for _, manifest := range report.Extensions {
		for _, command := range manifest.Commands {
			if owner, duplicate := commandOwners[command]; duplicate {
				report.Issues = append(report.Issues, fmt.Sprintf("command %q is declared by both packs %q and %q", command, owner, manifest.ID))
				continue
			}
			commandOwners[command] = manifest.ID
		}
	}
	sort.Strings(report.Overrides)
	sort.Strings(report.Issues)
	report.Count = len(report.Extensions)
	if len(report.Issues) > 0 {
		report.Status = "fail"
	}
	return report
}

func UserRoot() string {
	if root := strings.TrimSpace(os.Getenv("VIGIL_USER_PACK_ROOT")); root != "" {
		return filepath.Clean(root)
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configRoot, "vigil", "packs")
}

func ConfineRepositoryRoot(root, boundary string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository pack root: %w", err)
	}
	if strings.TrimSpace(boundary) == "" {
		return "", fmt.Errorf("repository pack boundary is empty")
	}
	absoluteBoundary, err := filepath.Abs(boundary)
	if err != nil {
		return "", fmt.Errorf("resolve repository boundary: %w", err)
	}
	if !PathInside(absoluteBoundary, absoluteRoot) {
		return "", fmt.Errorf("repository pack root escapes repository boundary: %s", redactedPath(absoluteRoot))
	}
	if directoryExists(absoluteRoot) {
		resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
		if err != nil {
			return "", fmt.Errorf("resolve repository pack root symlinks: %w", err)
		}
		resolvedBoundary, err := filepath.EvalSymlinks(absoluteBoundary)
		if err != nil {
			return "", fmt.Errorf("resolve repository boundary symlinks: %w", err)
		}
		if !PathInside(resolvedBoundary, resolvedRoot) {
			return "", fmt.Errorf("repository pack root symlink escapes repository boundary: %s", redactedPath(absoluteRoot))
		}
	}
	return absoluteRoot, nil
}

func PathInside(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func FindRootUpward(start, name string) string {
	dir := filepath.Clean(start)
	for {
		candidate := filepath.Join(dir, name)
		if rootExists(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func Validate(manifest Manifest) []string {
	var issues []string
	required := []struct {
		name  string
		value string
	}{
		{name: "schema_version", value: manifest.SchemaVersion},
		{name: "host_api_version", value: manifest.HostAPIVersion},
		{name: "id", value: manifest.ID},
		{name: "name", value: manifest.Name},
		{name: "kind", value: manifest.Kind},
		{name: "status", value: manifest.Status},
		{name: "description", value: manifest.Description},
		{name: "source_root", value: manifest.SourceRoot},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			issues = append(issues, manifest.Path+": missing "+field.name)
		}
	}
	if manifest.SchemaVersion != "" && manifest.SchemaVersion != SchemaVersion {
		issues = append(issues, manifest.Path+": unsupported schema_version "+manifest.SchemaVersion)
	}
	if manifest.HostAPIVersion != "" && manifest.HostAPIVersion != HostAPIVersion {
		issues = append(issues, manifest.Path+": unsupported host_api_version "+manifest.HostAPIVersion)
	}
	if manifest.ID != "" && !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(manifest.ID) {
		issues = append(issues, manifest.Path+": id must be lowercase kebab-case")
	}
	if manifest.SourceRoot != "" && !validSourceRoot(manifest.SourceRoot) {
		issues = append(issues, manifest.Path+": source_root must be a safe relative slash-separated path")
	}
	commands := map[string]bool{}
	if len(manifest.Commands) == 0 {
		issues = append(issues, manifest.Path+": missing commands")
	}
	for i, command := range manifest.Commands {
		command = strings.TrimSpace(command)
		if command == "" {
			issues = append(issues, fmt.Sprintf("%s: commands[%d] is empty", manifest.Path, i))
			continue
		}
		if !commandNamePattern.MatchString(command) {
			issues = append(issues, fmt.Sprintf("%s: commands[%d] must be a canonical lowercase command name", manifest.Path, i))
		}
		if commands[command] {
			issues = append(issues, fmt.Sprintf("%s: duplicate command %q", manifest.Path, command))
			continue
		}
		commands[command] = true
	}
	contracts := map[string]bool{}
	for i, contract := range manifest.CommandContracts {
		prefix := fmt.Sprintf("%s: command_contracts[%d]", manifest.Path, i)
		if strings.TrimSpace(contract.Command) == "" {
			issues = append(issues, prefix+" missing command")
			continue
		}
		if !commandNamePattern.MatchString(contract.Command) {
			issues = append(issues, prefix+" command must be a canonical lowercase command name")
		}
		if contracts[contract.Command] {
			issues = append(issues, fmt.Sprintf("%s: duplicate command contract for %q", manifest.Path, contract.Command))
			continue
		}
		contracts[contract.Command] = true
		if !commands[contract.Command] {
			issues = append(issues, prefix+" command is not listed in commands")
		}
		if contract.Access != "read" && contract.Access != "write" && contract.Access != "conditional-write" {
			issues = append(issues, prefix+" unsupported access")
		}
		if contract.Access == "conditional-write" && len(contract.WriteFlags) == 0 {
			issues = append(issues, prefix+" conditional-write missing write_flags")
		}
		if contract.Access == "read" && (len(contract.WriteFlags) > 0 || len(contract.ReadOnlyFlags) > 0) {
			issues = append(issues, prefix+" read command cannot declare mutation flags")
		}
		if strings.TrimSpace(contract.Usage) == "" {
			issues = append(issues, prefix+" missing usage")
		}
		if strings.TrimSpace(contract.Description) == "" {
			issues = append(issues, prefix+" missing description")
		}
		if contract.Binding != "builtin:"+contract.Command {
			issues = append(issues, prefix+" binding must be builtin:"+contract.Command)
		}
		timeout, err := time.ParseDuration(contract.Timeout)
		if err != nil || timeout <= 0 {
			issues = append(issues, prefix+" timeout must be a positive Go duration")
		}
		switch contract.Stability {
		case "experimental", "stable", "deprecated":
		default:
			issues = append(issues, prefix+" unsupported stability")
		}
		switch contract.Network {
		case "none", "optional", "required":
		default:
			issues = append(issues, prefix+" unsupported network behavior")
		}
		capabilities, capabilityIssues := validateContractValues(
			prefix,
			"capabilities",
			contract.Capabilities,
			true,
			map[string]bool{
				"filesystem:read":  true,
				"filesystem:write": true,
				"git:read":         true,
				"git:write":        true,
				"network":          true,
				"process":          true,
				"environment":      true,
				"secrets":          true,
				"interactive":      true,
			},
		)
		issues = append(issues, capabilityIssues...)
		if contract.RequiredTools == nil {
			issues = append(issues, prefix+" missing required_tools")
		} else {
			seenTools := map[string]bool{}
			for _, tool := range contract.RequiredTools {
				if !toolNamePattern.MatchString(tool) {
					issues = append(issues, prefix+" required_tools contains invalid tool "+fmt.Sprintf("%q", tool))
				} else if seenTools[tool] {
					issues = append(issues, prefix+" required_tools contains duplicate "+fmt.Sprintf("%q", tool))
				}
				seenTools[tool] = true
			}
		}
		_, formatIssues := validateContractValues(
			prefix,
			"output_formats",
			contract.OutputFormats,
			true,
			map[string]bool{
				"text": true, "json": true, "jsonl": true, "junit": true,
				"sarif": true, "github": true,
			},
		)
		issues = append(issues, formatIssues...)
		hasNetwork := capabilities["network"]
		if (contract.Network == "none" || contract.Network == "optional" || contract.Network == "required") &&
			((contract.Network == "none") == hasNetwork) {
			issues = append(issues, prefix+" network behavior and network capability disagree")
		}
		if (contract.Access == "write" || contract.Access == "conditional-write") &&
			!capabilities["filesystem:write"] && !capabilities["git:write"] {
			issues = append(issues, prefix+" mutating access requires filesystem:write or git:write capability")
		}
		if contract.Access == "read" && (capabilities["filesystem:write"] || capabilities["git:write"]) {
			issues = append(issues, prefix+" read access cannot declare a write capability")
		}
		for _, flag := range append(append([]string(nil), contract.WriteFlags...), contract.ReadOnlyFlags...) {
			if !strings.HasPrefix(strings.TrimSpace(flag), "--") {
				issues = append(issues, prefix+" flags must use --flag form")
			}
		}
	}
	for command := range commands {
		if !contracts[command] {
			issues = append(issues, fmt.Sprintf("%s: command %q is missing a command contract", manifest.Path, command))
		}
	}
	return issues
}

func validSourceRoot(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	if filepath.IsAbs(value) || strings.Contains(value, "\\") || strings.Contains(value, "//") {
		return false
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return false
	}
	if strings.ContainsAny(value, "?#:%") {
		return false
	}
	if path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.TrimSpace(segment) != segment {
			return false
		}
	}
	return true
}

func validateContractValues(prefix, field string, values []string, requireNonEmpty bool, allowed map[string]bool) (map[string]bool, []string) {
	seen := map[string]bool{}
	var issues []string
	if values == nil || (requireNonEmpty && len(values) == 0) {
		return seen, []string{prefix + " missing " + field}
	}
	for _, value := range values {
		if strings.TrimSpace(value) != value || value == "" || !allowed[value] {
			issues = append(issues, prefix+" "+field+" contains unsupported value "+fmt.Sprintf("%q", value))
			continue
		}
		if seen[value] {
			issues = append(issues, prefix+" "+field+" contains duplicate "+fmt.Sprintf("%q", value))
			continue
		}
		seen[value] = true
	}
	return seen, issues
}

func readLayer(current layer) ([]Manifest, []string) {
	var (
		entries []fs.DirEntry
		err     error
	)
	if current.embeddedFS != nil {
		entries, err = fs.ReadDir(current.embeddedFS, ".")
	} else {
		entries, err = os.ReadDir(current.root)
	}
	if err != nil {
		return nil, []string{current.root + ": " + err.Error()}
	}
	if len(entries) > maxLayerEntries {
		return nil, []string{fmt.Sprintf("%s: pack layer exceeds %d entries", current.root, maxLayerEntries)}
	}
	var manifests []Manifest
	var issues []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		relativePath := filepath.ToSlash(filepath.Join(entry.Name(), "extension.json"))
		displayPath := filepath.Join(current.root, entry.Name(), "extension.json")
		var data []byte
		if current.embeddedFS != nil {
			data, err = fs.ReadFile(current.embeddedFS, relativePath)
			if err == nil && len(data) > maxManifestSize {
				err = fmt.Errorf("manifest exceeds %d bytes", maxManifestSize)
			}
		} else {
			if err = ensurePathInsideRoot(current.root, displayPath); err == nil {
				data, err = readBoundedManifest(displayPath)
			}
		}
		if err != nil {
			issues = append(issues, displayPath+": "+err.Error())
			continue
		}
		manifest, err := parseManifest(data)
		if err != nil {
			issues = append(issues, displayPath+": invalid JSON: "+err.Error())
			continue
		}
		manifest.Path = displayPath
		manifest.Origin = current.name
		manifests = append(manifests, manifest)
	}
	return manifests, issues
}

func parseManifest(data []byte) (Manifest, error) {
	if len(data) > maxManifestSize {
		return Manifest{}, fmt.Errorf("manifest exceeds %d bytes", maxManifestSize)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return Manifest{}, err
	}
	return manifest, nil
}

func readBoundedManifest(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestSize {
		return nil, fmt.Errorf("manifest exceeds %d bytes", maxManifestSize)
	}
	return data, nil
}

func allowedByPolicy(manifest Manifest, settings Settings, report *Report) bool {
	allowedKinds := stringSet(settings.AllowedKinds)
	if len(allowedKinds) > 0 && !allowedKinds[manifest.Kind] {
		report.Issues = append(report.Issues, fmt.Sprintf("%s: pack kind %q is blocked by extensions.allowed_kinds", manifest.Path, manifest.Kind))
		return false
	}
	if settings.RequirePrivate && !manifest.Private {
		report.Issues = append(report.Issues, fmt.Sprintf("%s: public pack is blocked by extensions.require_private", manifest.Path))
		return false
	}
	return true
}

func ensurePathInsideRoot(root, candidate string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return err
	}
	if !PathInside(resolvedRoot, resolvedCandidate) {
		return fmt.Errorf("manifest symlink escapes declared pack root")
	}
	return nil
}

func rootExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if info, err := os.Stat(filepath.Join(path, entry.Name(), "extension.json")); err == nil && !info.IsDir() {
				return true
			}
		}
	}
	return false
}

func directoryExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func redactedPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Base(path)
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

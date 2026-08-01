package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
	"github.com/PayCal-Technologies/vigil-public/internal/config"
)

const (
	SchemaVersion = "1"
	MaxFileBytes  = 8 << 20
)

type Inputs struct {
	BinaryDigest          string `json:"binary_digest"`
	ConfigPath            string `json:"config_path"`
	ConfigDigest          string `json:"config_digest"`
	RepositoryRoot        string `json:"repository_root"`
	RepositoryHead        string `json:"repository_head"`
	WorkspaceDigest       string `json:"workspace_digest"`
	CommandRegistryDigest string `json:"command_registry_digest"`
	PackDigest            string `json:"pack_digest"`
}

type Options struct {
	TagFilter      string `json:"tag_filter,omitempty"`
	DefaultTimeout string `json:"default_timeout"`
	MaxParallel    int    `json:"max_parallel,omitempty"`
}

type Document struct {
	SchemaVersion string        `json:"schema_version"`
	PlanID        string        `json:"plan_id,omitempty"`
	Command       string        `json:"command"`
	CreatedAt     string        `json:"created_at"`
	Inputs        Inputs        `json:"inputs"`
	Options       Options       `json:"options"`
	Gates         []config.Gate `json:"gates"`
}

type Mismatch struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func New(command string, createdAt time.Time, inputs Inputs, options Options, gates []config.Gate) (Document, error) {
	document := Document{
		SchemaVersion: SchemaVersion,
		Command:       strings.TrimSpace(command),
		CreatedAt:     createdAt.UTC().Format(time.RFC3339Nano),
		Inputs:        inputs,
		Options:       options,
		Gates:         cloneGates(gates),
	}
	planID, err := ID(document)
	if err != nil {
		return Document{}, err
	}
	document.PlanID = planID
	if err := Validate(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func Validate(document Document) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported plan schema_version %q", document.SchemaVersion)
	}
	if document.Command != "workflow:local" {
		return fmt.Errorf("unsupported planned command %q", document.Command)
	}
	if _, err := time.Parse(time.RFC3339Nano, document.CreatedAt); err != nil {
		return fmt.Errorf("invalid plan created_at: %w", err)
	}
	timeout, err := time.ParseDuration(document.Options.DefaultTimeout)
	if err != nil || timeout <= 0 {
		return fmt.Errorf("invalid plan default_timeout %q", document.Options.DefaultTimeout)
	}
	if document.Options.MaxParallel < 0 || document.Options.MaxParallel > 32 {
		return fmt.Errorf("invalid plan max_parallel %d", document.Options.MaxParallel)
	}
	requiredDigests := map[string]string{
		"binary_digest":           document.Inputs.BinaryDigest,
		"config_digest":           document.Inputs.ConfigDigest,
		"workspace_digest":        document.Inputs.WorkspaceDigest,
		"command_registry_digest": document.Inputs.CommandRegistryDigest,
		"pack_digest":             document.Inputs.PackDigest,
	}
	for field, digest := range requiredDigests {
		if !digestPattern.MatchString(digest) {
			return fmt.Errorf("invalid %s %q", field, digest)
		}
	}
	if strings.TrimSpace(document.Inputs.ConfigPath) == "" {
		return errors.New("plan config_path is required")
	}
	if strings.TrimSpace(document.Inputs.RepositoryRoot) == "" {
		return errors.New("plan repository_root is required")
	}
	if issues := config.GateIssues(document.Gates); len(issues) > 0 {
		return errors.New("invalid plan gates: " + strings.Join(config.IssueMessages(issues), "; "))
	}
	expectedID, err := ID(document)
	if err != nil {
		return err
	}
	if document.PlanID != expectedID {
		return fmt.Errorf("plan_id mismatch: expected %s", expectedID)
	}
	return nil
}

func ID(document Document) (string, error) {
	document.PlanID = ""
	data, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	return DigestBytes(data), nil
}

func Compare(expected, actual Inputs) []Mismatch {
	fields := []struct {
		name     string
		expected string
		actual   string
	}{
		{"binary_digest", expected.BinaryDigest, actual.BinaryDigest},
		{"config_path", expected.ConfigPath, actual.ConfigPath},
		{"config_digest", expected.ConfigDigest, actual.ConfigDigest},
		{"repository_root", expected.RepositoryRoot, actual.RepositoryRoot},
		{"repository_head", expected.RepositoryHead, actual.RepositoryHead},
		{"workspace_digest", expected.WorkspaceDigest, actual.WorkspaceDigest},
		{"command_registry_digest", expected.CommandRegistryDigest, actual.CommandRegistryDigest},
		{"pack_digest", expected.PackDigest, actual.PackDigest},
	}
	var mismatches []Mismatch
	for _, field := range fields {
		if field.expected != field.actual {
			mismatches = append(mismatches, Mismatch{
				Field:    field.name,
				Expected: field.expected,
				Actual:   field.actual,
			})
		}
	}
	sort.Slice(mismatches, func(i, j int) bool {
		return mismatches[i].Field < mismatches[j].Field
	})
	return mismatches
}

func Write(path string, document Document) error {
	if err := Validate(document); err != nil {
		return err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = atomicfile.Write(path, data, atomicfile.Options{
		DefaultMode:          0o600,
		PreserveExistingMode: false,
	})
	return err
}

func Read(path string) (Document, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Document{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Document{}, fmt.Errorf("plan file must not be a symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return Document{}, fmt.Errorf("plan path is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return Document{}, err
	}
	defer file.Close()
	limited := io.LimitReader(file, MaxFileBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Document{}, err
	}
	if len(data) > MaxFileBytes {
		return Document{}, fmt.Errorf("plan file exceeds %d bytes", MaxFileBytes)
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Document{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Document{}, err
	}
	if err := Validate(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func DigestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("digest path is not a regular file: %s", path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func DigestJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return DigestBytes(data), nil
}

func DigestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func ExecutableDigest() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	return DigestFile(resolved)
}

func cloneGates(gates []config.Gate) []config.Gate {
	cloned := make([]config.Gate, len(gates))
	for index, gate := range gates {
		cloned[index] = gate
		cloned[index].Args = append([]string(nil), gate.Args...)
		cloned[index].Tags = append([]string(nil), gate.Tags...)
		cloned[index].DependsOn = append([]string(nil), gate.DependsOn...)
		if gate.Required != nil {
			required := *gate.Required
			cloned[index].Required = &required
		}
		if gate.Retry != nil {
			retry := *gate.Retry
			retry.On = append([]string(nil), gate.Retry.On...)
			cloned[index].Retry = &retry
		}
		if gate.Environment != nil {
			cloned[index].Environment = make(map[string]string, len(gate.Environment))
			for key, value := range gate.Environment {
				cloned[index].Environment[key] = value
			}
		}
		cloned[index].Artifacts = append([]config.GateArtifact(nil), gate.Artifacts...)
		for artifactIndex, artifact := range gate.Artifacts {
			if artifact.Required != nil {
				required := *artifact.Required
				cloned[index].Artifacts[artifactIndex].Required = &required
			}
		}
	}
	return cloned
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("plan file contains multiple JSON values")
	}
	return err
}

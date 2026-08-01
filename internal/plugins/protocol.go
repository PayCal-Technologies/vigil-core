package plugins

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
	"github.com/PayCal-Technologies/vigil-public/internal/output"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
)

const (
	maxPluginBytes    = 256 * 1024 * 1024
	maxHandshakeBytes = 1024 * 1024
	maxResponseBytes  = 8 * 1024 * 1024
	handshakeTimeout  = 3 * time.Second
)

type InstalledPlugin struct {
	Path           string   `json:"path"`
	Digest         string   `json:"digest"`
	MetadataDigest string   `json:"metadata_digest"`
	Metadata       Metadata `json:"metadata"`
}

type Request struct {
	SchemaVersion   string         `json:"schema_version"`
	ProtocolVersion string         `json:"protocol_version"`
	RequestID       string         `json:"request_id"`
	Command         string         `json:"command"`
	Args            []string       `json:"args"`
	Context         RequestContext `json:"context"`
}

type RequestContext struct {
	RepositoryRoot string `json:"repository_root"`
	ConfigPath     string `json:"config_path,omitempty"`
	OutputFormat   string `json:"output_format"`
	AllowMutation  bool   `json:"allow_mutation"`
}

type Response struct {
	SchemaVersion   string              `json:"schema_version"`
	ProtocolVersion string              `json:"protocol_version"`
	RequestID       string              `json:"request_id"`
	ExitCode        int                 `json:"exit_code"`
	Output          string              `json:"output"`
	Data            json.RawMessage     `json:"data"`
	Warnings        []output.Diagnostic `json:"warnings"`
	Errors          []output.Diagnostic `json:"errors"`
	Artifacts       []output.Artifact   `json:"artifacts"`
}

type ExecuteOptions struct {
	RepositoryRoot string
	ConfigPath     string
	OutputFormat   string
	AllowMutation  bool
	RequestID      string
}

func HandshakeExecutable(ctx context.Context, path string) (Handshake, string, error) {
	resolved, err := validateExecutable(path)
	if err != nil {
		return Handshake{}, "", err
	}
	digest, err := FileDigest(resolved)
	if err != nil {
		return Handshake{}, "", wrapPluginError(ErrorInternal, "digest plugin", err)
	}
	return runHandshake(ctx, resolved, digest)
}

func handshakeExecutableWithDigest(ctx context.Context, path, digest string) (Handshake, string, error) {
	resolved, err := validateExecutable(path)
	if err != nil {
		return Handshake{}, "", err
	}
	if !digestPattern.MatchString(digest) {
		return Handshake{}, "", pluginError(ErrorInvalid, "digest plugin", "known executable digest is invalid")
	}
	return runHandshake(ctx, resolved, digest)
}

func runHandshake(ctx context.Context, resolved, digest string) (Handshake, string, error) {
	stdout := &limitBuffer{limit: maxHandshakeBytes}
	stderr := &limitBuffer{limit: 64 * 1024}
	result := runner.Run(ctx, runner.Spec{
		Name:         "plugin-handshake",
		Mode:         runner.ModeArgv,
		Executable:   resolved,
		Args:         []string{"handshake", "--protocol-version=" + ProtocolVersion},
		ClearEnv:     true,
		Env:          sanitizedEnvironment(),
		Timeout:      handshakeTimeout,
		Stdout:       stdout,
		Stderr:       stderr,
		CaptureLimit: 64 * 1024,
	})
	if err := pluginProcessError("plugin handshake", result, stderr.String()); err != nil {
		return Handshake{}, "", err
	}
	if stdout.truncated {
		return Handshake{}, "", pluginError(ErrorInvalid, "plugin handshake", "response exceeds %d bytes", maxHandshakeBytes)
	}
	if err := validateHandshakeDocumentShape(stdout.Bytes()); err != nil {
		return Handshake{}, "", wrapPluginError(ErrorInvalid, "decode plugin handshake", err)
	}
	var handshake Handshake
	if err := decodeStrictJSON(stdout.Bytes(), &handshake); err != nil {
		return Handshake{}, "", wrapPluginError(ErrorInvalid, "decode plugin handshake", err)
	}
	if err := ValidateHandshake(handshake); err != nil {
		return Handshake{}, "", err
	}
	return handshake, digest, nil
}

func Execute(ctx context.Context, plugin InstalledPlugin, command Command, args []string, options ExecuteOptions) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.RequestID == "" {
		requestID, err := newRequestID()
		if err != nil {
			return Response{}, wrapPluginError(ErrorInternal, "create plugin request", err)
		}
		options.RequestID = requestID
	}
	if !requestIDPattern.MatchString(options.RequestID) {
		return Response{}, pluginError(ErrorInvalid, "create plugin request", "request_id is invalid")
	}
	timeout, err := time.ParseDuration(command.Timeout)
	if err != nil || timeout <= 0 {
		return Response{}, pluginError(ErrorInvalid, "execute plugin", "invalid command timeout %q", command.Timeout)
	}
	request := Request{
		SchemaVersion:   ProtocolVersion,
		ProtocolVersion: ProtocolVersion,
		RequestID:       options.RequestID,
		Command:         command.Name,
		Args:            append([]string{}, args...),
		Context: RequestContext{
			RepositoryRoot: options.RepositoryRoot,
			ConfigPath:     options.ConfigPath,
			OutputFormat:   firstNonEmpty(options.OutputFormat, "text"),
			AllowMutation:  options.AllowMutation,
		},
	}
	input, err := json.Marshal(request)
	if err != nil {
		return Response{}, wrapPluginError(ErrorInternal, "encode plugin request", err)
	}
	input = append(input, '\n')
	stdout := &limitBuffer{limit: maxResponseBytes}
	stderr := &limitBuffer{limit: 1024 * 1024}
	result := runner.Run(ctx, runner.Spec{
		Name:         command.Name,
		Mode:         runner.ModeArgv,
		Executable:   plugin.Path,
		Args:         []string{"execute", "--protocol-version=" + ProtocolVersion, "--command=" + command.Name},
		Dir:          options.RepositoryRoot,
		ClearEnv:     true,
		Env:          sanitizedEnvironment(),
		Timeout:      timeout,
		Stdin:        bytes.NewReader(input),
		Stdout:       stdout,
		Stderr:       stderr,
		CaptureLimit: 64 * 1024,
	})
	if err := pluginProcessError("execute plugin", result, stderr.String()); err != nil {
		return Response{}, err
	}
	if stdout.truncated {
		return Response{}, pluginError(ErrorInvalid, "execute plugin", "response exceeds %d bytes", maxResponseBytes)
	}
	if err := requireJSONObjectFields(stdout.Bytes(), "plugin response",
		"schema_version", "protocol_version", "request_id", "exit_code", "output", "data", "warnings", "errors", "artifacts"); err != nil {
		return Response{}, wrapPluginError(ErrorInvalid, "decode plugin response", err)
	}
	var response Response
	if err := decodeStrictJSON(stdout.Bytes(), &response); err != nil {
		return Response{}, wrapPluginError(ErrorInvalid, "decode plugin response", err)
	}
	if err := validateResponse(response, request, options.RepositoryRoot); err != nil {
		return Response{}, err
	}
	if response.Data == nil {
		response.Data = json.RawMessage("null")
	}
	return response, nil
}

func validateResponse(response Response, request Request, repositoryRoot string) error {
	if response.SchemaVersion != ProtocolVersion || response.ProtocolVersion != ProtocolVersion {
		return pluginError(ErrorInvalid, "validate plugin response", "unsupported schema or protocol version")
	}
	if response.RequestID != request.RequestID {
		return pluginError(ErrorInvalid, "validate plugin response", "request_id mismatch")
	}
	if err := cli.ValidateExit(response.ExitCode); err != nil {
		return wrapPluginError(ErrorInvalid, "validate plugin response", err)
	}
	if response.Warnings == nil || response.Errors == nil || response.Artifacts == nil {
		return pluginError(ErrorInvalid, "validate plugin response", "warnings, errors, and artifacts must be arrays")
	}
	for _, diagnostic := range append(append([]output.Diagnostic{}, response.Warnings...), response.Errors...) {
		if !diagnosticPattern.MatchString(diagnostic.Code) || strings.TrimSpace(diagnostic.Message) == "" {
			return pluginError(ErrorInvalid, "validate plugin response", "invalid diagnostic")
		}
	}
	for _, artifact := range response.Artifacts {
		if err := validateArtifact(repositoryRoot, artifact); err != nil {
			return err
		}
	}
	return nil
}

func validateArtifact(root string, artifact output.Artifact) error {
	if strings.TrimSpace(artifact.Kind) == "" || strings.TrimSpace(artifact.Path) == "" {
		return pluginError(ErrorInvalid, "validate plugin artifact", "kind and path are required")
	}
	if artifact.Kind != strings.TrimSpace(artifact.Kind) || artifact.Path != strings.TrimSpace(artifact.Path) || strings.ContainsRune(artifact.Path, '\x00') {
		return pluginError(ErrorInvalid, "validate plugin artifact", "artifact kind and path must be normalized")
	}
	if filepath.IsAbs(artifact.Path) {
		return pluginError(ErrorBlocked, "validate plugin artifact", "artifact path must be repository-relative")
	}
	cleanedPath := filepath.Clean(artifact.Path)
	if cleanedPath != artifact.Path || cleanedPath == "." || cleanedPath == ".." || strings.HasPrefix(cleanedPath, ".."+string(filepath.Separator)) {
		return pluginError(ErrorBlocked, "validate plugin artifact", "artifact path escapes repository")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return wrapPluginError(ErrorInternal, "validate plugin artifact", err)
	}
	absolutePath, err := filepath.Abs(filepath.Join(absoluteRoot, cleanedPath))
	if err != nil {
		return wrapPluginError(ErrorInternal, "validate plugin artifact", err)
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return pluginError(ErrorBlocked, "validate plugin artifact", "artifact path escapes repository")
	}
	info, err := os.Lstat(absolutePath)
	if errors.Is(err, os.ErrNotExist) {
		return pluginError(ErrorInvalid, "validate plugin artifact", "artifact does not exist")
	}
	if err != nil {
		return wrapPluginError(ErrorInternal, "validate plugin artifact", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return pluginError(ErrorBlocked, "validate plugin artifact", "artifact must be a regular non-symlink file")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return wrapPluginError(ErrorInternal, "validate plugin artifact root", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return wrapPluginError(ErrorInternal, "validate plugin artifact path", err)
	}
	resolvedRelative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return pluginError(ErrorBlocked, "validate plugin artifact", "artifact resolves outside repository")
	}
	if artifact.Digest != "" && !validDigest(artifact.Digest) {
		return pluginError(ErrorInvalid, "validate plugin artifact", "invalid artifact digest")
	}
	if artifact.Digest != "" {
		digest, err := FileDigest(resolvedPath)
		if err != nil {
			return wrapPluginError(ErrorInternal, "digest plugin artifact", err)
		}
		if digest != artifact.Digest {
			return pluginError(ErrorBlocked, "validate plugin artifact", "artifact digest does not match")
		}
	}
	return nil
}

func validateExecutable(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", pluginError(ErrorInvalid, "validate plugin", "executable path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", wrapPluginError(ErrorInvalid, "validate plugin path", err)
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return "", pluginError(ErrorMissing, "validate plugin", "executable does not exist: %s", filepath.Base(path))
	}
	if err != nil {
		return "", wrapPluginError(ErrorInternal, "validate plugin", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", pluginError(ErrorBlocked, "validate plugin", "executable must be a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > maxPluginBytes {
		return "", pluginError(ErrorBlocked, "validate plugin", "executable size is outside the supported range")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "", pluginError(ErrorBlocked, "validate plugin", "executable must not be group- or world-writable")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", pluginError(ErrorBlocked, "validate plugin", "executable is not executable")
	}
	return absolute, nil
}

func pluginProcessError(operation string, result runner.Result, stderr string) error {
	detail := strings.TrimSpace(firstNonEmpty(stderr, result.Error, result.Output))
	switch result.State {
	case runner.StateOK:
		return nil
	case runner.StateToolMissing:
		return pluginError(ErrorMissing, operation, "%s", firstNonEmpty(detail, "plugin executable is missing"))
	case runner.StateCancelled, runner.StateTimedOut:
		return pluginError(ErrorInterrupted, operation, "%s", firstNonEmpty(detail, string(result.State)))
	case runner.StateFailed:
		return pluginError(ErrorInvalid, operation, "process exited unsuccessfully: %s", firstNonEmpty(detail, "no diagnostic"))
	default:
		return pluginError(ErrorInternal, operation, "%s", firstNonEmpty(detail, "process failed"))
	}
}

func sanitizedEnvironment() []string {
	values := []string{
		"HOME=",
		"LANG=C",
		"LC_ALL=C",
		"VIGIL_PLUGIN_PROTOCOL=" + ProtocolVersion,
	}
	if path := os.Getenv("PATH"); path != "" {
		values = append(values, "PATH="+path)
	}
	if temporary := os.Getenv("TMPDIR"); temporary != "" {
		values = append(values, "TMPDIR="+temporary)
	}
	return values
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateHandshakeDocumentShape(data []byte) error {
	document, err := decodeJSONObject(data, "plugin handshake")
	if err != nil {
		return err
	}
	if err := requireFields(document, "plugin handshake", "schema_version", "protocol_version", "plugin"); err != nil {
		return err
	}
	metadata, err := decodeJSONObject(document["plugin"], "plugin metadata")
	if err != nil {
		return err
	}
	if err := requireFields(metadata, "plugin metadata", "id", "name", "version", "description", "host_api_version", "commands"); err != nil {
		return err
	}
	var commands []json.RawMessage
	if err := json.Unmarshal(metadata["commands"], &commands); err != nil {
		return fmt.Errorf("plugin metadata commands must be an array: %w", err)
	}
	for index, encoded := range commands {
		command, err := decodeJSONObject(encoded, fmt.Sprintf("plugin command %d", index))
		if err != nil {
			return err
		}
		if err := requireFields(command, fmt.Sprintf("plugin command %d", index),
			"name", "aliases", "summary", "access", "capabilities", "args", "flags", "arguments",
			"stability", "timeout", "network", "required_tools", "output_formats", "interactive",
			"write_flags", "read_only_flags", "usage", "examples"); err != nil {
			return err
		}
	}
	return nil
}

func requireJSONObjectFields(data []byte, context string, fields ...string) error {
	document, err := decodeJSONObject(data, context)
	if err != nil {
		return err
	}
	return requireFields(document, context, fields...)
}

func decodeJSONObject(data []byte, context string) (map[string]json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("%s must be an object", context)
	}
	return document, nil
}

func requireFields(document map[string]json.RawMessage, context string, fields ...string) error {
	for _, field := range fields {
		if _, ok := document[field]; !ok {
			return fmt.Errorf("%s is missing required field %q", context, field)
		}
	}
	return nil
}

func newRequestID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type limitBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *limitBuffer) Bytes() []byte {
	return append([]byte{}, b.buffer.Bytes()...)
}

func (b *limitBuffer) String() string {
	return b.buffer.String()
}

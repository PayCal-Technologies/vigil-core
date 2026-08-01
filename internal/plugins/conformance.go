package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

const ConformanceSchemaVersion = "1"

const defaultConformanceCommandTimeout = 10 * time.Second

type ConformanceOptions struct {
	ExecuteCommands bool
	CommandTimeout  time.Duration
	Policy          *Policy
}

type ConformanceCheck struct {
	Name             string `json:"name"`
	Command          string `json:"command,omitempty"`
	Status           string `json:"status"`
	Code             string `json:"code"`
	Detail           string `json:"detail"`
	ResponseExitCode *int   `json:"response_exit_code,omitempty"`
}

type ConformanceReport struct {
	SchemaVersion    string             `json:"schema_version"`
	Status           string             `json:"status"`
	Candidate        string             `json:"candidate"`
	ExecutableDigest string             `json:"executable_digest,omitempty"`
	MetadataDigest   string             `json:"metadata_digest,omitempty"`
	Plugin           *Metadata          `json:"plugin,omitempty"`
	ExecuteCommands  bool               `json:"execute_commands"`
	Checks           []ConformanceCheck `json:"checks"`
	Issues           []Issue            `json:"issues"`
}

func Conform(ctx context.Context, candidate string, options ConformanceOptions) ConformanceReport {
	if ctx == nil {
		ctx = context.Background()
	}
	report := ConformanceReport{
		SchemaVersion:   ConformanceSchemaVersion,
		Status:          "ok",
		Candidate:       candidate,
		ExecuteCommands: options.ExecuteCommands,
		Checks:          []ConformanceCheck{},
		Issues:          []Issue{},
	}
	policy, err := NormalizePolicy(options.Policy)
	if err != nil {
		report.fail("policy:validate", "", "VIGIL_PLUGIN_CONFORMANCE_POLICY_INVALID",
			wrapPluginError(ErrorInvalid, "validate plugin policy", err))
		return report
	}
	if err := CheckAcquisitionPolicy(policy, "local"); err != nil {
		report.fail("policy:acquisition", "", "VIGIL_PLUGIN_CONFORMANCE_POLICY_BLOCKED", err)
		return report
	}
	report.pass("policy:acquisition", "", "local candidate execution is permitted")

	handshake, digest, err := HandshakeExecutable(ctx, candidate)
	if err != nil {
		report.fail("handshake", "", "VIGIL_PLUGIN_CONFORMANCE_HANDSHAKE", err)
		return report
	}
	absoluteCandidate, err := filepath.Abs(candidate)
	if err != nil {
		report.fail("handshake", "", "VIGIL_PLUGIN_CONFORMANCE_PATH",
			wrapPluginError(ErrorInternal, "resolve plugin candidate", err))
		return report
	}
	metadataDigest, err := MetadataDigest(handshake.Plugin)
	if err != nil {
		report.fail("handshake", "", "VIGIL_PLUGIN_CONFORMANCE_METADATA_DIGEST",
			wrapPluginError(ErrorInternal, "digest plugin metadata", err))
		return report
	}
	report.Candidate = absoluteCandidate
	report.ExecutableDigest = digest
	report.MetadataDigest = metadataDigest
	report.Plugin = &handshake.Plugin
	report.pass("handshake", "", fmt.Sprintf(
		"protocol=%s host_api=%s commands=%d",
		handshake.ProtocolVersion,
		handshake.Plugin.HostAPIVersion,
		len(handshake.Plugin.Commands),
	))

	repeated, repeatedDigest, err := HandshakeExecutable(ctx, absoluteCandidate)
	if err != nil {
		report.fail("handshake:deterministic", "", "VIGIL_PLUGIN_CONFORMANCE_HANDSHAKE_REPEAT", err)
		return report
	}
	repeatedMetadataDigest, err := MetadataDigest(repeated.Plugin)
	if err != nil {
		report.fail("handshake:deterministic", "", "VIGIL_PLUGIN_CONFORMANCE_METADATA_DIGEST",
			wrapPluginError(ErrorInternal, "digest repeated plugin metadata", err))
		return report
	}
	if repeatedDigest != digest || repeatedMetadataDigest != metadataDigest {
		report.fail(
			"handshake:deterministic",
			"",
			"VIGIL_PLUGIN_CONFORMANCE_NONDETERMINISTIC",
			pluginError(ErrorBlocked, "verify plugin handshake", "executable or metadata digest changed across handshakes"),
		)
		return report
	}
	report.pass("handshake:deterministic", "", "executable and metadata digests are stable")

	if err := CheckPolicy(
		policy,
		handshake.Plugin.ID,
		MetadataCapabilities(handshake.Plugin),
		"local",
		nil,
		0,
	); err != nil {
		report.fail("policy:metadata", "", "VIGIL_PLUGIN_CONFORMANCE_POLICY_BLOCKED", err)
		return report
	}
	report.pass("policy:metadata", "", "plugin identity and capabilities satisfy repository policy")

	if !options.ExecuteCommands {
		return report
	}
	commandTimeout := options.CommandTimeout
	if commandTimeout == 0 {
		commandTimeout = defaultConformanceCommandTimeout
	}
	if commandTimeout < time.Millisecond || commandTimeout > time.Minute {
		report.fail(
			"execute:configuration",
			"",
			"VIGIL_PLUGIN_CONFORMANCE_TIMEOUT_INVALID",
			pluginError(ErrorInvalid, "configure conformance", "command timeout must be between 1ms and 1m"),
		)
		return report
	}
	repositoryRoot, err := os.MkdirTemp("", "vigil-plugin-conformance-")
	if err != nil {
		report.fail("execute:setup", "", "VIGIL_PLUGIN_CONFORMANCE_SETUP",
			wrapPluginError(ErrorInternal, "create conformance repository", err))
		return report
	}
	defer os.RemoveAll(repositoryRoot)

	installed := InstalledPlugin{
		Path:           absoluteCandidate,
		Digest:         digest,
		MetadataDigest: metadataDigest,
		Metadata:       handshake.Plugin,
	}
	for index, command := range handshake.Plugin.Commands {
		if ctx.Err() != nil {
			report.fail(
				"execute",
				command.Name,
				"VIGIL_PLUGIN_CONFORMANCE_INTERRUPTED",
				wrapPluginError(ErrorInterrupted, "run conformance", ctx.Err()),
			)
			break
		}
		currentDigest, digestErr := FileDigest(absoluteCandidate)
		if digestErr != nil {
			report.fail("execute", command.Name, "VIGIL_PLUGIN_CONFORMANCE_DIGEST",
				wrapPluginError(ErrorInternal, "digest plugin before conformance execution", digestErr))
			break
		}
		if currentDigest != digest {
			report.fail(
				"execute",
				command.Name,
				"VIGIL_PLUGIN_CONFORMANCE_DIGEST_DRIFT",
				pluginError(ErrorBlocked, "run conformance", "candidate digest changed before command execution"),
			)
			break
		}
		outputFormat := command.OutputFormats[0]
		if slices.Contains(command.OutputFormats, "json") {
			outputFormat = "json"
		}
		commandContext, cancel := context.WithTimeout(ctx, commandTimeout)
		response, executeErr := Execute(commandContext, installed, command, []string{}, ExecuteOptions{
			RepositoryRoot: repositoryRoot,
			OutputFormat:   outputFormat,
			AllowMutation:  false,
			RequestID:      fmt.Sprintf("conformance-%03d", index+1),
		})
		cancel()
		if executeErr != nil {
			report.fail("execute", command.Name, "VIGIL_PLUGIN_CONFORMANCE_RESPONSE", executeErr)
			continue
		}
		currentDigest, digestErr = FileDigest(absoluteCandidate)
		if digestErr != nil {
			report.fail("execute", command.Name, "VIGIL_PLUGIN_CONFORMANCE_DIGEST",
				wrapPluginError(ErrorInternal, "digest plugin after conformance execution", digestErr))
			continue
		}
		if currentDigest != digest {
			report.fail(
				"execute",
				command.Name,
				"VIGIL_PLUGIN_CONFORMANCE_DIGEST_DRIFT",
				pluginError(ErrorBlocked, "run conformance", "candidate digest changed during command execution"),
			)
			continue
		}
		exitCode := response.ExitCode
		report.Checks = append(report.Checks, ConformanceCheck{
			Name:             "execute",
			Command:          command.Name,
			Status:           "ok",
			Code:             "VIGIL_PLUGIN_CONFORMANCE_RESPONSE_VALID",
			Detail:           fmt.Sprintf("valid protocol response using %s output", outputFormat),
			ResponseExitCode: &exitCode,
		})
	}
	return report
}

func ConformanceExit(report ConformanceReport) int {
	exitCode := cli.ExitSuccess
	for _, issue := range report.Issues {
		classified := cli.ClassifyExit(issue.ExitCode).Code
		if classified > exitCode {
			exitCode = classified
		}
	}
	return exitCode
}

func (report *ConformanceReport) pass(name, command, detail string) {
	report.Checks = append(report.Checks, ConformanceCheck{
		Name:    name,
		Command: command,
		Status:  "ok",
		Code:    "VIGIL_PLUGIN_CONFORMANCE_OK",
		Detail:  detail,
	})
}

func (report *ConformanceReport) fail(name, command, code string, err error) {
	report.Status = "fail"
	report.Checks = append(report.Checks, ConformanceCheck{
		Name:    name,
		Command: command,
		Status:  "fail",
		Code:    code,
		Detail:  err.Error(),
	})
	pluginID := ""
	if report.Plugin != nil {
		pluginID = report.Plugin.ID
	}
	report.Issues = append(report.Issues, Issue{
		PluginID: pluginID,
		Code:     code,
		Message:  err.Error(),
		ExitCode: ExitCode(err),
	})
}

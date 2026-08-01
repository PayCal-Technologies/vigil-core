package plugins

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
	"github.com/PayCal-Technologies/vigil-public/internal/output"
)

func TestExternalReferencePluginPassesConformance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reference plugin is a POSIX shell executable")
	}
	candidate := filepath.Join("..", "..", "examples", "plugins", "reference", "vigil-plugin-reference")
	report := Conform(context.Background(), candidate, ConformanceOptions{ExecuteCommands: true})
	if exitCode := ConformanceExit(report); exitCode != 0 {
		t.Fatalf("conformance exit=%d report=%#v", exitCode, report)
	}
	if report.Status != "ok" || report.Plugin == nil || report.Plugin.ID != "reference" {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Checks) != 5 {
		t.Fatalf("checks = %#v", report.Checks)
	}
	executeCheck := report.Checks[len(report.Checks)-1]
	if executeCheck.Command != "reference:echo" || executeCheck.ResponseExitCode == nil || *executeCheck.ResponseExitCode != 0 {
		t.Fatalf("execute check = %#v", executeCheck)
	}
}

func TestConformanceEnforcesAcquisitionPolicyBeforeHandshake(t *testing.T) {
	policy := DefaultPolicy()
	policy.Local = "deny"
	policy.RequireSigned = true
	report := Conform(context.Background(), filepath.Join(t.TempDir(), "missing"), ConformanceOptions{Policy: &policy})
	if exitCode := ConformanceExit(report); exitCode != 3 {
		t.Fatalf("exit=%d report=%#v", exitCode, report)
	}
	if len(report.Issues) != 1 || report.Issues[0].Code != "VIGIL_PLUGIN_CONFORMANCE_POLICY_BLOCKED" {
		t.Fatalf("issues = %#v", report.Issues)
	}
}

func TestPluginLifecyclePinsTrustsDiscoversExecutesAndRevokes(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayout(filepath.Join(root, "user-plugins"), repository)
	if err != nil {
		t.Fatal(err)
	}
	candidate := writeFixturePlugin(t, root, validHandshakeJSON(), validResponseJSON("request-1"))

	if _, err := Install(context.Background(), InstallOptions{
		Layout: layout, Candidate: candidate, ApprovalTime: time.Unix(1, 0),
	}); ExitCode(err) != 3 {
		t.Fatalf("unapproved install error = %v", err)
	}
	installed, err := Install(context.Background(), InstallOptions{
		Layout: layout, Candidate: candidate, ApproveAll: true, ApprovalTime: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Action != "installed" || installed.Plugin.Status != "ok" {
		t.Fatalf("result = %#v", installed)
	}
	if info, err := os.Stat(layout.TrustPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("trust mode info=%v err=%v", info, err)
	}
	lock, err := ReadLock(layout.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := ReadTrust(layout.TrustPath)
	if err != nil {
		t.Fatal(err)
	}
	lock.Plugins[0].Capabilities[0], lock.Plugins[0].Capabilities[1] =
		lock.Plugins[0].Capabilities[1], lock.Plugins[0].Capabilities[0]
	trust.Records[0].Capabilities[0], trust.Records[0].Capabilities[1] =
		trust.Records[0].Capabilities[1], trust.Records[0].Capabilities[0]
	writeRawState(t, layout.LockPath, lock, 0o644)
	writeRawState(t, layout.TrustPath, trust, 0o600)

	discovery := Discover(context.Background(), layout)
	if discovery.Status != "ok" || len(discovery.Available) != 1 || len(discovery.Issues) != 0 {
		t.Fatalf("discovery = %#v", discovery)
	}
	command := discovery.Available[0].Metadata.Commands[0]
	response, err := Execute(context.Background(), discovery.Available[0], command, []string{"hello"}, ExecuteOptions{
		RepositoryRoot: repository,
		OutputFormat:   "json",
		RequestID:      "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ExitCode != 0 || response.Output != "fixture output" || !strings.Contains(string(response.Data), `"echo":"ok"`) {
		t.Fatalf("response = %#v", response)
	}

	removed, err := Remove(RemoveOptions{Layout: layout, ID: "example", Revoke: true})
	if err != nil {
		t.Fatal(err)
	}
	if !removed.Removed || !removed.Revoked {
		t.Fatalf("removed = %#v", removed)
	}
	if _, err := Install(context.Background(), InstallOptions{
		Layout: layout, Candidate: candidate, ApproveAll: true, ApprovalTime: time.Unix(2, 0),
	}); ExitCode(err) != 3 {
		t.Fatalf("revoked reinstall error = %v", err)
	}
	if _, err := Install(context.Background(), InstallOptions{
		Layout: layout, Candidate: candidate, ApproveAll: true, RestoreTrust: true, ApprovalTime: time.Unix(2, 0),
	}); err != nil {
		t.Fatalf("restore trust: %v", err)
	}
}

func TestDiscoveryRejectsExecutableDigestDrift(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayout(filepath.Join(root, "plugins"), repository)
	if err != nil {
		t.Fatal(err)
	}
	candidate := writeFixturePlugin(t, root, validHandshakeJSON(), validResponseJSON("request-1"))
	result, err := Install(context.Background(), InstallOptions{
		Layout: layout, Candidate: candidate, ApproveAll: true, ApprovalTime: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.ExecutablePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	report := Discover(context.Background(), layout)
	if report.Status != "fail" || len(report.Available) != 0 || len(report.Issues) != 1 ||
		report.Issues[0].Code != "VIGIL_PLUGIN_DIGEST_MISMATCH" || report.Issues[0].ExitCode != 3 {
		t.Fatalf("report = %#v", report)
	}
}

func TestHandshakeHonorsCancellationAndStrictJSON(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "vigil-plugin-slow")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, _, err := HandshakeExecutable(ctx, path); ExitCode(err) != 5 {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		root := t.TempDir()
		handshake := strings.TrimSuffix(validHandshakeJSON(), "}") + `,"unknown":true}`
		path := writeFixturePlugin(t, root, handshake, validResponseJSON("request-1"))
		if _, _, err := HandshakeExecutable(context.Background(), path); ExitCode(err) != 2 {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing explicit field", func(t *testing.T) {
		root := t.TempDir()
		handshake := strings.Replace(validHandshakeJSON(), `"interactive":false,`, "", 1)
		path := writeFixturePlugin(t, root, handshake, validResponseJSON("request-1"))
		if _, _, err := HandshakeExecutable(context.Background(), path); ExitCode(err) != 2 {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("read command with write capability", func(t *testing.T) {
		root := t.TempDir()
		var handshake Handshake
		if err := json.Unmarshal([]byte(validHandshakeJSON()), &handshake); err != nil {
			t.Fatal(err)
		}
		handshake.Plugin.Commands[0].Capabilities = append(
			handshake.Plugin.Commands[0].Capabilities,
			"filesystem:write",
		)
		data, err := json.Marshal(handshake)
		if err != nil {
			t.Fatal(err)
		}
		path := writeFixturePlugin(t, root, string(data), validResponseJSON("request-1"))
		if _, _, err := HandshakeExecutable(context.Background(), path); ExitCode(err) != 2 {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("reserved host flag", func(t *testing.T) {
		root := t.TempDir()
		var handshake Handshake
		if err := json.Unmarshal([]byte(validHandshakeJSON()), &handshake); err != nil {
			t.Fatal(err)
		}
		handshake.Plugin.Commands[0].Flags = []cli.Flag{{Long: "--json", Description: "Shadow host output."}}
		data, err := json.Marshal(handshake)
		if err != nil {
			t.Fatal(err)
		}
		path := writeFixturePlugin(t, root, string(data), validResponseJSON("request-1"))
		if _, _, err := HandshakeExecutable(context.Background(), path); ExitCode(err) != 2 {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestExecuteRequiresCompleteResponseAndValidRequestID(t *testing.T) {
	root := t.TempDir()
	var handshake Handshake
	if err := json.Unmarshal([]byte(validHandshakeJSON()), &handshake); err != nil {
		t.Fatal(err)
	}
	command := handshake.Plugin.Commands[0]

	missingData := strings.Replace(validResponseJSON("request-1"), `"data":{"echo":"ok"},`, "", 1)
	path := writeFixturePlugin(t, root, validHandshakeJSON(), missingData)
	plugin := InstalledPlugin{Path: path, Metadata: handshake.Plugin}
	if _, err := Execute(context.Background(), plugin, command, nil, ExecuteOptions{
		RepositoryRoot: root, RequestID: "request-1",
	}); ExitCode(err) != 2 {
		t.Fatalf("missing data error = %v", err)
	}
	if _, err := Execute(context.Background(), plugin, command, nil, ExecuteOptions{
		RepositoryRoot: root, RequestID: "request id with spaces",
	}); ExitCode(err) != 2 {
		t.Fatalf("invalid request id error = %v", err)
	}
}

func TestLockfileRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LockFilename)
	if err := os.WriteFile(path, []byte(`{"schema_version":"1","plugins":[],"unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(path); ExitCode(err) != 2 {
		t.Fatalf("unknown-field error = %v", err)
	}
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":"1","plugins":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadLock(path); ExitCode(err) != 2 {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestInstallRejectsCandidateChangedAfterHandshake(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayout(filepath.Join(root, "plugins"), repository)
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(root, "vigil-plugin-example")
	replacement := "#!/bin/sh\nexit 0\n"
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  handshake)\n" +
		"    printf '%s\\n' " + shellSingleQuote(validHandshakeJSON()) + "\n" +
		"    printf '%s' " + shellSingleQuote(replacement) + " > \"$0.replacement\"\n" +
		"    chmod 700 \"$0.replacement\"\n" +
		"    mv \"$0.replacement\" \"$0\"\n" +
		"    ;;\n" +
		"  *) exit 64 ;;\n" +
		"esac\n"
	if err := os.WriteFile(candidate, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err = Install(context.Background(), InstallOptions{
		Layout: layout, Candidate: candidate, ApproveAll: true, ApprovalTime: time.Unix(1, 0),
	})
	if ExitCode(err) != 3 || !strings.Contains(err.Error(), "candidate changed") {
		t.Fatalf("install error = %v", err)
	}
	destination, pathErr := ExecutablePath(layout.Root, "example", "1.2.3")
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("changed candidate was installed: %v", statErr)
	}
}

func TestArtifactValidationRequiresConfinedRegularDigestMatchedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(root, "reports", "result.json")
	if err := os.WriteFile(artifactPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := FileDigest(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	valid := output.Artifact{Kind: "report", Path: "reports/result.json", MediaType: "application/json", Digest: digest}
	if err := validateArtifact(root, valid); err != nil {
		t.Fatalf("valid artifact: %v", err)
	}

	missing := valid
	missing.Path = "reports/missing.json"
	if err := validateArtifact(root, missing); ExitCode(err) != 2 {
		t.Fatalf("missing artifact error = %v", err)
	}
	mismatched := valid
	mismatched.Digest = "sha256:" + strings.Repeat("0", 64)
	if err := validateArtifact(root, mismatched); ExitCode(err) != 3 {
		t.Fatalf("digest mismatch error = %v", err)
	}
	if err := validateArtifact(root, output.Artifact{Kind: "report", Path: "../outside.json"}); ExitCode(err) != 3 {
		t.Fatalf("escaping artifact error = %v", err)
	}
	for _, artifact := range []output.Artifact{
		{Kind: " report", Path: "reports/result.json"},
		{Kind: "report", Path: " reports/result.json"},
		{Kind: "report", Path: "reports/result.json "},
		{Kind: "report", Path: "reports/../reports/result.json"},
		{Kind: "report", Path: "./reports/result.json"},
	} {
		t.Run(artifact.Kind+" "+artifact.Path, func(t *testing.T) {
			if err := validateArtifact(root, artifact); err == nil {
				t.Fatal("expected normalized artifact rejection")
			}
		})
	}

	symlink := filepath.Join(root, "reports", "link.json")
	if err := os.Symlink(artifactPath, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := validateArtifact(root, output.Artifact{Kind: "report", Path: "reports/link.json"}); ExitCode(err) != 3 {
		t.Fatalf("symlink artifact error = %v", err)
	}
}

func TestPublisherTrustLifecycleIsExplicitAndRevocable(t *testing.T) {
	root := t.TempDir()
	layout, err := NewLayout(filepath.Join(root, "plugins"), filepath.Join(root, "repository"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(publicKey))
	trusted, err := TrustPublisher(layout, "Fixture Publisher", encoded, time.Unix(1, 0), false)
	if err != nil {
		t.Fatal(err)
	}
	if trusted.Action != "trusted" || trusted.Key.KeyID != publisherKeyID(publicKey) {
		t.Fatalf("trust result = %#v", trusted)
	}
	if info, err := os.Stat(layout.PublisherPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("publisher mode info=%v err=%v", info, err)
	}

	revoked, err := RevokePublisher(layout, trusted.Key.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	if !revoked.Revoked {
		t.Fatalf("revoke result = %#v", revoked)
	}
	if _, err := TrustPublisher(layout, "Fixture Publisher", encoded, time.Unix(2, 0), false); ExitCode(err) != 3 {
		t.Fatalf("revoked trust error = %v", err)
	}
	restored, err := TrustPublisher(layout, "Fixture Publisher", encoded, time.Unix(2, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Action != "unchanged" {
		t.Fatalf("restore result = %#v", restored)
	}
	store, err := ReadPublishers(layout.PublisherPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Keys) != 1 || len(store.RevokedKeyIDs) != 0 {
		t.Fatalf("publisher store = %#v", store)
	}
}

func TestPluginPolicyBlocksDeniedAcquisitionIdentityPublisherAndCapability(t *testing.T) {
	policy := DefaultPolicy()
	policy.Local = "deny"
	policy.RequireSigned = true
	policy.AllowedIDs = []string{"example"}
	policy.AllowedPublisherKeyIDs = []string{"sha256:" + strings.Repeat("a", 64)}
	policy.DeniedCapabilities = []string{"secrets"}
	if err := ValidatePolicy(policy); err != nil {
		t.Fatal(err)
	}
	if err := CheckPolicy(policy, "example", []string{"filesystem:read"}, "local", []string{}, 0); ExitCode(err) != 3 {
		t.Fatalf("local policy error = %v", err)
	}
	if _, err := Install(context.Background(), InstallOptions{
		Layout:    Layout{},
		Candidate: filepath.Join(t.TempDir(), "must-not-execute"),
		Policy:    &policy,
	}); ExitCode(err) != 3 || !strings.Contains(err.Error(), "local plugin acquisition is denied") {
		t.Fatalf("pre-handshake policy error = %v", err)
	}
	if err := CheckPolicy(
		policy,
		"other",
		[]string{"filesystem:read"},
		"signed-index",
		policy.AllowedPublisherKeyIDs,
		1,
	); ExitCode(err) != 3 {
		t.Fatalf("identity policy error = %v", err)
	}
	if err := CheckPolicy(
		policy,
		"example",
		[]string{"filesystem:read"},
		"signed-index",
		[]string{"sha256:" + strings.Repeat("b", 64)},
		1,
	); ExitCode(err) != 3 {
		t.Fatalf("publisher policy error = %v", err)
	}
	if err := CheckPolicy(
		policy,
		"example",
		[]string{"filesystem:read", "secrets"},
		"signed-index",
		policy.AllowedPublisherKeyIDs,
		1,
	); ExitCode(err) != 3 {
		t.Fatalf("capability policy error = %v", err)
	}
	if err := CheckPolicy(
		policy,
		"example",
		[]string{"filesystem:read"},
		"signed-index",
		policy.AllowedPublisherKeyIDs,
		1,
	); err != nil {
		t.Fatalf("allowed policy error = %v", err)
	}
}

func TestPluginPolicyEnforcesMinimumSignatureThreshold(t *testing.T) {
	policy := DefaultPolicy()
	policy.MinSignatureThreshold = 2
	if err := ValidatePolicy(policy); err != nil {
		t.Fatal(err)
	}
	keyA := "sha256:" + strings.Repeat("a", 64)
	keyB := "sha256:" + strings.Repeat("b", 64)
	if err := CheckPolicy(policy, "example", []string{"filesystem:read"}, "signed-index", []string{keyA}, 1); ExitCode(err) != 3 ||
		!strings.Contains(err.Error(), "below policy minimum") {
		t.Fatalf("minimum threshold error = %v", err)
	}
	if err := CheckPolicy(policy, "example", []string{"filesystem:read"}, "signed-index", []string{keyA, keyB}, 2); err != nil {
		t.Fatalf("minimum threshold allowed error = %v", err)
	}
	policy.MinSignatureThreshold = -1
	if err := ValidatePolicy(policy); err == nil || !strings.Contains(err.Error(), "min_signature_threshold") {
		t.Fatalf("negative minimum threshold error = %v", err)
	}
}

func TestSignedIndexSelectsAcquiresAndPinsExactPlugin(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayout(filepath.Join(root, "plugins"), repository)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TrustPublisher(
		layout,
		"Fixture Publisher",
		[]byte(base64.StdEncoding.EncodeToString(publicKey)),
		time.Unix(1, 0),
		false,
	); err != nil {
		t.Fatal(err)
	}

	artifactPath := writeFixturePlugin(t, root, validHandshakeJSON(), validResponseJSON("request-1"))
	artifactDigest, err := FileDigest(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	var handshake Handshake
	if err := json.Unmarshal([]byte(validHandshakeJSON()), &handshake); err != nil {
		t.Fatal(err)
	}
	metadataDigest, err := MetadataDigest(handshake.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	payload := IndexPayload{
		GeneratedAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt:          now.Add(time.Hour).Format(time.RFC3339Nano),
		SignatureThreshold: 1,
		Plugins: []IndexRelease{{
			ID: "example", Name: "Example", Version: "1.2.3", Description: "Fixture plugin",
			ProtocolVersion: ProtocolVersion, HostAPIVersion: HostAPIVersion,
			MetadataDigest: metadataDigest, Capabilities: MetadataCapabilities(handshake.Plugin),
			Artifacts: []IndexArtifact{{
				OS: runtime.GOOS, Arch: runtime.GOARCH, URL: filepath.Base(artifactPath),
				Digest: artifactDigest, Size: artifactInfo.Size(),
			}},
		}},
	}
	document := signFixtureIndex(t, payload, publisherKeyID(publicKey), privateKey)
	indexPath := filepath.Join(root, "index-v1.json")
	writeRawState(t, indexPath, document, 0o644)

	loaded, err := LoadVerifiedIndex(context.Background(), layout, indexPath, IndexLoadOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Verified.SignerIDs) != 1 || loaded.Verified.SignerIDs[0] != publisherKeyID(publicKey) {
		t.Fatalf("verified index = %#v", loaded.Verified)
	}
	selected, err := SelectIndexRelease(loaded.Verified, "example", "1.2.3", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := AcquireIndexedPlugin(context.Background(), layout, loaded, selected, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = RemoveAcquiredPlugin(acquired) }()
	installed, err := Install(context.Background(), InstallOptions{
		Layout: layout, Candidate: acquired.Path, ApproveAll: true, ApprovalTime: time.Unix(2, 0),
		ExpectedPluginID: "example", ExpectedVersion: "1.2.3",
		ExpectedDigest: artifactDigest, ExpectedMetadataDigest: metadataDigest,
		ExpectedCapabilities: payload.Plugins[0].Capabilities,
		Acquisition:          "signed-index", IndexDigest: acquired.IndexDigest,
		PublisherKeyIDs: acquired.SignerIDs, SignatureThreshold: acquired.SignatureThreshold,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Plugin.Digest != artifactDigest || installed.Plugin.MetadataDigest != metadataDigest {
		t.Fatalf("installed plugin = %#v", installed.Plugin)
	}
	if _, err := RevokePublisher(layout, publisherKeyID(publicKey)); err != nil {
		t.Fatal(err)
	}
	discovery := Discover(context.Background(), layout)
	if discovery.Status != "fail" || len(discovery.Issues) != 1 ||
		discovery.Issues[0].Code != "VIGIL_PLUGIN_PUBLISHER_THRESHOLD" {
		t.Fatalf("discovery after publisher revocation = %#v", discovery)
	}
	if _, err := TrustPublisher(
		layout,
		"Fixture Publisher",
		[]byte(base64.StdEncoding.EncodeToString(publicKey)),
		time.Unix(3, 0),
		true,
	); err != nil {
		t.Fatal(err)
	}
	discovery = Discover(context.Background(), layout)
	if discovery.Status != "ok" || len(discovery.Available) != 1 {
		t.Fatalf("discovery after publisher restore = %#v", discovery)
	}
	strictPolicy := DefaultPolicy()
	strictPolicy.MinSignatureThreshold = 2
	discovery = DiscoverWithPolicy(context.Background(), layout, strictPolicy)
	if discovery.Status != "fail" || len(discovery.Available) != 0 || len(discovery.Issues) != 1 ||
		discovery.Issues[0].Code != "VIGIL_PLUGIN_POLICY_BLOCKED" ||
		!strings.Contains(discovery.Issues[0].Message, "below policy minimum") {
		t.Fatalf("discovery after threshold policy = %#v", discovery)
	}

	tampered := document
	tampered.Signed.Plugins[0].Description = "tampered"
	tamperedPath := filepath.Join(root, "tampered-index.json")
	writeRawState(t, tamperedPath, tampered, 0o644)
	if _, err := LoadVerifiedIndex(context.Background(), layout, tamperedPath, IndexLoadOptions{Now: now}); ExitCode(err) != 3 {
		t.Fatalf("tampered index error = %v", err)
	}

	expiredPayload := payload
	expiredPayload.GeneratedAt = now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	expiredPayload.ExpiresAt = now.Add(-time.Hour).Format(time.RFC3339Nano)
	expired := signFixtureIndex(t, expiredPayload, publisherKeyID(publicKey), privateKey)
	if _, err := VerifyIndex(expired, mustReadPublishers(t, layout.PublisherPath), now); ExitCode(err) != 3 {
		t.Fatalf("expired index error = %v", err)
	}
}

func TestOfflineIndexAuthoringSupportsThresholdSigning(t *testing.T) {
	now := time.Now().UTC()
	document := IndexDocument{
		SchemaVersion: IndexSchemaVersion,
		Signed: IndexPayload{
			GeneratedAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
			ExpiresAt:          now.Add(time.Hour).Format(time.RFC3339Nano),
			SignatureThreshold: 2,
			Plugins: []IndexRelease{{
				ID: "example", Name: "Example", Version: "1.2.3", Description: "Offline signing fixture",
				ProtocolVersion: ProtocolVersion, HostAPIVersion: HostAPIVersion,
				MetadataDigest: "sha256:" + strings.Repeat("a", 64),
				Capabilities:   []string{"filesystem:read"},
				Artifacts: []IndexArtifact{{
					OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "vigil-plugin-example",
					Digest: "sha256:" + strings.Repeat("b", 64), Size: 128,
				}},
			}},
		},
		Signatures: []IndexSignature{},
	}
	if err := ValidateIndexDraft(document); err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndex(document); ExitCode(err) != 2 {
		t.Fatalf("unsigned threshold error = %v", err)
	}

	first, err := GeneratePublisherKey()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GeneratePublisherKey()
	if err != nil {
		t.Fatal(err)
	}
	document, err = SignIndexDraft(document, first.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	idempotent, err := SignIndexDraft(document, first.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(idempotent.Signatures) != 1 || idempotent.Signatures[0] != document.Signatures[0] {
		t.Fatalf("idempotent signatures = %#v", idempotent.Signatures)
	}
	encoded, err := EncodeIndexDraft(document)
	if err != nil {
		t.Fatal(err)
	}
	document, err = DecodeIndexDraft(encoded)
	if err != nil {
		t.Fatal(err)
	}
	document, err = SignIndexDraft(document, second.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndex(document); err != nil {
		t.Fatal(err)
	}
	approvedAt := now.Format(time.RFC3339Nano)
	store := PublisherStore{
		SchemaVersion: PublisherSchemaVersion,
		Keys: []PublisherKey{
			publisherRecord(t, first, approvedAt),
			publisherRecord(t, second, approvedAt),
		},
		RevokedKeyIDs: []string{},
	}
	verified, err := VerifyIndex(document, store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.SignerIDs) != 2 {
		t.Fatalf("verified = %#v", verified)
	}
}

func TestPublisherPrivateKeyFilesRequireRestrictedPermissions(t *testing.T) {
	generated, err := GeneratePublisherKey()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePublisherPrivateKey(generated.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publisher.key")
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if _, err := ReadPublisherPrivateKeyFile(path); ExitCode(err) != 3 {
			t.Fatalf("permissive key error = %v", err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadPublisherPrivateKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if PublisherKeyID(decoded.Public().(ed25519.PublicKey)) != generated.KeyID {
		t.Fatal("decoded private key changed publisher identity")
	}
	if err := WriteExclusiveFile(path, []byte("replacement"), 0o600); ExitCode(err) != 3 {
		t.Fatalf("exclusive overwrite error = %v", err)
	}
}

func TestIndexRejectsTraversalAndUntrustedThreshold(t *testing.T) {
	now := time.Now().UTC()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := IndexPayload{
		GeneratedAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt:          now.Add(time.Hour).Format(time.RFC3339Nano),
		SignatureThreshold: 1,
		Plugins: []IndexRelease{{
			ID: "example", Name: "Example", Version: "1.2.3", Description: "Fixture",
			ProtocolVersion: ProtocolVersion, HostAPIVersion: HostAPIVersion,
			MetadataDigest: "sha256:" + strings.Repeat("a", 64),
			Capabilities:   []string{"filesystem:read"},
			Artifacts: []IndexArtifact{{
				OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "../plugin",
				Digest: "sha256:" + strings.Repeat("b", 64), Size: 1,
			}},
		}},
	}
	for _, artifactURL := range []string{
		"../plugin",
		".",
		"plugin?token=1",
		"plugin#digest",
		"plugin%2Fescaped",
		"plugin:name",
		`plugin\name`,
		"http://example.test/plugin",
		"https://token@example.test/plugin",
		"https://example.test/plugin#digest",
	} {
		t.Run(artifactURL, func(t *testing.T) {
			payload.Plugins[0].Artifacts[0].URL = artifactURL
			document := signFixtureIndex(t, payload, publisherKeyID(publicKey), privateKey)
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeIndex(data); ExitCode(err) != 2 {
				t.Fatalf("unsafe artifact URL error = %v", err)
			}
		})
	}

	payload.Plugins[0].Artifacts[0].URL = "plugin"
	document := signFixtureIndex(t, payload, publisherKeyID(publicKey), privateKey)
	emptyStore := PublisherStore{SchemaVersion: PublisherSchemaVersion, Keys: []PublisherKey{}, RevokedKeyIDs: []string{}}
	if _, err := VerifyIndex(document, emptyStore, now); ExitCode(err) != 3 {
		t.Fatalf("untrusted threshold error = %v", err)
	}
}

func publisherRecord(t *testing.T, generated GeneratedPublisherKey, approvedAt string) PublisherKey {
	t.Helper()
	encoded, err := EncodePublisherPublicKey(generated.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return PublisherKey{
		KeyID: generated.KeyID, Name: generated.KeyID, Algorithm: PublisherAlgorithm,
		PublicKey: encoded, ApprovedAt: approvedAt,
	}
}

func TestHTTPSIndexAcquisitionEnforcesTransportAndBounds(t *testing.T) {
	root := t.TempDir()
	layout, err := NewLayout(filepath.Join(root, "plugins"), filepath.Join(root, "repository"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TrustPublisher(
		layout,
		"HTTPS Fixture",
		[]byte(base64.StdEncoding.EncodeToString(publicKey)),
		time.Unix(1, 0),
		false,
	); err != nil {
		t.Fatal(err)
	}
	artifactData := []byte("#!/bin/sh\nexit 0\n")
	now := time.Now().UTC()
	payload := IndexPayload{
		GeneratedAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt:          now.Add(time.Hour).Format(time.RFC3339Nano),
		SignatureThreshold: 1,
		Plugins: []IndexRelease{{
			ID: "example", Name: "Example", Version: "1.2.3", Description: "HTTPS fixture",
			ProtocolVersion: ProtocolVersion, HostAPIVersion: HostAPIVersion,
			MetadataDigest: "sha256:" + strings.Repeat("a", 64),
			Capabilities:   []string{"filesystem:read"},
			Artifacts: []IndexArtifact{{
				OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "plugin",
				Digest: digestBytes(artifactData), Size: int64(len(artifactData)),
			}},
		}},
	}
	document := signFixtureIndex(t, payload, publisherKeyID(publicKey), privateKey)
	indexData, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	server := newPluginHTTPTestServer(t, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(indexData)
		case "/plugin":
			writer.Header().Set("Content-Type", "application/octet-stream")
			_, _ = writer.Write(artifactData)
		case "/oversized":
			_, _ = writer.Write([]byte("too large"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	loaded, err := LoadVerifiedIndex(context.Background(), layout, server.URL+"/index", IndexLoadOptions{
		HTTPClient: server.Client(),
		Now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectIndexRelease(loaded.Verified, "example", "1.2.3", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := AcquireIndexedPlugin(context.Background(), layout, loaded, selected, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAcquiredPlugin(acquired); err != nil {
		t.Fatal(err)
	}

	oversizedURL, err := url.Parse(server.URL + "/oversized")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetchHTTPS(context.Background(), oversizedURL, 1, server.Client()); ExitCode(err) != 3 {
		t.Fatalf("oversized response error = %v", err)
	}

	insecureTarget := newPluginHTTPTestServer(t, false, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(indexData)
	}))
	defer insecureTarget.Close()
	redirector := newPluginHTTPTestServer(t, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, insecureTarget.URL, http.StatusFound)
	}))
	defer redirector.Close()
	if _, err := LoadVerifiedIndex(
		context.Background(),
		layout,
		redirector.URL,
		IndexLoadOptions{HTTPClient: redirector.Client(), Now: now},
	); ExitCode(err) != 3 {
		t.Fatalf("HTTPS downgrade error = %v", err)
	}
}

func FuzzStrictPluginHandshakeJSON(f *testing.F) {
	f.Add([]byte(validHandshakeJSON()))
	f.Add([]byte(`{"schema_version":"1","protocol_version":"1","plugin":null}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxHandshakeBytes {
			return
		}
		if err := validateHandshakeDocumentShape(data); err != nil {
			return
		}
		var handshake Handshake
		if err := decodeStrictJSON(data, &handshake); err != nil {
			return
		}
		_ = ValidateHandshake(handshake)
	})
}

func writeFixturePlugin(t *testing.T, root, handshake, response string) string {
	t.Helper()
	path := filepath.Join(root, "vigil-plugin-example")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  handshake) printf '%s\\n' " + shellSingleQuote(handshake) + " ;;\n" +
		"  execute) read request; printf '%s\\n' " + shellSingleQuote(response) + " ;;\n" +
		"  *) exit 64 ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func newPluginHTTPTestServer(t *testing.T, useTLS bool, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listeners unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	if useTLS {
		server.StartTLS()
	} else {
		server.Start()
	}
	return server
}

func validHandshakeJSON() string {
	handshake := Handshake{
		SchemaVersion:   HandshakeSchema,
		ProtocolVersion: ProtocolVersion,
		Plugin: Metadata{
			ID: "example", Name: "Example", Version: "1.2.3", Description: "Fixture plugin", HostAPIVersion: HostAPIVersion,
			Commands: []Command{{
				Name: "example:echo", Aliases: []string{}, Summary: "Echo a fixture.",
				Access: "read", Capabilities: []string{"filesystem:read", "process"}, Args: "[TEXT]",
				Flags: []cli.Flag{}, Arguments: []cli.Argument{{Name: "TEXT", Description: "Text to echo."}},
				Stability: "stable", Timeout: "2s", Network: "none", RequiredTools: []string{},
				OutputFormats: []string{"text", "json"}, WriteFlags: []string{}, ReadOnlyFlags: []string{},
				Usage: "vigil example:echo [TEXT]", Examples: []string{"vigil example:echo hello"},
			}},
		},
	}
	data, _ := json.Marshal(handshake)
	return string(data)
}

func validResponseJSON(requestID string) string {
	response := Response{
		SchemaVersion: ProtocolVersion, ProtocolVersion: ProtocolVersion, RequestID: requestID,
		ExitCode: 0, Output: "fixture output", Data: json.RawMessage(`{"echo":"ok"}`),
		Warnings: []output.Diagnostic{}, Errors: []output.Diagnostic{}, Artifacts: []output.Artifact{},
	}
	data, _ := json.Marshal(response)
	return string(data)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeRawState(t *testing.T, path string, value any, mode os.FileMode) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func signFixtureIndex(t *testing.T, payload IndexPayload, keyID string, privateKey ed25519.PrivateKey) IndexDocument {
	t.Helper()
	signingBytes, err := IndexSigningBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	return IndexDocument{
		SchemaVersion: IndexSchemaVersion,
		Signed:        payload,
		Signatures: []IndexSignature{{
			KeyID: keyID, Algorithm: PublisherAlgorithm,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, signingBytes)),
		}},
	}
}

func mustReadPublishers(t *testing.T, path string) PublisherStore {
	t.Helper()
	store, err := ReadPublishers(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

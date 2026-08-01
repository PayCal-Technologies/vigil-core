package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/plugins"
)

func TestPublisherUtilityGeneratesKeysAndSignsIndex(t *testing.T) {
	if exitCode := run([]string{"version"}); exitCode != 0 {
		t.Fatalf("version exit = %d", exitCode)
	}
	root := t.TempDir()
	privatePath := filepath.Join(root, "publisher.key")
	publicPath := filepath.Join(root, "publisher.pub")
	if exitCode := run([]string{
		"keygen",
		"--private-key", privatePath,
		"--public-key", publicPath,
	}); exitCode != 0 {
		t.Fatalf("keygen exit = %d", exitCode)
	}
	privateInfo, err := os.Stat(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %o", privateInfo.Mode().Perm())
	}
	if exitCode := run([]string{
		"keygen",
		"--private-key", privatePath,
		"--public-key", publicPath,
	}); exitCode != 3 {
		t.Fatalf("overwrite keygen exit = %d", exitCode)
	}
	if exitCode := run([]string{"key-id", "--public-key", publicPath}); exitCode != 0 {
		t.Fatalf("key-id exit = %d", exitCode)
	}

	now := time.Now().UTC()
	document := plugins.IndexDocument{
		SchemaVersion: plugins.IndexSchemaVersion,
		Signed: plugins.IndexPayload{
			GeneratedAt:        now.Add(-time.Minute).Format(time.RFC3339Nano),
			ExpiresAt:          now.Add(time.Hour).Format(time.RFC3339Nano),
			SignatureThreshold: 1,
			Plugins: []plugins.IndexRelease{{
				ID: "publisher-test", Name: "Publisher Test", Version: "1.0.0", Description: "Publisher utility test",
				ProtocolVersion: plugins.ProtocolVersion, HostAPIVersion: plugins.HostAPIVersion,
				MetadataDigest: "sha256:" + strings.Repeat("a", 64),
				Capabilities:   []string{"filesystem:read"},
				Artifacts: []plugins.IndexArtifact{{
					OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "vigil-plugin-publisher-test",
					Digest: "sha256:" + strings.Repeat("b", 64), Size: 128,
				}},
			}},
		},
		Signatures: []plugins.IndexSignature{},
	}
	draft, err := plugins.EncodeIndexDraft(document)
	if err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(root, "index-draft.json")
	if err := os.WriteFile(draftPath, draft, 0o644); err != nil {
		t.Fatal(err)
	}
	signedPath := filepath.Join(root, "index-signed.json")
	if exitCode := run([]string{
		"sign",
		"--index", draftPath,
		"--private-key", privatePath,
		"--output", signedPath,
	}); exitCode != 0 {
		t.Fatalf("sign exit = %d", exitCode)
	}
	signed, err := plugins.ReadIndexDraftFile(signedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugins.ValidateIndex(signed); err != nil {
		t.Fatal(err)
	}
	if exitCode := run([]string{"inspect", "--index", signedPath}); exitCode != 0 {
		t.Fatalf("inspect exit = %d", exitCode)
	}
	if exitCode := run([]string{
		"verify",
		"--index", signedPath,
		"--public-key", publicPath,
		"--at", now.Format(time.RFC3339Nano),
	}); exitCode != 0 {
		t.Fatalf("verify exit = %d", exitCode)
	}
	if exitCode := run([]string{
		"sign",
		"--index", draftPath,
		"--private-key", privatePath,
		"--output", signedPath,
	}); exitCode != 3 {
		t.Fatalf("overwrite sign exit = %d", exitCode)
	}
}

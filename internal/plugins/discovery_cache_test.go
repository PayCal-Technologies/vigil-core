package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryHandshakeCacheIsBoundedByContentAndHonorsCancellation(t *testing.T) {
	discoveryHandshakes.Clear()
	root := t.TempDir()
	path := filepath.Join(root, "vigil-plugin-example")
	countPath := filepath.Join(root, "handshakes")
	handshakeJSON := validHandshakeJSON()
	var expected Handshake
	if err := json.Unmarshal([]byte(handshakeJSON), &expected); err != nil {
		t.Fatal(err)
	}
	metadataDigest, err := MetadataDigest(expected.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	writeCountingHandshakePlugin(t, path, countPath, handshakeJSON, "one")
	digest, err := FileDigest(path)
	if err != nil {
		t.Fatal(err)
	}

	first, _, err := discoveryHandshake(context.Background(), path, digest, metadataDigest)
	if err != nil {
		t.Fatal(err)
	}
	first.Plugin.Description = "caller mutation"
	second, _, err := discoveryHandshake(context.Background(), path, digest, metadataDigest)
	if err != nil {
		t.Fatal(err)
	}
	if second.Plugin.Description != expected.Plugin.Description {
		t.Fatalf("cached handshake was mutable: %#v", second.Plugin)
	}
	if count := handshakeCount(t, countPath); count != 1 {
		t.Fatalf("handshake count after cache hit = %d", count)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := discoveryHandshake(cancelled, path, digest, metadataDigest); ExitCode(err) != 5 {
		t.Fatalf("cancelled cache lookup error = %v", err)
	}

	writeCountingHandshakePlugin(t, path, countPath, handshakeJSON, "two")
	changedDigest, err := FileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == digest {
		t.Fatal("fixture digest did not change")
	}
	if _, _, err := discoveryHandshake(context.Background(), path, changedDigest, metadataDigest); err != nil {
		t.Fatal(err)
	}
	if count := handshakeCount(t, countPath); count != 2 {
		t.Fatalf("stale cache survived executable change; count = %d", count)
	}
}

func writeCountingHandshakePlugin(t *testing.T, path, countPath, handshake, version string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"# " + version + "\n" +
		"case \"$1\" in\n" +
		"  handshake) printf x >> " + shellSingleQuote(countPath) + "; printf '%s\\n' " + shellSingleQuote(handshake) + " ;;\n" +
		"  *) exit 64 ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func handshakeCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(data)
}

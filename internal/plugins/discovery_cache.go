package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	vigilcache "github.com/PayCal-Technologies/vigil-public/internal/cache"
)

const maxCachedDiscoveryHandshakes = 64

var discoveryHandshakes = vigilcache.NewLRU[string, Handshake](maxCachedDiscoveryHandshakes)

func discoveryHandshake(ctx context.Context, path, executableDigest, expectedMetadataDigest string) (Handshake, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Handshake{}, "", wrapPluginError(ErrorInterrupted, "plugin handshake", err)
	}
	key := discoveryHandshakeKey(path, executableDigest, expectedMetadataDigest)
	if cached, ok := discoveryHandshakes.Get(key); ok {
		cached = cloneHandshake(cached)
		metadataDigest, err := MetadataDigest(cached.Plugin)
		if err == nil && metadataDigest == expectedMetadataDigest && ValidateHandshake(cached) == nil {
			return cached, executableDigest, nil
		}
		discoveryHandshakes.Delete(key)
	}

	handshake, digest, err := handshakeExecutableWithDigest(ctx, path, executableDigest)
	if err != nil {
		return Handshake{}, "", err
	}
	metadataDigest, err := MetadataDigest(handshake.Plugin)
	if err == nil && metadataDigest == expectedMetadataDigest {
		discoveryHandshakes.Put(key, cloneHandshake(handshake))
	}
	return handshake, digest, nil
}

func discoveryHandshakeKey(path, executableDigest, metadataDigest string) string {
	data, _ := json.Marshal(struct {
		Path             string `json:"path"`
		ExecutableDigest string `json:"executable_digest"`
		MetadataDigest   string `json:"metadata_digest"`
		ProtocolVersion  string `json:"protocol_version"`
	}{
		Path:             path,
		ExecutableDigest: executableDigest,
		MetadataDigest:   metadataDigest,
		ProtocolVersion:  ProtocolVersion,
	})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func cloneHandshake(handshake Handshake) Handshake {
	data, err := json.Marshal(handshake)
	if err != nil {
		return handshake
	}
	var cloned Handshake
	if err := json.Unmarshal(data, &cloned); err != nil {
		return handshake
	}
	return cloned
}

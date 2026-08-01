package plugins

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const IndexSchemaVersion = "1"

type IndexDocument struct {
	SchemaVersion string           `json:"schema_version"`
	Signed        IndexPayload     `json:"signed"`
	Signatures    []IndexSignature `json:"signatures"`
}

type IndexPayload struct {
	GeneratedAt        string         `json:"generated_at"`
	ExpiresAt          string         `json:"expires_at"`
	SignatureThreshold int            `json:"signature_threshold"`
	Plugins            []IndexRelease `json:"plugins"`
}

type IndexRelease struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Version         string          `json:"version"`
	Description     string          `json:"description"`
	ProtocolVersion string          `json:"protocol_version"`
	HostAPIVersion  string          `json:"host_api_version"`
	MetadataDigest  string          `json:"metadata_digest"`
	Capabilities    []string        `json:"capabilities"`
	Artifacts       []IndexArtifact `json:"artifacts"`
}

type IndexArtifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type IndexSignature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

type VerifiedIndex struct {
	Document    IndexDocument `json:"document"`
	IndexDigest string        `json:"index_digest"`
	SignerIDs   []string      `json:"signer_ids"`
}

type SelectedRelease struct {
	Release  IndexRelease  `json:"release"`
	Artifact IndexArtifact `json:"artifact"`
}

func DecodeIndex(data []byte) (IndexDocument, error) {
	document, err := DecodeIndexDraft(data)
	if err != nil {
		return IndexDocument{}, err
	}
	if err := ValidateIndex(document); err != nil {
		return IndexDocument{}, err
	}
	return document, nil
}

func DecodeIndexDraft(data []byte) (IndexDocument, error) {
	if err := validateIndexDocumentShape(data); err != nil {
		return IndexDocument{}, wrapPluginError(ErrorInvalid, "decode plugin index", err)
	}
	var document IndexDocument
	if err := decodeStrictJSON(data, &document); err != nil {
		return IndexDocument{}, wrapPluginError(ErrorInvalid, "decode plugin index", err)
	}
	if err := ValidateIndexDraft(document); err != nil {
		return IndexDocument{}, err
	}
	return document, nil
}

func ValidateIndex(document IndexDocument) error {
	if err := ValidateIndexDraft(document); err != nil {
		return err
	}
	if len(document.Signatures) < document.Signed.SignatureThreshold {
		return pluginError(ErrorInvalid, "validate plugin index", "signature count is below signature_threshold")
	}
	return nil
}

func ValidateIndexDraft(document IndexDocument) error {
	if document.SchemaVersion != IndexSchemaVersion {
		return pluginError(ErrorInvalid, "validate plugin index", "unsupported schema_version %q", document.SchemaVersion)
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, document.Signed.GeneratedAt)
	if err != nil {
		return pluginError(ErrorInvalid, "validate plugin index", "generated_at must be RFC3339")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, document.Signed.ExpiresAt)
	if err != nil || !expiresAt.After(generatedAt) {
		return pluginError(ErrorInvalid, "validate plugin index", "expires_at must be RFC3339 and later than generated_at")
	}
	if document.Signed.SignatureThreshold <= 0 {
		return pluginError(ErrorInvalid, "validate plugin index", "signature_threshold must be positive")
	}
	if document.Signed.Plugins == nil || document.Signatures == nil {
		return pluginError(ErrorInvalid, "validate plugin index", "plugins and signatures must be arrays")
	}
	releases := map[string]bool{}
	for releaseIndex, release := range document.Signed.Plugins {
		if !pluginIDPattern.MatchString(release.ID) || !semverPattern.MatchString(release.Version) {
			return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d] has invalid identity", releaseIndex)
		}
		key := release.ID + "\x00" + release.Version
		if releases[key] {
			return pluginError(ErrorInvalid, "validate plugin index", "duplicate release %s@%s", release.ID, release.Version)
		}
		releases[key] = true
		if strings.TrimSpace(release.Name) == "" || strings.TrimSpace(release.Description) == "" {
			return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d] requires name and description", releaseIndex)
		}
		if release.ProtocolVersion != ProtocolVersion || release.HostAPIVersion != HostAPIVersion {
			return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d] is incompatible with this host", releaseIndex)
		}
		if !validDigest(release.MetadataDigest) {
			return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d] has invalid metadata digest", releaseIndex)
		}
		if _, err := validateCapabilities(release.Capabilities); err != nil {
			return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d]: %v", releaseIndex, err)
		}
		if len(release.Artifacts) == 0 {
			return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d] requires artifacts", releaseIndex)
		}
		platforms := map[string]bool{}
		for artifactIndex, artifact := range release.Artifacts {
			platform := artifact.OS + "/" + artifact.Arch
			if platforms[platform] {
				return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d] has duplicate platform %s", releaseIndex, platform)
			}
			platforms[platform] = true
			if !supportedIndexPlatform(artifact.OS, artifact.Arch) {
				return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d].artifacts[%d] has unsupported platform", releaseIndex, artifactIndex)
			}
			if artifact.Size <= 0 || artifact.Size > maxPluginBytes {
				return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d].artifacts[%d] has invalid size", releaseIndex, artifactIndex)
			}
			if !validDigest(artifact.Digest) {
				return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d].artifacts[%d] has invalid digest", releaseIndex, artifactIndex)
			}
			if err := validateIndexArtifactURL(artifact.URL); err != nil {
				return pluginError(ErrorInvalid, "validate plugin index", "plugins[%d].artifacts[%d]: %v", releaseIndex, artifactIndex, err)
			}
		}
	}
	signatures := map[string]bool{}
	for index, signature := range document.Signatures {
		if !validDigest(signature.KeyID) || signature.Algorithm != PublisherAlgorithm {
			return pluginError(ErrorInvalid, "validate plugin index", "signatures[%d] has invalid key metadata", index)
		}
		if signatures[signature.KeyID] {
			return pluginError(ErrorInvalid, "validate plugin index", "duplicate signature for %s", signature.KeyID)
		}
		signatures[signature.KeyID] = true
		decoded, err := base64.StdEncoding.DecodeString(signature.Signature)
		if err != nil || len(decoded) != ed25519.SignatureSize {
			return pluginError(ErrorInvalid, "validate plugin index", "signatures[%d] is not an Ed25519 signature", index)
		}
	}
	return nil
}

func VerifyIndex(document IndexDocument, publishers PublisherStore, now time.Time) (VerifiedIndex, error) {
	if err := ValidateIndex(document); err != nil {
		return VerifiedIndex{}, err
	}
	if err := ValidatePublishers(publishers); err != nil {
		return VerifiedIndex{}, err
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	generatedAt, _ := time.Parse(time.RFC3339Nano, document.Signed.GeneratedAt)
	expiresAt, _ := time.Parse(time.RFC3339Nano, document.Signed.ExpiresAt)
	if generatedAt.After(now.Add(5 * time.Minute)) {
		return VerifiedIndex{}, pluginError(ErrorBlocked, "verify plugin index", "index generated_at is in the future")
	}
	if !expiresAt.After(now) {
		return VerifiedIndex{}, pluginError(ErrorBlocked, "verify plugin index", "index is expired")
	}
	signingBytes, err := IndexSigningBytes(document.Signed)
	if err != nil {
		return VerifiedIndex{}, err
	}
	keys := map[string]ed25519.PublicKey{}
	for _, publisher := range publishers.Keys {
		if slicesContains(publishers.RevokedKeyIDs, publisher.KeyID) {
			continue
		}
		publicKey, err := decodePublisherPublicKey(publisher.PublicKey)
		if err != nil {
			return VerifiedIndex{}, err
		}
		keys[publisher.KeyID] = publicKey
	}
	var signerIDs []string
	for _, signature := range document.Signatures {
		publicKey, trusted := keys[signature.KeyID]
		if !trusted {
			continue
		}
		decoded, _ := base64.StdEncoding.DecodeString(signature.Signature)
		if ed25519.Verify(publicKey, signingBytes, decoded) {
			signerIDs = append(signerIDs, signature.KeyID)
		}
	}
	sort.Strings(signerIDs)
	if len(signerIDs) < document.Signed.SignatureThreshold {
		return VerifiedIndex{}, pluginError(
			ErrorBlocked,
			"verify plugin index",
			"valid trusted signatures %d are below threshold %d",
			len(signerIDs),
			document.Signed.SignatureThreshold,
		)
	}
	indexDigest, err := IndexDocumentDigest(document)
	if err != nil {
		return VerifiedIndex{}, err
	}
	return VerifiedIndex{Document: document, IndexDigest: indexDigest, SignerIDs: signerIDs}, nil
}

func IndexSigningBytes(payload IndexPayload) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, wrapPluginError(ErrorInternal, "encode plugin index signing payload", err)
	}
	return data, nil
}

func IndexDocumentDigest(document IndexDocument) (string, error) {
	data, err := json.Marshal(document)
	if err != nil {
		return "", wrapPluginError(ErrorInternal, "encode plugin index digest", err)
	}
	return digestBytes(data), nil
}

func SelectIndexRelease(index VerifiedIndex, id, version, goos, goarch string) (SelectedRelease, error) {
	if !pluginIDPattern.MatchString(id) || !semverPattern.MatchString(version) {
		return SelectedRelease{}, pluginError(ErrorInvalid, "select plugin release", "exact plugin id and semantic version are required")
	}
	for _, release := range index.Document.Signed.Plugins {
		if release.ID != id || release.Version != version {
			continue
		}
		for _, artifact := range release.Artifacts {
			if artifact.OS == goos && artifact.Arch == goarch {
				return SelectedRelease{Release: release, Artifact: artifact}, nil
			}
		}
		return SelectedRelease{}, pluginError(ErrorMissing, "select plugin release", "no artifact for %s/%s", goos, goarch)
	}
	return SelectedRelease{}, pluginError(ErrorMissing, "select plugin release", "release %s@%s is not in the verified index", id, version)
}

func validateIndexDocumentShape(data []byte) error {
	document, err := decodeJSONObject(data, "plugin index")
	if err != nil {
		return err
	}
	if err := requireFields(document, "plugin index", "schema_version", "signed", "signatures"); err != nil {
		return err
	}
	signed, err := decodeJSONObject(document["signed"], "plugin index signed payload")
	if err != nil {
		return err
	}
	if err := requireFields(signed, "plugin index signed payload", "generated_at", "expires_at", "signature_threshold", "plugins"); err != nil {
		return err
	}
	return nil
}

func validateIndexArtifactURL(value string) error {
	if strings.TrimSpace(value) == "" || strings.Contains(value, "\\") {
		return fmt.Errorf("artifact URL is required and must use forward slashes")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid artifact URL")
	}
	switch parsed.Scheme {
	case "":
		if parsed.IsAbs() || strings.HasPrefix(parsed.Path, "/") || parsed.Host != "" {
			return fmt.Errorf("local artifact URL must be relative")
		}
		if strings.ContainsAny(value, "\\:%") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("relative artifact URL must be a plain slash-separated path")
		}
		clean := pathCleanSlash(parsed.Path)
		if clean == "" || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("relative artifact URL escapes its index root")
		}
	case "https":
		if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return fmt.Errorf("HTTPS artifact URL must not contain credentials or a fragment")
		}
	default:
		return fmt.Errorf("artifact URL must be relative or HTTPS")
	}
	return nil
}

func supportedIndexPlatform(goos, goarch string) bool {
	return (goos == "darwin" || goos == "linux") && (goarch == "amd64" || goarch == "arm64")
}

func pathCleanSlash(value string) string {
	parts := strings.Split(value, "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return ".."
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	return strings.Join(stack, "/")
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

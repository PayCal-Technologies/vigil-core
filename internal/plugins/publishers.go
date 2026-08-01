package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	PublisherSchemaVersion = "1"
	PublisherAlgorithm     = "ed25519"
	maxPublisherKeyBytes   = 4 * 1024
)

type PublisherStore struct {
	SchemaVersion string         `json:"schema_version"`
	Keys          []PublisherKey `json:"keys"`
	RevokedKeyIDs []string       `json:"revoked_key_ids"`
}

type PublisherKey struct {
	KeyID      string `json:"key_id"`
	Name       string `json:"name"`
	Algorithm  string `json:"algorithm"`
	PublicKey  string `json:"public_key"`
	ApprovedAt string `json:"approved_at"`
}

type PublisherTrustResult struct {
	Action        string       `json:"action"`
	Key           PublisherKey `json:"key"`
	PublisherPath string       `json:"publisher_path"`
}

type PublisherRevokeResult struct {
	KeyID         string `json:"key_id"`
	Revoked       bool   `json:"revoked"`
	PublisherPath string `json:"publisher_path"`
}

func ReadPublishers(path string) (PublisherStore, error) {
	store := PublisherStore{
		SchemaVersion: PublisherSchemaVersion,
		Keys:          []PublisherKey{},
		RevokedKeyIDs: []string{},
	}
	if err := readState(path, &store); errors.Is(err, os.ErrNotExist) {
		return store, nil
	} else if err != nil {
		return PublisherStore{}, wrapPluginError(ErrorInvalid, "read plugin publisher store", err)
	}
	if err := ValidatePublishers(store); err != nil {
		return PublisherStore{}, err
	}
	canonicalizePublishers(&store)
	return store, nil
}

func WritePublishers(path string, store PublisherStore) error {
	canonicalizePublishers(&store)
	if err := ValidatePublishers(store); err != nil {
		return err
	}
	return writeState(path, store, 0o600)
}

func ValidatePublishers(store PublisherStore) error {
	if store.SchemaVersion != PublisherSchemaVersion {
		return pluginError(ErrorInvalid, "validate plugin publisher store", "unsupported schema_version %q", store.SchemaVersion)
	}
	if store.Keys == nil || store.RevokedKeyIDs == nil {
		return pluginError(ErrorInvalid, "validate plugin publisher store", "keys and revoked_key_ids must be arrays")
	}
	seen := map[string]bool{}
	for _, key := range store.Keys {
		if seen[key.KeyID] {
			return pluginError(ErrorInvalid, "validate plugin publisher store", "duplicate key id %s", key.KeyID)
		}
		seen[key.KeyID] = true
		if strings.TrimSpace(key.Name) == "" || key.Algorithm != PublisherAlgorithm {
			return pluginError(ErrorInvalid, "validate plugin publisher store", "invalid publisher key metadata")
		}
		publicKey, err := decodePublisherPublicKey(key.PublicKey)
		if err != nil || publisherKeyID(publicKey) != key.KeyID {
			return pluginError(ErrorInvalid, "validate plugin publisher store", "invalid publisher key %s", key.KeyID)
		}
		if _, err := time.Parse(time.RFC3339Nano, key.ApprovedAt); err != nil {
			return pluginError(ErrorInvalid, "validate plugin publisher store", "invalid approval timestamp for %s", key.KeyID)
		}
	}
	if err := validateUniqueStrings("revoked_key_ids", store.RevokedKeyIDs, validDigest); err != nil {
		return pluginError(ErrorInvalid, "validate plugin publisher store", "%v", err)
	}
	return nil
}

func TrustPublisher(layout Layout, name string, encodedKey []byte, approvedAt time.Time, restore bool) (PublisherTrustResult, error) {
	publicKey, err := ParsePublisherPublicKey(encodedKey)
	if err != nil {
		return PublisherTrustResult{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return PublisherTrustResult{}, pluginError(ErrorInvalid, "trust plugin publisher", "publisher name is required")
	}
	store, err := ReadPublishers(layout.PublisherPath)
	if err != nil {
		return PublisherTrustResult{}, err
	}
	keyID := publisherKeyID(publicKey)
	if slices.Contains(store.RevokedKeyIDs, keyID) && !restore {
		return PublisherTrustResult{}, pluginError(ErrorBlocked, "trust plugin publisher", "key is revoked; --restore-trust is required")
	}
	if restore {
		store.RevokedKeyIDs = removeString(store.RevokedKeyIDs, keyID)
	}
	approvedAt = approvedAt.UTC()
	if approvedAt.IsZero() {
		approvedAt = time.Now().UTC()
	}
	record := PublisherKey{
		KeyID:      keyID,
		Name:       name,
		Algorithm:  PublisherAlgorithm,
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		ApprovedAt: approvedAt.Format(time.RFC3339Nano),
	}
	action := "trusted"
	for index, existing := range store.Keys {
		if existing.KeyID != keyID {
			continue
		}
		record.ApprovedAt = existing.ApprovedAt
		store.Keys[index] = record
		action = "unchanged"
		if existing.Name != record.Name {
			action = "updated"
		}
		if err := WritePublishers(layout.PublisherPath, store); err != nil {
			return PublisherTrustResult{}, err
		}
		return PublisherTrustResult{Action: action, Key: record, PublisherPath: layout.PublisherPath}, nil
	}
	store.Keys = append(store.Keys, record)
	if err := WritePublishers(layout.PublisherPath, store); err != nil {
		return PublisherTrustResult{}, err
	}
	return PublisherTrustResult{Action: action, Key: record, PublisherPath: layout.PublisherPath}, nil
}

func TrustPublisherFile(layout Layout, name, path string, approvedAt time.Time, restore bool) (PublisherTrustResult, error) {
	data, err := readBoundedRegularFile(path, maxPublisherKeyBytes)
	if err != nil {
		return PublisherTrustResult{}, err
	}
	return TrustPublisher(layout, name, data, approvedAt, restore)
}

func RevokePublisher(layout Layout, keyID string) (PublisherRevokeResult, error) {
	if !validDigest(keyID) {
		return PublisherRevokeResult{}, pluginError(ErrorInvalid, "revoke plugin publisher", "invalid key id")
	}
	store, err := ReadPublishers(layout.PublisherPath)
	if err != nil {
		return PublisherRevokeResult{}, err
	}
	found := false
	for _, key := range store.Keys {
		if key.KeyID == keyID {
			found = true
			break
		}
	}
	if !found {
		return PublisherRevokeResult{}, pluginError(ErrorMissing, "revoke plugin publisher", "publisher key is not trusted")
	}
	if !slices.Contains(store.RevokedKeyIDs, keyID) {
		store.RevokedKeyIDs = append(store.RevokedKeyIDs, keyID)
	}
	if err := WritePublishers(layout.PublisherPath, store); err != nil {
		return PublisherRevokeResult{}, err
	}
	return PublisherRevokeResult{KeyID: keyID, Revoked: true, PublisherPath: layout.PublisherPath}, nil
}

func ParsePublisherPublicKey(data []byte) (ed25519.PublicKey, error) {
	encoded := strings.TrimSpace(string(data))
	encoded = strings.TrimSpace(strings.TrimPrefix(encoded, PublisherAlgorithm+":"))
	if encoded == "" || strings.ContainsAny(encoded, " \t\r\n") {
		return nil, pluginError(ErrorInvalid, "parse plugin publisher key", "expected one base64-encoded Ed25519 public key")
	}
	return decodePublisherPublicKey(encoded)
}

func decodePublisherPublicKey(encoded string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, wrapPluginError(ErrorInvalid, "parse plugin publisher key", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, pluginError(ErrorInvalid, "parse plugin publisher key", "Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(append([]byte(nil), decoded...)), nil
}

func publisherKeyID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return fmt.Sprintf("sha256:%x", sum)
}

func canonicalizePublishers(store *PublisherStore) {
	if store.Keys == nil {
		store.Keys = []PublisherKey{}
	}
	if store.RevokedKeyIDs == nil {
		store.RevokedKeyIDs = []string{}
	}
	sort.Slice(store.Keys, func(i, j int) bool {
		return store.Keys[i].KeyID < store.Keys[j].KeyID
	})
	sort.Strings(store.RevokedKeyIDs)
}

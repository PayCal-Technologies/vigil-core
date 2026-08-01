package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
)

const (
	LockFilename      = "vigil.plugins.lock.json"
	TrustFilename     = "trust-v1.json"
	PublisherFilename = "publishers-v1.json"
	maxStateBytes     = 4 * 1024 * 1024
)

type Layout struct {
	Root          string `json:"root"`
	LockPath      string `json:"lock_path"`
	TrustPath     string `json:"trust_path"`
	PublisherPath string `json:"publisher_path"`
}

type Lockfile struct {
	SchemaVersion string         `json:"schema_version"`
	Plugins       []LockedPlugin `json:"plugins"`
}

type LockedPlugin struct {
	ID                 string   `json:"id"`
	Version            string   `json:"version"`
	Digest             string   `json:"digest"`
	MetadataDigest     string   `json:"metadata_digest"`
	ProtocolVersion    string   `json:"protocol_version"`
	HostAPIVersion     string   `json:"host_api_version"`
	Capabilities       []string `json:"capabilities"`
	Commands           []string `json:"commands"`
	InstalledBinding   string   `json:"installed_binding"`
	Acquisition        string   `json:"acquisition"`
	IndexDigest        string   `json:"index_digest"`
	PublisherKeyIDs    []string `json:"publisher_key_ids"`
	SignatureThreshold int      `json:"signature_threshold"`
}

type TrustStore struct {
	SchemaVersion  string        `json:"schema_version"`
	Records        []TrustRecord `json:"records"`
	RevokedDigests []string      `json:"revoked_digests"`
}

type TrustRecord struct {
	ID                 string   `json:"id"`
	Version            string   `json:"version"`
	Digest             string   `json:"digest"`
	MetadataDigest     string   `json:"metadata_digest"`
	Capabilities       []string `json:"capabilities"`
	Acquisition        string   `json:"acquisition"`
	IndexDigest        string   `json:"index_digest"`
	PublisherKeyIDs    []string `json:"publisher_key_ids"`
	SignatureThreshold int      `json:"signature_threshold"`
	ApprovedAt         string   `json:"approved_at"`
}

func DefaultUserRoot() string {
	if root := strings.TrimSpace(os.Getenv("VIGIL_PLUGIN_ROOT")); root != "" {
		return filepath.Clean(root)
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configRoot, "vigil", "plugins")
}

func NewLayout(userRoot, repositoryRoot string) (Layout, error) {
	if strings.TrimSpace(userRoot) == "" {
		userRoot = DefaultUserRoot()
	}
	if strings.TrimSpace(userRoot) == "" {
		return Layout{}, pluginError(ErrorInvalid, "plugin layout", "user plugin root is unavailable")
	}
	absoluteRoot, err := filepath.Abs(userRoot)
	if err != nil {
		return Layout{}, wrapPluginError(ErrorInvalid, "plugin layout", err)
	}
	absoluteRepository, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return Layout{}, wrapPluginError(ErrorInvalid, "plugin layout", err)
	}
	return Layout{
		Root:          absoluteRoot,
		LockPath:      filepath.Join(absoluteRepository, LockFilename),
		TrustPath:     filepath.Join(absoluteRoot, TrustFilename),
		PublisherPath: filepath.Join(absoluteRoot, PublisherFilename),
	}, nil
}

func ExecutablePath(root, id, version string) (string, error) {
	if !pluginIDPattern.MatchString(id) || !semverPattern.MatchString(version) {
		return "", pluginError(ErrorInvalid, "plugin path", "invalid plugin id or version")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", wrapPluginError(ErrorInvalid, "plugin path", err)
	}
	candidate := filepath.Join(absoluteRoot, id, version, "vigil-plugin-"+id)
	relative, err := filepath.Rel(absoluteRoot, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", pluginError(ErrorBlocked, "plugin path", "resolved path escapes plugin root")
	}
	return candidate, nil
}

func FileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, maxPluginBytes+1)); err != nil {
		return "", err
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > maxPluginBytes {
		return "", fmt.Errorf("plugin exceeds %d bytes", maxPluginBytes)
	}
	return fmt.Sprintf("sha256:%x", hasher.Sum(nil)), nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}

func ReadLock(path string) (Lockfile, error) {
	lock := Lockfile{SchemaVersion: LockSchemaVersion, Plugins: []LockedPlugin{}}
	if err := readState(path, &lock); errors.Is(err, os.ErrNotExist) {
		return lock, nil
	} else if err != nil {
		return Lockfile{}, wrapPluginError(ErrorInvalid, "read plugin lockfile", err)
	}
	if err := ValidateLock(lock); err != nil {
		return Lockfile{}, err
	}
	canonicalizeLock(&lock)
	return lock, nil
}

func ReadTrust(path string) (TrustStore, error) {
	trust := TrustStore{SchemaVersion: TrustSchemaVersion, Records: []TrustRecord{}, RevokedDigests: []string{}}
	if err := readState(path, &trust); errors.Is(err, os.ErrNotExist) {
		return trust, nil
	} else if err != nil {
		return TrustStore{}, wrapPluginError(ErrorInvalid, "read plugin trust store", err)
	}
	if err := ValidateTrust(trust); err != nil {
		return TrustStore{}, err
	}
	canonicalizeTrust(&trust)
	return trust, nil
}

func ValidateLock(lock Lockfile) error {
	if lock.SchemaVersion != LockSchemaVersion {
		return pluginError(ErrorInvalid, "validate plugin lockfile", "unsupported schema_version %q", lock.SchemaVersion)
	}
	if lock.Plugins == nil {
		return pluginError(ErrorInvalid, "validate plugin lockfile", "plugins must be an array")
	}
	seen := map[string]bool{}
	for _, plugin := range lock.Plugins {
		if !pluginIDPattern.MatchString(plugin.ID) || !semverPattern.MatchString(plugin.Version) {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "invalid plugin identity")
		}
		if seen[plugin.ID] {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "duplicate plugin id %q", plugin.ID)
		}
		seen[plugin.ID] = true
		if !validDigest(plugin.Digest) || !validDigest(plugin.MetadataDigest) {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "invalid digest for %s", plugin.ID)
		}
		if plugin.ProtocolVersion != ProtocolVersion || plugin.HostAPIVersion != HostAPIVersion {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "incompatible plugin %s", plugin.ID)
		}
		if plugin.Capabilities == nil || plugin.Commands == nil || len(plugin.Commands) == 0 {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "plugin %s has incomplete metadata", plugin.ID)
		}
		if _, err := validateCapabilities(plugin.Capabilities); err != nil {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "%s: %v", plugin.ID, err)
		}
		if err := validateUniqueStrings("commands", plugin.Commands, func(value string) bool {
			return commandPattern.MatchString(value) && strings.HasPrefix(value, plugin.ID+":")
		}); err != nil {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "%s: %v", plugin.ID, err)
		}
		expectedBinding := pluginBinding(plugin.ID, plugin.Version, plugin.Digest)
		if plugin.InstalledBinding != expectedBinding {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "plugin %s binding mismatch", plugin.ID)
		}
		if err := validateAcquisition(
			plugin.Acquisition,
			plugin.IndexDigest,
			plugin.PublisherKeyIDs,
			plugin.SignatureThreshold,
		); err != nil {
			return pluginError(ErrorInvalid, "validate plugin lockfile", "%s: %v", plugin.ID, err)
		}
	}
	return nil
}

func ValidateTrust(trust TrustStore) error {
	if trust.SchemaVersion != TrustSchemaVersion {
		return pluginError(ErrorInvalid, "validate plugin trust store", "unsupported schema_version %q", trust.SchemaVersion)
	}
	if trust.Records == nil || trust.RevokedDigests == nil {
		return pluginError(ErrorInvalid, "validate plugin trust store", "records and revoked_digests must be arrays")
	}
	seen := map[string]bool{}
	for _, record := range trust.Records {
		key := record.ID + "\x00" + record.Version + "\x00" + record.Digest
		if seen[key] {
			return pluginError(ErrorInvalid, "validate plugin trust store", "duplicate trust record")
		}
		seen[key] = true
		if !pluginIDPattern.MatchString(record.ID) || !semverPattern.MatchString(record.Version) ||
			!validDigest(record.Digest) || !validDigest(record.MetadataDigest) {
			return pluginError(ErrorInvalid, "validate plugin trust store", "invalid trust record")
		}
		if _, err := validateCapabilities(record.Capabilities); err != nil {
			return pluginError(ErrorInvalid, "validate plugin trust store", "%s: %v", record.ID, err)
		}
		if _, err := time.Parse(time.RFC3339Nano, record.ApprovedAt); err != nil {
			return pluginError(ErrorInvalid, "validate plugin trust store", "invalid approval timestamp")
		}
		if err := validateAcquisition(
			record.Acquisition,
			record.IndexDigest,
			record.PublisherKeyIDs,
			record.SignatureThreshold,
		); err != nil {
			return pluginError(ErrorInvalid, "validate plugin trust store", "%s: %v", record.ID, err)
		}
	}
	if err := validateUniqueStrings("revoked_digests", trust.RevokedDigests, validDigest); err != nil {
		return pluginError(ErrorInvalid, "validate plugin trust store", "%v", err)
	}
	return nil
}

func WriteLock(path string, lock Lockfile) error {
	canonicalizeLock(&lock)
	if err := ValidateLock(lock); err != nil {
		return err
	}
	return writeState(path, lock, 0o644)
}

func WriteTrust(path string, trust TrustStore) error {
	canonicalizeTrust(&trust)
	if err := ValidateTrust(trust); err != nil {
		return err
	}
	return writeState(path, trust, 0o600)
}

func pluginBinding(id, version, digest string) string {
	return "plugin:" + id + "@" + version + "#" + digest
}

func readState(path string, destination any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("state file must be a regular non-symlink file")
	}
	if info.Size() > maxStateBytes {
		return fmt.Errorf("state file exceeds %d bytes", maxStateBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeStrictJSON(data, destination)
}

func writeState(path string, value any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return wrapPluginError(ErrorInternal, "create plugin state directory", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return wrapPluginError(ErrorInternal, "encode plugin state", err)
	}
	data = append(data, '\n')
	_, err = atomicfile.Write(path, data, atomicfile.Options{DefaultMode: mode, PreserveExistingMode: false})
	if err != nil {
		return wrapPluginError(ErrorInternal, "write plugin state", err)
	}
	return nil
}

func canonicalizeLock(lock *Lockfile) {
	if lock.Plugins == nil {
		lock.Plugins = []LockedPlugin{}
	}
	for index := range lock.Plugins {
		sort.Strings(lock.Plugins[index].Capabilities)
		sort.Strings(lock.Plugins[index].Commands)
		sort.Strings(lock.Plugins[index].PublisherKeyIDs)
	}
	sort.Slice(lock.Plugins, func(i, j int) bool {
		return lock.Plugins[i].ID < lock.Plugins[j].ID
	})
}

func canonicalizeTrust(trust *TrustStore) {
	if trust.Records == nil {
		trust.Records = []TrustRecord{}
	}
	if trust.RevokedDigests == nil {
		trust.RevokedDigests = []string{}
	}
	for index := range trust.Records {
		sort.Strings(trust.Records[index].Capabilities)
		sort.Strings(trust.Records[index].PublisherKeyIDs)
	}
	sort.Slice(trust.Records, func(i, j int) bool {
		left := trust.Records[i]
		right := trust.Records[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		if left.Version != right.Version {
			return left.Version < right.Version
		}
		return left.Digest < right.Digest
	})
	sort.Strings(trust.RevokedDigests)
}

func validateAcquisition(acquisition, indexDigest string, publisherKeyIDs []string, signatureThreshold int) error {
	if publisherKeyIDs == nil {
		return fmt.Errorf("publisher_key_ids must be an array")
	}
	switch acquisition {
	case "local":
		if indexDigest != "" || len(publisherKeyIDs) != 0 || signatureThreshold != 0 {
			return fmt.Errorf("local acquisition cannot declare signed-index provenance")
		}
	case "signed-index":
		if !validDigest(indexDigest) {
			return fmt.Errorf("signed-index acquisition requires an index digest")
		}
		if signatureThreshold <= 0 || signatureThreshold > len(publisherKeyIDs) {
			return fmt.Errorf("signed-index acquisition has an invalid signature threshold")
		}
		if err := validateUniqueStrings("publisher_key_ids", publisherKeyIDs, validDigest); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported acquisition %q", acquisition)
	}
	return nil
}

func stateSnapshot(path string) ([]byte, os.FileMode, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, false, err
	}
	return bytes.Clone(data), info.Mode().Perm(), true, nil
}

func restoreSnapshot(path string, data []byte, mode os.FileMode, existed bool) {
	if !existed {
		_ = os.Remove(path)
		return
	}
	_, _ = atomicfile.Write(path, data, atomicfile.Options{DefaultMode: mode})
}

package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/atomicfile"
	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

type Issue struct {
	PluginID string `json:"plugin_id,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	ExitCode int    `json:"exit_code"`
}

type PluginStatus struct {
	ID                 string   `json:"id"`
	Version            string   `json:"version"`
	Digest             string   `json:"digest"`
	MetadataDigest     string   `json:"metadata_digest"`
	Binding            string   `json:"binding"`
	Capabilities       []string `json:"capabilities"`
	Commands           []string `json:"commands"`
	Installed          bool     `json:"installed"`
	DigestVerified     bool     `json:"digest_verified"`
	MetadataVerified   bool     `json:"metadata_verified"`
	Trusted            bool     `json:"trusted"`
	Revoked            bool     `json:"revoked"`
	Acquisition        string   `json:"acquisition"`
	IndexDigest        string   `json:"index_digest"`
	PublisherKeyIDs    []string `json:"publisher_key_ids"`
	SignatureThreshold int      `json:"signature_threshold"`
	Status             string   `json:"status"`
}

type Discovery struct {
	SchemaVersion string            `json:"schema_version"`
	Status        string            `json:"status"`
	Layout        Layout            `json:"layout"`
	Plugins       []PluginStatus    `json:"plugins"`
	Available     []InstalledPlugin `json:"-"`
	Issues        []Issue           `json:"issues"`
}

type InstallOptions struct {
	Layout                 Layout
	Candidate              string
	Approved               []string
	ApproveAll             bool
	RestoreTrust           bool
	Update                 bool
	ApprovalTime           time.Time
	ExpectedPluginID       string
	ExpectedVersion        string
	ExpectedDigest         string
	ExpectedMetadataDigest string
	ExpectedCapabilities   []string
	Acquisition            string
	IndexDigest            string
	PublisherKeyIDs        []string
	SignatureThreshold     int
	Policy                 *Policy
}

type InstallResult struct {
	Action         string        `json:"action"`
	Plugin         PluginStatus  `json:"plugin"`
	ExecutablePath string        `json:"executable_path"`
	LockPath       string        `json:"lock_path"`
	TrustPath      string        `json:"trust_path"`
	Handshake      Metadata      `json:"metadata"`
	Warnings       []string      `json:"warnings"`
	Previous       *LockedPlugin `json:"previous,omitempty"`
	Trust          *TrustRecord  `json:"trust_record,omitempty"`
	Lock           *LockedPlugin `json:"lock_record,omitempty"`
}

type RemoveOptions struct {
	Layout  Layout
	ID      string
	Version string
	Revoke  bool
}

type RemoveResult struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Digest     string `json:"digest"`
	Revoked    bool   `json:"revoked"`
	Removed    bool   `json:"removed"`
	LockPath   string `json:"lock_path"`
	TrustPath  string `json:"trust_path"`
	Executable string `json:"executable"`
}

func Discover(ctx context.Context, layout Layout) Discovery {
	return DiscoverWithPolicy(ctx, layout, DefaultPolicy())
}

func DiscoverWithPolicy(ctx context.Context, layout Layout, policy Policy) Discovery {
	report := Discovery{
		SchemaVersion: ProtocolVersion,
		Status:        "ok",
		Layout:        layout,
		Plugins:       []PluginStatus{},
		Available:     []InstalledPlugin{},
		Issues:        []Issue{},
	}
	if err := ValidatePolicy(policy); err != nil {
		report.AddIssue("", "VIGIL_PLUGIN_POLICY_INVALID", wrapPluginError(ErrorInvalid, "validate plugin policy", err))
		return report
	}
	lock, err := ReadLock(layout.LockPath)
	if err != nil {
		report.AddIssue("", "VIGIL_PLUGIN_LOCK_INVALID", err)
		return report
	}
	trust, err := ReadTrust(layout.TrustPath)
	if err != nil {
		report.AddIssue("", "VIGIL_PLUGIN_TRUST_INVALID", err)
		return report
	}
	var publishers PublisherStore
	var publisherErr error
	publishersLoaded := false
	for _, locked := range lock.Plugins {
		status := PluginStatus{
			ID:                 locked.ID,
			Version:            locked.Version,
			Digest:             locked.Digest,
			MetadataDigest:     locked.MetadataDigest,
			Binding:            locked.InstalledBinding,
			Capabilities:       append([]string{}, locked.Capabilities...),
			Commands:           append([]string{}, locked.Commands...),
			Acquisition:        locked.Acquisition,
			IndexDigest:        locked.IndexDigest,
			PublisherKeyIDs:    append([]string{}, locked.PublisherKeyIDs...),
			SignatureThreshold: locked.SignatureThreshold,
			Status:             "blocked",
		}
		if err := CheckPolicy(
			policy,
			locked.ID,
			locked.Capabilities,
			locked.Acquisition,
			locked.PublisherKeyIDs,
			locked.SignatureThreshold,
		); err != nil {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_POLICY_BLOCKED", err)
			continue
		}
		if slices.Contains(trust.RevokedDigests, locked.Digest) {
			status.Revoked = true
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_REVOKED", pluginError(ErrorBlocked, "discover plugin", "digest is locally revoked"))
			continue
		}
		record, trusted := matchingTrust(trust, locked)
		if !trusted {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_UNTRUSTED", pluginError(ErrorBlocked, "discover plugin", "no exact local trust record"))
			continue
		}
		status.Trusted = true
		if locked.Acquisition == "signed-index" {
			if !publishersLoaded {
				publishers, publisherErr = ReadPublishers(layout.PublisherPath)
				publishersLoaded = true
			}
			if publisherErr != nil {
				report.Plugins = append(report.Plugins, status)
				report.AddIssue(locked.ID, "VIGIL_PLUGIN_PUBLISHER_TRUST_INVALID", publisherErr)
				continue
			}
			if trustedPublisherCount(publishers, locked.PublisherKeyIDs) < locked.SignatureThreshold {
				report.Plugins = append(report.Plugins, status)
				report.AddIssue(
					locked.ID,
					"VIGIL_PLUGIN_PUBLISHER_THRESHOLD",
					pluginError(ErrorBlocked, "discover plugin", "trusted non-revoked publisher keys are below the locked threshold"),
				)
				continue
			}
		}
		path, pathErr := ExecutablePath(layout.Root, locked.ID, locked.Version)
		if pathErr != nil {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_PATH_INVALID", pathErr)
			continue
		}
		digest, digestErr := FileDigest(path)
		if errors.Is(digestErr, os.ErrNotExist) {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_MISSING", pluginError(ErrorMissing, "discover plugin", "installed executable is missing"))
			continue
		}
		if digestErr != nil {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_DIGEST_ERROR", wrapPluginError(ErrorInternal, "discover plugin", digestErr))
			continue
		}
		status.Installed = true
		if digest != locked.Digest {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_DIGEST_MISMATCH", pluginError(ErrorBlocked, "discover plugin", "installed digest differs from lockfile"))
			continue
		}
		status.DigestVerified = true
		handshake, handshakeDigest, handshakeErr := discoveryHandshake(ctx, path, digest, locked.MetadataDigest)
		if handshakeErr != nil {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_HANDSHAKE_FAILED", handshakeErr)
			continue
		}
		metadataDigest, digestErr := MetadataDigest(handshake.Plugin)
		if digestErr != nil {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_METADATA_DIGEST_ERROR", wrapPluginError(ErrorInternal, "discover plugin", digestErr))
			continue
		}
		if handshakeDigest != locked.Digest || handshake.Plugin.ID != locked.ID || handshake.Plugin.Version != locked.Version ||
			metadataDigest != locked.MetadataDigest || record.MetadataDigest != metadataDigest ||
			!slices.Equal(MetadataCapabilities(handshake.Plugin), locked.Capabilities) ||
			!slices.Equal(MetadataCapabilities(handshake.Plugin), record.Capabilities) ||
			!slices.Equal(MetadataCommandNames(handshake.Plugin), locked.Commands) {
			report.Plugins = append(report.Plugins, status)
			report.AddIssue(locked.ID, "VIGIL_PLUGIN_METADATA_MISMATCH", pluginError(ErrorBlocked, "discover plugin", "handshake metadata differs from lock or trust record"))
			continue
		}
		status.MetadataVerified = true
		status.Status = "ok"
		report.Plugins = append(report.Plugins, status)
		report.Available = append(report.Available, InstalledPlugin{
			Path:           path,
			Digest:         locked.Digest,
			MetadataDigest: metadataDigest,
			Metadata:       handshake.Plugin,
		})
	}
	sort.Slice(report.Plugins, func(i, j int) bool {
		return report.Plugins[i].ID < report.Plugins[j].ID
	})
	sort.Slice(report.Available, func(i, j int) bool {
		return report.Available[i].Metadata.ID < report.Available[j].Metadata.ID
	})
	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].PluginID != report.Issues[j].PluginID {
			return report.Issues[i].PluginID < report.Issues[j].PluginID
		}
		return report.Issues[i].Code < report.Issues[j].Code
	})
	return report
}

func Install(ctx context.Context, options InstallOptions) (InstallResult, error) {
	acquisition := strings.TrimSpace(options.Acquisition)
	if acquisition == "" {
		acquisition = "local"
	}
	publisherKeyIDs := append([]string{}, options.PublisherKeyIDs...)
	if publisherKeyIDs == nil {
		publisherKeyIDs = []string{}
	}
	if err := validateAcquisition(acquisition, options.IndexDigest, publisherKeyIDs, options.SignatureThreshold); err != nil {
		return InstallResult{}, pluginError(ErrorInvalid, "install plugin", "%v", err)
	}
	policy, err := NormalizePolicy(options.Policy)
	if err != nil {
		return InstallResult{}, wrapPluginError(ErrorInvalid, "validate plugin policy", err)
	}
	if err := CheckAcquisitionPolicy(policy, acquisition); err != nil {
		return InstallResult{}, err
	}
	handshake, digest, err := HandshakeExecutable(ctx, options.Candidate)
	if err != nil {
		return InstallResult{}, err
	}
	metadata := handshake.Plugin
	if options.ExpectedPluginID != "" && metadata.ID != options.ExpectedPluginID {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "expected plugin %q, got %q", options.ExpectedPluginID, metadata.ID)
	}
	if options.ExpectedVersion != "" && metadata.Version != options.ExpectedVersion {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "expected version %q, got %q", options.ExpectedVersion, metadata.Version)
	}
	if options.ExpectedDigest != "" && digest != options.ExpectedDigest {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "executable digest differs from the expected digest")
	}
	metadataDigest, err := MetadataDigest(metadata)
	if err != nil {
		return InstallResult{}, wrapPluginError(ErrorInternal, "install plugin", err)
	}
	capabilities := MetadataCapabilities(metadata)
	if options.ExpectedMetadataDigest != "" && metadataDigest != options.ExpectedMetadataDigest {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "metadata digest differs from the expected digest")
	}
	if options.ExpectedCapabilities != nil && !equalStringSets(capabilities, options.ExpectedCapabilities) {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "capabilities differ from the expected signed index")
	}
	if err := CheckPolicy(policy, metadata.ID, capabilities, acquisition, publisherKeyIDs, options.SignatureThreshold); err != nil {
		return InstallResult{}, err
	}
	if err := validateApproval(capabilities, options.Approved, options.ApproveAll); err != nil {
		return InstallResult{}, err
	}
	lock, err := ReadLock(options.Layout.LockPath)
	if err != nil {
		return InstallResult{}, err
	}
	trust, err := ReadTrust(options.Layout.TrustPath)
	if err != nil {
		return InstallResult{}, err
	}
	if slices.Contains(trust.RevokedDigests, digest) && !options.RestoreTrust {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "digest is revoked; --restore-trust is required")
	}
	existingIndex := lockedPluginIndex(lock, metadata.ID)
	var previous *LockedPlugin
	provenanceChanged := false
	if existingIndex >= 0 {
		copy := lock.Plugins[existingIndex]
		previous = &copy
		if copy.Version == metadata.Version && copy.Digest != digest {
			return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "version %s is immutable and already locked to another digest", metadata.Version)
		}
		if copy.Acquisition == "signed-index" && acquisition == "local" {
			return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "signed-index provenance cannot be downgraded to local; remove and reinstall after review")
		}
		provenanceChanged = copy.Acquisition != acquisition ||
			copy.IndexDigest != options.IndexDigest ||
			copy.SignatureThreshold != options.SignatureThreshold ||
			!equalStringSets(copy.PublisherKeyIDs, publisherKeyIDs)
		if !options.Update && (copy.Version != metadata.Version || copy.Digest != digest || provenanceChanged) {
			return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "plugin is already locked; use plugins:update")
		}
	} else if options.Update {
		return InstallResult{}, pluginError(ErrorMissing, "update plugin", "plugin %s is not locked", metadata.ID)
	}

	destination, err := ExecutablePath(options.Layout.Root, metadata.ID, metadata.Version)
	if err != nil {
		return InstallResult{}, err
	}
	if existingDigest, digestErr := FileDigest(destination); digestErr == nil && existingDigest != digest {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "destination contains a different executable")
	} else if digestErr != nil && !errors.Is(digestErr, os.ErrNotExist) {
		return InstallResult{}, wrapPluginError(ErrorInternal, "inspect plugin destination", digestErr)
	}
	candidateData, err := os.ReadFile(options.Candidate)
	if err != nil {
		return InstallResult{}, wrapPluginError(ErrorInternal, "read plugin candidate", err)
	}
	if candidateDigest := digestBytes(candidateData); candidateDigest != digest {
		return InstallResult{}, pluginError(ErrorBlocked, "install plugin", "candidate changed after its handshake")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return InstallResult{}, wrapPluginError(ErrorInternal, "create plugin directory", err)
	}
	if _, err := atomicfile.Write(destination, candidateData, atomicfile.Options{DefaultMode: 0o700}); err != nil {
		return InstallResult{}, wrapPluginError(ErrorInternal, "install plugin executable", err)
	}
	installedDigest, err := FileDigest(destination)
	if err != nil || installedDigest != digest {
		return InstallResult{}, pluginError(ErrorInternal, "install plugin", "post-install digest verification failed")
	}

	commands := MetadataCommandNames(metadata)
	locked := LockedPlugin{
		ID:                 metadata.ID,
		Version:            metadata.Version,
		Digest:             digest,
		MetadataDigest:     metadataDigest,
		ProtocolVersion:    ProtocolVersion,
		HostAPIVersion:     metadata.HostAPIVersion,
		Capabilities:       append([]string{}, capabilities...),
		Commands:           commands,
		InstalledBinding:   pluginBinding(metadata.ID, metadata.Version, digest),
		Acquisition:        acquisition,
		IndexDigest:        options.IndexDigest,
		PublisherKeyIDs:    append([]string{}, publisherKeyIDs...),
		SignatureThreshold: options.SignatureThreshold,
	}
	if existingIndex >= 0 {
		lock.Plugins[existingIndex] = locked
	} else {
		lock.Plugins = append(lock.Plugins, locked)
	}
	approvalTime := options.ApprovalTime.UTC()
	if approvalTime.IsZero() {
		approvalTime = time.Now().UTC()
	}
	record := TrustRecord{
		ID:                 metadata.ID,
		Version:            metadata.Version,
		Digest:             digest,
		MetadataDigest:     metadataDigest,
		Capabilities:       append([]string{}, capabilities...),
		Acquisition:        acquisition,
		IndexDigest:        options.IndexDigest,
		PublisherKeyIDs:    append([]string{}, publisherKeyIDs...),
		SignatureThreshold: options.SignatureThreshold,
		ApprovedAt:         approvalTime.Format(time.RFC3339Nano),
	}
	upsertTrust(&trust, record)
	if options.RestoreTrust {
		trust.RevokedDigests = removeString(trust.RevokedDigests, digest)
	}
	if err := writeStateTransaction(options.Layout, lock, trust); err != nil {
		return InstallResult{}, err
	}
	action := "installed"
	if previous != nil && (previous.Version != locked.Version || previous.Digest != locked.Digest || provenanceChanged) {
		action = "updated"
	} else if previous != nil {
		action = "unchanged"
	}
	status := PluginStatus{
		ID: metadata.ID, Version: metadata.Version, Digest: digest, MetadataDigest: metadataDigest,
		Binding: locked.InstalledBinding, Capabilities: capabilities, Commands: commands,
		Installed: true, DigestVerified: true, MetadataVerified: true, Trusted: true, Status: "ok",
		Acquisition: acquisition, IndexDigest: options.IndexDigest,
		PublisherKeyIDs: append([]string{}, publisherKeyIDs...), SignatureThreshold: options.SignatureThreshold,
	}
	return InstallResult{
		Action: action, Plugin: status, ExecutablePath: destination,
		LockPath: options.Layout.LockPath, TrustPath: options.Layout.TrustPath,
		Handshake: metadata, Previous: previous, Trust: &record, Lock: &locked,
		Warnings: []string{},
	}, nil
}

func Remove(options RemoveOptions) (RemoveResult, error) {
	if !pluginIDPattern.MatchString(options.ID) {
		return RemoveResult{}, pluginError(ErrorInvalid, "remove plugin", "invalid plugin id")
	}
	lock, err := ReadLock(options.Layout.LockPath)
	if err != nil {
		return RemoveResult{}, err
	}
	trust, err := ReadTrust(options.Layout.TrustPath)
	if err != nil {
		return RemoveResult{}, err
	}
	index := lockedPluginIndex(lock, options.ID)
	if index < 0 {
		return RemoveResult{}, pluginError(ErrorMissing, "remove plugin", "plugin %s is not locked", options.ID)
	}
	locked := lock.Plugins[index]
	if options.Version != "" && options.Version != locked.Version {
		return RemoveResult{}, pluginError(ErrorMissing, "remove plugin", "locked version is %s", locked.Version)
	}
	executable, err := ExecutablePath(options.Layout.Root, locked.ID, locked.Version)
	if err != nil {
		return RemoveResult{}, err
	}
	if digest, digestErr := FileDigest(executable); digestErr == nil && digest != locked.Digest {
		return RemoveResult{}, pluginError(ErrorBlocked, "remove plugin", "installed executable digest differs; refusing deletion")
	} else if digestErr != nil && !errors.Is(digestErr, os.ErrNotExist) {
		return RemoveResult{}, wrapPluginError(ErrorInternal, "remove plugin", digestErr)
	}
	lock.Plugins = append(lock.Plugins[:index], lock.Plugins[index+1:]...)
	trust.Records = removeTrustRecords(trust.Records, locked)
	if options.Revoke && !slices.Contains(trust.RevokedDigests, locked.Digest) {
		trust.RevokedDigests = append(trust.RevokedDigests, locked.Digest)
	}
	if err := writeStateTransaction(options.Layout, lock, trust); err != nil {
		return RemoveResult{}, err
	}
	if err := os.Remove(executable); err != nil && !errors.Is(err, os.ErrNotExist) {
		return RemoveResult{}, wrapPluginError(ErrorInternal, "remove plugin executable", err)
	}
	_ = os.Remove(filepath.Dir(executable))
	_ = os.Remove(filepath.Dir(filepath.Dir(executable)))
	return RemoveResult{
		ID: locked.ID, Version: locked.Version, Digest: locked.Digest,
		Revoked: options.Revoke, Removed: true, LockPath: options.Layout.LockPath,
		TrustPath: options.Layout.TrustPath, Executable: executable,
	}, nil
}

func (report *Discovery) AddIssue(pluginID, code string, err error) {
	report.Status = "fail"
	report.Issues = append(report.Issues, Issue{
		PluginID: pluginID,
		Code:     code,
		Message:  err.Error(),
		ExitCode: ExitCode(err),
	})
}

func DiscoveryExit(report Discovery) int {
	exitCode := cli.ExitSuccess
	for _, issue := range report.Issues {
		classified := cli.ClassifyExit(issue.ExitCode).Code
		if classified > exitCode {
			exitCode = classified
		}
	}
	return exitCode
}

func matchingTrust(trust TrustStore, locked LockedPlugin) (TrustRecord, bool) {
	for _, record := range trust.Records {
		if record.ID == locked.ID && record.Version == locked.Version && record.Digest == locked.Digest &&
			record.MetadataDigest == locked.MetadataDigest &&
			record.Acquisition == locked.Acquisition &&
			record.IndexDigest == locked.IndexDigest &&
			record.SignatureThreshold == locked.SignatureThreshold &&
			equalStringSets(record.Capabilities, locked.Capabilities) &&
			equalStringSets(record.PublisherKeyIDs, locked.PublisherKeyIDs) {
			return record, true
		}
	}
	return TrustRecord{}, false
}

func trustedPublisherCount(store PublisherStore, keyIDs []string) int {
	trusted := map[string]bool{}
	for _, key := range store.Keys {
		if !slices.Contains(store.RevokedKeyIDs, key.KeyID) {
			trusted[key.KeyID] = true
		}
	}
	count := 0
	for _, keyID := range keyIDs {
		if trusted[keyID] {
			count++
		}
	}
	return count
}

func equalStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}

func lockedPluginIndex(lock Lockfile, id string) int {
	for index, plugin := range lock.Plugins {
		if plugin.ID == id {
			return index
		}
	}
	return -1
}

func validateApproval(required, approved []string, approveAll bool) error {
	if approveAll {
		return nil
	}
	approvedSet := map[string]bool{}
	for _, capability := range approved {
		approvedSet[strings.TrimSpace(capability)] = true
	}
	var missing []string
	for _, capability := range required {
		if !approvedSet[capability] {
			missing = append(missing, capability)
		}
	}
	if len(missing) > 0 {
		return pluginError(ErrorBlocked, "approve plugin capabilities", "missing explicit approval for: %s", strings.Join(missing, ", "))
	}
	return nil
}

func upsertTrust(trust *TrustStore, record TrustRecord) {
	for index, existing := range trust.Records {
		if existing.ID == record.ID && existing.Version == record.Version {
			trust.Records[index] = record
			return
		}
	}
	trust.Records = append(trust.Records, record)
}

func removeTrustRecords(records []TrustRecord, locked LockedPlugin) []TrustRecord {
	out := make([]TrustRecord, 0, len(records))
	for _, record := range records {
		if record.ID == locked.ID && record.Version == locked.Version && record.Digest == locked.Digest {
			continue
		}
		out = append(out, record)
	}
	return out
}

func removeString(values []string, remove string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}

func writeStateTransaction(layout Layout, lock Lockfile, trust TrustStore) error {
	trustData, trustMode, trustExisted, err := stateSnapshot(layout.TrustPath)
	if err != nil {
		return wrapPluginError(ErrorInternal, "snapshot plugin trust store", err)
	}
	if err := WriteTrust(layout.TrustPath, trust); err != nil {
		return err
	}
	if err := WriteLock(layout.LockPath, lock); err != nil {
		restoreSnapshot(layout.TrustPath, trustData, trustMode, trustExisted)
		return err
	}
	return nil
}

func BindingFor(plugin InstalledPlugin) string {
	return pluginBinding(plugin.Metadata.ID, plugin.Metadata.Version, plugin.Digest)
}

func CommandFor(plugin InstalledPlugin, name string) (Command, bool) {
	for _, command := range plugin.Metadata.Commands {
		if command.Name == name || slices.Contains(command.Aliases, name) {
			return command, true
		}
	}
	return Command{}, false
}

func FormatIssue(issue Issue) string {
	if issue.PluginID == "" {
		return issue.Code + ": " + issue.Message
	}
	return fmt.Sprintf("%s (%s): %s", issue.Code, issue.PluginID, issue.Message)
}

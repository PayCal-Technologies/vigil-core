package operationalevidence

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = "1"

var (
	repositoryPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	tagPattern         = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	versionPattern     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	criterionIDPattern = regexp.MustCompile(`^VIGIL-(SI|AC)-[0-9]{2}$`)
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var nativeReleaseSmokeJobs = []string{
	"sign-macos",
	"Native release smoke (linux-amd64)",
	"Native release smoke (linux-arm64)",
	"Native release smoke (darwin-amd64)",
	"Native release smoke (darwin-arm64)",
}

var stableHomebrewJobs = []string{
	"Homebrew draft install test (darwin-amd64)",
	"Homebrew draft install test (darwin-arm64)",
	"Publish verified release",
	"Homebrew public install test (darwin-amd64)",
	"Homebrew public install test (darwin-arm64)",
	"publish-homebrew",
}

type Report struct {
	SchemaVersion    string                 `json:"schema_version"`
	GeneratedAt      string                 `json:"generated_at"`
	Repository       string                 `json:"repository"`
	Tag              string                 `json:"tag"`
	Version          string                 `json:"version"`
	TapRepository    string                 `json:"tap_repository,omitempty"`
	PluginIndexURL   string                 `json:"plugin_index_url,omitempty"`
	AcceptanceLedger string                 `json:"acceptance_ledger"`
	Release          *ReleaseSummary        `json:"release,omitempty"`
	WorkflowRun      *WorkflowRunSummary    `json:"workflow_run,omitempty"`
	PluginIndex      *PluginIndexSummary    `json:"plugin_index,omitempty"`
	DownloadedAssets []DownloadedAsset      `json:"downloaded_assets,omitempty"`
	Criteria         []CriterionResult      `json:"criteria"`
	Commands         []CommandRecord        `json:"commands,omitempty"`
	Notes            map[string]interface{} `json:"notes,omitempty"`
}

type CriterionResult struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail"`
	Evidence []string `json:"evidence,omitempty"`
}

type CommandRecord struct {
	Name           string `json:"name"`
	Command        string `json:"command"`
	ExitCode       int    `json:"exit_code"`
	DurationMillis int64  `json:"duration_millis"`
	Stdout         string `json:"stdout,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
}

type ReleaseSummary struct {
	TagName         string         `json:"tag_name"`
	TargetCommitish string         `json:"target_commitish"`
	ResolvedCommit  string         `json:"resolved_commit,omitempty"`
	URL             string         `json:"url"`
	IsDraft         bool           `json:"is_draft"`
	IsPrerelease    bool           `json:"is_prerelease"`
	IsImmutable     bool           `json:"is_immutable"`
	PublishedAt     string         `json:"published_at,omitempty"`
	Assets          []ReleaseAsset `json:"assets"`
}

type ReleaseAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
	URL  string `json:"url,omitempty"`
}

type WorkflowRunSummary struct {
	DatabaseID int64        `json:"database_id"`
	URL        string       `json:"url"`
	Status     string       `json:"status"`
	Conclusion string       `json:"conclusion"`
	HeadSHA    string       `json:"head_sha"`
	Jobs       []JobSummary `json:"jobs"`
}

type JobSummary struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url,omitempty"`
}

type PluginIndexSummary struct {
	Source             string                 `json:"source"`
	IndexDigest        string                 `json:"index_digest"`
	CeremonyURL        string                 `json:"ceremony_url,omitempty"`
	SignatureThreshold int                    `json:"signature_threshold"`
	SignerIDs          []string               `json:"signer_ids"`
	PublisherKeys      []PublisherKeyEvidence `json:"publisher_keys,omitempty"`
	PluginCount        int                    `json:"plugin_count"`
	ArtifactCount      int                    `json:"artifact_count"`
	ArtifactsVerified  bool                   `json:"artifacts_verified"`
}

type PublisherKeyEvidence struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Source    string `json:"source"`
}

type DownloadedAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func Validate(report Report) error {
	if report.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported operational evidence schema_version %q", report.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, report.GeneratedAt); err != nil {
		return fmt.Errorf("invalid generated_at: %w", err)
	}
	if !repositoryPattern.MatchString(report.Repository) {
		return fmt.Errorf("invalid repository %q", report.Repository)
	}
	if !tagPattern.MatchString(report.Tag) {
		return fmt.Errorf("invalid tag %q", report.Tag)
	}
	if !versionPattern.MatchString(report.Version) || "v"+report.Version != report.Tag {
		return fmt.Errorf("version %q does not match tag %q", report.Version, report.Tag)
	}
	if report.TapRepository != "" && !repositoryPattern.MatchString(report.TapRepository) {
		return fmt.Errorf("invalid tap_repository %q", report.TapRepository)
	}
	if strings.TrimSpace(report.PluginIndexURL) != "" {
		if err := validateHTTPSURL("plugin_index_url", report.PluginIndexURL); err != nil {
			return err
		}
	}
	if strings.TrimSpace(report.AcceptanceLedger) == "" {
		return fmt.Errorf("acceptance_ledger is required")
	}
	if report.Release != nil {
		if err := validateRelease(*report.Release); err != nil {
			return err
		}
	}
	if report.WorkflowRun != nil {
		if err := validateWorkflowRun(*report.WorkflowRun); err != nil {
			return err
		}
	}
	if report.PluginIndex != nil {
		if err := validatePluginIndex(*report.PluginIndex); err != nil {
			return err
		}
	}
	seenDownloadedAssets := map[string]bool{}
	for _, asset := range report.DownloadedAssets {
		if strings.TrimSpace(asset.Name) == "" || !sha256Pattern.MatchString(asset.SHA256) || asset.Size <= 0 {
			return fmt.Errorf("invalid downloaded asset %q", asset.Name)
		}
		if seenDownloadedAssets[asset.Name] {
			return fmt.Errorf("duplicate downloaded asset %q", asset.Name)
		}
		seenDownloadedAssets[asset.Name] = true
	}
	if len(report.Criteria) == 0 {
		return fmt.Errorf("criteria are required")
	}
	seenCriteria := map[string]bool{}
	for _, criterion := range report.Criteria {
		if err := validateCriterion(criterion); err != nil {
			return err
		}
		if criterion.Status == "verified" {
			if err := validateVerifiedCriterionClaim(report, criterion.ID); err != nil {
				return err
			}
		}
		if seenCriteria[criterion.ID] {
			return fmt.Errorf("duplicate criterion %s", criterion.ID)
		}
		seenCriteria[criterion.ID] = true
	}
	for _, command := range report.Commands {
		if err := validateCommand(command); err != nil {
			return err
		}
	}
	return nil
}

// ExpectedReleaseAssetNames returns the complete public asset set expected for
// a v1-compatible GitHub release.
func ExpectedReleaseAssetNames(version, tag string) []string {
	assets := []string{
		"SHA256SUMS",
		"SHA256SUMS.sigstore.json",
		"v1-acceptance-gate.json",
		"vigil.rb",
		fmt.Sprintf("vigil_%s_sbom.spdx.json", tag),
	}
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		assets = append(assets, fmt.Sprintf("vigil_%s_%s.tar.gz", version, platform))
	}
	for _, arch := range []string{"amd64", "arm64"} {
		assets = append(assets,
			fmt.Sprintf("vigil_%s_darwin_%s.notary-result.json", version, arch),
			fmt.Sprintf("vigil_%s_darwin_%s.notary-log.json", version, arch),
		)
	}
	sort.Strings(assets)
	return assets
}

// ExpectedChecksummedReleaseAssetNames returns the release assets that must be
// listed in SHA256SUMS and validated after download.
func ExpectedChecksummedReleaseAssetNames(version, tag string) []string {
	assets := make([]string, 0)
	for _, asset := range ExpectedReleaseAssetNames(version, tag) {
		if asset == "SHA256SUMS" || strings.HasSuffix(asset, ".sigstore.json") {
			continue
		}
		assets = append(assets, asset)
	}
	return assets
}

func RequiredNativeReleaseSmokeJobs() []string {
	return append([]string{}, nativeReleaseSmokeJobs...)
}

func RequiredStableHomebrewJobs() []string {
	return append([]string{}, stableHomebrewJobs...)
}

func RequiredJobsPassed(jobs []JobSummary, required []string) bool {
	byName := map[string]JobSummary{}
	for _, job := range jobs {
		byName[job.Name] = job
	}
	for _, name := range required {
		job, ok := byName[name]
		if !ok || job.Status != "completed" || job.Conclusion != "success" {
			return false
		}
	}
	return true
}

func SortCriteria(criteria []CriterionResult) {
	sort.Slice(criteria, func(i, j int) bool {
		return criteria[i].ID < criteria[j].ID
	})
}

func validateVerifiedCriterionClaim(report Report, criterionID string) error {
	switch criterionID {
	case "VIGIL-AC-09":
		if err := validateReleaseProof(report, criterionID); err != nil {
			return err
		}
		if err := validateWorkflowProof(report, criterionID, nativeReleaseSmokeJobs); err != nil {
			return err
		}
	case "VIGIL-AC-11":
		if err := validateReleaseProof(report, criterionID); err != nil {
			return err
		}
		if err := validateExactReleaseAssets(report, criterionID); err != nil {
			return err
		}
		if err := validateDownloadedAssets(report, criterionID); err != nil {
			return err
		}
	case "VIGIL-AC-12":
		if err := validateReleaseProof(report, criterionID); err != nil {
			return err
		}
		if strings.Contains(report.Tag, "-") {
			return fmt.Errorf("%s verified Homebrew evidence requires a stable tag", criterionID)
		}
		if strings.TrimSpace(report.TapRepository) == "" {
			return fmt.Errorf("%s verified Homebrew evidence requires tap_repository", criterionID)
		}
		requiredJobs := RequiredNativeReleaseSmokeJobs()
		requiredJobs = append(requiredJobs, RequiredStableHomebrewJobs()...)
		if err := validateWorkflowProof(report, criterionID, requiredJobs); err != nil {
			return err
		}
	case "VIGIL-AC-13":
		if err := validateReleaseProof(report, criterionID); err != nil {
			return err
		}
		if err := validateWorkflowProof(report, criterionID, []string{
			"sign-macos",
			"Native release smoke (darwin-amd64)",
			"Native release smoke (darwin-arm64)",
		}); err != nil {
			return err
		}
		if err := validateMacOSReleaseAssets(report, criterionID); err != nil {
			return err
		}
	case "VIGIL-AC-18":
		if report.PluginIndex == nil {
			return fmt.Errorf("%s verified plugin index evidence requires plugin_index", criterionID)
		}
		if strings.TrimSpace(report.PluginIndexURL) == "" {
			return fmt.Errorf("%s verified plugin index evidence requires plugin_index_url", criterionID)
		}
		if err := validateHTTPSURL("plugin_index_url", report.PluginIndexURL); err != nil {
			return fmt.Errorf("%s verified plugin index evidence: %w", criterionID, err)
		}
		index := report.PluginIndex
		if err := validateHTTPSURL("plugin index source", index.Source); err != nil {
			return fmt.Errorf("%s verified plugin index evidence: %w", criterionID, err)
		}
		if strings.TrimSpace(index.CeremonyURL) == "" {
			return fmt.Errorf("%s verified plugin index evidence requires a public ceremony URL", criterionID)
		}
		if err := validateHTTPSURL("plugin index ceremony_url", index.CeremonyURL); err != nil {
			return fmt.Errorf("%s verified plugin index evidence: %w", criterionID, err)
		}
		if index.SignatureThreshold < 2 {
			return fmt.Errorf("%s verified plugin index evidence requires a production signature threshold of at least 2", criterionID)
		}
		if len(index.SignerIDs) < index.SignatureThreshold {
			return fmt.Errorf("%s verified plugin index evidence has %d signers below threshold %d", criterionID, len(index.SignerIDs), index.SignatureThreshold)
		}
		if len(index.PublisherKeys) < index.SignatureThreshold {
			return fmt.Errorf("%s verified plugin index evidence has %d publisher keys below threshold %d", criterionID, len(index.PublisherKeys), index.SignatureThreshold)
		}
		publisherKeys := map[string]bool{}
		for _, key := range index.PublisherKeys {
			publisherKeys[key.KeyID] = true
		}
		for _, signerID := range index.SignerIDs {
			if !publisherKeys[signerID] {
				return fmt.Errorf("%s verified plugin index evidence signer %s is missing from publisher_keys", criterionID, signerID)
			}
		}
		if index.PluginCount <= 0 || index.ArtifactCount <= 0 {
			return fmt.Errorf("%s verified plugin index evidence requires at least one plugin artifact", criterionID)
		}
		if !index.ArtifactsVerified {
			return fmt.Errorf("%s verified plugin index evidence requires artifact verification", criterionID)
		}
	default:
		return fmt.Errorf("%s is not accepted as verified operational evidence", criterionID)
	}
	return nil
}

func validateReleaseProof(report Report, criterionID string) error {
	if report.Release == nil {
		return fmt.Errorf("%s verified release evidence requires release", criterionID)
	}
	release := report.Release
	if release.TagName != report.Tag {
		return fmt.Errorf("%s release tag_name %q does not match report tag %q", criterionID, release.TagName, report.Tag)
	}
	if strings.TrimSpace(release.ResolvedCommit) == "" {
		return fmt.Errorf("%s verified release evidence requires release resolved_commit", criterionID)
	}
	if strings.TrimSpace(release.PublishedAt) == "" {
		return fmt.Errorf("%s verified release evidence requires release published_at", criterionID)
	}
	if release.IsDraft || !release.IsImmutable {
		return fmt.Errorf("%s verified release evidence requires an immutable non-draft release", criterionID)
	}
	return nil
}

func validateWorkflowProof(report Report, criterionID string, requiredJobs []string) error {
	if report.WorkflowRun == nil {
		return fmt.Errorf("%s verified workflow evidence requires workflow_run", criterionID)
	}
	workflow := report.WorkflowRun
	if report.Release != nil && report.Release.ResolvedCommit != "" && !strings.EqualFold(workflow.HeadSHA, report.Release.ResolvedCommit) {
		return fmt.Errorf("%s workflow_run head_sha does not match release resolved_commit", criterionID)
	}
	if workflow.Status == "completed" && workflow.Conclusion != "success" {
		return fmt.Errorf("%s workflow_run did not complete successfully", criterionID)
	}
	if !RequiredJobsPassed(workflow.Jobs, requiredJobs) {
		return fmt.Errorf("%s workflow_run is missing successful required jobs", criterionID)
	}
	if err := validateRequiredJobURLs(workflow.Jobs, requiredJobs); err != nil {
		return fmt.Errorf("%s %w", criterionID, err)
	}
	return nil
}

func validateRequiredJobURLs(jobs []JobSummary, required []string) error {
	byName := map[string]JobSummary{}
	for _, job := range jobs {
		byName[job.Name] = job
	}
	for _, name := range required {
		job := byName[name]
		if strings.TrimSpace(job.URL) == "" {
			return fmt.Errorf("workflow job %q requires a public URL", name)
		}
	}
	return nil
}

func validateExactReleaseAssets(report Report, criterionID string) error {
	seen := map[string]ReleaseAsset{}
	for _, asset := range report.Release.Assets {
		seen[asset.Name] = asset
	}
	for _, name := range ExpectedReleaseAssetNames(report.Version, report.Tag) {
		asset, ok := seen[name]
		if !ok {
			return fmt.Errorf("%s release assets are missing %s", criterionID, name)
		}
		if asset.Size <= 0 {
			return fmt.Errorf("%s release asset %s must have a positive size", criterionID, name)
		}
		if strings.TrimSpace(asset.URL) == "" {
			return fmt.Errorf("%s release asset %s requires a public URL", criterionID, name)
		}
		delete(seen, name)
	}
	if len(seen) > 0 {
		extra := make([]string, 0, len(seen))
		for name := range seen {
			extra = append(extra, name)
		}
		sort.Strings(extra)
		return fmt.Errorf("%s release assets contain unexpected entries: %s", criterionID, strings.Join(extra, ", "))
	}
	return nil
}

func validateDownloadedAssets(report Report, criterionID string) error {
	seen := map[string]DownloadedAsset{}
	for _, asset := range report.DownloadedAssets {
		seen[asset.Name] = asset
	}
	for _, name := range ExpectedChecksummedReleaseAssetNames(report.Version, report.Tag) {
		asset, ok := seen[name]
		if !ok {
			return fmt.Errorf("%s downloaded assets are missing %s", criterionID, name)
		}
		if asset.Size <= 0 || !sha256Pattern.MatchString(asset.SHA256) {
			return fmt.Errorf("%s downloaded asset %s has invalid digest or size", criterionID, name)
		}
		delete(seen, name)
	}
	if len(seen) > 0 {
		extra := make([]string, 0, len(seen))
		for name := range seen {
			extra = append(extra, name)
		}
		sort.Strings(extra)
		return fmt.Errorf("%s downloaded assets contain unexpected entries: %s", criterionID, strings.Join(extra, ", "))
	}
	return nil
}

func validateMacOSReleaseAssets(report Report, criterionID string) error {
	assets := map[string]ReleaseAsset{}
	for _, asset := range report.Release.Assets {
		assets[asset.Name] = asset
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, suffix := range []string{".tar.gz", ".notary-result.json", ".notary-log.json"} {
			name := fmt.Sprintf("vigil_%s_darwin_%s%s", report.Version, arch, suffix)
			asset, ok := assets[name]
			if !ok {
				return fmt.Errorf("%s macOS release assets are missing %s", criterionID, name)
			}
			if asset.Size <= 0 {
				return fmt.Errorf("%s macOS release asset %s must have a positive size", criterionID, name)
			}
			if strings.TrimSpace(asset.URL) == "" {
				return fmt.Errorf("%s macOS release asset %s requires a public URL", criterionID, name)
			}
		}
	}
	return nil
}

func validateRelease(summary ReleaseSummary) error {
	if strings.TrimSpace(summary.TagName) == "" || strings.TrimSpace(summary.TargetCommitish) == "" {
		return fmt.Errorf("release tag_name and target_commitish are required")
	}
	if summary.ResolvedCommit != "" && !commitPattern.MatchString(summary.ResolvedCommit) {
		return fmt.Errorf("invalid release resolved_commit %q", summary.ResolvedCommit)
	}
	if err := validateURI("release url", summary.URL); err != nil {
		return err
	}
	if summary.PublishedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, summary.PublishedAt); err != nil {
			return fmt.Errorf("invalid release published_at: %w", err)
		}
	}
	if summary.Assets == nil {
		return fmt.Errorf("release assets must be an array")
	}
	seenAssets := map[string]bool{}
	for _, asset := range summary.Assets {
		if strings.TrimSpace(asset.Name) == "" || asset.Size < 0 {
			return fmt.Errorf("invalid release asset %q", asset.Name)
		}
		if seenAssets[asset.Name] {
			return fmt.Errorf("duplicate release asset %q", asset.Name)
		}
		seenAssets[asset.Name] = true
		if asset.URL != "" {
			if err := validateURI("release asset url", asset.URL); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateWorkflowRun(summary WorkflowRunSummary) error {
	if summary.DatabaseID <= 0 {
		return fmt.Errorf("workflow_run database_id must be positive")
	}
	if err := validateURI("workflow_run url", summary.URL); err != nil {
		return err
	}
	switch summary.Status {
	case "completed", "in_progress":
	default:
		return fmt.Errorf("unsupported workflow_run status %q", summary.Status)
	}
	if !commitPattern.MatchString(summary.HeadSHA) {
		return fmt.Errorf("invalid workflow_run head_sha %q", summary.HeadSHA)
	}
	if summary.Jobs == nil {
		return fmt.Errorf("workflow_run jobs must be an array")
	}
	seenJobs := map[string]bool{}
	for _, job := range summary.Jobs {
		if strings.TrimSpace(job.Name) == "" || strings.TrimSpace(job.Status) == "" {
			return fmt.Errorf("workflow job name and status are required")
		}
		if seenJobs[job.Name] {
			return fmt.Errorf("duplicate workflow job %q", job.Name)
		}
		seenJobs[job.Name] = true
		if job.URL != "" {
			if err := validateURI("workflow job url", job.URL); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePluginIndex(summary PluginIndexSummary) error {
	if strings.TrimSpace(summary.Source) == "" || !digestPattern.MatchString(summary.IndexDigest) {
		return fmt.Errorf("invalid plugin index evidence")
	}
	if err := validateHTTPSURL("plugin index source", summary.Source); err != nil {
		return err
	}
	if summary.CeremonyURL != "" {
		if err := validateHTTPSURL("plugin index ceremony_url", summary.CeremonyURL); err != nil {
			return err
		}
	}
	if summary.SignatureThreshold <= 0 || summary.PluginCount < 0 || summary.ArtifactCount < 0 {
		return fmt.Errorf("invalid plugin index counts or threshold")
	}
	seenSigners := map[string]bool{}
	for _, signerID := range summary.SignerIDs {
		if !digestPattern.MatchString(signerID) {
			return fmt.Errorf("invalid plugin index signer id %q", signerID)
		}
		if seenSigners[signerID] {
			return fmt.Errorf("duplicate plugin index signer id %q", signerID)
		}
		seenSigners[signerID] = true
	}
	seenPublisherKeys := map[string]bool{}
	for _, key := range summary.PublisherKeys {
		if !digestPattern.MatchString(key.KeyID) || strings.TrimSpace(key.Source) == "" || key.Algorithm != "ed25519" {
			return fmt.Errorf("invalid plugin index publisher key evidence")
		}
		if seenPublisherKeys[key.KeyID] {
			return fmt.Errorf("duplicate plugin index publisher key %q", key.KeyID)
		}
		seenPublisherKeys[key.KeyID] = true
	}
	return nil
}

func validateCriterion(criterion CriterionResult) error {
	if !criterionIDPattern.MatchString(criterion.ID) {
		return fmt.Errorf("invalid criterion id %q", criterion.ID)
	}
	switch criterion.Status {
	case "verified", "failed", "pending", "operational_pending", "external_pending":
	default:
		return fmt.Errorf("%s has unsupported status %q", criterion.ID, criterion.Status)
	}
	if strings.TrimSpace(criterion.Detail) == "" {
		return fmt.Errorf("%s detail is required", criterion.ID)
	}
	hasEvidence := false
	for _, evidence := range criterion.Evidence {
		if strings.TrimSpace(evidence) == "" {
			return fmt.Errorf("%s has empty evidence entry", criterion.ID)
		}
		if criterion.Status == "verified" {
			if err := validateHTTPSURL("criterion evidence", evidence); err != nil {
				return fmt.Errorf("%s: %w", criterion.ID, err)
			}
		}
		hasEvidence = true
	}
	if criterion.Status == "verified" && !hasEvidence {
		return fmt.Errorf("%s verified criterion requires evidence", criterion.ID)
	}
	return nil
}

func validateCommand(command CommandRecord) error {
	if strings.TrimSpace(command.Name) == "" || strings.TrimSpace(command.Command) == "" {
		return fmt.Errorf("command name and command are required")
	}
	if command.ExitCode < 0 {
		return fmt.Errorf("command %s has negative exit_code", command.Name)
	}
	if command.DurationMillis < 0 {
		return fmt.Errorf("command %s has negative duration_millis", command.Name)
	}
	return nil
}

func validateURI(field, value string) error {
	return validateHTTPSURL(field, value)
}

func validateHTTPSURL(field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%s must be an HTTPS URL", field)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s must not contain credentials or fragments", field)
	}
	return nil
}

//go:build ignore

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/acceptance"
	"github.com/PayCal-Technologies/vigil-public/internal/operationalevidence"
	"github.com/PayCal-Technologies/vigil-public/internal/plugins"
)

const (
	reportSchemaVersion = operationalevidence.SchemaVersion
	commandOutputLimit  = 1 << 20
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	tagPattern        = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	fullCommitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

type stringList []string

func (list *stringList) String() string {
	return strings.Join(*list, ",")
}

func (list *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty value")
	}
	*list = append(*list, value)
	return nil
}

type report = operationalevidence.Report
type criterionResult = operationalevidence.CriterionResult
type commandRecord = operationalevidence.CommandRecord
type releaseSummary = operationalevidence.ReleaseSummary
type releaseAsset = operationalevidence.ReleaseAsset
type workflowRunSummary = operationalevidence.WorkflowRunSummary
type jobSummary = operationalevidence.JobSummary
type pluginIndexSummary = operationalevidence.PluginIndexSummary
type downloadedAsset = operationalevidence.DownloadedAsset

type collector struct {
	ctx     context.Context
	timeout time.Duration
	records []commandRecord
}

func main() {
	var publisherKeys stringList
	fs := flag.NewFlagSet("collect-v1-operational-evidence", flag.ExitOnError)
	repo := fs.String("repo", "", "GitHub repository in OWNER/REPO form")
	tag := fs.String("tag", "", "release tag, for example v0.4.0")
	tapRepo := fs.String("tap-repo", "", "Homebrew tap repository in OWNER/REPO form")
	pluginIndexURL := fs.String("plugin-index", "", "official plugin index HTTPS URL")
	pluginCeremonyURL := fs.String("plugin-ceremony-url", "", "immutable HTTPS key ceremony record for the official plugin index")
	ledgerPath := fs.String("ledger", acceptance.CanonicalLedgerPath, "acceptance ledger path")
	outputPath := fs.String("output", "", "write JSON report to this path instead of stdout")
	adminTokenEnv := fs.String("admin-token-env", "RELEASE_ADMIN_READ_TOKEN", "environment variable containing the read-only Administration token for immutable-release policy checks")
	workflowRunID := fs.String("workflow-run-id", strings.TrimSpace(os.Getenv("GITHUB_RUN_ID")), "GitHub Actions workflow run ID to inspect; defaults to GITHUB_RUN_ID")
	timeout := fs.Duration("timeout", 60*time.Second, "timeout per external command")
	requireRelease := fs.Bool("require-release-proof", false, "exit non-zero unless AC09 and AC11-AC13 are proven")
	requirePlugin := fs.Bool("require-plugin-index-proof", false, "exit non-zero unless AC18 is proven")
	strict := fs.Bool("strict", false, "exit non-zero unless all v1 criteria are already verified or proven by this report")
	verifyPluginArtifacts := fs.Bool("verify-plugin-artifacts", true, "download and digest-check every plugin artifact from the signed index")
	fs.Var(&publisherKeys, "publisher-key", "production publisher public key file; repeat for threshold verification")
	_ = fs.Parse(os.Args[1:])

	if fs.NArg() != 0 || strings.TrimSpace(*repo) == "" || strings.TrimSpace(*tag) == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/collect-v1-operational-evidence.go --repo OWNER/REPO --tag vX.Y.Z [flags]")
		os.Exit(2)
	}
	if !repositoryPattern.MatchString(*repo) {
		fmt.Fprintf(os.Stderr, "invalid --repo %q\n", *repo)
		os.Exit(2)
	}
	if *tapRepo != "" && !repositoryPattern.MatchString(*tapRepo) {
		fmt.Fprintf(os.Stderr, "invalid --tap-repo %q\n", *tapRepo)
		os.Exit(2)
	}
	versionMatch := tagPattern.FindStringSubmatch(*tag)
	if versionMatch == nil {
		fmt.Fprintf(os.Stderr, "invalid --tag %q\n", *tag)
		os.Exit(2)
	}
	version := strings.TrimPrefix(*tag, "v")

	ctx := context.Background()
	collector := &collector{ctx: ctx, timeout: *timeout}
	now := time.Now().UTC()
	result := report{
		SchemaVersion:    reportSchemaVersion,
		GeneratedAt:      now.Format(time.RFC3339Nano),
		Repository:       *repo,
		Tag:              *tag,
		Version:          version,
		TapRepository:    *tapRepo,
		PluginIndexURL:   *pluginIndexURL,
		AcceptanceLedger: *ledgerPath,
		Criteria:         []criterionResult{},
		Notes: map[string]interface{}{
			"manual_external_criteria": []string{"VIGIL-AC-16", "VIGIL-AC-17", "VIGIL-AC-19", "VIGIL-AC-20", "VIGIL-AC-21"},
		},
	}

	ledger, err := acceptance.Read(*ledgerPath)
	if err != nil {
		setCriterion(&result, "VIGIL-AC-22", "failed", fmt.Sprintf("acceptance ledger is unreadable: %v", err))
	} else if err := acceptance.ValidateRepositoryEvidence(".", ledger); err != nil {
		setCriterion(&result, "VIGIL-AC-22", "failed", fmt.Sprintf("acceptance evidence paths are invalid: %v", err))
	} else {
		result.Notes["acceptance_ledger_validation"] = "strict, bounded, and repository-confined"
		addManualExternalCriteria(&result, ledger)
	}

	releaseOK := collectReleaseEvidence(collector, &result, *repo, *tag, version, *adminTokenEnv)
	workflowOK := collectWorkflowEvidence(collector, &result, *repo, *workflowRunID)
	tapOK := collectTapEvidence(collector, &result, *tapRepo, *tag, version)
	pluginOK := collectPluginIndexEvidence(collector, &result, *pluginIndexURL, *pluginCeremonyURL, publisherKeys, *verifyPluginArtifacts, now)

	result.Commands = collector.records
	if releaseOK && workflowOK {
		setCriterion(&result, "VIGIL-AC-09", "verified", "release workflow completed all native downloaded-archive smoke jobs", workflowEvidence(result.WorkflowRun)...)
	} else {
		setCriterion(&result, "VIGIL-AC-09", "failed", "native downloaded-archive smoke evidence is incomplete")
	}
	if releaseOK {
		setCriterion(&result, "VIGIL-AC-11", "verified", "immutable release assets pass checksum, release, Sigstore, and attestation verification", releaseEvidence(result.Release)...)
	} else {
		setCriterion(&result, "VIGIL-AC-11", "failed", "published release asset verification is incomplete")
	}
	if strings.Contains(*tag, "-") {
		setCriterion(&result, "VIGIL-AC-12", "pending", "Homebrew project tap evidence requires a stable non-prerelease tag")
	} else if tapOK && workflowOK {
		setCriterion(&result, "VIGIL-AC-12", "verified", "stable Homebrew candidate, public install, audit, and tap publication evidence is present", workflowEvidence(result.WorkflowRun)...)
	} else {
		setCriterion(&result, "VIGIL-AC-12", "failed", "Homebrew stable tap evidence is incomplete")
	}
	if releaseOK && workflowOK && macOSReleaseAssetsPresent(result.Release, version) {
		setCriterion(&result, "VIGIL-AC-13", "verified", "macOS signing, notarization records, and native Gatekeeper smoke jobs are present", workflowEvidence(result.WorkflowRun)...)
	} else {
		setCriterion(&result, "VIGIL-AC-13", "failed", "macOS signing/notarization evidence is incomplete")
	}
	if pluginOK {
		setCriterion(&result, "VIGIL-AC-18", "verified", "production publisher keys verify a threshold-signed official plugin index", pluginEvidence(result.PluginIndex)...)
	} else if strings.TrimSpace(*pluginIndexURL) == "" || strings.TrimSpace(*pluginCeremonyURL) == "" || len(publisherKeys) == 0 {
		setCriterion(&result, "VIGIL-AC-18", "pending", "provide --plugin-index, --plugin-ceremony-url, and repeated --publisher-key flags after the production key ceremony")
	} else if !hasCriterion(&result, "VIGIL-AC-18") {
		setCriterion(&result, "VIGIL-AC-18", "failed", "plugin index or publisher key verification failed")
	}
	sortCriteria(result.Criteria)
	if err := operationalevidence.Validate(result); err != nil {
		fmt.Fprintln(os.Stderr, "invalid operational evidence report:", err)
		os.Exit(2)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	data = append(data, '\n')
	if *outputPath == "" {
		_, _ = os.Stdout.Write(data)
	} else if err := writeReport(*outputPath, data); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if shouldFail(result, *requireRelease, *requirePlugin, *strict) {
		os.Exit(1)
	}
}

func collectReleaseEvidence(c *collector, result *report, repo, tag, version, adminTokenEnv string) bool {
	ok := true
	if _, err := exec.LookPath("gh"); err != nil {
		setCriterion(result, "VIGIL-AC-11", "failed", "gh CLI is required for release evidence collection")
		return false
	}
	_, _ = c.run("gh_auth_status", "gh", "auth", "status", "-h", "github.com")
	adminToken := strings.TrimSpace(os.Getenv(adminTokenEnv))
	if adminToken == "" {
		c.records = append(c.records, commandRecord{
			Name:     "github_immutable_releases",
			Command:  "gh api -H X-GitHub-Api-Version: 2026-03-10 repos/" + repo + "/immutable-releases",
			ExitCode: 2,
			Stderr:   "required admin token environment variable is empty: " + adminTokenEnv,
		})
		ok = false
	} else if immutableOut, err := c.runEnv(
		"github_immutable_releases",
		map[string]string{"GH_TOKEN": adminToken},
		"gh",
		"api",
		"-H",
		"X-GitHub-Api-Version: 2026-03-10",
		"repos/"+repo+"/immutable-releases",
	); err != nil {
		ok = false
	} else {
		var immutable struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal([]byte(immutableOut), &immutable); err != nil || !immutable.Enabled {
			ok = false
		}
	}
	releaseOut, err := c.run("release_view", "gh", "release", "view", tag, "-R", repo, "--json", "tagName,targetCommitish,url,isDraft,isPrerelease,isImmutable,publishedAt,assets")
	if err != nil {
		return false
	}
	summary, err := decodeReleaseSummary([]byte(releaseOut))
	if err != nil {
		setCriterion(result, "VIGIL-AC-11", "failed", fmt.Sprintf("release JSON could not be decoded: %v", err))
		return false
	}
	result.Release = &summary
	if summary.TagName != tag || summary.IsDraft || !summary.IsImmutable {
		ok = false
	}
	resolvedCommit, err := resolveReleaseCommit(c, repo, tag, summary.TargetCommitish)
	if err != nil {
		ok = false
	} else {
		summary.ResolvedCommit = resolvedCommit
	}
	if _, err := c.run("release_verify", "gh", "release", "verify", tag, "-R", repo); err != nil {
		ok = false
	}
	temporary, err := os.MkdirTemp("", "vigil-v1-evidence-*")
	if err != nil {
		setCriterion(result, "VIGIL-AC-11", "failed", fmt.Sprintf("create evidence tempdir: %v", err))
		return false
	}
	defer os.RemoveAll(temporary)
	if _, err := c.run("release_download", "gh", "release", "download", tag, "-R", repo, "--dir", temporary); err != nil {
		return false
	}
	assets, err := verifyDownloadedReleaseAssets(temporary, operationalevidence.ExpectedReleaseAssetNames(version, tag))
	if err != nil {
		setCriterion(result, "VIGIL-AC-11", "failed", err.Error())
		ok = false
	}
	result.DownloadedAssets = assets
	if err := verifyDownloadedAcceptanceGate(temporary, version); err != nil {
		setCriterion(result, "VIGIL-AC-11", "failed", err.Error())
		ok = false
	}
	if _, err := exec.LookPath("cosign"); err == nil {
		identity := fmt.Sprintf("https://github.com/%s/.github/workflows/release.yml@refs/tags/%s", repo, tag)
		if _, err := c.run("cosign_verify_checksums", "cosign", "verify-blob",
			filepath.Join(temporary, "SHA256SUMS"),
			"--bundle", filepath.Join(temporary, "SHA256SUMS.sigstore.json"),
			"--certificate-identity", identity,
			"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com"); err != nil {
			ok = false
		}
	} else {
		c.records = append(c.records, commandRecord{Name: "cosign_verify_checksums", Command: "cosign verify-blob", ExitCode: 127, Stderr: "cosign not found"})
		ok = false
	}
	for _, asset := range operationalevidence.ExpectedReleaseAssetNames(version, tag) {
		path := filepath.Join(temporary, asset)
		if _, err := os.Stat(path); err != nil {
			ok = false
			continue
		}
		if _, err := c.run("attestation_"+asset, "gh", "attestation", "verify", path, "--repo", repo); err != nil {
			ok = false
		}
	}
	return ok
}

func resolveReleaseCommit(c *collector, repo, tag, targetCommitish string) (string, error) {
	if fullCommitPattern.MatchString(targetCommitish) {
		return strings.ToLower(targetCommitish), nil
	}
	out, err := c.run("release_tag_ref", "gh", "api", "repos/"+repo+"/git/ref/tags/"+url.PathEscape(tag))
	if err != nil {
		return "", err
	}
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := json.Unmarshal([]byte(out), &ref); err != nil {
		return "", err
	}
	switch ref.Object.Type {
	case "commit":
		if fullCommitPattern.MatchString(ref.Object.SHA) {
			return strings.ToLower(ref.Object.SHA), nil
		}
	case "tag":
		tagOut, err := c.run("release_annotated_tag", "gh", "api", "repos/"+repo+"/git/tags/"+ref.Object.SHA)
		if err != nil {
			return "", err
		}
		var annotated struct {
			Object struct {
				SHA  string `json:"sha"`
				Type string `json:"type"`
			} `json:"object"`
		}
		if err := json.Unmarshal([]byte(tagOut), &annotated); err != nil {
			return "", err
		}
		if annotated.Object.Type == "commit" && fullCommitPattern.MatchString(annotated.Object.SHA) {
			return strings.ToLower(annotated.Object.SHA), nil
		}
	}
	return "", fmt.Errorf("release tag %s does not resolve to a commit", tag)
}

func collectWorkflowEvidence(c *collector, result *report, repo, workflowRunID string) bool {
	if result.Release == nil || workflowCommit(result.Release) == "" {
		return false
	}
	if strings.TrimSpace(workflowRunID) != "" {
		return collectWorkflowRunByID(c, result, repo, workflowRunID)
	}
	out, err := c.run("release_workflow_runs", "gh", "run", "list", "-R", repo, "--workflow", "release.yml", "--commit", workflowCommit(result.Release), "--event", "push", "--json", "databaseId,status,conclusion,url,headSha,workflowName,createdAt,updatedAt", "-L", "20")
	if err != nil {
		return false
	}
	var runs []struct {
		DatabaseID int64  `json:"databaseId"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		URL        string `json:"url"`
		HeadSHA    string `json:"headSha"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		setCriterion(result, "VIGIL-AC-09", "failed", fmt.Sprintf("workflow run list JSON could not be decoded: %v", err))
		return false
	}
	var selected int64
	for _, run := range runs {
		if run.Status == "completed" && run.Conclusion == "success" {
			selected = run.DatabaseID
			break
		}
	}
	if selected == 0 {
		return false
	}
	return collectWorkflowRunByID(c, result, repo, strconv.FormatInt(selected, 10))
}

func collectWorkflowRunByID(c *collector, result *report, repo, workflowRunID string) bool {
	workflowRunID = strings.TrimSpace(workflowRunID)
	if _, err := strconv.ParseInt(workflowRunID, 10, 64); err != nil {
		setCriterion(result, "VIGIL-AC-09", "failed", fmt.Sprintf("workflow run id is invalid: %q", workflowRunID))
		return false
	}
	viewOut, err := c.run("release_workflow_view", "gh", "run", "view", workflowRunID, "-R", repo, "--json", "databaseId,status,conclusion,url,headSha,jobs")
	if err != nil {
		return false
	}
	var view struct {
		DatabaseID int64  `json:"databaseId"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		URL        string `json:"url"`
		HeadSHA    string `json:"headSha"`
		Jobs       []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			URL        string `json:"url"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(viewOut), &view); err != nil {
		setCriterion(result, "VIGIL-AC-09", "failed", fmt.Sprintf("workflow run JSON could not be decoded: %v", err))
		return false
	}
	summary := workflowRunSummary{
		DatabaseID: view.DatabaseID,
		URL:        view.URL,
		Status:     view.Status,
		Conclusion: view.Conclusion,
		HeadSHA:    view.HeadSHA,
		Jobs:       []jobSummary{},
	}
	for _, job := range view.Jobs {
		summary.Jobs = append(summary.Jobs, jobSummary{Name: job.Name, Status: job.Status, Conclusion: job.Conclusion, URL: job.URL})
	}
	result.WorkflowRun = &summary
	if !strings.EqualFold(summary.HeadSHA, workflowCommit(result.Release)) {
		setCriterion(result, "VIGIL-AC-09", "failed", "workflow run head SHA does not match the immutable release commit")
		return false
	}
	if summary.Status == "completed" && summary.Conclusion != "success" {
		setCriterion(result, "VIGIL-AC-09", "failed", "workflow run did not complete successfully")
		return false
	}
	if summary.Status != "completed" && summary.Status != "in_progress" {
		setCriterion(result, "VIGIL-AC-09", "failed", "workflow run is not completed or in progress")
		return false
	}
	required := operationalevidence.RequiredNativeReleaseSmokeJobs()
	if !strings.Contains(result.Tag, "-") {
		required = append(required, operationalevidence.RequiredStableHomebrewJobs()...)
	}
	return operationalevidence.RequiredJobsPassed(summary.Jobs, required)
}

func collectTapEvidence(c *collector, result *report, tapRepo, tag, version string) bool {
	if strings.Contains(tag, "-") {
		return true
	}
	if strings.TrimSpace(tapRepo) == "" {
		return false
	}
	out, err := c.run("homebrew_tap_formula", "gh", "api", "-H", "Accept: application/vnd.github.v3.raw+json", "repos/"+tapRepo+"/contents/Formula/vigil.rb")
	if err != nil {
		return false
	}
	if !strings.Contains(out, "/releases/download/"+tag+"/") {
		return false
	}
	for _, asset := range []string{
		fmt.Sprintf("vigil_%s_darwin_amd64.tar.gz", version),
		fmt.Sprintf("vigil_%s_darwin_arm64.tar.gz", version),
		fmt.Sprintf("vigil_%s_linux_amd64.tar.gz", version),
		fmt.Sprintf("vigil_%s_linux_arm64.tar.gz", version),
	} {
		if !strings.Contains(out, asset) {
			return false
		}
	}
	return true
}

func collectPluginIndexEvidence(c *collector, result *report, indexURL, ceremonyURL string, publisherKeys []string, verifyArtifacts bool, now time.Time) bool {
	if strings.TrimSpace(indexURL) == "" || len(publisherKeys) == 0 {
		return false
	}
	if parsed, err := url.Parse(indexURL); err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		setCriterion(result, "VIGIL-AC-18", "failed", "plugin index URL must be HTTPS without credentials or fragments")
		return false
	}
	if strings.TrimSpace(ceremonyURL) == "" {
		setCriterion(result, "VIGIL-AC-18", "failed", "plugin ceremony URL is required")
		return false
	}
	if parsed, err := url.Parse(ceremonyURL); err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		setCriterion(result, "VIGIL-AC-18", "failed", "plugin ceremony URL must be HTTPS without credentials or fragments")
		return false
	}
	root, err := os.MkdirTemp("", "vigil-plugin-index-evidence-*")
	if err != nil {
		setCriterion(result, "VIGIL-AC-18", "failed", fmt.Sprintf("create plugin evidence tempdir: %v", err))
		return false
	}
	defer os.RemoveAll(root)
	layout, err := plugins.NewLayout(root, ".")
	if err != nil {
		setCriterion(result, "VIGIL-AC-18", "failed", err.Error())
		return false
	}
	keyEvidence := make([]operationalevidence.PublisherKeyEvidence, 0, len(publisherKeys))
	for index, keyPath := range publisherKeys {
		trusted, err := plugins.TrustPublisherFile(layout, fmt.Sprintf("v1 evidence publisher %d", index+1), keyPath, now, false)
		if err != nil {
			setCriterion(result, "VIGIL-AC-18", "failed", fmt.Sprintf("trust publisher key %s: %v", filepath.Base(keyPath), err))
			return false
		}
		keyEvidence = append(keyEvidence, operationalevidence.PublisherKeyEvidence{
			KeyID:     trusted.Key.KeyID,
			Algorithm: trusted.Key.Algorithm,
			Source:    keyEvidenceSource(keyPath),
		})
	}
	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()
	loaded, err := plugins.LoadVerifiedIndex(ctx, layout, indexURL, plugins.IndexLoadOptions{Now: now})
	if err != nil {
		setCriterion(result, "VIGIL-AC-18", "failed", err.Error())
		return false
	}
	artifactCount := 0
	artifactsVerified := true
	if verifyArtifacts {
		for _, release := range loaded.Verified.Document.Signed.Plugins {
			for _, artifact := range release.Artifacts {
				artifactCount++
				selected := plugins.SelectedRelease{Release: release, Artifact: artifact}
				acquired, err := plugins.AcquireIndexedPlugin(ctx, layout, loaded, selected, nil)
				if err != nil {
					setCriterion(result, "VIGIL-AC-18", "failed", err.Error())
					return false
				}
				if err := plugins.RemoveAcquiredPlugin(acquired); err != nil {
					artifactsVerified = false
				}
			}
		}
	} else {
		for _, release := range loaded.Verified.Document.Signed.Plugins {
			artifactCount += len(release.Artifacts)
		}
		artifactsVerified = false
	}
	result.PluginIndex = &pluginIndexSummary{
		Source:             loaded.Source,
		IndexDigest:        loaded.Verified.IndexDigest,
		CeremonyURL:        strings.TrimSpace(ceremonyURL),
		SignatureThreshold: loaded.Verified.Document.Signed.SignatureThreshold,
		SignerIDs:          loaded.Verified.SignerIDs,
		PublisherKeys:      keyEvidence,
		PluginCount:        len(loaded.Verified.Document.Signed.Plugins),
		ArtifactCount:      artifactCount,
		ArtifactsVerified:  artifactsVerified,
	}
	return loaded.Verified.Document.Signed.SignatureThreshold >= 2 &&
		len(loaded.Verified.SignerIDs) >= loaded.Verified.Document.Signed.SignatureThreshold &&
		len(keyEvidence) >= loaded.Verified.Document.Signed.SignatureThreshold &&
		signerKeysRecorded(loaded.Verified.SignerIDs, keyEvidence) &&
		len(loaded.Verified.Document.Signed.Plugins) > 0 &&
		artifactCount > 0 &&
		artifactsVerified
}

func keyEvidenceSource(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Base(path)
	}
	return path
}

func signerKeysRecorded(signerIDs []string, keys []operationalevidence.PublisherKeyEvidence) bool {
	byID := map[string]bool{}
	for _, key := range keys {
		byID[key.KeyID] = true
	}
	for _, signerID := range signerIDs {
		if !byID[signerID] {
			return false
		}
	}
	return true
}

func (c *collector) run(name string, command string, args ...string) (string, error) {
	return c.runEnv(name, nil, command, args...)
}

func (c *collector) runEnv(name string, env map[string]string, command string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()
	stdout := &limitedBuffer{limit: commandOutputLimit}
	stderr := &limitedBuffer{limit: commandOutputLimit}
	started := time.Now()
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			exitCode = 124
		} else if errors.Is(ctx.Err(), context.Canceled) {
			exitCode = 130
		} else if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(err, exec.ErrNotFound) {
			exitCode = 127
		}
		if exitCode < 0 {
			exitCode = 1
		}
	}
	record := commandRecord{
		Name:           name,
		Command:        strings.Join(append([]string{command}, args...), " "),
		ExitCode:       exitCode,
		DurationMillis: time.Since(started).Milliseconds(),
		Stdout:         stdout.String(),
		Stderr:         stderr.String(),
		Truncated:      stdout.truncated || stderr.truncated,
	}
	c.records = append(c.records, record)
	if err != nil {
		return record.Stdout, fmt.Errorf("%s failed with exit code %d", name, exitCode)
	}
	return record.Stdout, nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if buffer.buffer.Len() < buffer.limit {
		remaining := buffer.limit - buffer.buffer.Len()
		if len(data) > remaining {
			_, _ = buffer.buffer.Write(data[:remaining])
			buffer.truncated = true
		} else {
			_, _ = buffer.buffer.Write(data)
		}
	} else if len(data) > 0 {
		buffer.truncated = true
	}
	return len(data), nil
}

func (buffer *limitedBuffer) String() string {
	return strings.TrimSpace(buffer.buffer.String())
}

func decodeReleaseSummary(data []byte) (releaseSummary, error) {
	var raw struct {
		TagName         string `json:"tagName"`
		TargetCommitish string `json:"targetCommitish"`
		URL             string `json:"url"`
		IsDraft         bool   `json:"isDraft"`
		IsPrerelease    bool   `json:"isPrerelease"`
		IsImmutable     bool   `json:"isImmutable"`
		PublishedAt     string `json:"publishedAt"`
		Assets          []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return releaseSummary{}, err
	}
	summary := releaseSummary{
		TagName:         raw.TagName,
		TargetCommitish: raw.TargetCommitish,
		URL:             raw.URL,
		IsDraft:         raw.IsDraft,
		IsPrerelease:    raw.IsPrerelease,
		IsImmutable:     raw.IsImmutable,
		PublishedAt:     raw.PublishedAt,
		Assets:          []releaseAsset{},
	}
	for _, asset := range raw.Assets {
		summary.Assets = append(summary.Assets, releaseAsset{Name: asset.Name, Size: asset.Size, URL: asset.URL})
	}
	sort.Slice(summary.Assets, func(i, j int) bool {
		return summary.Assets[i].Name < summary.Assets[j].Name
	})
	return summary, nil
}

func workflowCommit(summary *releaseSummary) string {
	if summary == nil {
		return ""
	}
	if summary.ResolvedCommit != "" {
		return summary.ResolvedCommit
	}
	if fullCommitPattern.MatchString(summary.TargetCommitish) {
		return strings.ToLower(summary.TargetCommitish)
	}
	return ""
}

func verifyDownloadedReleaseAssets(root string, expected []string) ([]downloadedAsset, error) {
	expectedSet := map[string]bool{}
	for _, name := range expected {
		expectedSet[name] = true
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("downloaded release is missing %s", name)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("downloaded release asset is not a regular file: %s", name)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read downloaded release directory: %w", err)
	}
	for _, entry := range entries {
		if !expectedSet[entry.Name()] {
			return nil, fmt.Errorf("downloaded release contains unexpected asset %s", entry.Name())
		}
	}
	checksums, err := readChecksums(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	requiredChecksums := expectedChecksumEntries(expected)
	for name := range requiredChecksums {
		if _, ok := checksums[name]; !ok {
			return nil, fmt.Errorf("SHA256SUMS is missing %s", name)
		}
	}
	for name := range checksums {
		if !requiredChecksums[name] {
			return nil, fmt.Errorf("SHA256SUMS contains unexpected asset %s", name)
		}
	}
	var assets []downloadedAsset
	for name, expectedDigest := range checksums {
		path := filepath.Join(root, name)
		actualDigest, size, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		if actualDigest != expectedDigest {
			return nil, fmt.Errorf("checksum mismatch for %s", name)
		}
		assets = append(assets, downloadedAsset{Name: name, SHA256: actualDigest, Size: size})
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].Name < assets[j].Name
	})
	return assets, nil
}

func verifyDownloadedAcceptanceGate(root, version string) error {
	path := filepath.Join(root, "v1-acceptance-gate.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read v1 acceptance gate report: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var report acceptance.GateReport
	if err := decoder.Decode(&report); err != nil {
		return fmt.Errorf("decode v1 acceptance gate report: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("v1 acceptance gate report must contain exactly one JSON document")
	}
	if err := acceptance.ValidateReleaseGateReport(report, version); err != nil {
		return fmt.Errorf("invalid v1 acceptance gate report: %w", err)
	}
	return nil
}

func expectedChecksumEntries(expected []string) map[string]bool {
	checksummed := map[string]bool{}
	for _, name := range expected {
		if name == "SHA256SUMS" || strings.HasSuffix(name, ".sigstore.json") {
			continue
		}
		checksummed[name] = true
	}
	return checksummed
}

func readChecksums(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SHA256SUMS: %w", err)
	}
	checksums := map[string]string{}
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("invalid SHA256SUMS line %d", lineNumber+1)
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "." || filepath.IsAbs(name) || strings.Contains(name, "/") || strings.Contains(name, "\\") {
			return nil, fmt.Errorf("unsafe SHA256SUMS path %q", name)
		}
		checksums[name] = strings.ToLower(fields[0])
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("SHA256SUMS is empty")
	}
	return checksums, nil
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), size, nil
}

func macOSReleaseAssetsPresent(summary *releaseSummary, version string) bool {
	if summary == nil {
		return false
	}
	assets := map[string]bool{}
	for _, asset := range summary.Assets {
		assets[asset.Name] = true
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, suffix := range []string{".tar.gz", ".notary-result.json", ".notary-log.json"} {
			if !assets[fmt.Sprintf("vigil_%s_darwin_%s%s", version, arch, suffix)] {
				return false
			}
		}
	}
	return true
}

func releaseEvidence(summary *releaseSummary) []string {
	if summary == nil {
		return nil
	}
	return []string{summary.URL}
}

func workflowEvidence(summary *workflowRunSummary) []string {
	if summary == nil {
		return nil
	}
	return []string{summary.URL}
}

func pluginEvidence(summary *pluginIndexSummary) []string {
	if summary == nil {
		return nil
	}
	return []string{summary.Source, summary.CeremonyURL}
}

func addManualExternalCriteria(result *report, ledger acceptance.Ledger) {
	manual := map[string]bool{
		"VIGIL-AC-16": true,
		"VIGIL-AC-17": true,
		"VIGIL-AC-19": true,
		"VIGIL-AC-20": true,
		"VIGIL-AC-21": true,
	}
	for _, criterion := range ledger.Criteria {
		if !manual[criterion.ID] || criterion.Status == acceptance.StatusVerified {
			continue
		}
		setCriterion(result, criterion.ID, string(criterion.Status), criterion.Blocker)
	}
}

func setCriterion(result *report, id, status, detail string, evidence ...string) {
	for index := range result.Criteria {
		if result.Criteria[index].ID == id {
			result.Criteria[index] = criterionResult{ID: id, Status: status, Detail: detail, Evidence: compactStrings(evidence)}
			return
		}
	}
	result.Criteria = append(result.Criteria, criterionResult{ID: id, Status: status, Detail: detail, Evidence: compactStrings(evidence)})
}

func hasCriterion(result *report, id string) bool {
	for _, criterion := range result.Criteria {
		if criterion.ID == id {
			return true
		}
	}
	return false
}

func compactStrings(values []string) []string {
	clean := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			clean = append(clean, value)
		}
	}
	return clean
}

func sortCriteria(criteria []criterionResult) {
	operationalevidence.SortCriteria(criteria)
}

func shouldFail(result report, requireRelease, requirePlugin, strict bool) bool {
	required := map[string]bool{}
	if requireRelease {
		for _, id := range []string{"VIGIL-AC-09", "VIGIL-AC-11", "VIGIL-AC-12", "VIGIL-AC-13"} {
			required[id] = true
		}
	}
	if requirePlugin {
		required["VIGIL-AC-18"] = true
	}
	for _, criterion := range result.Criteria {
		if strict && criterion.Status != "verified" {
			return true
		}
		if required[criterion.ID] && criterion.Status != "verified" {
			return true
		}
	}
	return false
}

func writeReport(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		return fmt.Errorf("output path must be relative to the repository")
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output path escapes repository: %s", path)
	}
	if info, err := os.Lstat(clean); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path must not be a symlink: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(clean), ".v1-evidence-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, clean); err != nil {
		return err
	}
	keep = true
	return nil
}

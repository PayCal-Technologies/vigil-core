package operationalevidence

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptsMinimalReportAndSortsCriteria(t *testing.T) {
	report := validReport()
	report.Criteria = []CriterionResult{
		{ID: "VIGIL-AC-11", Status: "failed", Detail: "missing release"},
		{ID: "VIGIL-AC-09", Status: "failed", Detail: "missing native smoke"},
	}
	SortCriteria(report.Criteria)
	if report.Criteria[0].ID != "VIGIL-AC-09" {
		t.Fatalf("criteria were not sorted: %#v", report.Criteria)
	}
	if err := Validate(report); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsInvalidReportFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Report)
		want   string
	}{
		{"schema", func(report *Report) { report.SchemaVersion = "2" }, "schema_version"},
		{"tag mismatch", func(report *Report) { report.Version = "0.4.1" }, "does not match tag"},
		{"blank acceptance ledger", func(report *Report) { report.AcceptanceLedger = " " }, "acceptance_ledger"},
		{"duplicate criterion", func(report *Report) { report.Criteria = append(report.Criteria, report.Criteria[0]) }, "duplicate criterion"},
		{"bad criterion status", func(report *Report) { report.Criteria[0].Status = "ok" }, "unsupported status"},
		{"blank criterion detail", func(report *Report) { report.Criteria[0].Detail = " " }, "detail"},
		{"verified criterion without evidence", func(report *Report) { report.Criteria[0].Evidence = nil }, "requires evidence"},
		{"verified criterion with non-url evidence", func(report *Report) {
			report.Criteria[0].Evidence = []string{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
		}, "criterion evidence"},
		{"verified criterion with credentialed evidence url", func(report *Report) {
			report.Criteria[0].Evidence = []string{"https://token@example.test/evidence"}
		}, "criterion evidence"},
		{"negative command exit", func(report *Report) { report.Commands[0].ExitCode = -1 }, "negative exit_code"},
		{"duplicate release asset", func(report *Report) { report.Release.Assets = append(report.Release.Assets, report.Release.Assets[0]) }, "duplicate release asset"},
		{"blank release tag", func(report *Report) { report.Release.TagName = " " }, "tag_name"},
		{"blank release target", func(report *Report) { report.Release.TargetCommitish = " " }, "target_commitish"},
		{"blank release asset", func(report *Report) { report.Release.Assets[0].Name = " " }, "release asset"},
		{"bad release url", func(report *Report) {
			report.Release.URL = "http://example.test/release"
		}, "release url"},
		{"credentialed release asset url", func(report *Report) {
			report.Release.Assets[0].URL = "https://token@example.test/asset"
		}, "release asset url"},
		{"duplicate workflow job", func(report *Report) {
			report.WorkflowRun.Jobs = append(report.WorkflowRun.Jobs, report.WorkflowRun.Jobs[0])
		}, "duplicate workflow job"},
		{"blank workflow job name", func(report *Report) { report.WorkflowRun.Jobs[0].Name = " " }, "workflow job name"},
		{"blank workflow job status", func(report *Report) { report.WorkflowRun.Jobs[0].Status = " " }, "workflow job name"},
		{"bad workflow url", func(report *Report) {
			report.WorkflowRun.URL = "http://example.test/workflow"
		}, "workflow_run url"},
		{"fragmented workflow job url", func(report *Report) {
			report.WorkflowRun.Jobs[0].URL = "https://example.test/job#logs"
		}, "workflow job url"},
		{"duplicate downloaded asset", func(report *Report) {
			report.DownloadedAssets = append(report.DownloadedAssets, report.DownloadedAssets[0])
		}, "duplicate downloaded asset"},
		{"blank downloaded asset", func(report *Report) { report.DownloadedAssets[0].Name = " " }, "downloaded asset"},
		{"duplicate signer", func(report *Report) {
			report.PluginIndex.SignerIDs = append(report.PluginIndex.SignerIDs, report.PluginIndex.SignerIDs[0])
		}, "duplicate plugin index signer"},
		{"bad plugin index url", func(report *Report) {
			report.PluginIndexURL = "file:///tmp/index-v1.json"
		}, "plugin_index_url"},
		{"bad plugin index source", func(report *Report) {
			report.PluginIndex.Source = "/tmp/index-v1.json"
		}, "plugin index source"},
		{"bad ceremony URL", func(report *Report) {
			report.PluginIndex.CeremonyURL = "http://example.test/ceremony"
		}, "ceremony_url"},
		{"bad publisher key", func(report *Report) {
			report.PluginIndex.PublisherKeys[0].Algorithm = "rsa"
		}, "publisher key"},
		{"blank publisher key source", func(report *Report) {
			report.PluginIndex.PublisherKeys[0].Source = " "
		}, "publisher key"},
		{"duplicate publisher key", func(report *Report) {
			report.PluginIndex.PublisherKeys = append(report.PluginIndex.PublisherKeys, report.PluginIndex.PublisherKeys[0])
		}, "duplicate plugin index publisher key"},
		{"bad workflow sha", func(report *Report) { report.WorkflowRun.HeadSHA = "main" }, "head_sha"},
		{"bad digest", func(report *Report) { report.PluginIndex.IndexDigest = "sha256:nope" }, "plugin index"},
		{"blank command name", func(report *Report) { report.Commands[0].Name = " " }, "command name"},
		{"blank command", func(report *Report) { report.Commands[0].Command = " " }, "command name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validReport()
			test.mutate(&report)
			if err := Validate(report); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateAcceptsVerifiedOperationalCriterionProofs(t *testing.T) {
	for _, criterionID := range []string{"VIGIL-AC-09", "VIGIL-AC-11", "VIGIL-AC-12", "VIGIL-AC-13", "VIGIL-AC-18"} {
		t.Run(criterionID, func(t *testing.T) {
			report := validReport()
			report.Criteria = []CriterionResult{verifiedCriterionResult(criterionID)}
			if err := Validate(report); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateRejectsVerifiedNonOperationalCriteria(t *testing.T) {
	for _, criterionID := range []string{"VIGIL-AC-16", "VIGIL-AC-22"} {
		t.Run(criterionID, func(t *testing.T) {
			report := validReport()
			report.Criteria = []CriterionResult{verifiedCriterionResult(criterionID)}
			err := Validate(report)
			if err == nil || !strings.Contains(err.Error(), "not accepted as verified operational evidence") {
				t.Fatalf("Validate error = %v", err)
			}
		})
	}
}

func TestValidateRejectsVerifiedOperationalClaimsWithoutRequiredProof(t *testing.T) {
	tests := []struct {
		name        string
		criterionID string
		mutate      func(*Report)
		want        string
	}{
		{
			name:        "native smoke without release resolved commit",
			criterionID: "VIGIL-AC-09",
			mutate: func(report *Report) {
				report.Release.ResolvedCommit = ""
			},
			want: "resolved_commit",
		},
		{
			name:        "native smoke without release publication timestamp",
			criterionID: "VIGIL-AC-09",
			mutate: func(report *Report) {
				report.Release.PublishedAt = ""
			},
			want: "published_at",
		},
		{
			name:        "native smoke without workflow",
			criterionID: "VIGIL-AC-09",
			mutate:      func(report *Report) { report.WorkflowRun = nil },
			want:        "requires workflow_run",
		},
		{
			name:        "native smoke without required job url",
			criterionID: "VIGIL-AC-09",
			mutate: func(report *Report) {
				report.WorkflowRun.Jobs[0].URL = ""
			},
			want: "requires a public URL",
		},
		{
			name:        "published release with extra asset",
			criterionID: "VIGIL-AC-11",
			mutate: func(report *Report) {
				report.Release.Assets = append(report.Release.Assets, ReleaseAsset{Name: "unexpected.txt", Size: 1, URL: "https://example.test/unexpected.txt"})
			},
			want: "unexpected entries",
		},
		{
			name:        "published release without asset url",
			criterionID: "VIGIL-AC-11",
			mutate: func(report *Report) {
				report.Release.Assets[0].URL = ""
			},
			want: "requires a public URL",
		},
		{
			name:        "published release without downloaded asset",
			criterionID: "VIGIL-AC-11",
			mutate:      func(report *Report) { report.DownloadedAssets = report.DownloadedAssets[1:] },
			want:        "downloaded assets are missing",
		},
		{
			name:        "homebrew proof on prerelease tag",
			criterionID: "VIGIL-AC-12",
			mutate: func(report *Report) {
				report.Tag = "v0.4.0-rc.1"
				report.Version = "0.4.0-rc.1"
				report.Release.TagName = "v0.4.0-rc.1"
			},
			want: "requires a stable tag",
		},
		{
			name:        "macos proof without archive url",
			criterionID: "VIGIL-AC-13",
			mutate: func(report *Report) {
				for index := range report.Release.Assets {
					if report.Release.Assets[index].Name == "vigil_0.4.0_darwin_arm64.tar.gz" {
						report.Release.Assets[index].URL = ""
					}
				}
			},
			want: "requires a public URL",
		},
		{
			name:        "macos proof without notary asset",
			criterionID: "VIGIL-AC-13",
			mutate: func(report *Report) {
				report.Release.Assets = withoutReleaseAsset(report.Release.Assets, "vigil_0.4.0_darwin_arm64.notary-log.json")
			},
			want: "macOS release assets are missing",
		},
		{
			name:        "plugin proof without live index url",
			criterionID: "VIGIL-AC-18",
			mutate: func(report *Report) {
				report.PluginIndexURL = ""
			},
			want: "plugin_index_url",
		},
		{
			name:        "plugin proof without ceremony url",
			criterionID: "VIGIL-AC-18",
			mutate: func(report *Report) {
				report.PluginIndex.CeremonyURL = ""
			},
			want: "public ceremony URL",
		},
		{
			name:        "plugin proof below production threshold",
			criterionID: "VIGIL-AC-18",
			mutate: func(report *Report) {
				report.PluginIndex.SignatureThreshold = 1
			},
			want: "production signature threshold",
		},
		{
			name:        "plugin proof without enough publisher keys",
			criterionID: "VIGIL-AC-18",
			mutate: func(report *Report) {
				report.PluginIndex.PublisherKeys = report.PluginIndex.PublisherKeys[:1]
			},
			want: "publisher keys below threshold",
		},
		{
			name:        "plugin proof missing signer key record",
			criterionID: "VIGIL-AC-18",
			mutate: func(report *Report) {
				report.PluginIndex.PublisherKeys[0].KeyID = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
			},
			want: "missing from publisher_keys",
		},
		{
			name:        "plugin proof without verified artifacts",
			criterionID: "VIGIL-AC-18",
			mutate: func(report *Report) {
				report.PluginIndex.ArtifactsVerified = false
			},
			want: "requires artifact verification",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validReport()
			report.Criteria = []CriterionResult{verifiedCriterionResult(test.criterionID)}
			test.mutate(&report)
			if err := Validate(report); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func validReport() Report {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	secondDigest := "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	sha := strings.TrimPrefix(digest, "sha256:")
	commit := "0123456789abcdef0123456789abcdef01234567"
	version := "0.4.0"
	tag := "v0.4.0"
	return Report{
		SchemaVersion:    SchemaVersion,
		GeneratedAt:      now,
		Repository:       "PayCal-Technologies/vigil-public",
		Tag:              tag,
		Version:          version,
		TapRepository:    "PayCal-Technologies/homebrew-tap",
		PluginIndexURL:   "https://example.test/index-v1.json",
		AcceptanceLedger: "docs/v1-acceptance.json",
		Release: &ReleaseSummary{
			TagName:         tag,
			TargetCommitish: commit,
			ResolvedCommit:  commit,
			URL:             "https://github.com/PayCal-Technologies/vigil-public/releases/tag/v0.4.0",
			IsImmutable:     true,
			PublishedAt:     now,
			Assets:          releaseAssets(version, tag),
		},
		WorkflowRun: &WorkflowRunSummary{
			DatabaseID: 1,
			URL:        "https://github.com/PayCal-Technologies/vigil-public/actions/runs/1",
			Status:     "completed",
			Conclusion: "success",
			HeadSHA:    commit,
			Jobs:       workflowJobs(append(RequiredNativeReleaseSmokeJobs(), RequiredStableHomebrewJobs()...)),
		},
		PluginIndex: &PluginIndexSummary{
			Source:             "https://example.test/index-v1.json",
			IndexDigest:        digest,
			CeremonyURL:        "https://example.test/vigil-plugin-ceremony",
			SignatureThreshold: 2,
			SignerIDs:          []string{digest, secondDigest},
			PublisherKeys: []PublisherKeyEvidence{
				{KeyID: digest, Algorithm: "ed25519", Source: "docs/plugin-publishers/custodian-a.pub"},
				{KeyID: secondDigest, Algorithm: "ed25519", Source: "docs/plugin-publishers/custodian-b.pub"},
			},
			PluginCount:       1,
			ArtifactCount:     1,
			ArtifactsVerified: true,
		},
		DownloadedAssets: downloadedAssets(version, tag, sha),
		Criteria:         []CriterionResult{verifiedCriterionResult("VIGIL-AC-09")},
		Commands:         []CommandRecord{{Name: "release_view", Command: "gh release view", ExitCode: 0, DurationMillis: 10}},
	}
}

func verifiedCriterionResult(id string) CriterionResult {
	return CriterionResult{
		ID:       id,
		Status:   "verified",
		Detail:   "criterion is verified",
		Evidence: []string{"https://github.com/PayCal-Technologies/vigil-public/actions/runs/1"},
	}
}

func releaseAssets(version, tag string) []ReleaseAsset {
	assets := make([]ReleaseAsset, 0)
	for _, name := range ExpectedReleaseAssetNames(version, tag) {
		assets = append(assets, ReleaseAsset{Name: name, Size: 128, URL: "https://example.test/assets/" + name})
	}
	return assets
}

func withoutReleaseAsset(assets []ReleaseAsset, name string) []ReleaseAsset {
	filtered := make([]ReleaseAsset, 0, len(assets))
	for _, asset := range assets {
		if asset.Name != name {
			filtered = append(filtered, asset)
		}
	}
	return filtered
}

func downloadedAssets(version, tag, sha string) []DownloadedAsset {
	assets := make([]DownloadedAsset, 0)
	for _, name := range ExpectedChecksummedReleaseAssetNames(version, tag) {
		assets = append(assets, DownloadedAsset{Name: name, SHA256: sha, Size: 128})
	}
	return assets
}

func workflowJobs(names []string) []JobSummary {
	jobs := make([]JobSummary, 0, len(names))
	for index, name := range names {
		jobs = append(jobs, JobSummary{
			Name:       name,
			Status:     "completed",
			Conclusion: "success",
			URL:        "https://github.com/PayCal-Technologies/vigil-public/actions/runs/1/job/" + strings.ReplaceAll(strings.ToLower(name), " ", "-") + "-" + string(rune('a'+index)),
		})
	}
	return jobs
}

package contracts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var actionUsePattern = regexp.MustCompile(`(?m)^\s*-\s+uses:\s+([^@\s]+)@([^\s]+)`)
var immutableActionRefPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func TestGitHubActionsUseImmutablePinsAndCurrentGoVersion(t *testing.T) {
	root := filepath.Join("..", "..")
	goModule, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(goModule))
	goVersion := ""
	for index, field := range fields {
		if field == "go" && index+1 < len(fields) {
			goVersion = fields[index+1]
			break
		}
	}
	if goVersion == "" {
		t.Fatal("go.mod has no Go version")
	}

	paths := []string{
		".github/workflows/nightly.yml",
		".github/workflows/release.yml",
		".github/workflows/vigil.yml",
	}
	for _, relative := range paths {
		t.Run(relative, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			matches := actionUsePattern.FindAllStringSubmatch(string(data), -1)
			if len(matches) == 0 {
				t.Fatal("workflow has no external action uses")
			}
			for _, match := range matches {
				if !immutableActionRefPattern.MatchString(match[2]) {
					t.Fatalf("action %s uses mutable ref %q", match[1], match[2])
				}
			}
			if strings.Contains(string(data), "actions/setup-go@") &&
				!strings.Contains(string(data), "go-version: '"+goVersion+"'") {
				t.Fatalf("workflow does not pin go.mod version %s", goVersion)
			}
		})
	}
}

func TestReleaseChannelsRetainRequiredEvidence(t *testing.T) {
	root := filepath.Join("..", "..")
	release, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"Classify release channel",
		"Capture v1 acceptance gate report",
		"-o bin/v1-acceptance-check",
		"./scripts/v1-acceptance-check.go",
		"./bin/v1-acceptance-check",
		"--json > dist/v1-acceptance-gate.json",
		"Upload v1 acceptance gate report",
		"v1-acceptance-gate-${{ github.ref_name }}-${{ github.sha }}",
		"Refuse release asset replacement",
		"environment: release",
		"scripts/check-release-reproducibility.sh dist",
		"scripts/v1-acceptance-check.go",
		"steps.v1_acceptance.outputs.exit_code",
		"scripts/collect-v1-operational-evidence.go",
		`--workflow-run-id "$GITHUB_RUN_ID"`,
		"sign-macos:",
		"Verify immutable releases are enabled",
		"RELEASE_ADMIN_READ_TOKEN",
		"repos/${GITHUB_REPOSITORY}/immutable-releases",
		"X-GitHub-Api-Version: 2026-03-10",
		"scripts/sign-notarize-macos-archive.sh",
		"*.notary-*.json",
		"dist/v1-acceptance-gate.json",
		"dist/vigil.rb",
		"Create draft release",
		`gh release create "$GITHUB_REF_NAME"`,
		"v1-acceptance-gate.json",
		"--draft",
		"Download uploaded draft assets",
		"Verify uploaded draft assets",
		"native-release-smoke:",
		"ubuntu-24.04-arm",
		"macos-15-intel",
		"runner: macos-15",
		"Verify native archive provenance and checksum",
		"Smoke test native downloaded archive",
		"scripts/smoke-release-archive.sh",
		"homebrew-candidate-test:",
		"Homebrew draft install test",
		`local_url="file://`,
		`local_archive="downloaded/vigil-${version}.tar.gz"`,
		`cmp "downloaded/$archive" "$local_archive"`,
		"brew tap-new --no-git",
		`brew audit --strict "$tap/vigil"`,
		`'.formulae[0].versions.stable'`,
		"publish-verified-release:",
		"Publish verified release",
		"--draft=false",
		"isImmutable",
		`gh release verify "$GITHUB_REF_NAME"`,
		"homebrew-public-test:",
		"Download and verify immutable public release formula",
		`gh release verify-asset "$GITHUB_REF_NAME" downloaded/vigil.rb`,
		`tap="vigil/public-release"`,
		`brew audit --strict --online --installed "$tap/vigil"`,
		"publish-homebrew:",
		"PayCal-Technologies/homebrew-tap",
		"--prerelease=false",
		"Publish draft in its final channel state",
	} {
		if !strings.Contains(string(release), required) {
			t.Fatalf("release workflow is missing %q", required)
		}
	}

	nightly, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "nightly.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"schedule:",
		"SKIP_HOMEBREW_FORMULA: '1'",
		"scripts/check-release-reproducibility.sh dist",
		"cosign sign-blob",
		"actions/attest@",
		"Upload nightly channel artifact",
		"Download nightly channel artifact",
		"Verify downloaded nightly channel",
		"scripts/smoke-release-archive.sh",
		"retention-days: 14",
	} {
		if !strings.Contains(string(nightly), required) {
			t.Fatalf("nightly workflow is missing %q", required)
		}
	}
}

func TestV1OperationalEvidenceCollectorIsDocumented(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, relative := range []string{
		"scripts/collect-v1-operational-evidence.go",
		"docs/releasing.md",
		"docs/reviews/README.md",
	} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("missing v1 operational evidence artifact %s: %v", relative, err)
		}
	}
	releasing, err := os.ReadFile(filepath.Join(root, "docs", "releasing.md"))
	if err != nil {
		t.Fatal(err)
	}
	reviews, err := os.ReadFile(filepath.Join(root, "docs", "reviews", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"collect-v1-operational-evidence.go",
		"--require-release-proof",
		"--workflow-run-id",
		"--plugin-ceremony-url",
		"--require-plugin-index-proof",
		"VIGIL-AC-09",
		"VIGIL-AC-18",
	} {
		if !strings.Contains(string(releasing), required) {
			t.Fatalf("release documentation is missing %q", required)
		}
		if !strings.Contains(string(reviews), required) {
			t.Fatalf("review evidence documentation is missing %q", required)
		}
	}
}

func TestMacOSReleaseScriptsRequireSigningNotarizationAndGatekeeper(t *testing.T) {
	root := filepath.Join("..", "..")
	signing, err := os.ReadFile(filepath.Join(root, "scripts", "sign-notarize-macos-archive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"APPLE_SIGNING_IDENTITY",
		"--options runtime",
		"--timestamp",
		"notarytool submit",
		"--wait",
		"notarytool log",
		`submission_status" != "Accepted`,
		"spctl --assess --type execute",
		"vigil-release-archive",
	} {
		if !strings.Contains(string(signing), required) {
			t.Fatalf("macOS signing script is missing %q", required)
		}
	}

	smoke, err := os.ReadFile(filepath.Join(root, "scripts", "smoke-release-archive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"ALLOW_UNSIGNED_MACOS_SMOKE",
		"archive entry escapes expected root",
		"archive contains a non-regular entry",
		"codesign --verify --strict",
		"spctl --assess --type execute",
		`repo_root="$(cd -- "$script_dir/.." && pwd)"`,
		`find "$repo_root/schemas" -maxdepth 1 -type f -name '*.schema.json'`,
		`jq empty "$packaged_schema"`,
		"archive schema count mismatch",
	} {
		if !strings.Contains(string(smoke), required) {
			t.Fatalf("release smoke script is missing %q", required)
		}
	}
}

func TestReleaseBuilderGuardsDestructiveOutputPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "build-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		`"$dist_input" == "/"`,
		`"$dist_name" == "."`,
		`"$dist_name" == ".."`,
		`"$repo_root/.git/"`,
		`-L "$dist"`,
		`rm -rf -- "$dist"`,
		`v1-acceptance-gate.json`,
		`gate_tmp="$(mktemp -d)"`,
		`gate_checker="$gate_tmp/v1-acceptance-check"`,
		`go build -mod=readonly -buildvcs=false -trimpath -o "$gate_checker" ./scripts/v1-acceptance-check.go`,
		`gate_report="$dist/v1-acceptance-gate.json"`,
		`"$gate_checker" --version "$version" --json > "$gate_report"`,
		`gate_status=$?`,
		`exit "$gate_status"`,
		`"$gate_checker" --version "$version"`,
		`cp "$dist/v1-acceptance-gate.json" "$stage/"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("release builder is missing destructive-path guard %q", required)
		}
	}
}

func TestOperationalCollectorExpectsAcceptanceGateReleaseAsset(t *testing.T) {
	root := filepath.Join("..", "..")
	collector, err := os.ReadFile(filepath.Join(root, "scripts", "collect-v1-operational-evidence.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`"v1-acceptance-gate.json"`,
		"verifyDownloadedAcceptanceGate",
		"acceptance.ValidateReleaseGateReport",
		"DisallowUnknownFields",
		"must contain exactly one JSON document",
	} {
		if !strings.Contains(string(collector), required) {
			t.Fatalf("operational collector must validate the v1 acceptance gate release asset: missing %q", required)
		}
	}
	smoke, err := os.ReadFile(filepath.Join(root, "scripts", "smoke-release-archive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"v1-acceptance-gate.json",
		`.schema_version == "1"`,
		`.target == "v1.0"`,
		`.status == "not_required" or .status == "satisfied"`,
	} {
		if !strings.Contains(string(smoke), required) {
			t.Fatalf("release archive smoke is missing acceptance gate check %q", required)
		}
	}
}

func TestQualityWorkflowEnforcesPerformanceBudgetsOnEveryPlatform(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "vigil.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if !strings.Contains(workflow, "os: [ubuntu-latest, macos-latest]") ||
		!strings.Contains(workflow, "Enforce startup performance budgets") ||
		!strings.Contains(workflow, "run: scripts/check-performance.sh") ||
		!strings.Contains(workflow, "Fuzz parser and boundary smoke") ||
		!strings.Contains(workflow, "run: scripts/run-fuzz-smoke.sh") {
		t.Fatal("quality workflow no longer enforces cross-platform startup budgets and bounded fuzz smoke")
	}
}

package contracts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PayCal-Technologies/vigil-public/internal/acceptance"
)

func TestV1AcceptanceCheckerJSONContract(t *testing.T) {
	root := filepath.Join("..", "..")
	script, err := os.ReadFile(filepath.Join(root, "scripts", "v1-acceptance-check.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"repositoryHeadCommit",
		"ValidateRepositoryEvidenceForCandidate",
		"EvidenceCandidate",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("v1 acceptance checker must bind evidence to the release candidate: missing %q", required)
		}
	}

	binary := filepath.Join(t.TempDir(), "v1-acceptance-check")
	build := exec.Command("go", "build", "-mod=readonly", "-buildvcs=false", "-trimpath", "-o", binary, "./scripts/v1-acceptance-check.go")
	build.Dir = root
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build v1 acceptance checker: %v\n%s", err, output)
	}

	ok, code := runGateChecker(t, root, binary, "--version", "0.4.0", "--json")
	if code != 0 || ok.Status != acceptance.GateStatusNotRequired || ok.Version != "0.4.0" {
		t.Fatalf("pre-v1 report code=%d report=%#v", code, ok)
	}

	blocked, code := runGateChecker(t, root, binary, "--version", "1.0.0", "--json")
	if code != 1 || blocked.Status != acceptance.GateStatusBlocked || blocked.PendingCount != 10 {
		t.Fatalf("blocked report code=%d report=%#v", code, blocked)
	}

	missingVersion, code := runGateChecker(t, root, binary, "--json")
	if code != 2 || missingVersion.Status != acceptance.GateStatusInvalid || !strings.Contains(strings.Join(missingVersion.Errors, "\n"), "usage:") {
		t.Fatalf("missing-version report code=%d report=%#v", code, missingVersion)
	}

	badFlag, code := runGateChecker(t, root, binary, "--version", "0.4.0", "--json", "--bogus")
	if code != 2 || badFlag.Status != acceptance.GateStatusInvalid || badFlag.Version != "0.4.0" ||
		!strings.Contains(strings.Join(badFlag.Errors, "\n"), "flag provided but not defined") {
		t.Fatalf("bad-flag report code=%d report=%#v", code, badFlag)
	}

	repeatedJSON, code := runGateChecker(t, root, binary, "--version", "0.4.0", "--json=false", "--json", "--bogus")
	if code != 2 || repeatedJSON.Status != acceptance.GateStatusInvalid || repeatedJSON.Version != "0.4.0" {
		t.Fatalf("repeated-json report code=%d report=%#v", code, repeatedJSON)
	}

	malformedVersion, code := runGateChecker(t, root, binary, "--version", "--json", "--bogus")
	if code != 2 || malformedVersion.Status != acceptance.GateStatusInvalid || malformedVersion.Version != "" {
		t.Fatalf("malformed-version report code=%d report=%#v", code, malformedVersion)
	}
}

func runGateChecker(t *testing.T, root, binary string, args ...string) (acceptance.GateReport, int) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = root
	output, err := command.Output()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run checker %v: %v", args, err)
		}
		code = exitErr.ExitCode()
		output = append(output, exitErr.Stderr...)
	}
	var report acceptance.GateReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("checker output is not JSON for %v: %v\n%s", args, err, output)
	}
	if err := acceptance.ValidateGateReport(report); err != nil {
		t.Fatalf("checker report is invalid for %v: %v\n%s", args, err, output)
	}
	return report, code
}

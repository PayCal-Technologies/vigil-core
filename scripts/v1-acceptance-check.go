//go:build ignore

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/PayCal-Technologies/vigil-public/internal/acceptance"
)

const usage = "usage: go run ./scripts/v1-acceptance-check.go --version VERSION [--ledger PATH] [--json]"

func main() {
	args := os.Args[1:]
	jsonRequested := wantsJSON(args)
	reportVersion := argValue(args, "version")
	reportLedger := argValue(args, "ledger")
	if reportLedger == "" {
		reportLedger = acceptance.CanonicalLedgerPath
	}

	fs := flag.NewFlagSet("v1-acceptance-check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	version := fs.String("version", "", "release version without the v prefix")
	ledgerPath := fs.String("ledger", acceptance.CanonicalLedgerPath, "acceptance ledger path")
	jsonOutput := fs.Bool("json", false, "emit a schema-versioned acceptance gate report")
	if err := fs.Parse(args); err != nil {
		failInvalid(jsonRequested, reportVersion, reportLedger, err)
		os.Exit(2)
	}
	if *version == "" || fs.NArg() != 0 {
		if *jsonOutput {
			writeJSON(acceptance.InvalidGateReport(*version, *ledgerPath, errors.New(usage)))
		} else {
			fmt.Fprintln(os.Stderr, usage)
		}
		os.Exit(2)
	}

	ledger, err := acceptance.Read(*ledgerPath)
	if err != nil {
		failInvalid(*jsonOutput, *version, *ledgerPath, err)
		os.Exit(2)
	}
	gateRequired, err := acceptance.GateRequiredForVersion(*version)
	if err != nil {
		failInvalid(*jsonOutput, *version, *ledgerPath, err)
		os.Exit(2)
	}
	if gateRequired {
		candidateCommit, err := repositoryHeadCommit(".")
		if err != nil {
			failInvalid(*jsonOutput, *version, *ledgerPath, err)
			os.Exit(2)
		}
		if err := acceptance.ValidateRepositoryEvidenceForCandidate(".", ledger, acceptance.EvidenceCandidate{
			Commit:  candidateCommit,
			Version: *version,
		}); err != nil {
			failInvalid(*jsonOutput, *version, *ledgerPath, err)
			os.Exit(2)
		}
	} else if err := acceptance.ValidateRepositoryEvidence(".", ledger); err != nil {
		failInvalid(*jsonOutput, *version, *ledgerPath, err)
		os.Exit(2)
	}
	report, err := acceptance.BuildGateReport(*version, *ledgerPath, ledger)
	if err != nil {
		failInvalid(*jsonOutput, *version, *ledgerPath, err)
		os.Exit(2)
	}

	if *jsonOutput {
		writeJSON(report)
		if report.Status == acceptance.GateStatusBlocked {
			os.Exit(1)
		}
		return
	}
	if !report.GateRequired {
		fmt.Printf("v1 acceptance completion is not required for prerelease or pre-v1 version %s\n", *version)
		return
	}
	if report.PendingCount > 0 {
		fmt.Fprintf(os.Stderr, "stable version %s is blocked by %d v1 acceptance criteria:\n", *version, report.PendingCount)
		for _, criterion := range report.Pending {
			fmt.Fprintf(os.Stderr, "- %s [%s]: %s\n", criterion.ID, criterion.Status, criterion.Blocker)
		}
		os.Exit(1)
	}
	fmt.Printf("stable version %s satisfies all v1 acceptance criteria\n", *version)
}

func failInvalid(jsonOutput bool, version, ledgerPath string, err error) {
	if jsonOutput {
		writeJSON(acceptance.InvalidGateReport(version, ledgerPath, err))
		return
	}
	fmt.Fprintln(os.Stderr, err)
}

func writeJSON(report acceptance.GateReport) {
	if err := acceptance.ValidateGateReport(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	data = append(data, '\n')
	_, _ = os.Stdout.Write(data)
}

func repositoryHeadCommit(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve repository HEAD commit: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(string(output))), nil
}

func wantsJSON(args []string) bool {
	requested := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json" || arg == "-json":
			requested = true
		case arg == "--json=false" || arg == "-json=false":
			requested = false
		case strings.HasPrefix(arg, "--json="):
			value, err := strconv.ParseBool(strings.TrimPrefix(arg, "--json="))
			if err == nil {
				requested = value
			}
		case strings.HasPrefix(arg, "-json="):
			value, err := strconv.ParseBool(strings.TrimPrefix(arg, "-json="))
			if err == nil {
				requested = value
			}
		}
	}
	return requested
}

func argValue(args []string, name string) string {
	long := "--" + name
	short := "-" + name
	for index := 0; index < len(args); index++ {
		arg := args[index]
		for _, prefix := range []string{long + "=", short + "="} {
			if strings.HasPrefix(arg, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(arg, prefix))
			}
		}
		if (arg == long || arg == short) && index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
			return strings.TrimSpace(args[index+1])
		}
	}
	return ""
}

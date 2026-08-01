//go:build ignore

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/PayCal-Technologies/vigil-public/internal/externalevidence"
)

const usage = "usage: go run ./scripts/validate-v1-external-evidence.go --report PATH [--criterion VIGIL-AC-NN] [--json]"

func main() {
	args := os.Args[1:]
	jsonRequested := wantsJSON(args)
	reportPathForError := argValue(args, "report")
	criterionForError := argValue(args, "criterion")

	fs := flag.NewFlagSet("validate-v1-external-evidence", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	reportPath := fs.String("report", "", "external evidence report JSON path")
	criterion := fs.String("criterion", "", "optional acceptance criterion that the report must verify")
	jsonOutput := fs.Bool("json", false, "emit a schema-versioned validation report")
	if err := fs.Parse(args); err != nil {
		failInvalid(jsonRequested, reportPathForError, criterionForError, err)
		os.Exit(2)
	}
	if strings.TrimSpace(*reportPath) == "" || fs.NArg() != 0 {
		failInvalid(*jsonOutput, *reportPath, *criterion, errors.New(usage))
		os.Exit(2)
	}
	data, err := os.ReadFile(*reportPath)
	if err != nil {
		failInvalid(*jsonOutput, *reportPath, *criterion, err)
		os.Exit(2)
	}

	result := externalevidence.BuildValidationReport(*reportPath, *criterion, data)
	if *jsonOutput {
		writeJSON(result)
		if result.Status == externalevidence.ValidationStatusInvalid {
			os.Exit(1)
		}
		return
	}
	if result.Status == externalevidence.ValidationStatusInvalid {
		for _, err := range result.Errors {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
	if strings.TrimSpace(*criterion) != "" {
		fmt.Printf("external evidence report %s verifies %s\n", *reportPath, *criterion)
		return
	}
	if len(result.VerifiedCriteria) == 0 {
		fmt.Printf("external evidence report %s is valid with no verified criteria\n", *reportPath)
		return
	}
	fmt.Printf("external evidence report %s is valid; verified criteria: %s\n", *reportPath, strings.Join(result.VerifiedCriteria, ", "))
}

func failInvalid(jsonOutput bool, reportPath, criterion string, err error) {
	if jsonOutput {
		writeJSON(externalevidence.InvalidValidationReport(reportPath, criterion, err))
		return
	}
	fmt.Fprintln(os.Stderr, err)
}

func writeJSON(report externalevidence.ValidationReport) {
	if err := externalevidence.ValidateValidationReport(report); err != nil {
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

func wantsJSON(args []string) bool {
	requested := false
	for _, arg := range args {
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

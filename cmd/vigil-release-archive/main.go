package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	vigiloutput "github.com/PayCal-Technologies/vigil-public/internal/output"
	"github.com/PayCal-Technologies/vigil-public/internal/releasearchive"
)

func main() {
	source := flag.String("source", "", "directory to archive")
	root := flag.String("root", "", "archive root directory")
	output := flag.String("output", "", "output .tar.gz path")
	epoch := flag.String("epoch", "", "SOURCE_DATE_EPOCH value")
	stream := flag.String("stream", "", "stream phase status: text or jsonl")
	verbose := flag.Bool("verbose", false, "stream text phase status")
	flag.Parse()
	reporter, err := archiveStreamReporter(*stream, *verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	seconds, err := strconv.ParseInt(*epoch, 10, 64)
	if err != nil {
		if reporter != nil {
			_ = reporter.Fail("parse epoch", 2, 0, err.Error())
		}
		fmt.Fprintln(os.Stderr, "invalid epoch:", err)
		os.Exit(2)
	}
	started := time.Now()
	if reporter != nil {
		_ = reporter.Start("archive release", *output)
	}
	if err := releasearchive.Write(releasearchive.Options{
		Source:      *source,
		ArchiveRoot: *root,
		Output:      *output,
		ModTime:     time.Unix(seconds, 0),
	}); err != nil {
		if reporter != nil {
			_ = reporter.Fail("archive release", 1, time.Since(started), err.Error())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if reporter != nil {
		_ = reporter.OK("archive release", time.Since(started), *output)
	}
}

func archiveStreamReporter(stream string, verbose bool) (*vigiloutput.StreamReporter, error) {
	stream = strings.ToLower(strings.TrimSpace(stream))
	if stream == "" && !verbose {
		return nil, nil
	}
	format := vigiloutput.FormatText
	switch stream {
	case "", "text":
	case "jsonl":
		format = vigiloutput.FormatJSONL
	default:
		return nil, fmt.Errorf("--stream must be text or jsonl")
	}
	return vigiloutput.NewStreamReporter(vigiloutput.StreamOptions{
		Writer:  os.Stderr,
		Command: "vigil-release-archive",
		Format:  format,
		Verbose: verbose,
	}), nil
}

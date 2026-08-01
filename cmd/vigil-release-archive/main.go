package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/releasearchive"
)

func main() {
	source := flag.String("source", "", "directory to archive")
	root := flag.String("root", "", "archive root directory")
	output := flag.String("output", "", "output .tar.gz path")
	epoch := flag.String("epoch", "", "SOURCE_DATE_EPOCH value")
	flag.Parse()
	seconds, err := strconv.ParseInt(*epoch, 10, 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid epoch:", err)
		os.Exit(2)
	}
	if err := releasearchive.Write(releasearchive.Options{
		Source:      *source,
		ArchiveRoot: *root,
		Output:      *output,
		ModTime:     time.Unix(seconds, 0),
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

//go:build ignore

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type budget struct {
	name    string
	args    []string
	median  time.Duration
	samples []time.Duration
}

func main() {
	binary := flag.String("binary", "", "path to the Vigil binary")
	sampleCount := flag.Int("samples", 21, "number of measured samples")
	flag.Parse()
	if strings.TrimSpace(*binary) == "" || *sampleCount < 5 || *sampleCount > 101 {
		fmt.Fprintln(os.Stderr, "--binary is required and --samples must be between 5 and 101")
		os.Exit(2)
	}
	resolvedBinary, err := filepath.Abs(*binary)
	if err != nil {
		fail(err)
	}
	emptyDirectory, err := os.MkdirTemp("", "vigil-performance-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(emptyDirectory)

	budgets := []budget{
		{name: "version", args: []string{"version"}, median: 20 * time.Millisecond},
		{name: "help", args: []string{"help"}, median: 50 * time.Millisecond},
		{name: "discovery", args: []string{"list", "--json"}, median: 100 * time.Millisecond},
		{name: "setup-detection", args: []string{"setup", "--dry-run", "--json"}, median: 500 * time.Millisecond},
	}
	environment := withEnvironment(map[string]string{
		"CI":                   "1",
		"NO_COLOR":             "1",
		"VIGIL_PLUGIN_ROOT":    filepath.Join(emptyDirectory, "plugins"),
		"VIGIL_USER_PACK_ROOT": filepath.Join(emptyDirectory, "packs"),
	})
	failed := false
	for index := range budgets {
		current := &budgets[index]
		for warmup := 0; warmup < 3; warmup++ {
			if _, err := measure(resolvedBinary, emptyDirectory, environment, current.args); err != nil {
				fail(fmt.Errorf("%s warmup: %w", current.name, err))
			}
		}
		current.samples = make([]time.Duration, 0, *sampleCount)
		for sample := 0; sample < *sampleCount; sample++ {
			duration, err := measure(resolvedBinary, emptyDirectory, environment, current.args)
			if err != nil {
				fail(fmt.Errorf("%s sample: %w", current.name, err))
			}
			current.samples = append(current.samples, duration)
		}
		sort.Slice(current.samples, func(i, j int) bool {
			return current.samples[i] < current.samples[j]
		})
		median := current.samples[len(current.samples)/2]
		p95Index := int(math.Ceil(float64(len(current.samples))*0.95)) - 1
		p95 := current.samples[p95Index]
		status := "ok"
		if median > current.median {
			status = "fail"
			failed = true
		}
		fmt.Printf(
			"%s %s/%s median=%s p95=%s budget=%s samples=%d\n",
			status,
			runtime.GOOS,
			runtime.GOARCH,
			roundedDuration(median),
			roundedDuration(p95),
			current.median,
			len(current.samples),
		)
	}
	if failed {
		os.Exit(1)
	}
}

func measure(binary, directory string, environment, args []string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Env = environment
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	started := time.Now()
	err := command.Run()
	duration := time.Since(started)
	if ctx.Err() != nil {
		return duration, ctx.Err()
	}
	return duration, err
}

func roundedDuration(duration time.Duration) time.Duration {
	return duration.Round(100 * time.Microsecond)
}

func withEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if _, overridden := overrides[key]; !overridden {
			environment = append(environment, value)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

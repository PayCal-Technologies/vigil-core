package git

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	MaxUntrackedFiles = 100_000
	MaxUntrackedBytes = int64(2 << 30)
)

type CommandRunner func(args ...string) (output string, exitCode int)

type Fingerprint struct {
	Hash  string
	Clean bool
}

type StatusEntry struct {
	Status       string
	Path         string
	OriginalPath string
}

func Root(run CommandRunner) string {
	root, _ := RootResult(run)
	return root
}

func RootResult(run CommandRunner) (string, int) {
	output, code := run("rev-parse", "--show-toplevel")
	if code != 0 {
		return "", code
	}
	return strings.TrimSpace(output), 0
}

func MutationFingerprint(run CommandRunner) (Fingerprint, bool) {
	root := Root(run)
	if root == "" {
		return Fingerprint{}, false
	}
	status, code := run("status", "--porcelain")
	if code != 0 {
		return Fingerprint{}, false
	}
	unstaged, code := run("diff", "--binary")
	if code != 0 {
		return Fingerprint{}, false
	}
	staged, code := run("diff", "--cached", "--binary")
	if code != 0 {
		return Fingerprint{}, false
	}
	untracked, ok := untrackedFingerprint(root, run)
	if !ok {
		return Fingerprint{}, false
	}
	sum := sha256.Sum256([]byte(status + "\x00" + unstaged + "\x00" + staged + "\x00" + untracked))
	return Fingerprint{
		Hash:  fmt.Sprintf("%x", sum[:]),
		Clean: strings.TrimSpace(status) == "",
	}, true
}

func LinesResult(run CommandRunner, args ...string) ([]string, bool) {
	output, code := run(args...)
	if code != 0 {
		return nil, false
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, true
	}
	return lines, true
}

func PathsResult(run CommandRunner, args ...string) ([]string, bool) {
	output, code := run(args...)
	if code != 0 {
		return nil, false
	}
	if output == "" {
		return []string{}, true
	}
	if !strings.HasSuffix(output, "\x00") {
		return nil, false
	}
	records := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	paths := make([]string, 0, len(records))
	for _, record := range records {
		if record != "" {
			paths = append(paths, record)
		}
	}
	return paths, true
}

func StatusResult(run CommandRunner) ([]StatusEntry, bool) {
	records, ok := PathsResult(run, "status", "--porcelain=v1", "-z")
	if !ok {
		return nil, false
	}
	entries := make([]StatusEntry, 0, len(records))
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 4 || record[2] != ' ' {
			return nil, false
		}
		entry := StatusEntry{
			Status: record[:2],
			Path:   record[3:],
		}
		if entry.Path == "" {
			return nil, false
		}
		if strings.ContainsAny(entry.Status, "RC") {
			index++
			if index >= len(records) || records[index] == "" {
				return nil, false
			}
			entry.OriginalPath = records[index]
		}
		entries = append(entries, entry)
	}
	return entries, true
}

func MutationEvidence(run CommandRunner) []byte {
	var evidence strings.Builder
	for _, command := range [][]string{
		{"status", "--porcelain=v1"},
		{"diff", "--binary"},
		{"diff", "--cached", "--binary"},
	} {
		evidence.WriteString("$ git " + strings.Join(command, " ") + "\n")
		output, code := run(command...)
		evidence.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			evidence.WriteByte('\n')
		}
		evidence.WriteString(fmt.Sprintf("[exit %d]\n\n", code))
	}
	return []byte(evidence.String())
}

func untrackedFingerprint(root string, run CommandRunner) (string, bool) {
	output, code := run("ls-files", "-z", "--others", "--exclude-standard")
	if code != 0 {
		return "", false
	}
	files := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	if len(files) == 1 && files[0] == "" {
		return "", true
	}
	if len(files) > MaxUntrackedFiles {
		return "", false
	}
	sort.Strings(files)
	hash := sha256.New()
	var totalBytes int64
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		info, err := os.Lstat(path)
		if err != nil {
			return "", false
		}
		if info.IsDir() {
			continue
		}
		_, _ = io.WriteString(hash, file)
		_, _ = hash.Write([]byte{0})
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", false
			}
			totalBytes += int64(len(target))
			if totalBytes > MaxUntrackedBytes {
				return "", false
			}
			_, _ = io.WriteString(hash, "symlink\x00"+target)
		} else {
			if !info.Mode().IsRegular() || info.Size() > MaxUntrackedBytes-totalBytes {
				return "", false
			}
			input, err := os.Open(path)
			if err != nil {
				return "", false
			}
			copied, copyErr := io.Copy(hash, io.LimitReader(input, MaxUntrackedBytes-totalBytes+1))
			closeErr := input.Close()
			if copyErr != nil || closeErr != nil || copied > MaxUntrackedBytes-totalBytes {
				return "", false
			}
			totalBytes += copied
		}
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), true
}

package main

import (
	"context"
	vigilgit "github.com/PayCal-Technologies/vigil-public/internal/git"
	"github.com/PayCal-Technologies/vigil-public/internal/runner"
	"time"
)

func gitRoot() string {
	return vigilgit.Root(runGitCommand)
}

func gitRootResult() (string, int) {
	root, code := vigilgit.RootResult(runGitCommand)
	if code == 0 && root == "" {
		return "", exitInternal
	}
	return root, code
}

func gitInvocationFailedInternally(code int) bool {
	return code == exitDependencyMissing || code == exitInterrupted || code == exitInternal
}

type mutationFingerprint = vigilgit.Fingerprint

func gitMutationFingerprint() (mutationFingerprint, bool) {
	return vigilgit.MutationFingerprint(runGitCommand)
}

func gitLinesChecked(args ...string) ([]string, bool) {
	return vigilgit.LinesResult(runGitCommand, args...)
}

func gitPathsChecked(args ...string) ([]string, bool) {
	return vigilgit.PathsResult(runGitCommand, args...)
}

func gitStatusChecked() ([]vigilgit.StatusEntry, bool) {
	return vigilgit.StatusResult(runGitCommand)
}

func runGitCommand(args ...string) (string, int) {
	return runCommand("git", args...)
}

func runCommand(name string, args ...string) (string, int) {
	return runCommandWithCaptureLimit(name, 32*1024*1024, args...)
}

func runCommandWithCaptureLimit(name string, captureLimit int, args ...string) (string, int) {
	result := runner.Run(context.Background(), runner.Spec{
		Name:         name,
		Mode:         runner.ModeArgv,
		Executable:   name,
		Args:         args,
		Timeout:      2 * time.Minute,
		CaptureLimit: captureLimit,
	})
	if result.Truncated {
		return runnerOutput(result), exitInternal
	}
	return runnerOutput(result), result.ExitCode
}

func runnerOutput(result runner.Result) string {
	if result.Error == "" {
		return result.Output
	}
	if result.Output == "" {
		return result.Error
	}
	separator := "\n"
	if result.Output[len(result.Output)-1] == '\n' {
		separator = ""
	}
	return result.Output + separator + result.Error
}

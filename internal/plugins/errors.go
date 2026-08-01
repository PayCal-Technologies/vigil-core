package plugins

import (
	"errors"
	"fmt"

	"github.com/PayCal-Technologies/vigil-public/internal/cli"
)

type ErrorKind string

const (
	ErrorInvalid     ErrorKind = "invalid"
	ErrorBlocked     ErrorKind = "blocked"
	ErrorMissing     ErrorKind = "missing"
	ErrorInterrupted ErrorKind = "interrupted"
	ErrorInternal    ErrorKind = "internal"
)

type PluginError struct {
	Kind ErrorKind
	Op   string
	Err  error
}

func (e *PluginError) Error() string {
	if e.Op == "" {
		return e.Err.Error()
	}
	return e.Op + ": " + e.Err.Error()
}

func (e *PluginError) Unwrap() error {
	return e.Err
}

func pluginError(kind ErrorKind, op, format string, args ...any) error {
	return &PluginError{Kind: kind, Op: op, Err: fmt.Errorf(format, args...)}
}

func wrapPluginError(kind ErrorKind, op string, err error) error {
	if err == nil {
		return nil
	}
	return &PluginError{Kind: kind, Op: op, Err: err}
}

func BlockedError(operation, message string) error {
	return pluginError(ErrorBlocked, operation, "%s", message)
}

func InvalidError(operation, message string) error {
	return pluginError(ErrorInvalid, operation, "%s", message)
}

func ExitCode(err error) int {
	if err == nil {
		return cli.ExitSuccess
	}
	var typed *PluginError
	if !errors.As(err, &typed) {
		return cli.ExitInternal
	}
	switch typed.Kind {
	case ErrorInvalid:
		return cli.ExitUsage
	case ErrorBlocked:
		return cli.ExitPolicyBlocked
	case ErrorMissing:
		return cli.ExitDependencyMissing
	case ErrorInterrupted:
		return cli.ExitInterrupted
	default:
		return cli.ExitInternal
	}
}

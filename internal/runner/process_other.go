//go:build plan9 || js || wasip1

package runner

import (
	"os"
	"os/exec"
	"time"
)

func configureProcess(command *exec.Cmd) {
	command.WaitDelay = 250 * time.Millisecond
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return command.Process.Kill()
	}
}

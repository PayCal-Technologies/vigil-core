//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 250 * time.Millisecond
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		pid := command.Process.Pid
		err := syscall.Kill(-pid, syscall.SIGTERM)
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		go func() {
			timer := time.NewTimer(200 * time.Millisecond)
			defer timer.Stop()
			<-timer.C
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}()
		return nil
	}
}

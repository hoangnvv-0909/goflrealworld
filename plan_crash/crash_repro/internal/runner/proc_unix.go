//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// prepare puts the command in its own process group and kills the group on
// cancellation, so the test binary started by go test dies too.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

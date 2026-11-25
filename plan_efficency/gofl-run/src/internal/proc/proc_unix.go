//go:build unix

package proc

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand runs line through sh.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", line)
}

// killTree puts the command in its own process group and makes cancellation
// kill the whole group.
func killTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

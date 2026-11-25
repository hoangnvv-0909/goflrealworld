//go:build windows

package proc

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// shellCommand runs line through cmd.exe. /S keeps the quotes inside line as
// they are.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + line + `"`}
	return cmd
}

// killTree makes cancellation kill the process and all its children.
func killTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if kill.Run() == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

//go:build windows

package runner

import (
	"os/exec"
	"strconv"
)

// prepare makes cancellation kill the whole process tree: go test starts the
// test binary as a child, which would otherwise keep running after a timeout.
func prepare(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}

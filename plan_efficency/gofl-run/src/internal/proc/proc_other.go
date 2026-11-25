//go:build !unix && !windows

package proc

import (
	"context"
	"os/exec"
)

func shellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", line)
}

func killTree(cmd *exec.Cmd) {}

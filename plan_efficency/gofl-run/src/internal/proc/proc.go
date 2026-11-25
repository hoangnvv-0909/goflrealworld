// Package proc runs external commands with a time limit. When the limit is
// reached the whole process tree is killed: a benchmark run by a test binary,
// or the children of a setup command, must not outlive gofl.
package proc

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// Spec is one command. Either Name (with Args) or Shell is set; Shell runs
// a command line through sh -c, or cmd /C on Windows.
type Spec struct {
	Name    string
	Args    []string
	Shell   string
	Dir     string
	Env     []string // full environment; nil means the parent's
	Timeout time.Duration
	Out     io.Writer // receives stdout, and stderr unless Err is set; nil discards them
	Err     io.Writer // receives stderr when set
}

// Result says how the command ended.
type Result struct {
	ExitCode int           // -1 if it did not exit by itself
	TimedOut bool          // the time limit was reached
	Err      error         // the command could not start, or ctx was cancelled
	Elapsed  time.Duration // wall time
}

// OK reports whether the command ran and exited with status 0.
func (r Result) OK() bool { return r.Err == nil && !r.TimedOut && r.ExitCode == 0 }

// grace is how long Wait keeps waiting for output after the process tree was
// killed, in case a grandchild still holds the pipe.
const grace = 5 * time.Second

// Run runs s and waits for it.
func Run(ctx context.Context, s Spec) Result {
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if s.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, s.Timeout)
	}
	defer cancel()

	var cmd *exec.Cmd
	if s.Shell != "" {
		cmd = shellCommand(runCtx, s.Shell)
	} else {
		cmd = exec.CommandContext(runCtx, s.Name, s.Args...)
	}
	cmd.Dir = s.Dir
	cmd.Env = s.Env
	if s.Out != nil {
		cmd.Stdout = s.Out
		cmd.Stderr = s.Out
	}
	if s.Err != nil {
		cmd.Stderr = s.Err
	}
	killTree(cmd)
	cmd.WaitDelay = grace

	start := time.Now()
	err := cmd.Run()
	res := Result{ExitCode: -1, Elapsed: time.Since(start)}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	switch {
	case ctx.Err() != nil:
		res.Err = ctx.Err()
	case runCtx.Err() == context.DeadlineExceeded:
		res.TimedOut = true
	case err != nil:
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			// ExitCode says it.
		case errors.Is(err, exec.ErrWaitDelay) && res.ExitCode >= 0:
			// It exited, but a child it started in the background (a
			// server a setup command launched) still holds the output.
		default:
			res.Err = err
		}
	}
	return res
}

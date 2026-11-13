// Command gofl ranks suspicious locations in a Go project.
//
// Owner: TV1. Flag names are a draft to be fixed together with the
// interface contract in week 1, day 1-3 (plan, section "San pham cuoi").
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gofl/internal/report"
)

const usage = `gofl - fault localization for Go projects

Usage:
  gofl crash [flags]              read panic stacks from stdin or -log
  gofl perf  [flags] [packages]   compare CPU profiles at two input sizes

Run "gofl <command> -h" for the flags of a command.
`

var errNotImplemented = errors.New("not implemented yet")

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is main without os.Exit, so it can be tested.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "crash":
		err = runCrash(args[1:], stdin, stdout, stderr)
	case "perf":
		err = runPerf(args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "gofl: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	default:
		fmt.Fprintln(stderr, "gofl:", err)
		return 1
	}
}

var errUsage = errors.New("usage error")

// common holds the flags shared by every command.
type common struct {
	format  string
	out     string
	timeout time.Duration
	env     envList
}

func addCommon(fs *flag.FlagSet, c *common) {
	fs.StringVar(&c.format, "format", "md", "output format: md, json or sarif")
	fs.StringVar(&c.out, "o", "", "write output to `file` instead of stdout")
	fs.DurationVar(&c.timeout, "timeout", 5*time.Minute, "time limit for running tests or benchmarks")
	fs.Var(&c.env, "env", "extra `KEY=VALUE` for the test process (repeatable)")
}

func (c *common) validate() error {
	_, err := report.ParseFormat(c.format)
	return err
}

func parse(fs *flag.FlagSet, args []string, c *common) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	if err := c.validate(); err != nil {
		fmt.Fprintln(fs.Output(), "gofl:", err)
		return errUsage
	}
	return nil
}

// envList is a repeatable -env flag.
type envList []string

func (e *envList) String() string { return strings.Join(*e, ",") }

func (e *envList) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("want KEY=VALUE, got %q", v)
	}
	*e = append(*e, v)
	return nil
}

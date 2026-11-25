package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{nil, 2},
		{[]string{"help"}, 0},
		{[]string{"nope"}, 2},
		{[]string{"crash", "-h"}, 0},
		{[]string{"crash", "-format", "xml"}, 2},
		{[]string{"crash", "-env", "NOEQUALS"}, 2},
		{[]string{"crash"}, 0}, // empty input: report with a note
		{[]string{"perf"}, 2},  // needs -bench or -base/-scaled
		{[]string{"perf", "-base", "a.pprof"}, 2},
		{[]string{"perf", "-bench", "BenchmarkX"}, 1}, // not implemented yet
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		if got := run(c.args, strings.NewReader(""), &out, &errOut); got != c.want {
			t.Errorf("gofl %v: exit %d, want %d\nstderr: %s", c.args, got, c.want, errOut.String())
		}
	}
}

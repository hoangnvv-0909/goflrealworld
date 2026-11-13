// Package logprofile is the plugin point for reading application-specific
// log formats into crash events.
//
// Owner: TV3. The default profile reads standard Go panic stacks embedded
// in a log. Not in any paper: this is the team's design.
package logprofile

import (
	"io"
	"iter"
	"time"

	"gofl/internal/crashrank"
)

// Profile turns raw log bytes into crash events.
// Implementations must stream: never read the whole log into memory.
type Profile interface {
	Name() string
	Entries(r io.Reader, loc *time.Location) iter.Seq2[crashrank.Event, error]
}

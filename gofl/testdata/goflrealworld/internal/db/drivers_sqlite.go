package db

// goflrealworld: upstream used github.com/mattn/go-sqlite3 (needs cgo/gcc).
// Replaced with the pure-Go driver so the sample builds on every machine.
import (
	_ "modernc.org/sqlite"
)


`debugstack.txt`: output of `runtime/debug.Stack()` inside a `recover()` in a
tiny program (assignment to a nil map in `(*Store).Save`, line 18). It has no
`panic:` line; `Parse` must still find the crash point.

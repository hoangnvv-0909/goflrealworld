// Package crash holds seeded panic/crash bugs (benchmark layer 1, owner: P4).
// Every line tagged "BUG:<id>" is the ground-truth fault location.
package crash

import "fmt"

type User struct {
	Name    string
	Profile *Profile
}

type Profile struct{ Email string }

// EmailOf returns the user's email.
func EmailOf(u *User) string {
	return u.Profile.Email // BUG:C1 nil pointer dereference when Profile is nil
}

// Nth returns the n-th element (1-based).
func Nth(xs []int, n int) int {
	return xs[n] // BUG:C2 index out of range: should be xs[n-1] guarded by bounds check
}

// Registry maps names to counters.
type Registry struct{ counts map[string]int }

// Inc increments the named counter.
func (r *Registry) Inc(name string) {
	r.counts[name]++ // BUG:C3 assignment to entry in nil map (counts never initialised)
}

// Average returns the mean of xs.
func Average(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum / len(xs) // BUG:C4 integer divide by zero on empty slice
}

// Describe formats a loosely typed value.
func Describe(v any) string {
	s := v.(string) // BUG:C5 unchecked type assertion panics for non-string
	return fmt.Sprintf("value=%s", s)
}

// Finish closes a channel that callers may already have closed.
func Finish(ch chan int) {
	close(ch) // BUG:C6 close of closed channel when Finish is called twice
}

// Process collects the email of every user.
func Process(users []*User) []string {
	var out []string
	for _, u := range users {
		out = append(out, EmailOf(u))
	}
	return out
}

// Counter is shared between goroutines without a lock.
type Counter struct{ m map[int]int }

func NewCounter() *Counter { return &Counter{m: map[int]int{}} }

// Add writes to the shared map.
func (c *Counter) Add(k int) {
	c.m[k]++ // BUG:C7 fatal error: concurrent map writes (no mutex)
}

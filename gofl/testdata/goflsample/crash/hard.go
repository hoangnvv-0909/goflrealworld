// Hard crash cases (benchmark layer 1, group C, week 1 day 4-5).
// Unlike C1-C7, the panic site is often NOT the place that has to be fixed.
// Every line tagged "BUG:<id>" is the ground-truth fault location.
package crash

import (
	"strconv"
	"strings"
	"sync"
)

// ---- C8: deep stack ------------------------------------------------------
// The panic is six frames below the test. Tests whether ranking copes with
// frames from several functions of the same file.

// Run feeds raw "k=v" settings through a five-stage pipeline.
func Run(raw string) map[string]string { return stageSplit(raw) }

func stageSplit(raw string) map[string]string { return stageClean(strings.Split(raw, ";")) }

func stageClean(parts []string) map[string]string {
	var kept []string
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			kept = append(kept, t)
		}
	}
	return stageBuild(kept)
}

func stageBuild(parts []string) map[string]string {
	out := map[string]string{}
	for _, p := range parts {
		k, v := parseKV(p)
		out[k] = v
	}
	return out
}

func parseKV(p string) (string, string) {
	kv := strings.SplitN(p, "=", 2)
	return kv[0], kv[1] // BUG:C8 index out of range when a setting has no "="
}

// ---- C9: one root cause, three different stacks --------------------------
// NewSession forgets to create auth for anonymous users. Three entry points
// crash in Token with different callers. Grouping level 3 should merge them.

type auth struct{ token string }

// Session is a login session.
type Session struct{ auth *auth }

// NewSession creates a session; anonymous sessions have no credentials.
func NewSession(anonymous bool) *Session {
	if anonymous {
		return &Session{} // BUG:C9 auth is left nil, every method below crashes
	}
	return &Session{auth: &auth{token: "t"}}
}

// Token returns the session token.
func (s *Session) Token() string { return s.auth.token }

func (s *Session) Login() string   { return "login:" + s.Token() }
func (s *Session) Logout() string  { return "logout:" + s.Token() }
func (s *Session) Refresh() string { return "refresh:" + s.Token() }

// ---- C10: crash point far from the fix -----------------------------------
// The constructor accepts capacity 0, the crash happens later in evict.

// Cache is a FIFO cache.
type Cache struct {
	cap   int
	order []string
	data  map[string]int
}

// NewCache returns a cache holding at most capacity entries.
func NewCache(capacity int) *Cache {
	if capacity < 0 { // BUG:C10 must be <= 0, capacity 0 later leaves evict with nothing to drop
		capacity = 1
	}
	return &Cache{cap: capacity, data: map[string]int{}}
}

// Put stores a value, evicting the oldest key when full.
func (c *Cache) Put(k string, v int) {
	if len(c.order) >= c.cap {
		c.evict()
	}
	c.order = append(c.order, k)
	c.data[k] = v
}

func (c *Cache) evict() {
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.data, oldest)
}

// ---- C11: thin wrapper ---------------------------------------------------
// The panic is raised inside a generic helper; the missing validation is
// in the caller. The top frame (must) is useless for localization.

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// ParsePort reads a TCP port from a setting.
func ParsePort(s string) int {
	return must(strconv.Atoi(s)) // BUG:C11 empty or non-numeric setting is not validated before the panicking helper
}

// ---- C12: recursion, truncated traceback ---------------------------------
// The traceback of a deep recursion is cut by the Go runtime
// ("...additional frames elided..."): the parser must cope with it.

// Node is a singly linked list node.
type Node struct {
	Val  int
	Next *Node
}

// Total sums the list recursively.
func (n *Node) Total() int {
	return n.Val + n.Next.Total() // BUG:C12 no base case for the last node: n becomes nil
}

// ---- C13: panic in a goroutine -------------------------------------------
// No testing.tRunner frame: the stack ends with "created by".

// Fanout squares each item in its own goroutine and returns the results.
func Fanout(items []int) []int {
	out := make([]int, len(items))
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i+1] = items[i] * items[i] // BUG:C13 off-by-one index, the last worker writes past the end
		}()
	}
	wg.Wait()
	return out
}

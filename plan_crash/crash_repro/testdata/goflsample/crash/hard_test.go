package crash

import "testing"

// Each test below panics on purpose; run them one at a time (see bugs.json).

func TestRunMissingEquals(t *testing.T) { Run("a=1; b ;c=3") } // C8

func TestRunOK(t *testing.T) {
	if got := Run("a=1; b=2"); got["a"] != "1" || got["b"] != "2" {
		t.Fatal(got)
	}
}

func TestLoginAnon(t *testing.T)   { NewSession(true).Login() }   // C9, entry 1
func TestLogoutAnon(t *testing.T)  { NewSession(true).Logout() }  // C9, entry 2
func TestRefreshAnon(t *testing.T) { NewSession(true).Refresh() } // C9, entry 3

func TestSessionOK(t *testing.T) {
	if got := NewSession(false).Login(); got != "login:t" {
		t.Fatal(got)
	}
}

func TestCacheZeroCapacity(t *testing.T) { NewCache(0).Put("a", 1) } // C10

func TestCacheOK(t *testing.T) {
	c := NewCache(2)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)
	if _, ok := c.data["a"]; ok {
		t.Fatal("a not evicted")
	}
}

func TestParsePortEmpty(t *testing.T) { ParsePort("") } // C11

func TestParsePortOK(t *testing.T) {
	if got := ParsePort("8080"); got != 8080 {
		t.Fatal(got)
	}
}

func TestTotalLongList(t *testing.T) { // C12
	var head *Node
	for i := 0; i < 150; i++ {
		head = &Node{Val: i, Next: head}
	}
	head.Total()
}

func TestFanout(t *testing.T) { Fanout([]int{1, 2, 3}) } // C13

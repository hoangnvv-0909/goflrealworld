package crash

import (
	"os"
	"sync"
	"testing"
)

func TestEmailOfNilProfile(t *testing.T) {
	_ = EmailOf(&User{Name: "a"}) // panics (C1)
}

func TestEmailOfOK(t *testing.T) {
	if got := EmailOf(&User{Profile: &Profile{Email: "a@b"}}); got != "a@b" {
		t.Fatal(got)
	}
}

func TestProcessChain(t *testing.T) {
	Process([]*User{{Name: "x"}}) // panics via Process -> EmailOf (C1, deeper stack)
}

func TestNth(t *testing.T) { Nth([]int{1, 2, 3}, 3) } // C2

func TestInc(t *testing.T) { (&Registry{}).Inc("a") } // C3

func TestAverageEmpty(t *testing.T) { Average(nil) } // C4

func TestDescribe(t *testing.T) { Describe(42) } // C5

func TestFinishTwice(t *testing.T) { // C6
	ch := make(chan int)
	Finish(ch)
	Finish(ch)
}

// C7 kills the whole test binary (fatal error, not a recoverable panic),
// so it only runs when GOFL_RUN_FATAL=1.
func TestConcurrentMap(t *testing.T) {
	if os.Getenv("GOFL_RUN_FATAL") != "1" {
		t.Skip("set GOFL_RUN_FATAL=1 to run")
	}
	c := NewCounter()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100000; i++ {
				c.Add(i % 100)
			}
		}()
	}
	wg.Wait()
}

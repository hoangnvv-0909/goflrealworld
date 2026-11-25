package broken

import "testing"

func BenchmarkBroken(b *testing.B) {
	var n int = "not a number"
	for i := 0; i < b.N; i++ {
		_ = n
	}
}

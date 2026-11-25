package sem

import (
	"math"
	"testing"
	"time"
)

func TestPagesExact(t *testing.T) { // passes
	if Pages(10, 5) != 2 {
		t.Fatal()
	}
}

func TestPagesPartial(t *testing.T) { // fails (S1)
	if got := Pages(11, 5); got != 3 {
		t.Fatalf("got %d want 3", got)
	}
}

var ict = time.FixedZone("ICT", 7*3600)

func TestSameDayUTC(t *testing.T) { // passes
	a := time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)
	b := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	if !SameDay(a, b, time.UTC) {
		t.Fatal()
	}
}

func TestSameDayLocal(t *testing.T) { // fails (S2): 23:00 on the 10th vs 00:00 on the 11th in ICT
	a := time.Date(2026, 11, 10, 16, 0, 0, 0, time.UTC)
	b := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	if SameDay(a, b, ict) {
		t.Fatal("different days in ICT")
	}
}

func TestMaxOfSingle(t *testing.T) { // passes
	if MaxOf([]int{4}) != 4 {
		t.Fatal()
	}
}

func TestMaxOfMany(t *testing.T) { // fails (S3)
	if got := MaxOf([]int{3, 9, 2}); got != 9 {
		t.Fatalf("got %d want 9", got)
	}
}

func TestSoftmaxSmall(t *testing.T) { // passes: no overflow for small values
	s := Softmax([]float64{0, 0})
	if math.Abs(s[0]-0.5) > 1e-9 {
		t.Fatal(s)
	}
}

func TestSoftmaxLarge(t *testing.T) { // fails (S4): exp(2000) overflows to +Inf -> NaN
	s := Softmax([]float64{1000, 1001})
	if math.IsNaN(s[0]) || math.Abs(s[0]+s[1]-1) > 1e-9 {
		t.Fatal(s)
	}
}

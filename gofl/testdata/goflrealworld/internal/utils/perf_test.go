package utils

import (
	"fmt"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Sub-benchmarks named n=<size> are the base/scaled load pair used by `gofl perf`.
var sizes = []int{100, 1000, 5000}

// tagInput returns n tags with about 10% duplicates, as a client might send.
func tagInput(n int) []string {
	tags := make([]string, n)
	for i := range tags {
		tags[i] = fmt.Sprintf("Tag-%d", i*9/10)
	}
	return tags
}

// WP1: NormalizeTags on a large tag list (bulk import of articles).
func BenchmarkNormalizeTags(b *testing.B) {
	for _, n := range sizes {
		in := tagInput(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				NormalizeTags(in)
			}
		})
	}
}

// WP2: one slug per imported article.
func BenchmarkGenerateSlug(b *testing.B) {
	for _, n := range sizes {
		titles := make([]string, n)
		for i := range titles {
			titles[i] = fmt.Sprintf("Article number %d: notes on Go", i)
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				for _, t := range titles {
					GenerateSlug(t)
				}
			}
		})
	}
}

func TestNormalizeTagsDedup(t *testing.T) {
	got := NormalizeTags([]string{"Go", "go ", "Rust", "GO"})
	if len(got) != 2 || got[0] != "go" || got[1] != "rust" {
		t.Fatal(got)
	}
}

// htmlBody returns an article body of n bytes with a tag every ~40 bytes.
func htmlBody(n int) string {
	var b []byte
	for len(b) < n {
		b = append(b, "<p>Some <b>bold</b> text in a paragraph.</p>\n"...)
	}
	return string(b[:n])
}

// WP7: StripTags on article bodies of n bytes (1 KB to 50 KB).
func BenchmarkStripTags(b *testing.B) {
	for _, n := range []int{1000, 10000, 50000} {
		body := htmlBody(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				StripTags(body)
			}
		})
	}
}

// WP8: one password hash (register; login costs the same). The cost does
// not depend on input size, so compare with the reference benchmark below.
func BenchmarkHashPassword(b *testing.B) {
	for i := 0; i < b.N; i++ {
		HashPassword("password123")
	}
}

// Reference for WP8: the same hash at bcrypt.DefaultCost (upstream).
func BenchmarkHashPasswordDefaultCost(b *testing.B) {
	for i := 0; i < b.N; i++ {
		bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	}
}

func TestStripTags(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                     "plain",
		"<p>Hi <b>there</b></p>":    "Hi there",
		"a < b and c > d":           "a  d",
		"<script>x()</script>ok":    "x()ok",
		"Xin chào <i>thế giới</i>!": "Xin chào thế giới!",
		"2 > 1":                     "2 > 1",
	} {
		if got := StripTags(in); got != want {
			t.Errorf("StripTags(%q) = %q, want %q", in, got, want)
		}
	}
}

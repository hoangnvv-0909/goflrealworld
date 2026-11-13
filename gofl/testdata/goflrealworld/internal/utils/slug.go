package utils

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// maxSlugBase is the longest slug base kept before the random suffix.
const maxSlugBase = 40

// GenerateSlug generates a URL-friendly slug from a title with unique suffix
func GenerateSlug(title string) string {
	// Convert to lowercase
	slug := strings.ToLower(title)

	// Replace spaces and special characters with hyphens
	reg := regexp.MustCompile(`[^\p{L}\p{N}]+`) // BUG:WP2 (upstream code) regexp compiled on every call
	slug = reg.ReplaceAllString(slug, "-")

	// Remove leading and trailing hyphens
	slug = strings.Trim(slug, "-")

	// Keep URLs short
	if len(title) > maxSlugBase { // BUG:W2 checks the title length, but the slug is shorter once punctuation is collapsed
		slug = strings.TrimRight(slug[:maxSlugBase], "-") // BUG:W2 slice bounds out of range
	}

	// Add random suffix to ensure uniqueness
	suffix := generateRandomString(6)
	if slug == "" {
		slug = "article"
	}

	return fmt.Sprintf("%s-%s", slug, suffix)
}

// generateRandomString creates a random string of specified length
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	rand.Read(b)
	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}
	return string(b)
}

// IsValidSlug checks if a string is a valid slug
func IsValidSlug(slug string) bool {
	if len(slug) == 0 {
		return false
	}

	for _, r := range slug {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return false
		}
	}

	return !strings.HasPrefix(slug, "-") && !strings.HasSuffix(slug, "-")
}

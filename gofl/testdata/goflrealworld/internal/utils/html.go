package utils

// goflrealworld: not in upstream. Article bodies are stored without HTML
// tags so that clients that render them as HTML cannot be tricked into
// running scripts.

// StripTags removes everything between '<' and '>' from s.
func StripTags(s string) string {
	out := ""
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>' && inTag:
			inTag = false
		case !inTag:
			out += string(r) // BUG:WP7 each += copies the whole string: O(n^2) bytes copied; use strings.Builder
		}
	}
	return out
}

package llm

import "regexp"

// Redact removes secrets and personal data before anything is sent.
//
// It is best effort, deterministic and idempotent: Redact(Redact(s)) ==
// Redact(s). It never touches Go identifiers, line numbers or hex addresses
// that the prompt needs. Placeholders carry the kind of what was removed.
//
// It does NOT guarantee that no sensitive data is left (free text, business
// data in source code, unusual secret formats). That is why gofl shows the
// user the final prompt before sending (DryRun).
func Redact(s string) string {
	for _, r := range redactRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

type redactRule struct {
	re   *regexp.Regexp
	repl string
}

func rule(pattern, repl string) redactRule {
	return redactRule{regexp.MustCompile(pattern), repl}
}

// Order matters: wide patterns (private keys, JWT) go first.
var redactRules = []redactRule{
	rule(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`, `<REDACTED:PRIVATE_KEY>`),
	rule(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`, `<REDACTED:JWT>`),
	rule(`\bsk-[A-Za-z0-9_-]{16,}`, `<REDACTED:KEY>`),
	rule(`\bAKIA[0-9A-Z]{16}\b`, `<REDACTED:KEY>`),
	rule(`\bgh[pousr]_[A-Za-z0-9]{20,}`, `<REDACTED:KEY>`),
	rule(`\bxox[baprs]-[A-Za-z0-9-]{10,}`, `<REDACTED:KEY>`),
	rule(`\bAIza[0-9A-Za-z_-]{30,}`, `<REDACTED:KEY>`),
	rule(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`, `Bearer <REDACTED:TOKEN>`),
	// scheme://user:password@host -> credentials removed, host kept.
	rule(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`, `${1}<REDACTED:CREDENTIALS>@`),
	// name = value / name: "value" for secret-looking names. Any value
	// needs 6+ characters (so token: "t" in code is left alone) and a bare
	// value may not start with = or : (so Go's ":=" and "==" stay).
	rule(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|auth)\b(["']?\s*[:=]\s*)("[^"\n]{6,}"|'[^'\n]{6,}'|[^\s"'<>,;&=:(){}\[\]]{6,})`, `${1}${2}<REDACTED:SECRET>`),
	rule(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`, `<EMAIL>`),
	rule(`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b`, `<IP>`),
	rule(`\b(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}\b`, `<IP>`),
	rule(`\b(?:[0-9A-Fa-f]{1,4}:){1,6}:(?:[0-9A-Fa-f]{1,4})?\b`, `<IP>`),
	// Home directories: keep the rest of the path, drop the user name.
	rule(`(?i)\b([A-Z]:[\\/]+Users[\\/]+)[^\\/\s]+`, `${1}<USER>`),
	rule(`(/home/|/Users/)[^/\s]+`, `${1}<USER>`),
}

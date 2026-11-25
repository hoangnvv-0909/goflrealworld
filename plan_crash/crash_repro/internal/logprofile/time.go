package logprofile

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Timestamp formats seen in front of Go panics in real logs:
//
//	2024-05-01T10:20:30Z / 2024-05-01 10:20:30,123 +0700   (RFC 3339 and kin)
//	2024/05/01 10:20:30                                      (Go "log" package)
//	time="2024-05-01T10:20:30Z" level=error ...              (logrus text)
//	{"ts":1714558830.12,"level":"error",...}                 (zap / JSON lines)
//	2024-05-01T10:20:30Z stderr F panic: ...                 (container runtime)
//
// A timestamp without zone is read in loc.

var (
	isoRe   = regexp.MustCompile(`^\[?(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?)(Z|[+-]\d{2}:?\d{2})?\]?`)
	goLogRe = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?)`)
	logrusR = regexp.MustCompile(`\btime="([^"]+)"`)
	jsonRe  = regexp.MustCompile(`"(?:time|ts|timestamp|@timestamp)"\s*:\s*(?:"([^"]+)"|([0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?))`)
	cri     = regexp.MustCompile(`^(?:stdout|stderr) [FP] `)
)

// parseTimestamp reads a timestamp at the start of line (or, for logrus and
// JSON lines, anywhere in it). rest is the text after the timestamp prefix;
// ok is false when no timestamp was found (rest is then line itself).
func parseTimestamp(line string, loc *time.Location) (t time.Time, rest string, ok bool) {
	if loc == nil {
		loc = time.UTC
	}
	if m := isoRe.FindStringSubmatch(line); m != nil {
		if t, ok := parseISO(m[1], m[2], loc); ok {
			// One separator space only: the message keeps its own leading
			// tab, which marks stack lines.
			return t, stripCRI(strings.TrimPrefix(line[len(m[0]):], " ")), true
		}
	}
	if m := goLogRe.FindStringSubmatch(line); m != nil {
		s := m[1]
		layout := "2006/01/02 15:04:05"
		if strings.Contains(s, ".") {
			layout = "2006/01/02 15:04:05.999999999"
		}
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, strings.TrimPrefix(line[len(m[0]):], " "), true
		}
	}
	if strings.HasPrefix(line, "{") {
		if m := jsonRe.FindStringSubmatch(line); m != nil {
			if m[1] != "" {
				if t, ok := parseISOString(m[1], loc); ok {
					return t, line, true
				}
			} else if f, err := strconv.ParseFloat(m[2], 64); err == nil {
				return epoch(f), line, true
			}
		}
		return time.Time{}, line, false
	}
	if m := logrusR.FindStringSubmatch(line); m != nil {
		if t, ok := parseISOString(m[1], loc); ok {
			return t, line, true
		}
	}
	return time.Time{}, line, false
}

func stripCRI(s string) string { return cri.ReplaceAllString(s, "") }

func parseISOString(s string, loc *time.Location) (time.Time, bool) {
	m := isoRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	return parseISO(m[1], m[2], loc)
}

func parseISO(base, zone string, loc *time.Location) (time.Time, bool) {
	base = strings.Replace(base, ",", ".", 1)
	sep := "T"
	if strings.Contains(base, " ") {
		sep = " "
	}
	layout := "2006-01-02" + sep + "15:04:05"
	if strings.Contains(base, ".") {
		layout += ".999999999"
	}
	switch {
	case zone == "":
		t, err := time.ParseInLocation(layout, base, loc)
		return t, err == nil
	case zone == "Z":
		t, err := time.ParseInLocation(layout, base, time.UTC)
		return t, err == nil
	}
	z := zone
	if !strings.Contains(z, ":") {
		z = z[:3] + ":" + z[3:]
	}
	t, err := time.Parse(layout+"Z07:00", base+z)
	return t, err == nil
}

func epoch(f float64) time.Time {
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
}

package sdk

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultLocation is used for human-readable time parsing and day truncation.
var DefaultLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.UTC
}()

// ParseTimeRange parses a unified time range expression into a [from, to)
// window. Supported forms:
//
//	last_5m / last_1h / last_1d / last_1w
//	today / yesterday
//	now-15m~now-5m        (a ~ b, each side any supported point)
//	1718451045~1718454645 (unix seconds or milliseconds)
//	2024-01-01 00:00:00~2024-01-01 01:00:00
//
// An empty expression defaults to the last hour.
func ParseTimeRange(expr string) (time.Time, time.Time, error) {
	now := time.Now().In(DefaultLocation)
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return now.Add(-time.Hour), now, nil
	}

	switch strings.ToLower(expr) {
	case "today":
		return startOfDay(now), now, nil
	case "yesterday":
		y := now.AddDate(0, 0, -1)
		return startOfDay(y), startOfDay(now), nil
	}

	if strings.HasPrefix(strings.ToLower(expr), "last_") {
		d, err := parseDuration(expr[len("last_"):])
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid time range %q: %w", expr, err)
		}
		return now.Add(-d), now, nil
	}

	if a, b, ok := strings.Cut(expr, "~"); ok {
		from, err := parseTimePoint(strings.TrimSpace(a), now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid time range start %q: %w", a, err)
		}
		to, err := parseTimePoint(strings.TrimSpace(b), now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid time range end %q: %w", b, err)
		}
		if to.Before(from) {
			return time.Time{}, time.Time{}, fmt.Errorf("time range end is before start")
		}
		return from, to, nil
	}

	// Single point: treat as "from that point to now".
	from, err := parseTimePoint(expr, now)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return from, now, nil
}

func parseTimePoint(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	if lower == "now" {
		return now, nil
	}
	if strings.HasPrefix(lower, "now") {
		return parseNowExpr(s[3:], now)
	}
	if isAllDigits(s) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, err
		}
		return epochToTime(n), nil
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"2006-01-02",
		"2006/01/02",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, DefaultLocation); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", s)
}

// ParseTimePoint parses a single absolute time (unix seconds/milliseconds, a
// supported date-time layout, or a now-relative expression such as now-15m).
// Time is interpreted in DefaultLocation when no zone is given.
func ParseTimePoint(s string) (time.Time, error) {
	return parseTimePoint(s, time.Now().In(DefaultLocation))
}

// ResolveWindow resolves a time window from either a single range expression or
// explicit from/to values. Explicit values take precedence; either side may be
// left empty to fall back to (now-1h, now). This lets a caller pass exact
// instants without depending on the range grammar.
func ResolveWindow(rangeExpr, fromExpr, toExpr string) (time.Time, time.Time, error) {
	fromExpr = strings.TrimSpace(fromExpr)
	toExpr = strings.TrimSpace(toExpr)
	if fromExpr == "" && toExpr == "" {
		return ParseTimeRange(rangeExpr)
	}
	now := time.Now().In(DefaultLocation)
	from, to := now.Add(-time.Hour), now
	if fromExpr != "" {
		var err error
		if from, err = parseTimePoint(fromExpr, now); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from %q: %w", fromExpr, err)
		}
	}
	if toExpr != "" {
		var err error
		if to, err = parseTimePoint(toExpr, now); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to %q: %w", toExpr, err)
		}
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to is before from")
	}
	return from, to, nil
}

// parseNowExpr handles the suffix after "now", e.g. "-15m", "/d", "-1h/d".
func parseNowExpr(rest string, now time.Time) (time.Time, error) {
	rest = strings.TrimSpace(rest)
	var truncate string
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		truncate = rest[i+1:]
		rest = rest[:i]
	}
	t := now
	if rest != "" {
		if !strings.HasPrefix(rest, "-") {
			return time.Time{}, fmt.Errorf("expected '-' or '/' after now, got %q", rest)
		}
		d, err := parseDuration(rest[1:])
		if err != nil {
			return time.Time{}, err
		}
		t = now.Add(-d)
	}
	switch strings.ToLower(truncate) {
	case "":
	case "d":
		t = startOfDay(t)
	case "h":
		t = t.Truncate(time.Hour)
	case "m":
		t = t.Truncate(time.Minute)
	default:
		return time.Time{}, fmt.Errorf("unsupported truncation %q", truncate)
	}
	return t, nil
}

func epochToTime(n int64) time.Time {
	// Heuristic: 13-digit values are milliseconds.
	if n >= 1_000_000_000_000 {
		return time.UnixMilli(n).In(DefaultLocation)
	}
	return time.Unix(n, 0).In(DefaultLocation)
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseDuration parses a duration supporting s, m, h, d, w units.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	switch unit {
	case 's':
		return time.Duration(n) * time.Second, nil
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported duration unit %q", string(unit))
	}
}

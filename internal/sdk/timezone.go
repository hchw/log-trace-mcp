package sdk

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed the IANA database so the binary works without system tzdata
)

// TimeConversion is the result of expressing one instant in a target zone.
type TimeConversion struct {
	Input     string `json:"input"`
	FromTZ    string `json:"from_tz"`
	ToTZ      string `json:"to_tz"`
	Unix      int64  `json:"unix"`
	UTCTime   string `json:"utc_time"`
	FromTime  string `json:"from_time"`
	ToTime    string `json:"to_time"`
	ToLocal   string `json:"to_local"`
	ToWeekday string `json:"to_weekday"`
	ToOffset  string `json:"to_offset"`
	HadOffset bool   `json:"had_offset"`
}

// ConvertTime converts a wall-clock or absolute time from one zone to another.
//
// timeExpr may carry its own offset (RFC3339) or be a bare wall-clock value
// interpreted in fromTZ. Both zone arguments accept an IANA name ("Asia/Shanghai",
// "UTC") or a fixed offset ("+08:00", "-0530", "Z"). A bare value needs fromTZ;
// an absolute value does not, but fromTZ is still shown for reference.
func ConvertTime(timeExpr, fromTZ, toTZ string) (TimeConversion, error) {
	timeExpr = strings.TrimSpace(timeExpr)
	if timeExpr == "" {
		return TimeConversion{}, fmt.Errorf("time is required")
	}
	if strings.TrimSpace(toTZ) == "" {
		return TimeConversion{}, fmt.Errorf("to_tz is required")
	}

	fromLoc, fromName, err := LoadZone(fromTZ)
	if err != nil {
		return TimeConversion{}, fmt.Errorf("invalid from_tz: %w", err)
	}
	toLoc, toName, err := LoadZone(toTZ)
	if err != nil {
		return TimeConversion{}, fmt.Errorf("invalid to_tz: %w", err)
	}

	instant, hadOffset, err := parseInstant(timeExpr, fromLoc)
	if err != nil {
		return TimeConversion{}, err
	}

	to := instant.In(toLoc)
	return TimeConversion{
		Input:     timeExpr,
		FromTZ:    fromName,
		ToTZ:      toName,
		Unix:      instant.Unix(),
		UTCTime:   instant.UTC().Format(time.RFC3339),
		FromTime:  instant.In(fromLoc).Format(time.RFC3339),
		ToTime:    to.Format(time.RFC3339),
		ToLocal:   to.Format("2006-01-02 15:04:05"),
		ToWeekday: to.Format("Monday"),
		ToOffset:  to.Format("-07:00"),
		HadOffset: hadOffset,
	}, nil
}

// LoadZone resolves a zone name or fixed offset into a location and a canonical
// name for display.
func LoadZone(tz string) (*time.Location, string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return nil, "", fmt.Errorf("time zone is required")
	}
	switch strings.ToUpper(tz) {
	case "UTC", "Z", "GMT":
		return time.UTC, tz, nil
	}
	if off, ok := parseFixedOffset(tz); ok {
		return time.FixedZone(tz, off), tz, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, "", fmt.Errorf("unknown time zone %q (use an IANA name like Asia/Shanghai or an offset like +08:00)", tz)
	}
	return loc, loc.String(), nil
}

func parseInstant(s string, loc *time.Location) (time.Time, bool, error) {
	// Values carrying an explicit offset are absolute; the from zone is cosmetic.
	offsetLayouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02T15:04:05",
	}
	for _, l := range offsetLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true, nil
		}
	}

	if isAllDigits(s) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("invalid epoch %q", s)
		}
		return epochToTime(n), true, nil
	}

	localLayouts := []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006/01/02 15:04:05", "2006/01/02 15:04"}
	for _, l := range localLayouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("unrecognized time %q", s)
}

// parseFixedOffset accepts +08:00, -05:30, +0800, +08, and Z.
func parseFixedOffset(s string) (int, bool) {
	u := strings.ToUpper(strings.TrimSpace(s))
	if u == "Z" {
		return 0, true
	}
	if len(u) < 2 || (u[0] != '+' && u[0] != '-') {
		return 0, false
	}
	sign := 1
	if u[0] == '-' {
		sign = -1
	}
	body := strings.ReplaceAll(u[1:], ":", "")
	hh, mm := 0, 0
	switch len(body) {
	case 2:
		hh, _ = strconv.Atoi(body)
	case 4:
		hh, _ = strconv.Atoi(body[:2])
		mm, _ = strconv.Atoi(body[2:])
	default:
		return 0, false
	}
	if hh > 23 || mm > 59 {
		return 0, false
	}
	return sign * (hh*3600 + mm*60), true
}

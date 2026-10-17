package sdk

import (
	"strings"
	"testing"
)

func TestConvertTimeShanghaiToUTC(t *testing.T) {
	got, err := ConvertTime("2026-10-17 12:26:00", "Asia/Shanghai", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if got.ToLocal != "2026-10-17 04:26:00" {
		t.Fatalf("want 04:26:00, got %s", got.ToLocal)
	}
	if got.ToOffset != "+00:00" {
		t.Fatalf("offset = %s", got.ToOffset)
	}
	if got.Unix != 1792211160 {
		t.Fatalf("unix = %d", got.Unix)
	}
}

func TestConvertTimeUTCToShanghai(t *testing.T) {
	got, err := ConvertTime("2026-10-17 04:26:00", "UTC", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if got.ToLocal != "2026-10-17 12:26:00" || got.ToOffset != "+08:00" {
		t.Fatalf("got %s %s", got.ToLocal, got.ToOffset)
	}
}

func TestConvertTimeFixedOffset(t *testing.T) {
	got, err := ConvertTime("2026-10-17 12:26:00", "+08:00", "-05:00")
	if err != nil {
		t.Fatal(err)
	}
	if got.ToLocal != "2026-10-16 23:26:00" {
		t.Fatalf("want 2026-10-16 23:26:00, got %s", got.ToLocal)
	}
}

func TestConvertTimeExplicitOffsetWins(t *testing.T) {
	// RFC3339 carries its own offset; from_tz is only for display.
	got, err := ConvertTime("2026-10-17T12:26:00+08:00", "UTC", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if got.ToLocal != "2026-10-17 04:26:00" || !got.HadOffset {
		t.Fatalf("got %s hadOffset=%v", got.ToLocal, got.HadOffset)
	}
}

func TestConvertTimeEpoch(t *testing.T) {
	got, err := ConvertTime("1792211160", "UTC", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if got.ToLocal != "2026-10-17 12:26:00" {
		t.Fatalf("got %s", got.ToLocal)
	}
}

func TestConvertTimeErrors(t *testing.T) {
	if _, err := ConvertTime("2026-10-17 12:26", "Asia/Shanghai", ""); err == nil {
		t.Fatal("expected error for missing to_tz")
	}
	if _, err := ConvertTime("2026-10-17 12:26", "Mars/Olympus", "UTC"); err == nil {
		t.Fatal("expected error for unknown from_tz")
	}
	if _, err := ConvertTime("not-a-time", "UTC", "UTC"); err == nil {
		t.Fatal("expected error for bad time")
	} else if !strings.Contains(err.Error(), "unrecognized time") {
		t.Fatalf("unexpected error: %v", err)
	}
}

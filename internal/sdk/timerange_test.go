package sdk

import (
	"testing"
	"time"
)

func TestParseTimeRangeRelative(t *testing.T) {
	from, to, err := ParseTimeRange("last_1h")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != time.Hour {
		t.Fatalf("want 1h window, got %v", d)
	}
}

func TestParseTimeRangeInterval(t *testing.T) {
	from, to, err := ParseTimeRange("now-15m~now-5m")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != 10*time.Minute {
		t.Fatalf("want 10m window, got %v", d)
	}
}

func TestParseTimeRangeEpoch(t *testing.T) {
	from, to, err := ParseTimeRange("1718451045~1718454645")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != time.Hour {
		t.Fatalf("want 1h, got %v", d)
	}
}

func TestParseTimeRangeEpochMillis(t *testing.T) {
	from, to, err := ParseTimeRange("1718451045000~1718454645000")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != time.Hour {
		t.Fatalf("want 1h, got %v", d)
	}
}

func TestParseTimeRangeHuman(t *testing.T) {
	from, to, err := ParseTimeRange("2024-01-01 00:00:00~2024-01-01 01:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != time.Hour {
		t.Fatalf("want 1h, got %v", d)
	}
}

func TestParseTimeRangeToday(t *testing.T) {
	from, to, err := ParseTimeRange("today")
	if err != nil {
		t.Fatal(err)
	}
	if from.Hour() != 0 || from.Minute() != 0 {
		t.Fatalf("today should start at midnight, got %v", from)
	}
	if to.Before(from) {
		t.Fatalf("to before from")
	}
}

func TestParseTimeRangeReversed(t *testing.T) {
	if _, _, err := ParseTimeRange("now~now-1h"); err == nil {
		t.Fatal("expected error for reversed range")
	}
}

func TestParseTimeRangeInvalid(t *testing.T) {
	if _, _, err := ParseTimeRange("last_2x"); err == nil {
		t.Fatal("expected error for bad unit")
	}
}

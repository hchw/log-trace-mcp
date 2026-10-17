package sdk

import (
	"testing"
	"time"
)

func TestParseTimeRangeMinutePrecision(t *testing.T) {
	from, to, err := ParseTimeRange("2026-10-17 12:26~2026-10-17 12:30")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != 4*time.Minute {
		t.Fatalf("want 4m window, got %v", d)
	}
}

func TestResolveWindowExplicitFromTo(t *testing.T) {
	from, to, err := ResolveWindow("last_1h", "1718451045", "1718454645")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != time.Hour {
		t.Fatalf("want 1h, got %v", d)
	}
}

func TestResolveWindowFromOverridesRange(t *testing.T) {
	// time_range would give a 1h window; from/to must win.
	from, to, err := ResolveWindow("last_1h", "1718451045", "1718451645")
	if err != nil {
		t.Fatal(err)
	}
	if d := to.Sub(from); d != 10*time.Minute {
		t.Fatalf("want 10m, got %v", d)
	}
}

func TestResolveWindowOnlyFromDefaultsToNow(t *testing.T) {
	from, _, err := ResolveWindow("", "now-30m", "")
	if err != nil {
		t.Fatal(err)
	}
	if ago := time.Since(from); ago < 29*time.Minute || ago > 31*time.Minute {
		t.Fatalf("from should be ~30m ago, got %v", ago)
	}
}

func TestResolveWindowReversed(t *testing.T) {
	if _, _, err := ResolveWindow("", "1718454645", "1718451045"); err == nil {
		t.Fatal("expected error for to before from")
	}
}

func TestParseTimePointLooseFormats(t *testing.T) {
	for _, s := range []string{"2026-10-17 12:26:00", "2026-10-17 12:26", "2026-10-17", "2026/10/17 12:26"} {
		if _, err := ParseTimePoint(s); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
}

package retention

import (
	"testing"
	"time"
)

func TestCutoffUsesUTCAndRejectsInvalidDays(t *testing.T) {
	now := time.Date(2026, 8, 6, 4, 5, 6, 0, time.FixedZone("test", 8*60*60))
	cutoff, err := Cutoff(now, 30)
	if err != nil {
		t.Fatal(err)
	}
	if cutoff.Location() != time.UTC || !cutoff.Equal(now.UTC().AddDate(0, 0, -30)) {
		t.Fatalf("cutoff = %s", cutoff)
	}
	if _, err := Cutoff(now, 0); err == nil {
		t.Fatal("expected invalid retention days error")
	}
}

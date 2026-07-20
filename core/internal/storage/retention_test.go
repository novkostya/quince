package storage

import (
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// Story 8 (unit): selectPrunable honors keep policy and protects adopted + latest.

func TestSelectPrunableKeepRecentAndProtectsAdoptedAndLatest(t *testing.T) {
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	job := "j"
	rows := []store.VersionRow{
		{ID: "latest", UDID: "U", JobID: &job, CreatedAt: base.Add(5 * time.Hour), IsLatest: true},
		{ID: "keep1", UDID: "U", JobID: &job, CreatedAt: base.Add(4 * time.Hour)},
		{ID: "keep2", UDID: "U", JobID: &job, CreatedAt: base.Add(3 * time.Hour)},
		{ID: "drop1", UDID: "U", JobID: &job, CreatedAt: base.Add(2 * time.Hour)},
		{ID: "drop2", UDID: "U", JobID: &job, CreatedAt: base.Add(1 * time.Hour)},
		{ID: "adopted", UDID: "U", JobID: nil, CreatedAt: base}, // adopted → never pruned
	}
	prune := selectPrunable(rows, RetentionPolicy{KeepRecent: 2, KeepDaily: 0, KeepWeekly: 0})
	got := map[string]bool{}
	for _, r := range prune {
		got[r.ID] = true
	}
	if !got["drop1"] || !got["drop2"] {
		t.Fatalf("expected drop1+drop2 pruned, got %v", ids2(prune))
	}
	for _, protected := range []string{"latest", "adopted", "keep1", "keep2"} {
		if got[protected] {
			t.Fatalf("%s must NOT be pruned, got %v", protected, ids2(prune))
		}
	}
}

func TestSelectPrunableDailyKeepsOnePerDay(t *testing.T) {
	job := "j"
	day := func(d, h int) time.Time { return time.Date(2026, 7, d, h, 0, 0, 0, time.UTC) }
	// Two versions on each of two days; KeepRecent 0, KeepDaily 2 → keep newest per day (2 kept).
	rows := []store.VersionRow{
		{ID: "d2b", UDID: "U", JobID: &job, CreatedAt: day(2, 20)},
		{ID: "d2a", UDID: "U", JobID: &job, CreatedAt: day(2, 8)},
		{ID: "d1b", UDID: "U", JobID: &job, CreatedAt: day(1, 20)},
		{ID: "d1a", UDID: "U", JobID: &job, CreatedAt: day(1, 8)},
	}
	prune := selectPrunable(rows, RetentionPolicy{KeepRecent: 0, KeepDaily: 2, KeepWeekly: 0})
	got := map[string]bool{}
	for _, r := range prune {
		got[r.ID] = true
	}
	// The older of each day is pruned; the newest per day is kept.
	if !got["d2a"] || !got["d1a"] {
		t.Fatalf("expected d2a+d1a pruned (older per day), got %v", ids2(prune))
	}
	if got["d2b"] || got["d1b"] {
		t.Fatalf("newest-per-day must be kept, got %v", ids2(prune))
	}
}

func ids2(rows []store.VersionRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

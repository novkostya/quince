package storage

import (
	"fmt"

	"github.com/novkostya/quince/core/internal/store"
)

// RetentionPolicy is the backend-uniform keep policy (config storage.retention; design §5):
// keep the N most recent, plus one per day for KeepDaily days, plus one per week for
// KeepWeekly weeks. Acts on quince-created versions only.
type RetentionPolicy struct {
	KeepRecent int
	KeepDaily  int
	KeepWeekly int
}

// selectPrunable returns the versions a Prune should delete: quince-created (job_id set),
// present (not missing), non-latest versions not retained by the policy. Adopted versions
// (job_id nil) and the latest are ALWAYS kept (design §5 — adopted is protected until the user
// says otherwise; latest is never retention-pruned). rows must be newest-first.
func selectPrunable(rows []store.VersionRow, p RetentionPolicy) []store.VersionRow {
	keep := map[string]bool{}
	var candidates []store.VersionRow
	for _, r := range rows {
		if r.JobID == nil || r.IsLatest || r.Missing {
			keep[r.ID] = true // adopted / latest / already-gone are never pruned here
			continue
		}
		candidates = append(candidates, r)
	}

	// Recent: the newest KeepRecent candidates.
	for i := 0; i < len(candidates) && i < p.KeepRecent; i++ {
		keep[candidates[i].ID] = true
	}
	// Daily: the newest candidate per calendar day, up to KeepDaily distinct days.
	keepBucketed(candidates, p.KeepDaily, keep, func(r store.VersionRow) string {
		return r.CreatedAt.UTC().Format("2006-01-02")
	})
	// Weekly: the newest candidate per ISO week, up to KeepWeekly distinct weeks.
	keepBucketed(candidates, p.KeepWeekly, keep, func(r store.VersionRow) string {
		y, w := r.CreatedAt.UTC().ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	})

	var prune []store.VersionRow
	for _, r := range candidates {
		if !keep[r.ID] {
			prune = append(prune, r)
		}
	}
	return prune
}

// keepBucketed keeps the newest candidate in each of the first `limit` distinct buckets
// (candidates are newest-first, so the first seen per bucket is the newest).
func keepBucketed(candidates []store.VersionRow, limit int, keep map[string]bool, bucket func(store.VersionRow) string) {
	if limit <= 0 {
		return
	}
	seen := map[string]bool{}
	for _, r := range candidates {
		b := bucket(r)
		if seen[b] {
			continue
		}
		if len(seen) >= limit {
			break
		}
		seen[b] = true
		keep[r.ID] = true
	}
}

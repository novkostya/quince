package clonetree

import (
	"strings"
	"testing"
	"time"
)

// THE PROBE WAITS FOR A SYNCED TXG RATHER THAN GIVING UP — quince#790, Operator ruling 2026-08-19.
//
// ZFS block cloning refuses a source not yet in a synced transaction group, so the probe's
// write-then-clone loses a race the filesystem resolves within one txg. Before this, the first
// EAGAIN ended the probe and `hardlink` was selected on a filesystem that can reflink — which
// matters because hardlink's safety is CONTINGENT (it links every regular file and rests on
// `idevicebackup2` unlinking before it creates) where reflink cannot alias at all.
//
// DRIVEN THROUGH `probeClone` AND `reflinkProbeSleep`, so these cost no real seconds and need no
// pool. `probeClone` is the probe's own seam; `Clone`'s `reflinkFile` is untouched, because the
// retry is the probe's and the seed question is deliberately still open.
func TestTheProbeRetriesUntilTheTxgSettles(t *testing.T) {
	dir := t.TempDir()

	var slept []time.Duration
	restore := func(clone func(string, string) error) func() {
		oc, os_ := probeClone, reflinkProbeSleep
		probeClone = clone
		reflinkProbeSleep = func(d time.Duration) { slept = append(slept, d) }
		return func() { probeClone, reflinkProbeSleep = oc, os_ }
	}

	t.Run("an EAGAIN that clears is not a refusal", func(t *testing.T) {
		slept = nil
		calls := 0
		defer restore(func(dst, src string) error {
			calls++
			if calls <= 3 {
				return ErrReflinkUnavailable
			}
			return reflinkFile(dst, src) // the real one, so the rest of the probe runs honestly
		})()

		res, why := ReflinkProbeDetail(dir)
		if res == ReflinkUnavailable {
			t.Fatalf("gave up on a transient EAGAIN: %v — %s", res, why)
		}
		if calls != 4 {
			t.Errorf("clone attempted %d times, want 4 (three EAGAINs then success)", calls)
		}
		if len(slept) != 3 {
			t.Errorf("slept %d times, want 3 — one wait between each attempt", len(slept))
		}
		for _, d := range slept {
			if d != reflinkProbeInterval {
				t.Errorf("slept %s, want %s — the interval tracks zfs_txg_timeout", d, reflinkProbeInterval)
			}
		}
	})

	// THE BOUND IS REAL. Without it a filesystem that answers EAGAIN forever would hang startup,
	// which is worse than selecting hardlink.
	t.Run("an EAGAIN that never clears expires, bounded", func(t *testing.T) {
		slept = nil
		calls := 0
		defer restore(func(dst, src string) error { calls++; return ErrReflinkUnavailable })()

		res, why := ReflinkProbeDetail(dir)
		if res != ReflinkUnavailable {
			t.Fatalf("res = %v, want ReflinkUnavailable — %s", res, why)
		}
		if calls != reflinkProbeRetries+1 {
			t.Errorf("clone attempted %d times, want %d (the first plus %d retries)",
				calls, reflinkProbeRetries+1, reflinkProbeRetries)
		}
		// HONEST WHEN IT EXPIRES — quince#936's line, which the ruling says not to re-cross. A
		// timeout must not be promoted to "unsupported": that sends an operator to the wrong
		// question and picks hardlink on a filesystem that can reflink.
		if !strings.Contains(why, "still declined") || !strings.Contains(why, "txg") {
			t.Errorf("reason %q does not say what was OBSERVED — it must not read as 'unsupported'", why)
		}
	})

	// A SETTLED ANSWER MUST NOT BE RETRIED. EOPNOTSUPP, ENOTTY, EINVAL and EXDEV are facts about the
	// filesystem or the pair of files; waiting changes none of them, and retrying would add six
	// seconds to every startup on every non-reflink filesystem.
	t.Run("a settled refusal returns immediately", func(t *testing.T) {
		slept = nil
		calls := 0
		defer restore(func(dst, src string) error { calls++; return ErrReflinkUnsupported })()

		res, _ := ReflinkProbeDetail(dir)
		if res != ReflinkUnsupported {
			t.Fatalf("res = %v, want ReflinkUnsupported", res)
		}
		if calls != 1 {
			t.Errorf("clone attempted %d times, want 1 — a settled errno is not retried", calls)
		}
		if len(slept) != 0 {
			t.Errorf("slept %d times on a settled refusal, want 0", len(slept))
		}
	})
}

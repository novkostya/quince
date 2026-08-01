package backup

import (
	"testing"
	"time"
)

// TestTerminalRowStaysTerminal guards the invariant quince#178 turned out to be about: once a job's
// row is terminal it is FINAL, and no later write may resurrect it.
//
// Every other wait in this package returns on the FIRST terminal observation, so a write that lands
// afterwards is invisible to all of them — which is exactly why this went unnoticed while producing
// an ~8% CI failure. The bug was a stale progress snapshot overwriting the terminal row: both
// writers copied the row under lj.mu and then hit SQLite with the lock released, so the two DB
// writes could land in the opposite order to the two mutations. See the block comment above
// `transition` in engine.go for the interleaving.
//
// WHAT THIS TEST IS AND IS NOT. It asserts the observable property — after the dust settles the row
// is still terminal — and it would have caught the CI symptom, which is a non-terminal row for a job
// the engine no longer owns. It is NOT a deterministic reproduction: the losing interleaving needs
// the sampler descheduled inside a window that is nanoseconds wide once the lock is held correctly,
// and nothing here forces that. The mechanism was proven separately by widening that window by hand
// (a 50 ms sleep between the snapshot and the write), which failed with the exact CI signature
// before the fix and passes with it; that experiment is recorded on the PR rather than committed,
// because a test that only fails when you first break the code guards nothing.
//
// disk-full-105 is the right fixture because it is the shape the flake always took: the device
// refuses the backup right after the passcode prompt, so the job ends while parked in
// `waiting_for_passcode` — the phase a stale row then goes on advertising forever.
func TestTerminalRowStaysTerminal(t *testing.T) {
	m := loadMeta(t, "disk-full-105")
	h := newHarness(t, m.params(t), m.Transport)

	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 10*time.Second)
	if !isTerminal(final.State) {
		t.Fatalf("job did not reach a terminal state: %s", final.State)
	}

	// Let any straggler writer finish. The engine has released the job by now, so nothing legitimate
	// is still writing — which is the point: anything that lands here is a write that should not exist.
	time.Sleep(300 * time.Millisecond)

	after, ok := h.eng.Job(job.ID)
	if !ok {
		t.Fatal("job row vanished after the job ended")
	}
	if !isTerminal(after.State) {
		t.Fatalf("a terminal row was overwritten: state=%s phase=%s liveness=%s engine_owns=%v "+
			"(it had reached %s)\n"+
			"  → a write landed after the job ended. Per-job writes must be ordered by lj.mu spanning "+
			"the persist, not just the mutation (quince#178).",
			after.State, after.Progress.Phase, after.Progress.Liveness,
			engineOwns(h.eng, job.ID), final.State)
	}
	// The phase must not outlive the job either — quince#313 ruled that separately, and a resurrected
	// row is the other way it comes back, so assert it here rather than trusting the state alone.
	if after.Progress.Phase == PhaseWaitingForPasscode {
		t.Errorf("terminal job still advertises %s — a user would be told to unlock for a job that is over",
			after.Progress.Phase)
	}
}

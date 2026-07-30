package backup

import (
	"testing"

	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// TestProgressNoteIsSilentOnATerminalJob pins quince#313's consumer half: a job that is over must
// not be narrated as if it were running. The engine now clears `Phase` on termination, so this
// asserts the CONSUMER independently of that — the defect was reading a running field without
// asking whether anything was running, and that is wrong whatever a producer does. Demo scripts,
// reconciled rows and future producers all reach this function.
func TestProgressNoteIsSilentOnATerminalJob(t *testing.T) {
	for _, state := range []string{StateSucceeded, StateFailed, StateCancelled, StateConnectionLost} {
		var j wire.Job
		j.State = state
		j.Progress.Phase = PhaseWaitingForPasscode // the stale live phase quince#313 reported
		pct := 42.0
		j.Progress.Percent = &pct
		if note := progressNote(j); note != "" {
			t.Errorf("state=%s: a finished job was narrated as live: %q", state, note)
		}
	}
}

// TestProgressNoteStillNarratesALiveJob is the control, and it is the one that matters: the fix is
// one careless step from silencing the narration this function exists to produce. A passcode prompt
// nobody sees is a backup that never happens, which is worse than the defect being fixed.
func TestProgressNoteStillNarratesALiveJob(t *testing.T) {
	var waiting wire.Job
	waiting.State = StateBackingUp
	waiting.Progress.Phase = PhaseWaitingForPasscode
	if note := progressNote(waiting); note != " (enter the passcode on the device)" {
		t.Errorf("a running job stopped asking for the passcode: %q", note)
	}

	var moving wire.Job
	moving.State = StateBackingUp
	moving.Progress.Phase = "receiving"
	pct := 63.0
	moving.Progress.Percent, moving.Progress.FilesReceived = &pct, 149
	if note := progressNote(moving); note != " (63%, 149 files)" {
		t.Errorf("a running job stopped reporting progress: %q", note)
	}
}

// TestTerminateClearsTheLivePhase pins quince#313's engine half at the unit the bug lived in.
// `succeed` clears `Phase`; `terminate` did not, so a job that failed while parked at
// `waiting_for_passcode` stayed terminal-with-a-live-phase and every consumer inherited it.
//
// `Percent` is asserted UNCHANGED on purpose. It is the last true measurement of how far the job
// got — information about the past rather than a claim about now — and clearing it would lose the
// one number a user wants after a failure.
func TestTerminateClearsTheLivePhase(t *testing.T) {
	h := newHarness(t, fakeParams{}, TransportUSB)
	lj := &liveJob{row: store.JobRow{
		ID: "01TERMINATEPHASE0000000000", UDID: testUDID, State: StateBackingUp,
		Phase: PhaseWaitingForPasscode, Liveness: LivenessActive, Percent: f64(37),
	}}

	h.eng.terminate(lj, StateFailed, "device_locked", "the device was never unlocked")

	if lj.row.State != StateFailed {
		t.Fatalf("state = %q, want %q", lj.row.State, StateFailed)
	}
	if lj.row.Phase != "" {
		t.Errorf("a terminal job kept the live phase %q — quince#313", lj.row.Phase)
	}
	if lj.row.Liveness != "" {
		t.Errorf("a terminal job kept liveness %q — the other field describing a live process", lj.row.Liveness)
	}
	if lj.row.Percent == nil || *lj.row.Percent != 37 {
		t.Errorf("percent was not preserved: %v — it is the last true measurement, not a live claim", lj.row.Percent)
	}
}

package deviceops

import (
	"context"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/muxd"
)

// THE LAST PRE-WALK PROTECTION THAT EXISTS. qn.6p D7's promise is retired and qn.6r D3
// forecloses a replacement, so this refusal is the whole remainder of *refuse before the user
// crosses the room*. If it stops firing nothing says so — the pair proceeds and fails
// afterwards, which is the outcome it exists to prevent (quince#1341 review).
func TestPairRefusesBeforeTheWalkWhenTheMuxerCannotBeReached(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")
	m.tools.pairRecords = func(string) muxd.PairRecord {
		return muxd.PairRecord{State: muxd.PairRecordUnknown}
	}

	opID, status, reason := m.Pair(context.Background(), fakeUDID)
	if status != 409 {
		t.Fatalf("Pair = %d %q (want 409 before any op starts)", status, reason)
	}
	if opID != "" {
		t.Fatalf("an op was started (%q) — the refusal must come BEFORE idevicepair runs", opID)
	}
	// Actionable: it must name what to check, not merely that something is wrong.
	if !strings.Contains(reason, "muxer") {
		t.Errorf("the refusal does not name the muxer: %q", reason)
	}
	if !strings.Contains(reason, "socket") {
		t.Errorf("the refusal does not say what to check: %q", reason)
	}
}

// The control: a muxer that ANSWERS still gets past the refusal, so the test above is not
// passing because Pair refuses everything.
func TestPairProceedsWhenTheMuxerAnswers(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	_, status, reason := m.Pair(context.Background(), fakeUDID)
	if status != 202 {
		t.Fatalf("Pair = %d %q (want 202)", status, reason)
	}
}

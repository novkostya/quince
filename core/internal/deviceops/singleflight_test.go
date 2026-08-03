package deviceops

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// quince#465's ruling, asserted on the SHIPPING manager rather than only on the demo copy:
// one in-flight device op per UDID, whatever its kind.

// These tests use wifisync_test.go's pairedUSBDevice: it satisfies every precondition all three
// op routes check, so a refusal here can only be the single-flight guard and never a validation
// branch.

// busyManager returns a manager with a pair op parked in its poll loop, and that op's id. The
// fake never reports Trust, so the op stays non-terminal for the whole test without a sleep.
func busyManager(t *testing.T, devs *fakeDevices) (*Manager, string) {
	t.Helper()
	m := newTestManager(t, devs,
		"DEVICEOPS_FAKE=trust_then_success",
		"DEVICEOPS_COUNTER="+t.TempDir()+"/c",
		"DEVICEOPS_TRUST_UNTIL=1000000", // never reached: the op stays in flight
	)
	opID, status, reason := m.Pair(context.Background(), fakeUDID)
	if status != http.StatusAccepted {
		t.Fatalf("first Pair = %d %q (want 202)", status, reason)
	}
	return m, opID
}

// TestSecondOpOnOneDeviceIsRefusedWhateverItsKind is the ruling's load-bearing case. A
// per-(UDID, kind) key would accept every row below except the first — and the wifi_sync row is
// the combination quince#363/quince#366 measured severing the transport another op runs over, so
// the finer key would be DEFINED to permit the one pairing that is known to break.
func TestSecondOpOnOneDeviceIsRefusedWhateverItsKind(t *testing.T) {
	devs := newFakeDevices()
	devs.add(pairedUSBDevice(fakeUDID))
	m, _ := busyManager(t, devs)

	for _, tc := range []struct {
		kind string
		call func() (string, int, string)
	}{
		{"pair", func() (string, int, string) { return m.Pair(context.Background(), fakeUDID) }},
		{"wifi_sync", func() (string, int, string) { return m.WifiSync(context.Background(), fakeUDID, "enable") }},
		{"encryption", func() (string, int, string) {
			return m.Encryption(context.Background(), fakeUDID, "enable", "test", "", "")
		}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			opID, status, reason := tc.call()
			if status != http.StatusConflict {
				t.Fatalf("%s while an op is in flight = %d %q (want 409)", tc.kind, status, reason)
			}
			if reason != inFlightMsg {
				t.Fatalf("%s refusal = %q (want the single-flight message)", tc.kind, reason)
			}
			if opID != "" {
				t.Fatalf("%s refused but still handed out op id %q — a refused op must record nothing", tc.kind, opID)
			}
		})
	}
}

// TestRefusedOpRecordsNothing — a refusal must not leave an op behind. If it did, the demo's
// unbounded-map finding would simply move from the accept path to the refuse path, and a burst
// would still buy one map entry per request.
func TestRefusedOpRecordsNothing(t *testing.T) {
	devs := newFakeDevices()
	devs.add(pairedUSBDevice(fakeUDID))
	m, firstID := busyManager(t, devs)

	m.mu.Lock()
	before := len(m.ops)
	m.mu.Unlock()

	for i := 0; i < 20; i++ {
		if _, status, _ := m.Pair(context.Background(), fakeUDID); status != http.StatusConflict {
			t.Fatalf("refused Pair #%d = %d (want 409)", i, status)
		}
	}

	m.mu.Lock()
	after, inflight := len(m.ops), len(m.inflight)
	m.mu.Unlock()
	if after != before {
		t.Fatalf("ops grew from %d to %d over 20 REFUSED requests (want no growth)", before, after)
	}
	if inflight != 1 {
		t.Fatalf("inflight = %d (want exactly the one op %s)", inflight, firstID)
	}
}

// TestSlotIsFreedWhenTheOpEnds — the guard must not strand the device. The slot is released by a
// defer in startGuardedOp's goroutine, so it frees on every exit path the run* can take, not only
// on the success path this exercises.
func TestSlotIsFreedWhenTheOpEnds(t *testing.T) {
	devs := newFakeDevices()
	devs.add(pairedUSBDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	first, status, reason := m.Pair(context.Background(), fakeUDID)
	if status != http.StatusAccepted {
		t.Fatalf("first Pair = %d %q (want 202)", status, reason)
	}
	if op := waitOp(t, m, first); op.State != "succeeded" {
		t.Fatalf("first pair op = %+v (want succeeded)", op)
	}

	// Terminal state and slot release are two different moments: the op goes terminal inside the
	// goroutine, the slot frees as that goroutine returns. Poll rather than assume an ordering
	// the code does not promise.
	deadline := time.Now().Add(5 * time.Second)
	for {
		second, status, reason := m.Pair(context.Background(), fakeUDID)
		if status == http.StatusAccepted && second != "" {
			return
		}
		if status != http.StatusConflict {
			t.Fatalf("second Pair after the first ended = %d %q (want 202, or 409 while releasing)", status, reason)
		}
		if time.Now().After(deadline) {
			t.Fatal("the single-flight slot was never released — the device is stranded until restart")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestTheGuardIsPerDeviceNotGlobal — a busy device must not block a different one. The key is the
// UDID, and this is what distinguishes the ruling from a global op lock.
func TestTheGuardIsPerDeviceNotGlobal(t *testing.T) {
	const otherUDID = "SYNTHETIC-UDID-AAAA-0002"
	devs := newFakeDevices()
	devs.add(pairedUSBDevice(fakeUDID))
	devs.add(pairedUSBDevice(otherUDID))
	m, _ := busyManager(t, devs)

	opID, status, reason := m.Pair(context.Background(), otherUDID)
	if status != http.StatusAccepted || opID == "" {
		t.Fatalf("Pair on a DIFFERENT device = %d %q (want 202 — the guard keys on udid)", status, reason)
	}
}

package deviceops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// qn.6p D7. A pairing that cannot be RECORDED is not a pairing: without this refusal idevicepair
// runs, the phone shows Trust, somebody walks over and taps it, and the record fails to write.
// The refusal comes before the user is asked to act.
//
// This is a CALL-SITE test, not a test of Writable() — which has its own. The distinction is the
// one quince#1059 cost: a guard that exists and is tested can still be wired to nothing, and this
// one fails SILENT (pairing appears to start, then cannot persist) rather than loud.
func TestPairRefusesWhenPairingRecordsCannotBeWritten(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	// A lockdown dir that cannot exist: the path is occupied by a file. Chosen over chmod because
	// `make gates` runs as ROOT, where mode bits refuse nobody — a permissions fixture would skip
	// on the ladder and this assertion would never run there.
	sys := filepath.Join(t.TempDir(), "lockdown")
	if err := os.WriteFile(sys, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	m.SetLockdown(NewLockdownStore(t.TempDir(), sys, discard()))

	_, status, reason := m.Pair(context.Background(), fakeUDID)
	if status != 409 {
		t.Fatalf("pair with an unwritable lockdown dir = %d (want 409); reason %q", status, reason)
	}
	// The reason reaches a user, so it must say what is wrong and what it implies — not an errno.
	for _, want := range []string{"cannot be written", "would not survive", sys} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal %q does not carry %q", reason, want)
		}
	}
}

// The store is OPTIONAL (SetLockdown: "nil means no persistence"), and nil must not refuse pairing:
// that is how the demo and most tests run, and the honest reading of nil is "quince is not
// recording pairings here", not "quince cannot".
func TestPairWithNoLockdownStoreIsNotRefused(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	if _, status, reason := m.Pair(context.Background(), fakeUDID); status != 202 {
		t.Fatalf("pair with no lockdown store = %d (want 202); reason %q", status, reason)
	}
}

package backup

import (
	"testing"

	"github.com/novkostya/quince/core/internal/store"
)

// RunningFor MUST AGREE WITH THE 409, because they read the same map and the notifier's whole reason
// for asking is "is quince already doing the thing I am about to nag about".
//
// The teardown row is the case worth pinning. `StartBackup` deliberately words that window
// differently — the user has just cancelled and is owed an honest sentence — and it would be easy to
// carry that distinction here by mistake. It must not be: the question this method answers is
// whether to interrupt somebody, and a device whose backup is still tearing down is a device whose
// owner is looking at a screen about it.
//
// Seeded directly, following TestStartBackupNamesTeardownRatherThanClaimingItRuns: there is no run
// goroutine behind the entry, and racing for the window would assert on teardown timing.
func TestRunningForAgreesWithTheBusyCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
	}{
		{"genuinely running", StateBackingUp},
		{"cancelled and still tearing down", StateCancelled},
		{"failed and still tearing down", StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, fakeParams{}, TransportUSB)
			if h.eng.RunningFor(testUDID) {
				t.Fatalf("an idle device reported a backup in flight")
			}

			h.eng.mu.Lock()
			h.eng.running[testUDID] = &liveJob{
				row:    store.JobRow{ID: "HELD", UDID: testUDID, State: tc.state, Transport: "usb"},
				cancel: func() {},
			}
			h.eng.mu.Unlock()
			defer func() {
				h.eng.mu.Lock()
				delete(h.eng.running, testUDID)
				h.eng.mu.Unlock()
			}()

			if !h.eng.RunningFor(testUDID) {
				t.Errorf("RunningFor said no about a device StartBackup refuses with 409")
			}
			// The same engine, a device that holds no slot. A method that answered by "is anything
			// running" rather than "is anything running FOR THIS DEVICE" would pass every assertion
			// above and suppress reminders for the whole fleet whenever one phone was busy.
			if h.eng.RunningFor("SOME-OTHER-DEVICE") {
				t.Errorf("one busy device made every other device look busy")
			}
		})
	}
}

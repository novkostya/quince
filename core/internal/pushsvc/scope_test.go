package pushsvc

import (
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/notify"
	"github.com/novkostya/quince/core/internal/store"
)

// THE SEND-PATH FILTER (spec D7, slice 10a).
//
// 0011 stated the read this narrows: "Reads are 'every live subscription', which is what a send
// does." True while quince had one principal; a device-scoped holder would otherwise receive every
// device's backups, their failures, and the device NAMES in the titles.
//
// These call `receives` — the predicate the send loop itself uses. An earlier version of this file
// restated the condition instead, which would have passed just as happily with the real one wrong.

func scopedSub(udid string) store.PushSubscription {
	return store.PushSubscription{ID: "s", Endpoint: "e", ScopeUDID: &udid}
}

func TestAnAdminSubscriptionReceivesEveryDevice(t *testing.T) {
	admin := store.PushSubscription{ID: "s", Endpoint: "e"} // nil scope
	for _, udid := range []string{"DEVICE-A", "DEVICE-B", ""} {
		if !receives(admin, notify.Decision{UDID: udid}) {
			t.Errorf("the admin was filtered out of a notification for %q", udid)
		}
	}
}

func TestAScopedSubscriptionReceivesOnlyItsDevice(t *testing.T) {
	scoped := scopedSub("DEVICE-A")
	if !receives(scoped, notify.Decision{UDID: "DEVICE-A"}) {
		t.Fatal("a scoped holder was denied their OWN device's notification")
	}
	if receives(scoped, notify.Decision{UDID: "DEVICE-B"}) {
		t.Fatal("a scoped holder received ANOTHER device's notification — the leak D7 is about, " +
			"and the title carries that device's name")
	}
}

// A decision with no device reaches only the admin. It should not arise — every kind is about a
// device — and the safe direction is silence for a scoped holder rather than a notification they
// cannot place.
func TestADecisionWithNoDeviceDoesNotReachAScopedSubscription(t *testing.T) {
	if receives(scopedSub("DEVICE-A"), notify.Decision{}) {
		t.Fatal("a device-less decision reached a scoped subscription")
	}
}

// The LIVE guard is separate and still first in the loop. Asserted so the new filter cannot be read
// as having replaced it.
func TestAnExpiredSubscriptionIsStillFilteredByLive(t *testing.T) {
	now := time.Now().UTC()
	expired := store.PushSubscription{ID: "s", Endpoint: "e", ExpiredAt: &now}
	if expired.Live() {
		t.Fatal("an expired subscription reports Live")
	}
	// And `receives` alone does NOT exclude it — the two guards are independent, which is why the
	// loop applies both.
	if !receives(expired, notify.Decision{UDID: "DEVICE-A"}) {
		t.Fatal("receives is doing Live's job; the guards must stay separate")
	}
}

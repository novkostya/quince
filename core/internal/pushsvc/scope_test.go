package pushsvc

import (
	"context"
	"net/http"
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

// THE OTHER HALF OF THE PRECEDENCE PROPERTY (qn.13 slice 10b).
//
// The device mute used to be answered in `notify`, beside the category gate, so ONE test could see
// both. With two principals there is no single answer, so the device half moved here and the two
// halves are now tested in two packages. `notify.TestCategoryPrecedenceSurvivesTheMove` is the
// other; neither sees the composition, and that is declared rather than papered over.
func TestAMutedOwnerReceivesNothing(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, raw := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	d := decision()
	// `senderWith`'s subscription has a NULL scope, so it is the admin's.
	if err := raw.SetDeviceNotificationsEnabled(d.UDID, "", false); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if err := s.DeliverDecision(context.Background(), d); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if staged.got != nil {
		t.Fatal("a muted owner received the notification — the mute did not survive the move to send")
	}
}

// AND THE CONTROL, which is the whole reason the mute moved: one principal's mute must not silence
// another's.
func TestAnAdminMuteDoesNotSilenceTheScopedHolder(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, raw := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	d := decision()
	if err := raw.SetDeviceNotificationsEnabled(d.UDID, "", false); err != nil { // the admin's mute
		t.Fatalf("mute: %v", err)
	}
	scope := d.UDID
	if err := raw.AddPushSubscription(store.PushSubscription{
		ID: "scoped-sub", Endpoint: srv.URL + "/push/scoped", P256DH: rfcP256DH, Auth: rfcAuth,
		Label: "household iPhone", Origin: testOrigin, CreatedAt: time.Now().UTC(),
		ScopeUDID: &scope,
	}); err != nil {
		t.Fatalf("seed scoped subscription: %v", err)
	}

	if err := s.DeliverDecision(context.Background(), d); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if staged.got == nil {
		t.Fatal("the scoped holder received nothing because the ADMIN muted the device — this is " +
			"the ruling inverted, and the whole reason the mute moved to send")
	}
}

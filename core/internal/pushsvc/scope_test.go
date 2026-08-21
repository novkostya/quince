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

// THE NEIGHBOUR HALF, REBUILT AT THE LAYER IT MOVED TO (quince#1409 review, finding 8).
//
// `notify.TestAMutedNeverBackedUpDeviceIsNotInvitedAndItsNeighbourStillIs` was labelled *the case
// that prompted the feature* and is quince#1270's named acceptance. Slice 10b deleted it, correctly
// — it asserted the mute at a layer that no longer reads it — but the property it protected did not
// move on its own. This is it: muting ONE device must not silence the same owner's other device.
func TestMutingOneDeviceDoesNotSilenceTheOwnersOtherDevice(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, raw := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	muted := decision()
	if err := raw.SetDeviceNotificationsEnabled(muted.UDID, "", false); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if err := s.DeliverDecision(context.Background(), muted); err != nil {
		t.Fatalf("deliver the muted one: %v", err)
	}
	if staged.got != nil {
		t.Fatal("fixture: the muted device was delivered; the neighbour assertion below would prove nothing")
	}

	// THE NEIGHBOUR. Same subscription, same owner, a different device — and nothing was said
	// about this one, so it must still arrive.
	neighbour := decision()
	neighbour.UDID = "UDID-NEIGHBOUR"
	neighbour.Navigate = "/devices/UDID-NEIGHBOUR"
	if err := s.DeliverDecision(context.Background(), neighbour); err != nil {
		t.Fatalf("deliver the neighbour: %v", err)
	}
	if staged.got == nil {
		t.Fatal("muting one device silenced its owner's OTHER device — quince#1270's whole gap")
	}
}

// THE EVERY-KIND HALF, ALSO REBUILT (finding 8).
//
// `notify.TestMutingCoversTheTerminalKindsToo` pinned the subject axis across the terminal kinds:
// *"notifications about this device: on or off" is the whole of it*, and a per-kind reading would be
// the per-(device × category) matrix quince#1270 defers.
//
// AT THIS LAYER IT IS FREE BY CONSTRUCTION — the send loop never looks at `Kind` — and that is a
// reason to SAY SO rather than to drop it. The test is what keeps it free: a future filter that
// grew a kind-dependent branch would fail here.
func TestTheMuteCoversEveryKind(t *testing.T) {
	for _, kind := range []notify.Kind{
		notify.KindBackupAvailable, notify.KindBackupCompleted, notify.KindBackupFailed,
	} {
		t.Run(string(kind), func(t *testing.T) {
			staged := &stagedPush{status: http.StatusCreated}
			srv := staged.server(t)
			s, raw := senderWith(t, srv.URL+"/push/token")
			s = s.WithHTTPClient(srv.Client())

			d := decision()
			d.Kind = kind
			// THE FIXTURE FIRST: unmuted, this kind must actually deliver, or the refusal below
			// would be indistinguishable from a kind that never sends at all.
			if err := s.DeliverDecision(context.Background(), d); err != nil {
				t.Fatalf("deliver unmuted: %v", err)
			}
			if staged.got == nil {
				t.Fatalf("fixture: kind %q delivered nothing unmuted; the assertion below proves nothing", kind)
			}

			staged.got = nil
			if err := raw.SetDeviceNotificationsEnabled(d.UDID, "", false); err != nil {
				t.Fatalf("mute: %v", err)
			}
			if err := s.DeliverDecision(context.Background(), d); err != nil {
				t.Fatalf("deliver muted: %v", err)
			}
			if staged.got != nil {
				t.Fatalf("kind %q survived the mute — the subject axis must cover every kind", kind)
			}
		})
	}
}

package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func pushStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/quince.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sub(id, endpoint string) PushSubscription {
	return PushSubscription{
		ID: id, Endpoint: endpoint, P256DH: "p256dh-" + id, Auth: "auth-" + id,
		Label: "iPhone", CreatedAt: time.Now().UTC(),
	}
}

// AN EXPIRED SUBSCRIPTION IS KEPT AND MARKED, NEVER DELETED (spec D8).
//
// Deleting is what makes a phone that quietly stopped receiving invisible, and its first symptom is
// a missed backup. The status surface has to be able to say *which* device died, so the row has to
// survive to carry that.
func TestAnExpiredSubscriptionIsKeptAndMarked(t *testing.T) {
	s := pushStore(t)
	if err := s.AddPushSubscription(sub("A", "https://push.example.net/a")); err != nil {
		t.Fatalf("add: %v", err)
	}
	when := time.Now().UTC()
	if err := s.ExpirePushSubscription("https://push.example.net/a", when); err != nil {
		t.Fatalf("expire: %v", err)
	}
	got, err := s.PushSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expiring removed the row; the failure is now invisible. got %d rows", len(got))
	}
	if got[0].Live() {
		t.Errorf("the row is still live after a 410")
	}
	if got[0].ExpiredAt == nil {
		t.Errorf("expired_at was not recorded, so nothing can say WHEN it died")
	}
}

// RE-SUBSCRIBING REVIVES THE ROW rather than duplicating it. Two rows for one endpoint would mean
// two pushes to one phone for every notification, and the endpoint — not the id — is the identity.
func TestResubscribingRevivesRatherThanDuplicating(t *testing.T) {
	s := pushStore(t)
	const ep = "https://push.example.net/a"
	if err := s.AddPushSubscription(sub("A", ep)); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.ExpirePushSubscription(ep, time.Now()); err != nil {
		t.Fatalf("expire: %v", err)
	}
	// The same device comes back, with fresh keys and a new client-side id.
	again := sub("B", ep)
	again.P256DH = "fresh-p256dh"
	if err := s.AddPushSubscription(again); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got, err := s.PushSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("re-subscribing produced %d rows for one endpoint — that is two pushes per notification", len(got))
	}
	if !got[0].Live() {
		t.Errorf("the revived row is still marked expired")
	}
	if got[0].P256DH != "fresh-p256dh" {
		t.Errorf("the revived row kept the OLD keys (%q); nothing could decrypt a push to it", got[0].P256DH)
	}
}

// A CLEAN INSTALL GENERATES; A DIVERGENT ONE REFUSES.
//
// This is the ruling's constraint made structural (quince#1128). "Subscriptions exist, key does not"
// is a state the ruling makes unreachable by ordinary means, so meeting it means the DB was
// partially restored or altered — and minting a fresh key there would leave every subscribed phone
// holding a subscription quince can no longer sign for, while quince looked healthy.
func TestAMissingKeyIsAFreshInstallOnlyWhenNoSubscriptionsExist(t *testing.T) {
	s := pushStore(t)

	// Clean install: no key, no subscriptions. The caller may generate.
	if _, ok, err := s.VAPIDPrivateKey(); err != nil || ok {
		t.Fatalf("a clean install should report absent with no error; ok=%v err=%v", ok, err)
	}

	// Now the divergent state.
	if err := s.AddPushSubscription(sub("A", "https://push.example.net/a")); err != nil {
		t.Fatalf("add: %v", err)
	}
	_, ok, err := s.VAPIDPrivateKey()
	if ok {
		t.Fatalf("a key was reported present when none is stored")
	}
	if !errors.Is(err, ErrVAPIDKeyMissing) {
		t.Fatalf("subscriptions without a key did not refuse: err=%v", err)
	}
	// THE REFUSAL MUST NAME THE REMEDY. Under the troubleshooting rule an error that says only
	// "missing" leaves the operator to guess, and the guess ("delete the rows") is the destructive one.
	if !strings.Contains(err.Error(), "re-subscribe") {
		t.Errorf("the refusal does not say what has to happen: %v", err)
	}
}

// THE KEY SURVIVES A ROUND TRIP, AND A SECOND WRITE IS REFUSED.
//
// "Generate if absent" is the only correct use. Two startups racing, or a retried request, must not
// replace a key that subscriptions already depend on — so the guard is in the store rather than left
// to every caller to remember.
func TestTheVAPIDKeyRoundTripsAndCannotBeReplaced(t *testing.T) {
	s := pushStore(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := s.SetVAPIDPrivateKey(key); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := s.VAPIDPrivateKey()
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if string(got) != string(key) {
		t.Errorf("the key changed across the round trip")
	}
	other := make([]byte, 32)
	if err := s.SetVAPIDPrivateKey(other); err == nil {
		t.Errorf("a second write replaced the key — every existing subscription would be silently unsignable")
	}
}

// A CORRUPTED STORED KEY FAILS TO DECODE RATHER THAN ARRIVING SHORT. A short key would produce
// signatures no push service accepts, which presents as notifications that never arrive and nothing
// anywhere saying why.
func TestACorruptStoredKeyIsRefused(t *testing.T) {
	s := pushStore(t)
	for name, bad := range map[string]string{
		"not base64": "!!!!",
		"too short":  "AAAA",
	} {
		if err := s.SetSetting(VAPIDKeySetting, bad); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		if _, ok, err := s.VAPIDPrivateKey(); err == nil || ok {
			t.Errorf("%s stored key was accepted: ok=%v err=%v", name, ok, err)
		}
	}
}

// THE REMINDER LEDGER IS KEYED ON THE DEVICE, NOT ON (device, kind) — spec D5. A row per kind would
// permit exactly the double-notification the one-track design exists to make impossible.
func TestTheReminderLedgerIsOneTrackPerDevice(t *testing.T) {
	s := pushStore(t)
	const udid = "UDID-FIXTURE"
	if _, ok, err := s.PushReminder(udid); err != nil || ok {
		t.Fatalf("a device never reminded reported ok=%v err=%v", ok, err)
	}
	first := time.Now().UTC().Truncate(time.Second)
	if err := s.SetPushReminder(udid, first); err != nil {
		t.Fatalf("set: %v", err)
	}
	// The escalation writes the SAME row, which is what keeps one lapse to one cooldown.
	second := first.Add(25 * time.Hour)
	if err := s.SetPushReminder(udid, second); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	at, ok, err := s.PushReminder(udid)
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if !at.Equal(second) {
		t.Errorf("the track holds %v, want the newer %v", at, second)
	}
	// A successful backup clears it, so the next lapse starts a fresh cooldown.
	if err := s.ClearPushReminder(udid); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok, _ := s.PushReminder(udid); ok {
		t.Errorf("the track survived a successful backup")
	}
}

// DELETING IS THE USER'S ACT AND IT REALLY DELETES. It is the other way a row leaves the list, and
// unlike expiry it is a choice somebody made.
func TestDeletingASubscriptionRemovesIt(t *testing.T) {
	s := pushStore(t)
	if err := s.AddPushSubscription(sub("A", "https://push.example.net/a")); err != nil {
		t.Fatalf("add: %v", err)
	}
	gone, err := s.DeletePushSubscription("A")
	if err != nil || !gone {
		t.Fatalf("delete: gone=%v err=%v", gone, err)
	}
	got, _ := s.PushSubscriptions()
	if len(got) != 0 {
		t.Errorf("the row survived an explicit delete")
	}
	// Deleting one that is not there is not an error — the same shape DeleteSetting uses.
	if gone, err := s.DeletePushSubscription("A"); err != nil || gone {
		t.Errorf("a second delete reported gone=%v err=%v", gone, err)
	}
}

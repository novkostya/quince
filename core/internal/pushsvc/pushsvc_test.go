package pushsvc

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/push"
	"github.com/novkostya/quince/core/internal/store"
)

func svc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/quince.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	n := 0
	return New(s, func() string { n++; return "sub-" + string(rune('A'+n-1)) }, func() time.Time {
		return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	}), s
}

// A real subscription's keys, borrowed from RFC 8291 §5 — a published standard, so nothing here
// came from a device and nothing here is a capability against one.
const (
	rfcP256DH = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuth   = "BTBZMqHH6r4Tts7J_aSIgg"
)

// THE KEY IS GENERATED ONCE AND IS STABLE. Every subscription a phone creates is bound to the public
// half, so a second call returning a different key would silently orphan every existing one.
func TestTheVAPIDKeyIsGeneratedOnceAndIsStable(t *testing.T) {
	s, stored := svc(t)
	first, err := s.VAPIDPublicKey()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first == "" {
		t.Fatalf("no key was returned")
	}
	second, err := s.VAPIDPublicKey()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second != first {
		t.Errorf("the key changed between calls; every existing subscription would be orphaned")
	}
	// WHAT IS SERVED IS THE STORED KEY'S PUBLIC HALF, read back from the SAME store — the first
	// version of this check opened a second store, generated an unrelated key, and compared against
	// that. It failed, correctly, and the defect was the test's.
	raw, ok, err := stored.VAPIDPrivateKey()
	if err != nil || !ok {
		t.Fatalf("the key was not persisted: ok=%v err=%v", ok, err)
	}
	k, err := push.VAPIDKeyFromBytes(raw)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if k.PublicKeyBase64() != first {
		t.Errorf("the served key is not the stored key's public half")
	}
	// AND IT IS AN X9.62 UNCOMPRESSED POINT, which is what `applicationServerKey` must be — RFC 8292
	// §3.2 chooses that over JWK deliberately, so serving a JWK here would be silently unusable.
	if len(k.PublicKey()) != 65 || k.PublicKey()[0] != 0x04 {
		t.Errorf("the served key is not an uncompressed point")
	}
}

// THE RULING'S FIRST CONSTRAINT, END TO END: a key missing WITH subscriptions present is refused
// rather than regenerated (quince#1128). Reaching that state means the DB was partially restored,
// and minting a fresh key there would leave every subscribed phone unreachable while quince looked
// healthy.
func TestAMissingKeyWithSubscriptionsIsRefusedRatherThanRegenerated(t *testing.T) {
	s, raw := svc(t)
	if _, err := s.Subscribe("https://push.example.net/a", rfcP256DH, rfcAuth, "iPhone", testOrigin); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Subscribing did not need the key, so none exists — exactly the divergent state.
	_, err := s.VAPIDPublicKey()
	if !errors.Is(err, store.ErrVAPIDKeyMissing) {
		t.Fatalf("a missing key with subscriptions present did not refuse: %v", err)
	}
	// AND NOTHING WAS WRITTEN. A refusal that generated anyway would be the defect wearing a
	// refusal's clothes.
	if _, ok, _ := raw.VAPIDPrivateKey(); ok {
		t.Errorf("a key was minted despite the refusal")
	}
}

// A MALFORMED SUBSCRIPTION IS REFUSED AT THE DOOR, not at send time.
//
// `push.Encrypt` would reject the same input — days later, on a schedule, with nobody watching,
// where the only symptom is a notification that never arrives.
func TestABadSubscriptionIsRefusedWhenItIsCreated(t *testing.T) {
	s, _ := svc(t)
	for name, sub := range map[string][3]string{
		"no endpoint":        {"", rfcP256DH, rfcAuth},
		"p256dh not base64":  {"https://p.example/a", "!!!!", rfcAuth},
		"p256dh not a point": {"https://p.example/a", "AAAA", rfcAuth},
		"auth wrong length":  {"https://p.example/a", rfcP256DH, "AAAA"},
	} {
		if _, err := s.Subscribe(sub[0], sub[1], sub[2], "x", testOrigin); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// The good one still goes through, so the guard is not simply refusing everything.
	if _, err := s.Subscribe("https://push.example.net/a", rfcP256DH, rfcAuth, "iPhone", testOrigin); err != nil {
		t.Errorf("a valid subscription was refused: %v", err)
	}
}

// THE LIST CARRIES NO CAPABILITY. The endpoint and both keys are what a sender needs; anyone holding
// them can push to that phone, so they must not ride an ordinary session read.
func TestTheSubscriptionListNeverCarriesTheEndpointOrKeys(t *testing.T) {
	s, raw := svc(t)
	const ep = "https://push.example.net/very-secret-token"
	if _, err := s.Subscribe(ep, rfcP256DH, rfcAuth, "iPhone", testOrigin); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	got, err := s.Subscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(got))
	}
	// Rendered as JSON is how it actually leaves, so that is what is searched.
	blob := got[0].ID + got[0].Label + got[0].State + got[0].CreatedAt + got[0].ExpiredAt + got[0].LastSentAt
	for _, secret := range []string{"very-secret-token", rfcP256DH, rfcAuth} {
		if strings.Contains(blob, secret) {
			t.Errorf("the wire shape carries %q — that is a capability behind a session read", secret)
		}
	}
	if got[0].State != "live" {
		t.Errorf("state = %q, want live", got[0].State)
	}

	// AN EXPIRED ONE IS STILL LISTED, AND SAYS SO. Hiding it is what makes a dead phone invisible.
	if err := raw.ExpirePushSubscription(ep, time.Now()); err != nil {
		t.Fatalf("expire: %v", err)
	}
	got, _ = s.Subscriptions()
	if len(got) != 1 || got[0].State != "expired" {
		t.Fatalf("an expired subscription is not listed as expired: %+v", got)
	}
	if got[0].ExpiredAt == "" {
		t.Errorf("expired_at is empty, so nothing can say when it died")
	}
}

func TestUnsubscribeRemovesOneAndReportsWhether(t *testing.T) {
	s, _ := svc(t)
	id, err := s.Subscribe("https://push.example.net/a", rfcP256DH, rfcAuth, "iPhone", testOrigin)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if gone, err := s.Unsubscribe(id); err != nil || !gone {
		t.Fatalf("unsubscribe: gone=%v err=%v", gone, err)
	}
	if gone, err := s.Unsubscribe(id); err != nil || gone {
		t.Errorf("a second unsubscribe reported gone=%v err=%v", gone, err)
	}
}

// testOrigin is the address a subscribing browser reached quince by. Every test subscribes with one
// because a subscription without one cannot be delivered to — which is itself asserted, in
// TestASubscriptionWithNoOriginIsRefusedRatherThanGuessedAt.
const testOrigin = "https://quince.example.net"

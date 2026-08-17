package pushsvc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/notify"
	"github.com/novkostya/quince/core/internal/store"
)

// The RFC 8291 §5 user-agent keypair, so a test can DECRYPT what quince sent and check it is the
// notification that was asked for. Published in the standard, so nothing here came from a device.
const rfcUAPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"

func decision() notify.Decision {
	return notify.Decision{
		Kind: notify.KindBackupAvailable, UDID: "UDID-FIXTURE",
		Title: "iPhone is ready to back up", Body: "Its last backup was 5 days ago. Tap to start.",
		Navigate: "/devices/UDID-FIXTURE",
	}
}

// stagedPush is a fake push service. It records what arrived and answers what the test chose.
type stagedPush struct {
	status int
	got    *http.Request
	body   []byte
}

func (p *stagedPush) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.got = r
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		p.body = buf[:n]
		w.WriteHeader(p.status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func senderWith(t *testing.T, endpoint string) (*Service, *store.Store) {
	t.Helper()
	s, raw := svc(t)
	if _, err := s.VAPIDPublicKey(); err != nil { // generate the key
		t.Fatalf("key: %v", err)
	}
	if _, err := s.Subscribe(endpoint, rfcP256DH, rfcAuth, "iPhone"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return s, raw
}

// A REAL USER AGENT COULD READ THIS. The delivery path encrypts with a fresh ephemeral key and salt,
// which no fixture can cover — so the check is a round trip: send, then decrypt as the device.
//
// It is the only test in the rung that spans encryption, VAPID signing, HTTP framing and the store
// together, which is the point: each of those has its own suite and none of them proves they compose.
func TestADeliveredPushDecryptsToTheNotificationThatWasAsked(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, sender := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	results, err := s.Deliver(context.Background(), sender, decision(), "mailto:ops@example.com")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(results) != 1 || !results[0].Sent {
		t.Fatalf("delivery did not report success: %+v", results)
	}

	// THE HEADERS THE PUSH SERVICE REQUIRES. A missing `Content-Encoding` is accepted by some
	// services and rejected by others, which is the worst kind of bug: it works in testing.
	if got := staged.got.Header.Get("Content-Encoding"); got != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", got)
	}
	if !strings.HasPrefix(staged.got.Header.Get("Authorization"), "vapid t=") {
		t.Errorf("Authorization is not an RFC 8292 credential: %q", staged.got.Header.Get("Authorization"))
	}
	if staged.got.Header.Get("TTL") == "" {
		t.Errorf("no TTL header; RFC 8030 requires one and some services refuse without it")
	}

	// AND IT DECRYPTS TO WHAT WAS ASKED FOR.
	uaPriv, err := ecdh.P256().NewPrivateKey(mustB64(t, rfcUAPrivate))
	if err != nil {
		t.Fatalf("ua key: %v", err)
	}
	plain, err := decryptAsUserAgentHere(t, uaPriv, mustB64(t, rfcAuth), staged.body)
	if err != nil {
		t.Fatalf("a user agent could not decrypt the delivered body: %v", err)
	}
	for _, want := range []string{`"web_push":8030`, "ready to back up", "/devices/UDID-FIXTURE"} {
		if !strings.Contains(string(plain), want) {
			t.Errorf("the delivered payload is missing %q:\n%s", want, plain)
		}
	}
}

// 410 MARKS THE SUBSCRIPTION AND DOES NOT DELETE IT (spec D8). Deleting is what makes a phone that
// quietly stopped receiving invisible, and its first symptom is a missed backup.
func TestA410MarksTheSubscriptionExpiredAndKeepsTheRow(t *testing.T) {
	staged := &stagedPush{status: http.StatusGone}
	srv := staged.server(t)
	s, sender := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	results, err := s.Deliver(context.Background(), sender, decision(), "mailto:ops@example.com")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(results) != 1 || !results[0].Expired || results[0].Sent {
		t.Fatalf("a 410 was not reported as expiry: %+v", results)
	}
	rows, err := sender.PushSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the row was deleted; the dead device is now invisible")
	}
	if rows[0].Live() {
		t.Errorf("the row is still marked live after a 410")
	}
}

// A TRANSPORT FAILURE IS NOT AN EXPIRY. "The NAS was offline" and "the phone is gone" are different
// facts, and marking a live device dead because a CDN had a bad minute is the failure this
// distinction exists to prevent.
func TestAServerErrorDoesNotExpireTheSubscription(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusBadRequest} {
		staged := &stagedPush{status: status}
		srv := staged.server(t)
		s, sender := senderWith(t, srv.URL+"/push/token")
		s = s.WithHTTPClient(srv.Client())

		results, err := s.Deliver(context.Background(), sender, decision(), "mailto:ops@example.com")
		if err != nil {
			t.Fatalf("deliver: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("status %d: got %d results", status, len(results))
		}
		if results[0].Expired {
			t.Errorf("status %d expired a live subscription", status)
		}
		if results[0].Err == nil {
			t.Errorf("status %d was reported as success", status)
		}
		rows, _ := sender.PushSubscriptions()
		if len(rows) != 1 || !rows[0].Live() {
			t.Errorf("status %d killed the subscription", status)
		}
	}
}

// AN ENDPOINT NEVER REACHES AN ERROR STRING. Errors are how a capability gets into a log without
// anybody deciding it should — the log line is built from the error, and the error from the URL.
func TestDeliveryErrorsRedactTheEndpoint(t *testing.T) {
	staged := &stagedPush{status: http.StatusInternalServerError}
	srv := staged.server(t)
	const secret = "very-secret-token"
	s, sender := senderWith(t, srv.URL+"/push/"+secret)
	s = s.WithHTTPClient(srv.Client())

	results, _ := s.Deliver(context.Background(), sender, decision(), "mailto:ops@example.com")
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected one failing result: %+v", results)
	}
	if strings.Contains(results[0].Err.Error(), secret) {
		t.Errorf("the error carries the endpoint's secret path: %v", results[0].Err)
	}
	// And a Result never carries the endpoint at all — it is the shape that ends up in a log line.
	if strings.Contains(results[0].Label, secret) {
		t.Errorf("the Result label carries the endpoint")
	}
}

// AN EXPIRED SUBSCRIPTION IS SKIPPED rather than retried forever. It is already known dead, and
// posting to it again would be a request per notification per dead phone.
func TestAnExpiredSubscriptionIsNotDeliveredTo(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, sender := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())
	if err := sender.ExpirePushSubscription(srv.URL+"/push/token", time.Now()); err != nil {
		t.Fatalf("expire: %v", err)
	}
	results, err := s.Deliver(context.Background(), sender, decision(), "mailto:ops@example.com")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("an expired subscription was delivered to: %+v", results)
	}
}

// A FAILURE IS URGENT AND A REMINDER IS NOT (RFC 8030 §5.3). `low` lets a push service batch
// delivery to save battery, which is right for "due for a backup" and wrong for "the backup failed"
// — the second is the one somebody is waiting on.
func TestUrgencyVariesByKind(t *testing.T) {
	cases := map[notify.Kind]string{
		notify.KindBackupAvailable: "low",
		notify.KindBackupOverdue:   "low",
		notify.KindBackupCompleted: "low",
		notify.KindActionRequired:  "high",
		notify.KindBackupFailed:    "high",
	}
	for kind, want := range cases {
		staged := &stagedPush{status: http.StatusCreated}
		srv := staged.server(t)
		s, sender := senderWith(t, srv.URL+"/push/token")
		s = s.WithHTTPClient(srv.Client())

		d := decision()
		d.Kind = kind
		if _, err := s.Deliver(context.Background(), sender, d, "mailto:ops@example.com"); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if got := staged.got.Header.Get("Urgency"); got != want {
			t.Errorf("%s → Urgency %q, want %q", kind, got, want)
		}
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("fixture %q: %v", s, err)
	}
	return b
}

// decryptAsUserAgentHere is the receiver side, implemented locally.
//
// NOT EXPORTED FROM `push`, deliberately: a decrypt path in that package's public surface would be a
// capability sitting in production code to save a test twenty lines. `push`'s own copy lives in its
// test files and is therefore not importable, which is the correct consequence of it being a helper
// rather than API.
func decryptAsUserAgentHere(t *testing.T, uaPriv *ecdh.PrivateKey, authSecret, body []byte) ([]byte, error) {
	t.Helper()
	if len(body) < 21 {
		return nil, errShort
	}
	salt, idlen := body[:16], int(body[20])
	asPublicRaw := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]

	asPublic, err := ecdh.P256().NewPublicKey(asPublicRaw)
	if err != nil {
		return nil, err
	}
	shared, err := uaPriv.ECDH(asPublic)
	if err != nil {
		return nil, err
	}
	// RFC 8291 §3.4: the receiver's own public key first, then the sender's. `key_info` is
	// asymmetric, and swapping these is how two correct implementations disagree.
	keyInfo := append([]byte("WebPush: info\x00"), uaPriv.PublicKey().Bytes()...)
	keyInfo = append(keyInfo, asPublicRaw...)
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	record, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return record[:len(record)-1], nil // strip the padding delimiter
}

var errShort = errors.New("body is shorter than an aes128gcm header")

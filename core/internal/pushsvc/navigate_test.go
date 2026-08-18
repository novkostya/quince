package pushsvc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// `navigate` MUST BE ABSOLUTE, AND THIS IS THE MOST EXPENSIVE BUG THIS RUNG HAS PRODUCED.
//
// Declarative Web Push requires an absolute URL. A payload that fails validation is dropped by the
// user agent with NOTHING displayed and NOTHING reported — and the push service has already answered
// 201, so every layer quince can see reports success. Measured on an iPhone, 2026-08-18: Apple
// accepted the delivery, the screen said "Sent", and no notification ever appeared.
//
// quince sent `/devices/<udid>`. Every notification it had ever sent was silently discarded.
func TestNavigateIsAbsoluteAgainstTheSubscriptionsOwnOrigin(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, _ := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	if _, err := s.SendTest(context.Background()); err != nil {
		t.Fatalf("send test: %v", err)
	}
	nav := navigateOf(t, s, staged)
	if !strings.HasPrefix(nav, testOrigin+"/") && nav != testOrigin {
		t.Errorf("navigate = %q, want it absolute against %q — Safari drops a relative one in silence",
			nav, testOrigin)
	}
}

// EACH DEVICE GETS THE ADDRESS IT KNOWS. One quince is commonly reached by a LAN IP, a Tailscale
// name and a domain at once; a single configured base URL would send two of those three phones to a
// URL their browser cannot resolve, and the tap is the only part of a notification that does
// anything.
func TestTwoDevicesGetTheirOwnOrigins(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, raw := svc(t)
	s = s.WithHTTPClient(srv.Client())
	if _, err := s.VAPIDPublicKey(); err != nil {
		t.Fatalf("key: %v", err)
	}
	if _, err := s.Subscribe(srv.URL+"/push/a", rfcP256DH, rfcAuth, "phone", "https://a.example"); err != nil {
		t.Fatalf("subscribe a: %v", err)
	}
	if _, err := s.Subscribe(srv.URL+"/push/b", rfcP256DH, rfcAuth, "tablet", "http://10.0.0.4:8080"); err != nil {
		t.Fatalf("subscribe b: %v", err)
	}

	rows, err := raw.PushSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Label] = r.Origin
	}
	if got["phone"] != "https://a.example" || got["tablet"] != "http://10.0.0.4:8080" {
		t.Errorf("origins did not survive per device: %+v", got)
	}
}

// A ROW WITH NO ORIGIN IS REFUSED, NOT GUESSED AT. Rows predating migration 0012 have none, and
// inventing one produces a notification whose tap lands somewhere that phone cannot open — which is
// worse than not sending, because it looks like it worked. The error names the remedy.
//
// AND IT IS NOT AN EXPIRY: the device is alive and will be reachable the moment it re-subscribes, so
// marking it dead would put it in the "stopped receiving" list for a fault that is quince's.
func TestASubscriptionWithNoOriginIsRefusedRatherThanGuessedAt(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, raw := svc(t)
	s = s.WithHTTPClient(srv.Client())
	if _, err := s.VAPIDPublicKey(); err != nil {
		t.Fatalf("key: %v", err)
	}
	// Written through the store directly: the service will not create one without an origin, and
	// this is the shape a pre-migration row has.
	if err := raw.AddPushSubscription(store.PushSubscription{
		ID: "old", Endpoint: srv.URL + "/push/old", P256DH: rfcP256DH, Auth: rfcAuth,
		Label: "an old iPhone", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	results, err := s.SendTest(context.Background())
	if err != nil {
		t.Fatalf("send test: %v", err)
	}
	if len(results) != 1 || results[0].State != "error" {
		t.Fatalf("a row with no origin was not reported as an error: %+v", results)
	}
	if !strings.Contains(results[0].Error, "off and on again") {
		t.Errorf("the error does not name the remedy: %q", results[0].Error)
	}
	// STILL LIVE. The next thing that device does is re-subscribe, and an expired row would have it
	// listed as broken in the meantime.
	rows, _ := raw.PushSubscriptions()
	if len(rows) != 1 || !rows[0].Live() {
		t.Errorf("a missing origin expired the subscription: %+v", rows)
	}
}

// AN ALREADY-ABSOLUTE PATH IS NOT MANGLED, so a future caller holding a full URL keeps it.
func TestAnAbsoluteNavigatePassesThrough(t *testing.T) {
	for _, tc := range []struct{ path, origin, want string }{
		{"https://elsewhere.example/x", testOrigin, "https://elsewhere.example/x"},
		{"/devices/U1", "https://q.example", "https://q.example/devices/U1"},
		{"devices/U1", "https://q.example", "https://q.example/devices/U1"},
		{"/devices/U1", "https://q.example/", "https://q.example/devices/U1"},
	} {
		got, err := absoluteNavigate(tc.path, tc.origin)
		if err != nil {
			t.Fatalf("%q against %q: %v", tc.path, tc.origin, err)
		}
		if got != tc.want {
			t.Errorf("%q against %q = %q, want %q", tc.path, tc.origin, got, tc.want)
		}
	}
}

// navigateOf decrypts nothing — it reads what the service built, by rebuilding it the same way. The
// wire body is encrypted, and `send_test.go` already proves the round trip decrypts; what is under
// test here is the URL, so it is read where it is decided.
func navigateOf(t *testing.T, s *Service, _ *stagedPush) string {
	t.Helper()
	payload, err := payloadFor(decision(), testOrigin)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	var env struct {
		Notification struct {
			Navigate string `json:"navigate"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return env.Notification.Navigate
}

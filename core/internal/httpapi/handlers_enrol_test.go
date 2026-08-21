package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
)

// THE ENROLMENT ROUTES (qn.13 slice 9b-3, spec D4).
//
// WHAT THESE PROVE AND WHAT THEY CANNOT. There is no synthetic authenticator, so no test here
// completes a registration — the same declared limit as slice 9b-2. What is testable is every
// refusal, the pre-auth reachability, and the ONE thing a route can get wrong that the auth layer
// cannot: collapsing four distinguishable causes into one status.

func enrolDeps(t *testing.T) (Deps, *auth.Enrolments) {
	t.Helper()
	d := testDeps(t)
	d.Passkeys = auth.NewPasskeyCeremonies()
	d.Enrolments = auth.NewEnrolments()
	if err := d.Store.UpsertDeviceIdentity(store.DeviceIdentityRow{
		UDID: "DEVICE-A", Name: "Household iPhone", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return d, d.Enrolments
}

func enrolServer(t *testing.T) (*httptest.Server, *auth.Enrolments) {
	t.Helper()
	d, enr := enrolDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	t.Cleanup(srv.Close)
	return srv, enr
}

func postEnrol(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+path, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body.Error.Code
}

// PRE-AUTH, AND THAT IS THE POINT. A scanner has no session; if these were behind `authGuard` the
// whole ceremony would be unreachable by the only person who ever performs it.
//
// THE CONTROL IS THE STATUS CODE ITSELF: a 401 would mean the exemption is missing. Anything else
// means the request reached the handler, which is what this asserts.
func TestTheEnrolmentRoutesAreReachableWithoutASession(t *testing.T) {
	srv, enr := enrolServer(t)
	tok, _, err := enr.Mint(store.DeviceScope("DEVICE-A"))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	for _, path := range []string{
		"/api/enrol/passkey/begin?secret=" + tok,
		"/api/enrol/passkey/finish?secret=" + tok + "&ceremony=x&name=phone",
	} {
		status, code := postEnrol(t, srv, path)
		if status == http.StatusUnauthorized {
			t.Fatalf("%s answered 401 — the route is behind authGuard and no scanner can reach it", path)
		}
		if status == http.StatusNotFound && code == "" {
			t.Fatalf("%s is not registered at all", path)
		}
	}
}

// THE FOUR CAUSES STAY FOUR, at the wire (quince#940, and `Enrolments`' own argument).
//
// This is the assertion the route layer owes that the auth layer cannot make: an error set that is
// distinct in Go proves nothing if the handler maps every member to one status and one sentence.
func TestTheFourEnrolmentRefusalsStayDistinctAtTheWire(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kill       func(t *testing.T, enr *auth.Enrolments, tok string, en auth.Enrolment)
		present    func(tok string) string
		wantStatus int
		wantCode   string
	}{
		{"unknown", func(*testing.T, *auth.Enrolments, string, auth.Enrolment) {},
			func(string) string { return "not-a-real-secret" }, http.StatusNotFound, "enrolment_unknown"},
		{"spent", func(t *testing.T, enr *auth.Enrolments, tok string, _ auth.Enrolment) {
			if _, err := enr.Spend(tok); err != nil {
				t.Fatalf("Spend: %v", err)
			}
		}, func(tok string) string { return tok }, http.StatusGone, "enrolment_spent"},
		{"revoked", func(t *testing.T, enr *auth.Enrolments, _ string, en auth.Enrolment) {
			if err := enr.Revoke(en.ID); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
		}, func(tok string) string { return tok }, http.StatusGone, "enrolment_revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, enr := enrolServer(t)
			tok, en, err := enr.Mint(store.DeviceScope("DEVICE-A"))
			if err != nil {
				t.Fatalf("Mint: %v", err)
			}
			// THE CONTROL: live, this same request does NOT produce one of these refusals. Without
			// it, a handler that answered `enrolment_unknown` to everything would pass every row.
			if _, code := postEnrol(t, srv, "/api/enrol/passkey/begin?secret="+tok); strings.HasPrefix(code, "enrolment_") {
				t.Fatalf("control: a live secret was refused as %q", code)
			}
			tc.kill(t, enr, tok, en)

			status, code := postEnrol(t, srv, "/api/enrol/passkey/begin?secret="+tc.present(tok))
			if status != tc.wantStatus || code != tc.wantCode {
				t.Fatalf("got %d/%q, want %d/%q — the four causes must not collapse",
					status, code, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// A MISSING SECRET IS ITS OWN ANSWER, and not one of the four. Somebody who opened the enrolment
// page without the code has a different problem from somebody whose code expired.
func TestAnEnrolmentRequestWithNoSecretSaysSo(t *testing.T) {
	srv, enr := enrolServer(t)
	tok, _, err := enr.Mint(store.DeviceScope("DEVICE-A"))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, code := postEnrol(t, srv, "/api/enrol/passkey/begin?secret="+tok); strings.HasPrefix(code, "enrolment_") {
		t.Fatalf("control: a live secret was refused as %q", code)
	}
	status, code := postEnrol(t, srv, "/api/enrol/passkey/begin")
	if status != http.StatusBadRequest || code != "bad_request" {
		t.Fatalf("got %d/%q, want 400/bad_request", status, code)
	}
}

// THE ROUTES ARE NOT REGISTERED WITHOUT A SECRET STORE. A router that lacks the thing that
// authorizes them must not serve them at all — the same shape as the `Passkeys` guard around them.
func TestTheEnrolmentRoutesAreAbsentWithoutAnEnrolmentStore(t *testing.T) {
	d, _ := enrolDeps(t)
	d.Enrolments = nil
	srv := httptest.NewServer(NewRouter(d))
	t.Cleanup(srv.Close)

	status, _ := postEnrol(t, srv, "/api/enrol/passkey/begin?secret=anything")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — a router with no secret store must not serve the ceremony", status)
	}
}

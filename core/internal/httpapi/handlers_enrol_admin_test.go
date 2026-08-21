package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// THE ADMIN'S ENROLMENT SURFACE (qn.13 slice 9c, spec D4 and D9).

type stubDevices struct{ list []wire.Device }

func (s stubDevices) Devices() []wire.Device { return s.list }

func (s stubDevices) Device(udid string) (wire.Device, bool) {
	for _, d := range s.list {
		if d.UDID == udid {
			return d, true
		}
	}
	return wire.Device{}, false
}

func deleteCSRF(t *testing.T, c *http.Client, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	req := newReq(t, http.MethodDelete, srv.URL+path, "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func bodyString(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func adminEnrolServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	d, _ := enrolDeps(t)
	d.Devices = stubDevices{list: []wire.Device{{UDID: "DEVICE-A", Name: "Household iPhone"}}}
	srv := httptest.NewServer(NewRouter(d))
	t.Cleanup(srv.Close)
	return srv, authedClient(t, srv)
}

func mintViaAPI(t *testing.T, c *http.Client, srv *httptest.Server) wire.EnrolmentIssued {
	t.Helper()
	resp := postCSRF(t, c, srv, "/api/devices/DEVICE-A/enrolments", "{}")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint: status = %d, want 201", resp.StatusCode)
	}
	var out wire.EnrolmentIssued
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// THE SECRET IS RETURNED ONCE AND NEVER LISTED. This is the contract, and a listing that could
// re-display it would make every GET of the device page a fresh chance to leak a live credential.
func TestTheSecretIsReturnedOnceAndTheListingCarriesNone(t *testing.T) {
	srv, c := adminEnrolServer(t)
	issued := mintViaAPI(t, c, srv)

	if issued.Secret == "" {
		t.Fatal("the mint returned no secret — the one call that must")
	}
	if issued.ID == "" || issued.UDID != "DEVICE-A" {
		t.Fatalf("issued = %+v, want an id and this device", issued)
	}

	resp, err := c.Get(srv.URL + "/api/devices/DEVICE-A/enrolments")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := bodyString(t, resp.Body)

	if !strings.Contains(body, issued.ID) {
		t.Fatalf("the listing does not contain the secret's id — the admin cannot revoke it: %s", body)
	}
	// THE ASSERTION THAT MATTERS: the SECRET's value must appear nowhere in it.
	if strings.Contains(body, issued.Secret) {
		t.Fatal("the listing carried the secret value — it is returned once, by the mint, and never again")
	}
}

// ADMIN ONLY, AND ASSERTED AT THE WIRE RATHER THAN BY READING THE TABLE (spec D3).
//
// A scoped holder who could mint a secret for their own device could hand out further credentials
// to it, which is delegation quince never granted. The route sits under `/api/devices/{udid}/`,
// where its neighbours are `scopedOwnDevice` — so the prefix is exactly what would make somebody
// classify it wrongly, and this is what catches that.
func TestTheEnrolmentSurfaceIsAdminOnly(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/devices/DEVICE-A/enrolments"},
		{http.MethodGet, "/api/devices/DEVICE-A/enrolments"},
		{http.MethodDelete, "/api/devices/DEVICE-A/enrolments/01ANY"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			if got := routeScope[tc.method+" "+tc.path[:strings.LastIndex(tc.path, "/")]]; got == scopedOwnDevice {
				t.Fatalf("%s is scopedOwnDevice — a scoped holder could issue credentials to their own device", tc.path)
			}
		})
	}
	// AND THE TABLE ITSELF, BY EXACT PATTERN — the loop above uses a truncated path, so this is
	// what actually pins the three entries.
	for _, pattern := range []string{
		"POST /api/devices/{udid}/enrolments",
		"GET /api/devices/{udid}/enrolments",
		"DELETE /api/devices/{udid}/enrolments/{id}",
	} {
		if routeScope[pattern] != adminOnly {
			t.Fatalf("%q is %v, want adminOnly — issuing credentials is not a scoped holder's (D3)",
				pattern, routeScope[pattern])
		}
	}
}

// A DEVICE QUINCE DOES NOT KNOW GETS A 404, NOT A SECRET. Minting for an unknown udid produces a
// live credential-issuing token confined to nothing quince can name, and the ceremony would then
// fail after a human had already scanned it.
func TestMintingForAnUnknownDeviceIsRefused(t *testing.T) {
	srv, c := adminEnrolServer(t)

	// The control: the known device mints.
	_ = mintViaAPI(t, c, srv)

	resp := postCSRF(t, c, srv, "/api/devices/NO-SUCH-DEVICE/enrolments", "{}")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// REVOCATION'S THREE ANSWERS STAY THREE. Cancelling an already-USED secret is not a tidy no-op: the
// credential it minted exists, and "cancelled" would be the opposite of the truth at the moment the
// admin most needs it. The message names the real remedy instead.
func TestRevokingSaysWhichCaseItWas(t *testing.T) {
	srv, c := adminEnrolServer(t)
	issued := mintViaAPI(t, c, srv)

	// The control: a live one cancels, 204.
	resp := deleteCSRF(t, c, srv, "/api/devices/DEVICE-A/enrolments/"+issued.ID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancelling a live secret: status = %d, want 204", resp.StatusCode)
	}

	// Cancelling it again is not another 204.
	resp2 := deleteCSRF(t, c, srv, "/api/devices/DEVICE-A/enrolments/"+issued.ID)
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("cancelling twice: status = %d, want 409", resp2.StatusCode)
	}

	// And an id that names nothing is a 404, which is a different problem from a 409.
	resp3 := deleteCSRF(t, c, srv, "/api/devices/DEVICE-A/enrolments/01NOTHING")
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("cancelling an unknown id: status = %d, want 404", resp3.StatusCode)
	}
}

// A REVOKED SECRET LEAVES THE LISTING, because the listing answers *what authority is outstanding*
// and a cancelled link is not authority.
func TestACancelledSecretLeavesTheListing(t *testing.T) {
	srv, c := adminEnrolServer(t)
	keep := mintViaAPI(t, c, srv)
	drop := mintViaAPI(t, c, srv)

	resp := deleteCSRF(t, c, srv, "/api/devices/DEVICE-A/enrolments/"+drop.ID)
	_ = resp.Body.Close()

	list, err := c.Get(srv.URL + "/api/devices/DEVICE-A/enrolments")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = list.Body.Close() }()
	body := bodyString(t, list.Body)

	if !strings.Contains(body, keep.ID) {
		t.Fatalf("the surviving secret left the listing too: %s", body)
	}
	if strings.Contains(body, drop.ID) {
		t.Fatalf("a cancelled secret is still listed as outstanding: %s", body)
	}
}

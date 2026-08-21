package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// stubDeviceNotifs is a DeviceNotifications whose outcome the test fixes, recording what reached it
// so the handler can be proved to pass the value through rather than to have coincidentally agreed.
type stubDeviceNotifs struct {
	// `stored` is what this stub reports as written. It DEFAULTS TO THE REQUEST rather than
	// being fixed at a constant, so the ordinary tests read naturally — and a test that wants
	// to prove the handler echoes the STORED value sets it to disagree with what it sends.
	stored *bool
	status int
	reason string

	called    bool
	recUDID   string
	recEnable bool
}

func (s *stubDeviceNotifs) SetNotificationsEnabled(udid, owner string, enabled bool) (bool, int, string) {
	s.called, s.recUDID, s.recEnable = true, udid, enabled
	if s.stored != nil {
		return *s.stored, s.status, s.reason
	}
	return enabled, s.status, s.reason
}

func notifsServer(t *testing.T, n DeviceNotifications) (*httptest.Server, *http.Client) {
	t.Helper()
	deps := testDeps(t)
	deps.DeviceNotifs = n
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	return srv, authedClient(t, srv)
}

func putCSRF(t *testing.T, c *http.Client, srv *httptest.Server, path, body string) *http.Response {
	t.Helper()
	req := newReq(t, http.MethodPut, srv.URL+path, body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// The value in the body reaches the subsystem, and the response echoes what was stored — in BOTH
// directions, because a handler that hard-coded either one would pass a single-direction test.
func TestDeviceNotificationsPassesTheValueAndEchoesIt(t *testing.T) {
	for _, want := range []bool{false, true} {
		n := &stubDeviceNotifs{status: http.StatusOK}
		srv, c := notifsServer(t, n)
		body := `{"enabled":false}`
		if want {
			body = `{"enabled":true}`
		}
		resp := putCSRF(t, c, srv, "/api/devices/DEV-1/notifications", body)
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if n.recUDID != "DEV-1" {
			t.Errorf("udid reaching the subsystem = %q, want DEV-1", n.recUDID)
		}
		if n.recEnable != want {
			t.Errorf("enabled reaching the subsystem = %v, want %v", n.recEnable, want)
		}
		if got.Enabled != want {
			t.Errorf("echoed enabled = %v, want %v", got.Enabled, want)
		}
	}
}

// AN OMITTED `enabled` IS 422, AND NOTHING IS WRITTEN.
//
// This is the assertion the whole pointer-vs-bool decision rests on: `{}` decodes to Go's `false`,
// so a handler taking a plain bool would answer 200 and MUTE the device. The second half — that the
// subsystem was never called — is what distinguishes "refused" from "refused after doing it".
func TestDeviceNotificationsWithoutEnabledIs422AndWritesNothing(t *testing.T) {
	// `{}` only. `{"other":true}` is a 400 from decodeJSON's DisallowUnknownFields, one layer
	// earlier and for a different reason, so asserting it here would test that guard rather
	// than this one.
	for _, body := range []string{`{}`} {
		n := &stubDeviceNotifs{status: http.StatusOK}
		srv, c := notifsServer(t, n)
		resp := putCSRF(t, c, srv, "/api/devices/DEV-1/notifications", body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("body %s → status %d, want 422", body, resp.StatusCode)
		}
		if n.called {
			t.Fatalf("body %s was refused AFTER reaching the subsystem; nothing may be written", body)
		}
	}
}

// An unknown UDID is a 404 the subsystem decides, mapped rather than swallowed.
func TestDeviceNotificationsUnknownDeviceIs404(t *testing.T) {
	n := &stubDeviceNotifs{status: http.StatusNotFound, reason: "no such device"}
	srv, c := notifsServer(t, n)
	resp := putCSRF(t, c, srv, "/api/devices/NOPE/notifications", `{"enabled":false}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// A router with nothing wired refuses with 503 rather than accepting a write nobody performs.
//
// The nil substitution in NewRouter is what makes this true, and it is the same guard `Ops` has:
// a silent no-op here is a device the user believes they muted and did not.
func TestDeviceNotificationsUnwiredIs503(t *testing.T) {
	deps := testDeps(t)
	deps.DeviceNotifs = nil
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)
	resp := putCSRF(t, c, srv, "/api/devices/DEV-1/notifications", `{"enabled":false}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// THE BODY ECHOES WHAT WAS STORED, NOT WHAT WAS ASKED FOR — and this is the only test that can
// tell the two apart (quince#1281 review).
//
// Every other assertion here sends `enabled` and gets `enabled` back, which passes whether the
// handler echoes the request or the write's return value. Those two agree today because the store
// writes the bool it is given; they agree by ACCIDENT, and a storage policy that ever refused or
// coerced would leave the handler echoing the request while the frozen contract said otherwise.
//
// So the stub is made to disagree with its own input. There is no such storage policy today and
// this arrangement cannot occur in production — that is the point: the test pins the SHAPE of the
// guarantee, not a behaviour anything currently exhibits.
func TestDeviceNotificationsEchoesTheStoredValueNotTheRequest(t *testing.T) {
	stored := true
	n := &stubDeviceNotifs{status: http.StatusOK, stored: &stored}
	srv, c := notifsServer(t, n)

	resp := putCSRF(t, c, srv, "/api/devices/DEV-1/notifications", `{"enabled":false}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if n.recEnable {
		// Guard on the guard: if the request never carried `false`, the assertion below proves
		// nothing about which of the two values was echoed.
		t.Fatalf("the request did not reach the subsystem as false; this test would pass vacuously")
	}
	if !got.Enabled {
		t.Fatalf("echoed enabled=false — the handler echoed the REQUEST. It must echo what the "+
			"write reported storing, which this stub set to %v", stored)
	}
}

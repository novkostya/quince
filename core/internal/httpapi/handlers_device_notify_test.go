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
	status int
	reason string

	called    bool
	recUDID   string
	recEnable bool
}

func (s *stubDeviceNotifs) SetNotificationsEnabled(udid string, enabled bool) (int, string) {
	s.called, s.recUDID, s.recEnable = true, udid, enabled
	return s.status, s.reason
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

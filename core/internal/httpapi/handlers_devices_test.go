package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// stubMuxer is a MuxerControl whose Rescan outcome the test fixes.
type stubMuxer struct {
	accepted bool
	reason   string
}

func (s stubMuxer) Rescan(context.Context) (bool, string) { return s.accepted, s.reason }
func (stubMuxer) MuxersHealth() []MuxerHealth {
	return []MuxerHealth{{Name: "usbmuxd", Role: "usb", Managed: true, State: "running", Rescan: true}}
}

// TestRescanManagedReturns202: with a managed muxer, POST /api/devices/rescan is accepted (202).
func TestRescanManagedReturns202(t *testing.T) {
	deps := testDeps(t)
	deps.Muxer = stubMuxer{accepted: true}
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	req := newReq(t, http.MethodPost, srv.URL+"/api/devices/rescan", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, req); code != http.StatusAccepted {
		t.Fatalf("managed rescan = %d, want 202", code)
	}
}

// TestRescanUnmanagedReturns409: the default (external/--demo) muxer refuses rescan with 409,
// and the endpoint is CSRF-guarded like every mutation.
func TestRescanUnmanagedReturns409(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t))) // Muxer nil → UnmanagedMuxer
	defer srv.Close()
	c := authedClient(t, srv)

	// Without the CSRF header → 403 (proves the endpoint is on the mutating chain).
	req := newReq(t, http.MethodPost, srv.URL+"/api/devices/rescan", "")
	if code := doStatus(t, c, req); code != http.StatusForbidden {
		t.Fatalf("rescan without CSRF = %d, want 403", code)
	}

	// With CSRF, an unmanaged muxer → 409.
	req = newReq(t, http.MethodPost, srv.URL+"/api/devices/rescan", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, req); code != http.StatusConflict {
		t.Fatalf("unmanaged rescan = %d, want 409", code)
	}
}

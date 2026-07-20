package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// stubVersionAdmin fixes the delete outcome and records the id it received.
type stubVersionAdmin struct {
	status int
	lastID string
}

func (s *stubVersionAdmin) Delete(id string) (int, error) {
	s.lastID = id
	return s.status, nil
}

func TestVersionDelete(t *testing.T) {
	cases := map[string]int{
		"accepted":    http.StatusAccepted,
		"not found":   http.StatusNotFound,
		"unavailable": http.StatusServiceUnavailable,
	}
	for name, status := range cases {
		t.Run(name, func(t *testing.T) {
			deps := testDeps(t)
			admin := &stubVersionAdmin{status: status}
			deps.VersionAdmin = admin
			srv := httptest.NewServer(NewRouter(deps))
			t.Cleanup(srv.Close)
			c := authedClient(t, srv)

			req := newReq(t, http.MethodDelete, srv.URL+"/api/versions/01ABC", "")
			req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
			resp, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != status {
				t.Fatalf("%s: status = %d, want %d", name, resp.StatusCode, status)
			}
			if admin.lastID != "01ABC" {
				t.Fatalf("handler passed id %q, want 01ABC", admin.lastID)
			}
		})
	}
}

// TestVersionDeleteUnavailableByDefault proves the default stub (no storage wired) refuses 503.
func TestVersionDeleteUnavailableByDefault(t *testing.T) {
	deps := testDeps(t) // VersionAdmin left nil → NewRouter installs UnavailableVersionAdmin
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	c := authedClient(t, srv)
	req := newReq(t, http.MethodDelete, srv.URL+"/api/versions/x", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/demo"
	"github.com/novkostya/quince/core/internal/store"
)

func testDeps(t *testing.T) Deps {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfgSvc := config.NewService(filepath.Join(t.TempDir(), "config.yml"), log)
	b := bus.New()
	prov := demo.NewProvider(b, log)
	return Deps{
		Log:      log,
		Version:  "test-1.2.3",
		Config:   cfgSvc,
		Auth:     auth.NewService(st, log),
		Bus:      b,
		Devices:  prov,
		Jobs:     prov,
		Versions: prov,
	}
}

func TestHealthReturnsOKAndVersion(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	var got HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" || got.Version != "test-1.2.3" {
		t.Fatalf("body = %+v", got)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("missing CSP frame-ancestors")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing X-Frame-Options")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing nosniff")
	}
}

func TestUnknownAPIRouteIs404(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv) // auth runs before routing, so an unknown path needs a session to reach the 404
	resp, err := c.Get(srv.URL + "/api/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not the SPA fallback)", resp.StatusCode)
	}
}

func TestUnauthenticatedReadRejected(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// authedClient runs the full first-run flow and returns a cookie-jar client that is logged
// in, plus the server. Setup is CSRF-exempt, so no header is needed for it.
func authedClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	// status → needs_setup + a csrf cookie
	statusResp, err := c.Get(srv.URL + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	_ = statusResp.Body.Close()

	setupResp, err := c.Post(srv.URL+"/api/auth/setup", "application/json", strings.NewReader(`{"password":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = setupResp.Body.Close() }()
	if setupResp.StatusCode != http.StatusOK {
		t.Fatalf("setup status = %d", setupResp.StatusCode)
	}
	// session + csrf cookies must be present + flagged
	var sawSession bool
	for _, ck := range setupResp.Cookies() {
		if ck.Name == auth.SessionCookieName {
			sawSession = true
			if !ck.HttpOnly {
				t.Error("session cookie not HttpOnly")
			}
			if ck.SameSite != http.SameSiteStrictMode {
				t.Error("session cookie not SameSite=Strict")
			}
		}
	}
	if !sawSession {
		t.Fatal("no session cookie after setup")
	}
	return c
}

func csrfFromJar(t *testing.T, c *http.Client, srv *httptest.Server) string {
	t.Helper()
	for _, ck := range c.Jar.Cookies(mustURL(t, srv.URL)) {
		if ck.Name == auth.CSRFCookieName {
			return ck.Value
		}
	}
	t.Fatal("no csrf cookie in jar")
	return ""
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func newReq(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func doStatus(t *testing.T, c *http.Client, req *http.Request) int {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

func TestSetupIsOnceThen409(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	resp, err := c.Post(srv.URL+"/api/auth/setup", "application/json", strings.NewReader(`{"password":"other"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second setup status = %d, want 409", resp.StatusCode)
	}
}

func TestCSRFRequiredOnMutations(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	body := `{"backup":{"transport":"usb","require_encryption":true},` +
		`"storage":{"backend":"auto","zfs":{"parent_dataset":"","mode":"exec","hook_cmd":"","mirror":"auto"},` +
		`"retention":{"keep_recent":10,"keep_daily":30,"keep_weekly":12}},` +
		`"devices":{"usbmuxd_socket":"/var/run/usbmuxd","netmuxd_addr":"127.0.0.1:27015"},` +
		`"sessions":{"ttl_minutes":30},"automation":{"staleness_days":3,"reminder_cooldown_hours":24},` +
		`"ui":{"theme":"system"}}`

	// Without CSRF header → 403.
	req := newReq(t, http.MethodPut, srv.URL+"/api/config", body)
	if code := doStatus(t, c, req); code != http.StatusForbidden {
		t.Fatalf("PUT without CSRF = %d, want 403", code)
	}

	// With CSRF header → 200.
	req = newReq(t, http.MethodPut, srv.URL+"/api/config", body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, req); code != http.StatusOK {
		t.Fatalf("PUT with CSRF = %d, want 200", code)
	}
}

// TestReadEndpointsMatchGolden is the story-3 gate: the wire shapes must match the frozen
// golden fixtures. Regenerate with UPDATE_GOLDEN=1 after an intentional contract change,
// then eyeball the diff against contracts.md.
func TestReadEndpointsMatchGolden(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	for _, tc := range []struct{ name, path string }{
		{"devices", "/api/devices"},
		{"jobs", "/api/jobs"},
		{"versions", "/api/versions"},
	} {
		resp, err := c.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			t.Fatalf("%s: not JSON: %v", tc.name, err)
		}
		got := append(pretty.Bytes(), '\n')

		golden := filepath.Join("testdata", tc.name+".json")
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("read golden %s (run with UPDATE_GOLDEN=1 to create): %v", golden, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s wire shape drift:\n--- want ---\n%s\n--- got ---\n%s", tc.name, want, got)
		}
	}
}

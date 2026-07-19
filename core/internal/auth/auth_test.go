package auth

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// newTestAuth builds a Service with cheap argon params, a small limiter, and an injectable
// clock. The returned *time.Time lets tests advance time.
func newTestAuth(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	clock := time.Now().UTC()
	svc := NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.now = func() time.Time { return clock }
	svc.params = argonParams{memory: 8, iterations: 1, parallelism: 1, saltLen: 8, keyLen: 16}
	svc.limiter = newLoginLimiter(3, time.Minute)
	return svc, &clock
}

func TestArgon2RoundTrip(t *testing.T) {
	h, err := hashPassword("test", argonParams{memory: 8, iterations: 1, parallelism: 1, saltLen: 8, keyLen: 16})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := verifyPassword("test", h); err != nil || !ok {
		t.Fatalf("verify correct: ok=%v err=%v", ok, err)
	}
	if ok, _ := verifyPassword("wrong", h); ok {
		t.Fatal("verify wrong password returned true")
	}
}

func TestSetPasswordThenLoginRotates(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	s1, csrf, err := svc.Login("test", "10.0.0.1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if csrf == "" {
		t.Fatal("empty csrf token")
	}
	if _, err := svc.Authenticate(s1.ID); err != nil {
		t.Fatalf("authenticate s1: %v", err)
	}
	s2, _, err := svc.Login("test", "10.0.0.1")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if s2.ID == s1.ID {
		t.Fatal("session id not rotated")
	}
	if _, err := svc.Authenticate(s1.ID); err == nil {
		t.Fatal("old session still valid after rotation")
	}
}

func TestLoginWritesAuditRow(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Login("test", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.store.ListAudit(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Event == "login" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no login audit row written; got %+v", rows)
	}
}

func TestSetPasswordTwiceIs409(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPassword("other"); err != ErrAlreadyConfigured {
		t.Fatalf("want ErrAlreadyConfigured, got %v", err)
	}
}

func TestLoginBadPassword(t *testing.T) {
	svc, _ := newTestAuth(t)
	_ = svc.SetPassword("test")
	if _, _, err := svc.Login("nope", "10.0.0.1"); err != ErrBadPassword {
		t.Fatalf("want ErrBadPassword, got %v", err)
	}
}

func TestLoginRateLimited(t *testing.T) {
	svc, _ := newTestAuth(t) // limiter max = 3
	_ = svc.SetPassword("test")
	for i := 0; i < 3; i++ {
		if _, _, err := svc.Login("wrong", "1.2.3.4"); err != ErrBadPassword {
			t.Fatalf("attempt %d: want ErrBadPassword, got %v", i, err)
		}
	}
	if _, _, err := svc.Login("wrong", "1.2.3.4"); err != ErrRateLimited {
		t.Fatalf("4th attempt: want ErrRateLimited, got %v", err)
	}
}

func TestStatusTriState(t *testing.T) {
	svc, _ := newTestAuth(t)
	if st, _ := svc.Status(""); st != StateNeedsSetup {
		t.Fatalf("want needs_setup, got %q", st)
	}
	_ = svc.SetPassword("test")
	if st, _ := svc.Status(""); st != StateNeedsLogin {
		t.Fatalf("want needs_login, got %q", st)
	}
	sess, _, _ := svc.Login("test", "10.0.0.1")
	if st, _ := svc.Status(sess.ID); st != StateAuthenticated {
		t.Fatalf("want authenticated, got %q", st)
	}
	if st, _ := svc.Status("bogus"); st != StateNeedsLogin {
		t.Fatalf("bogus session: want needs_login, got %q", st)
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	svc, clock := newTestAuth(t)
	svc.idleTimeout = 30 * time.Minute
	_ = svc.SetPassword("test")
	sess, _, _ := svc.Login("test", "10.0.0.1")
	*clock = clock.Add(31 * time.Minute)
	if _, err := svc.Authenticate(sess.ID); err != ErrSessionExpired {
		t.Fatalf("want ErrSessionExpired, got %v", err)
	}
}

func TestCheckCSRF(t *testing.T) {
	mk := func(cookie, header string) *http.Request {
		r := httptest.NewRequest("POST", "http://localhost/api/config", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: cookie})
		}
		if header != "" {
			r.Header.Set(CSRFHeaderName, header)
		}
		return r
	}
	if !CheckCSRF(mk("tok", "tok")) {
		t.Error("matching token should pass")
	}
	if CheckCSRF(mk("tok", "other")) {
		t.Error("mismatched token should fail")
	}
	if CheckCSRF(mk("", "tok")) || CheckCSRF(mk("tok", "")) {
		t.Error("missing cookie or header should fail")
	}
}

func TestSecureCookieRule(t *testing.T) {
	loopback := httptest.NewRequest("GET", "http://localhost:8080/api/health", nil)
	if secureCookie(loopback) {
		t.Error("loopback http should not be Secure")
	}
	lan := httptest.NewRequest("GET", "http://10.20.30.40/api/health", nil)
	if !secureCookie(lan) {
		t.Error("non-loopback http should be Secure")
	}
	tlsReq := httptest.NewRequest("GET", "http://localhost/api/health", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	if !secureCookie(tlsReq) {
		t.Error("TLS should be Secure")
	}
	proxied := httptest.NewRequest("GET", "http://localhost/api/health", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if !secureCookie(proxied) {
		t.Error("X-Forwarded-Proto https should be Secure")
	}
}

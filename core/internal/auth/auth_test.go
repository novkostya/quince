package auth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// TestVerifyPasswordRejectsEmptyKey guards against a fail-open: a corrupt/truncated hash
// with an empty key field must never accept a password (ConstantTimeCompare([], []) == 1).
func TestVerifyPasswordRejectsEmptyKey(t *testing.T) {
	good, err := hashPassword("test", argonParams{memory: 8, iterations: 1, parallelism: 1, saltLen: 8, keyLen: 16})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(good, "$") // ["","argon2id","v=19","m..,t..,p..",salt,key]
	parts[5] = ""                     // empty key
	bad := strings.Join(parts, "$")
	if ok, err := verifyPassword("test", bad); ok || err == nil {
		t.Fatalf("empty-key hash: ok=%v err=%v, want (false, error)", ok, err)
	}
	if ok, _ := verifyPassword("", bad); ok {
		t.Fatal("empty password must not match an empty-key hash")
	}
}

// TestLoginLimiterSweepsStaleBuckets proves the per-IP bucket map does not grow unbounded:
// a bucket for an IP that went quiet is evicted once the sweep window elapses.
func TestLoginLimiterSweepsStaleBuckets(t *testing.T) {
	l := newLoginLimiter(3, time.Minute)
	base := time.Now().UTC()
	l.allow("1.1.1.1", base)
	if len(l.buckets) != 1 {
		t.Fatalf("bucket count = %d, want 1", len(l.buckets))
	}
	// A later attempt from a different IP, past the window, sweeps the stale bucket.
	l.allow("2.2.2.2", base.Add(2*time.Minute))
	if _, ok := l.buckets["1.1.1.1"]; ok {
		t.Error("stale bucket for 1.1.1.1 was not swept")
	}
	if len(l.buckets) != 1 {
		t.Fatalf("post-sweep bucket count = %d, want 1 (only the active IP)", len(l.buckets))
	}
}

func TestSetPasswordThenLoginRotates(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	s1, csrf, err := svc.Login("test", "10.0.0.1", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if csrf == "" {
		t.Fatal("empty csrf token")
	}
	if _, err := svc.Authenticate(s1.ID); err != nil {
		t.Fatalf("authenticate s1: %v", err)
	}
	// The SAME client logs in again — it presents the session it already holds, so that one is
	// superseded. This is the rotation the fixation defence is named for.
	s2, _, err := svc.Login("test", "10.0.0.1", s1.ID)
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if s2.ID == s1.ID {
		t.Fatal("session id not rotated")
	}
	if _, err := svc.Authenticate(s1.ID); err == nil {
		t.Fatal("the caller's own old session still valid after rotation")
	}
}

// TestASecondDeviceDoesNotEvictTheFirst is the quince#373 fix, and it is the assertion the old
// policy could not make: quince is multi-device (ui.design.md — the iPhone is a first-class
// client), so logging in on a phone must leave the desktop signed in. The Operator reported the
// opposite from real use — "whenever I do anything on one device it logs me out on another".
//
// The discriminator against TestSetPasswordThenLoginRotates above is the LAST ARGUMENT: a second
// device arrives with no session cookie of its own (""), so there is nothing of its own to
// supersede, and nothing of anybody else's may be touched.
func TestASecondDeviceDoesNotEvictTheFirst(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	desktop, _, err := svc.Login("test", "10.0.0.1", "")
	if err != nil {
		t.Fatalf("desktop login: %v", err)
	}
	phone, _, err := svc.Login("test", "10.0.0.2", "") // a different client, no cookie of its own
	if err != nil {
		t.Fatalf("phone login: %v", err)
	}
	if phone.ID == desktop.ID {
		t.Fatal("the two devices share a session id")
	}
	if _, err := svc.Authenticate(desktop.ID); err != nil {
		t.Fatalf("the desktop was signed out by the phone logging in: %v "+
			"(quince#373 — rotation is per client, not global)", err)
	}
	if _, err := svc.Authenticate(phone.ID); err != nil {
		t.Fatalf("authenticate phone: %v", err)
	}
}

// A login presenting a session id that is not in the table (stale cookie, already expired, or
// simply wrong) must still succeed and must not disturb anyone else. The delete is best-effort
// cleanup of the caller's own row, not a precondition.
func TestLoginWithAStaleCookieStillSucceeds(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	other, _, err := svc.Login("test", "10.0.0.1", "")
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	fresh, _, err := svc.Login("test", "10.0.0.2", "no-such-session-id")
	if err != nil {
		t.Fatalf("login with a stale cookie: %v", err)
	}
	if _, err := svc.Authenticate(fresh.ID); err != nil {
		t.Fatalf("authenticate the new session: %v", err)
	}
	if _, err := svc.Authenticate(other.ID); err != nil {
		t.Fatalf("an unrelated session died for a stale cookie: %v", err)
	}
}

func TestLoginWritesAuditRow(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Login("test", "10.0.0.1", ""); err != nil {
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
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPassword("other", "1.2.3.4"); err != ErrAlreadyConfigured {
		t.Fatalf("want ErrAlreadyConfigured, got %v", err)
	}
}

func TestLoginBadPassword(t *testing.T) {
	svc, _ := newTestAuth(t)
	_ = svc.SetPassword("test", "1.2.3.4")
	if _, _, err := svc.Login("nope", "10.0.0.1", ""); err != ErrBadPassword {
		t.Fatalf("want ErrBadPassword, got %v", err)
	}
}

func TestLoginRateLimited(t *testing.T) {
	svc, _ := newTestAuth(t) // limiter max = 3
	// The setup call bills a DIFFERENT client, because setup and login now SHARE the limiter
	// (quince#463) and this test is about login's budget. Billing both to one address would have
	// the setup consume a token and the third login trip the limit — a real consequence of the
	// sharing, and not the one under test here.
	_ = svc.SetPassword("test", "198.51.100.1")
	for i := 0; i < 3; i++ {
		if _, _, err := svc.Login("wrong", "1.2.3.4", ""); err != ErrBadPassword {
			t.Fatalf("attempt %d: want ErrBadPassword, got %v", i, err)
		}
	}
	if _, _, err := svc.Login("wrong", "1.2.3.4", ""); err != ErrRateLimited {
		t.Fatalf("4th attempt: want ErrRateLimited, got %v", err)
	}
}

func TestStatusTriState(t *testing.T) {
	svc, _ := newTestAuth(t)
	if st, _ := svc.Status(""); st != StateNeedsSetup {
		t.Fatalf("want needs_setup, got %q", st)
	}
	_ = svc.SetPassword("test", "1.2.3.4")
	if st, _ := svc.Status(""); st != StateNeedsLogin {
		t.Fatalf("want needs_login, got %q", st)
	}
	sess, _, _ := svc.Login("test", "10.0.0.1", "")
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
	_ = svc.SetPassword("test", "1.2.3.4")
	sess, _, _ := svc.Login("test", "10.0.0.1", "")
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

// The default rule, with the opt-in OFF. Every call states the mode explicitly, which is
// why secureCookie takes it as a parameter rather than reading a package variable.
func TestSecureCookieRule(t *testing.T) {
	loopback := httptest.NewRequest("GET", "http://localhost:8080/api/health", nil)
	if secureCookie(loopback, false, nil) {
		t.Error("loopback http should not be Secure")
	}
	lan := httptest.NewRequest("GET", "http://10.20.30.40/api/health", nil)
	if !secureCookie(lan, false, nil) {
		t.Error("non-loopback http should be Secure")
	}
	tlsReq := httptest.NewRequest("GET", "http://localhost/api/health", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	if !secureCookie(tlsReq, false, nil) {
		t.Error("TLS should be Secure")
	}
	proxied := httptest.NewRequest("GET", "http://localhost/api/health", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if !secureCookie(proxied, false, nil) {
		t.Error("X-Forwarded-Proto https should be Secure")
	}
}

// The opt-in relaxes THE FALLBACK ONLY (qn.6f slice 8, Operator ruling 2026-08-02). The two
// rows that matter here are the last two: a positive signal must still win, or the flag
// would strip Secure from a genuine HTTPS session — a different and much worse setting than
// the one that was ruled, and reachable by moving one branch three lines up.
func TestAllowInsecureTransportRelaxesOnlyTheFallback(t *testing.T) {
	tlsReq := httptest.NewRequest("GET", "http://quince.example/api/health", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	proxied := httptest.NewRequest("GET", "http://quince.example/api/health", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")

	tests := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{
			name: "the case the opt-in exists for: plain http to a lan address",
			req:  httptest.NewRequest("GET", "http://quince.example:8968/api/health", nil),
			want: false, // relaxed — the cookie is usable, which is the whole point
		},
		{
			name: "loopback is unaffected",
			req:  httptest.NewRequest("GET", "http://localhost:8968/api/health", nil),
			want: false, // already false without the flag
		},
		{
			name: "direct TLS still gets Secure",
			req:  tlsReq,
			want: true,
		},
		{
			name: "a proxy saying https still gets Secure — the header can only ever upgrade",
			req:  proxied,
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := secureCookie(tc.req, true, nil); got != tc.want {
				t.Errorf("secureCookie(_, allowInsecure=true) = %v, want %v", got, tc.want)
			}
		})
	}
}

// The payoff of defining CookieWillBeDiscarded in terms of Secure rather than re-deriving
// the host test: the quince#497 refusal switches itself off when the user opts in, with no
// second condition anywhere to keep in step. contracts §1 states this as contract.
func TestOptInAlsoDisarmsTheInsecureOriginRefusal(t *testing.T) {
	lan := httptest.NewRequest("POST", "http://quince.example:8968/api/auth/login", nil)

	if !(&Service{}).CookieWillBeDiscarded(lan) {
		t.Fatal("without the opt-in the cookie IS discarded — the refusal must fire")
	}
	if (&Service{allowInsecureTransport: true}).CookieWillBeDiscarded(lan) {
		t.Error("with the opt-in the cookie survives, so the refusal must NOT fire; " +
			"a second switch has appeared somewhere and the two can now disagree")
	}
}

// CookieWillBeDiscarded is true for ONE of the four cases secureCookie distinguishes: the
// cookie is marked Secure and the origin is not one the browser calls secure. That case is
// quince#497's login loop. The table exists for the other rows — they all reach `Secure` by
// different routes and every one of them must stay false, or the refusal starts turning
// away logins that would have worked.
func TestCookieWillBeDiscarded(t *testing.T) {
	tlsReq := httptest.NewRequest("GET", "http://quince.example/api/health", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	proxied := httptest.NewRequest("GET", "http://quince.example/api/health", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")

	tests := []struct {
		name string
		req  *http.Request
		demo bool
		want bool
	}{
		{
			name: "plain http to a non-loopback host",
			req:  httptest.NewRequest("GET", "http://quince.example:8080/api/health", nil),
			want: true, // Secure is set and the browser drops it — the loop
		},
		{
			name: "plain http to loopback",
			req:  httptest.NewRequest("GET", "http://localhost:8080/api/health", nil),
			want: false, // Secure is never set, so nothing is discarded
		},
		{name: "direct tls", req: tlsReq, want: false},
		{name: "tls terminated at a trusted proxy", req: proxied, want: false},
		{
			name: "demo mode on the loop-producing origin",
			req:  httptest.NewRequest("GET", "http://quince.example:8080/api/health", nil),
			demo: true,
			want: false, // demo forces Secure off, so the cookie survives
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{insecureCookies: tc.demo}
			if got := s.CookieWillBeDiscarded(tc.req); got != tc.want {
				t.Errorf("CookieWillBeDiscarded = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSetPasswordDoesNotDeriveWhenAlreadyConfigured is the regression guard for quince#463:
// POST /api/auth/setup is pre-auth and un-rate-limited by design (first-run setup must be
// reachable with no session), so a derivation on a path whose 409 is already decided is a
// remote memory-and-CPU amplifier. Measured before the fix at 9 MB → 2063 MB RSS over 60
// requests. The assertion is on the COUNT rather than on timing, so it cannot go flaky.
func TestSetPasswordDoesNotDeriveWhenAlreadyConfigured(t *testing.T) {
	svc, _ := newTestAuth(t)
	derivations := 0
	inner := svc.hash
	svc.hash = func(pw string, p argonParams) (string, error) {
		derivations++
		return inner(pw, p)
	}

	if err := svc.SetPassword("first", "1.2.3.4"); err != nil {
		t.Fatalf("first-run setup: %v", err)
	}
	if derivations != 1 {
		t.Fatalf("first-run setup derived %d time(s), want exactly 1", derivations)
	}

	// A DISTINCT client per probe: this test counts DERIVATIONS, and sharing one address would
	// have the limiter (quince#463's other half) refuse the later probes before they reached the
	// existence check. The count would then be right for a reason that has nothing to do with
	// what is being asserted.
	for i := 0; i < 5; i++ {
		ip := fmt.Sprintf("203.0.113.%d", i+1)
		if err := svc.SetPassword("again", ip); !errors.Is(err, ErrAlreadyConfigured) {
			t.Fatalf("setup #%d on a configured service: err=%v, want ErrAlreadyConfigured", i+2, err)
		}
	}
	if derivations != 1 {
		t.Fatalf("guaranteed-409 setups derived %d extra time(s), want 0 — quince#463", derivations-1)
	}
}

// TestSetPasswordFirstRunRaceStillHasExactlyOneWinner guards the half the fix must NOT break.
// The cheap HasPassword check is an optimisation for the already-configured case and is NOT
// atomic with the insert; SetSettingIfAbsent is what actually decides the first-run race. A
// future simplification that dropped it would pass the test above and silently turn setup into
// an unauthenticated password reset.
func TestSetPasswordFirstRunRaceStillHasExactlyOneWinner(t *testing.T) {
	svc, _ := newTestAuth(t)

	const racers = 8
	errs := make(chan error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		// A DISTINCT client per racer, because the race under test is the INSERT race and not the
		// limiter. Sharing one address would have the limiter refuse most of them before they ever
		// reached SetSettingIfAbsent — correct behaviour (quince#463), but it would make this test
		// pass for the wrong reason and stop guarding what it exists to guard.
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		go func() {
			<-start
			errs <- svc.SetPassword("concurrent", ip)
		}()
	}
	close(start)

	winners, configured := 0, 0
	for i := 0; i < racers; i++ {
		switch err := <-errs; {
		case err == nil:
			winners++
		case errors.Is(err, ErrAlreadyConfigured):
			configured++
		default:
			t.Fatalf("unexpected error from concurrent setup: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent first-run setup: %d winners, want exactly 1", winners)
	}
	if configured != racers-1 {
		t.Fatalf("concurrent first-run setup: %d already-configured, want %d", configured, racers-1)
	}
}

// TestSetPasswordIsRateLimited is quince#463's second half. quince#520 fixed the first — the 409
// no longer derives — but an UNCONFIGURED instance still derives 64 MiB on every request, because
// until somebody sets a password every request legitimately reaches the derivation. That window is
// first-run: the one moment the route must stay open, and the one moment nobody is watching.
//
// Asserted on a FRESH service, so it exercises the expensive path rather than the cheap 409.
func TestSetPasswordIsRateLimited(t *testing.T) {
	svc, _ := newTestAuth(t) // limiter is 3 per minute in tests
	var limited bool
	for i := 0; i < 6; i++ {
		// Deliberately WEAK so no attempt succeeds and the limiter is what stops us — a successful
		// setup would end the loop for the wrong reason.
		err := svc.SetPassword("", "203.0.113.5")
		if errors.Is(err, ErrRateLimited) {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("setup was never rate-limited from one client — an unconfigured instance is still " +
			"a 64 MiB amplifier (quince#463)")
	}
}

// TestSetPasswordLimiterIsPerClient guards the half that makes sharing the login limiter safe. One
// client exhausting its budget must not deny setup to a different client — which is only true
// because quince#547 made the bucket per-visitor rather than per-proxy.
func TestSetPasswordLimiterIsPerClient(t *testing.T) {
	svc, _ := newTestAuth(t)
	for i := 0; i < 6; i++ {
		_ = svc.SetPassword("", "203.0.113.5") // burn one client's budget
	}
	if err := svc.SetPassword("goodpassword", "198.51.100.9"); err != nil {
		t.Fatalf("a DIFFERENT client was denied setup after another exhausted its budget: %v", err)
	}
}

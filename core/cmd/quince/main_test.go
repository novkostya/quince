package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/demo"
	"github.com/novkostya/quince/core/internal/store"
)

func TestConfigValidateExitCodes(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "good.yml")
	if err := os.WriteFile(valid, []byte("sessions:\n  ttl_minutes: 45\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configCmd([]string{"validate", valid}); err != nil {
		t.Errorf("valid config should pass, got %v", err)
	}

	bad := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(bad, []byte("ui:\n  theme: neon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configCmd([]string{"validate", bad}); err == nil {
		t.Error("invalid config should return a nonzero error")
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	if err := run([]string{"frobnicate"}); err == nil {
		t.Error("unknown subcommand should error")
	}
}

// newDemoAuth builds a real auth.Service over a throwaway store, so the mode tests assert against
// the actual service rather than a stub — the whole claim is about how the two modes CONFIGURE it.
func newDemoAuth(t *testing.T) *auth.Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return auth.NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestPublicDemoStartsAtNeedsLogin is spec story 1: the mode starts with the password already set,
// so a visitor sees the login screen rather than first-run setup.
func TestPublicDemoStartsAtNeedsLogin(t *testing.T) {
	svc := newDemoAuth(t)
	if err := configureDemoAuth(svc, discardLog(), true); err != nil {
		t.Fatalf("configureDemoAuth(public): %v", err)
	}
	state, err := svc.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if state != auth.StateNeedsLogin {
		t.Fatalf("public demo starts at %q, want %q", state, auth.StateNeedsLogin)
	}
}

// TestPublicDemoSetupIsAlreadyClosed is spec story 2 and the whole of D3: presetting the password
// IS what makes it immutable, with no new refusal added. If this fails, a visitor can set the
// password to anything they like, because POST /api/auth/setup is authExempt by design.
func TestPublicDemoSetupIsAlreadyClosed(t *testing.T) {
	svc := newDemoAuth(t)
	if err := configureDemoAuth(svc, discardLog(), true); err != nil {
		t.Fatalf("configureDemoAuth(public): %v", err)
	}
	if err := svc.SetPassword("attacker-chosen", "1.2.3.4"); !errors.Is(err, auth.ErrAlreadyConfigured) {
		t.Fatalf("setup on a public demo: err=%v, want ErrAlreadyConfigured — the route is pre-auth", err)
	}
}

// TestPublicDemoDoesNotForceSecureOff is spec story 3, and the finding quince#444 called
// load-bearing: `SetInsecureCookies` is documented "Never set in production", and a public instance
// on the internet over HTTPS is production in the only sense that matters.
//
// Asserted through Service.Secure over BOTH proxy shapes, because a public demo reaches quince
// through a TLS-terminating proxy and therefore arrives as plain http with X-Forwarded-Proto set.
func TestPublicDemoDoesNotForceSecureOff(t *testing.T) {
	svc := newDemoAuth(t)
	if err := configureDemoAuth(svc, discardLog(), true); err != nil {
		t.Fatalf("configureDemoAuth(public): %v", err)
	}
	for _, tc := range []struct {
		name string
		req  *http.Request
	}{
		{"direct TLS", &http.Request{Host: "demo.example", TLS: &tls.ConnectionState{}}},
		{"behind a TLS-terminating proxy", func() *http.Request {
			r := &http.Request{Host: "demo.example", Header: http.Header{}}
			r.Header.Set("X-Forwarded-Proto", "https")
			return r
		}()},
	} {
		if !svc.Secure(tc.req) {
			t.Errorf("%s: Secure=false on a public demo — the session cookie would ship without Secure "+
				"on an https origin, which is exactly what auth/service.go forbids", tc.name)
		}
	}
}

// TestPlainDemoIsUnchanged is spec story 4, and it is the half that protects the SHIPPING product:
// --demo must still start at needs_setup so e2e keeps exercising first-run, and must still force
// Secure off because the e2e host is plain http and NOT loopback.
//
// Without this, "presetting the password" could be quietly widened to --demo and delete e2e's
// set-password coverage — a test-coverage loss disguised as a feature flag, which is the argument
// that made public-demo a separate mode at all.
func TestPlainDemoIsUnchanged(t *testing.T) {
	svc := newDemoAuth(t)
	if err := configureDemoAuth(svc, discardLog(), false); err != nil {
		t.Fatalf("configureDemoAuth(plain): %v", err)
	}
	state, err := svc.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if state != auth.StateNeedsSetup {
		t.Fatalf("--demo starts at %q, want %q — first-run coverage is gone", state, auth.StateNeedsSetup)
	}
	r := &http.Request{Host: "e2e-host:8080", TLS: &tls.ConnectionState{}}
	if svc.Secure(r) {
		t.Fatal("--demo returned Secure=true; it must force the flag off so plain-http e2e can log in")
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestPublicDemoBannerDoesNotTellTheOperatorToSetAPassword guards a state-honesty defect found by
// reading a real run's logs rather than the code: the shared demo branch printed "set the admin
// password to begin" for BOTH modes, and under --public-demo that instructs the operator to do
// something the same binary refuses with a 409.
//
// Asserted on the emitted line rather than on a flag, because the defect was invisible in the
// control flow — both modes were correct, and one of them said something untrue on the way past.
func TestPublicDemoBannerDoesNotTellTheOperatorToSetAPassword(t *testing.T) {
	var buf bytes.Buffer
	svc := newDemoAuth(t)
	if err := configureDemoAuth(svc, slog.New(slog.NewTextHandler(&buf, nil)), true); err != nil {
		t.Fatalf("configureDemoAuth(public): %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "set the admin password") {
		t.Fatalf("public-demo banner instructs the operator to set a password, which setup 409s:\n%s", got)
	}
	if !strings.Contains(got, "preset") {
		t.Fatalf("public-demo banner does not say the password is preset:\n%s", got)
	}
}

// TestPlainDemoBannerStillAsksForTheSetup is the other half: --demo genuinely does start at
// needs_setup, so its banner must keep saying so. Without this, "fix the false line" could be
// satisfied by deleting the message from both modes.
func TestPlainDemoBannerStillAsksForTheSetup(t *testing.T) {
	var buf bytes.Buffer
	svc := newDemoAuth(t)
	if err := configureDemoAuth(svc, slog.New(slog.NewTextHandler(&buf, nil)), false); err != nil {
		t.Fatalf("configureDemoAuth(plain): %v", err)
	}
	if !strings.Contains(buf.String(), "set the admin password") {
		t.Fatalf("--demo banner no longer tells the operator to set a password, but it still starts at needs_setup:\n%s", buf.String())
	}
}

// TestResetIntervalIsReportedOnlyByTheModeThatResets is story 6's gate. --public-demo is restarted
// from outside the process (D4); nothing restarts a --demo instance or the shipping product, so an
// interval reported there would put a destructive promise on a screen where it is false.
//
// The WARNING is asserted, not just the zero. Setting the var outside the mode is the mistake this
// deployment invites — copy the compose file, drop the flag, keep the env — and its symptom is
// nothing at all, which is also what a correct non-demo deployment looks like.
func TestResetIntervalIsReportedOnlyByTheModeThatResets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		minutes int
		public  bool
		want    int
		warn    bool
	}{
		{"public demo reports what it was told", 30, true, 30, false},
		{"public demo with nothing configured", 0, true, 0, false},
		{"set on a non-public instance", 30, false, 0, true},
		{"unset on a non-public instance", 0, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			got := reportableResetMinutes(tc.minutes, tc.public, slog.New(slog.NewTextHandler(&buf, nil)))
			if got != tc.want {
				t.Errorf("reportableResetMinutes(%d, public=%v) = %d, want %d",
					tc.minutes, tc.public, got, tc.want)
			}
			warned := strings.Contains(buf.String(), "QUINCE_DEMO_RESET_MINUTES")
			if warned != tc.warn {
				t.Errorf("warned = %v, want %v — an interval that is silently ignored leaves the "+
					"operator believing the notice is showing:\n%s", warned, tc.warn, buf.String())
			}
		})
	}
}

// demoBoot performs the STATE half of a demo startup over `cache`, in the order serve() does it:
// derive the throwaway paths, wipe whatever the last run left, open the store, configure auth for
// the mode, build a fresh fixture provider. It returns the live pieces plus the two shutdown halves
// serve() defers — closing the store, and the cleanup that only runs on a GRACEFUL exit.
//
// Split that way deliberately: story 7's interesting case is the restart where cleanup never ran.
//
// `public` is a parameter rather than a constant because the two modes fail DIFFERENTLY when the
// startup wipe is missing, and only one of those failures is loud (see prepareDemoState).
func demoBoot(t *testing.T, cache string, public bool) (*auth.Service, *demo.Provider, string, func(), func()) {
	t.Helper()
	// prepareDemoState, NOT a local copy of what it does: a test that reimplements the sequence it
	// is asserting would keep passing with the startup wipe deleted from serve()'s path.
	dbPath, cfgPath, cleanup := prepareDemoState(cache)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	log := discardLog()
	svc := auth.NewService(st, log)
	if err := configureDemoAuth(svc, log, public); err != nil {
		t.Fatalf("configureDemoAuth(public=%v): %v", public, err)
	}
	return svc, demo.NewProvider(bus.New(), log), cfgPath, func() { _ = st.Close() }, cleanup
}

// TestDemoRestartResetsEverything is spec story 7, and it is the story that makes the mode worth
// deploying at all: "versions deleted by a visitor are back, the config edit is gone, and the
// instance is at needs_login again rather than needs_setup".
//
// THE KILLED CASE IS THE ONE THAT MATTERS, which is why every mode runs both. A demo is reset by
// something outside the process restarting it (design D4), and a container stop is entitled to be a
// SIGKILL — so the deferred cleanup is exactly what a real reset cannot rely on. State is wiped at
// BOTH ends for that reason, and a test exercising only a graceful exit would pass just as happily
// with the STARTUP wipe deleted, which is the half that actually carries the guarantee.
//
// BOTH MODES, because deleting that wipe fails them differently and only one failure is loud
// (prepareDemoState records the measurement). `--public-demo` refuses to start, which nothing can
// miss. `--demo` starts and silently inherits — and quietly loses e2e's first-run set-password
// coverage, which is the more expensive of the two precisely because nothing announces it. The
// first revision of this test gated only the loud one, which left the failure it named as costlier
// as the one nothing caught (found in review, quince#575).
//
// TestPlainDemoIsUnchanged does NOT cover the plain arm: it builds a fresh service with no state
// dir and no restart, so it asserts --demo starts at needs_setup on a CLEAN boot — exactly the case
// a surviving DB is not.
//
// The auth assertion is the load-bearing one. `needs_login` vs `needs_setup` is the subtlest of the
// three claims, and it is mode-specific in both directions: a public instance left on an open
// set-password form is owned by the first visitor to arrive, and a plain one that comes up at
// needs_login has silently deleted the coverage --demo exists to provide.
func TestDemoRestartResetsEverything(t *testing.T) {
	for _, mode := range []struct {
		name        string
		public      bool
		wantRestart string
		because     string
	}{
		{"--public-demo", true, auth.StateNeedsLogin,
			"a public instance left on an open set-password form is owned by the first visitor to arrive"},
		{"--demo", false, auth.StateNeedsSetup,
			"--demo exists to exercise first-run setup; coming up at needs_login deletes that coverage silently"},
	} {
		for _, tc := range []struct {
			name     string
			graceful bool
		}{
			{"graceful exit runs cleanup", true},
			{"KILLED — cleanup never runs, so startup must wipe", false},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				cache := t.TempDir()
				svc, prov, cfgPath, closeDB, cleanup := demoBoot(t, cache, mode.public)

				// Whatever the mode starts at, a visitor ends up logged in against a password. In
				// --demo that means completing first-run setup, which is the state that must NOT
				// survive; in --public-demo it is already preset.
				if !mode.public {
					if err := svc.SetPassword("demo", "startup"); err != nil {
						t.Fatalf("first boot: complete first-run setup: %v", err)
					}
				}
				if got, err := svc.Status(""); err != nil || got != auth.StateNeedsLogin {
					t.Fatalf("first boot: status = %q (err %v), want %q — the mutation this test rests "+
						"on did not happen", got, err, auth.StateNeedsLogin)
				}

				// A visitor deletes every version.
				before := prov.Versions("")
				if len(before) == 0 {
					t.Fatal("the demo fixture has no versions, so the story cannot be asserted at all")
				}
				for _, v := range before {
					if status, err := prov.Delete(v.ID); err != nil || status != http.StatusAccepted {
						t.Fatalf("delete %s: status %d, err %v", v.ID, status, err)
					}
				}
				if n := len(prov.Versions("")); n != 0 {
					t.Fatalf("%d version(s) survived the delete — the mutation this test rests on did not happen", n)
				}

				// A visitor edits the config.
				if err := os.WriteFile(cfgPath, []byte("sessions:\n  ttl_minutes: 999\n"), 0o600); err != nil {
					t.Fatalf("write config edit: %v", err)
				}

				closeDB()
				if tc.graceful {
					cleanup()
				}

				// --- the restart ---
				svc2, prov2, cfgPath2, closeDB2, _ := demoBoot(t, cache, mode.public)
				defer closeDB2()

				// NOT EVIDENCE THAT THE RESET COVERS VERSIONS, and a later reader should not take it
				// as such. Demo versions live in the provider's in-memory map, seeded by NewProvider,
				// so a fresh provider has the full set whether or not removeDemoState ever ran. It
				// mirrors production honestly — serve() builds a new provider per start, so a process
				// restart genuinely is what restores them — but this assertion is carried by the
				// restart itself, never by the wipe. The auth and config assertions are the ones that
				// fail when the reset breaks (observed in review, quince#575).
				if n := len(prov2.Versions("")); n != len(before) {
					t.Errorf("after restart: %d version(s), want %d — a visitor's deletions outlived the restart",
						n, len(before))
				}
				if _, err := os.Stat(cfgPath2); !os.IsNotExist(err) {
					t.Errorf("after restart: %s still exists (stat err %v) — the config edit outlived the reset",
						filepath.Base(cfgPath2), err)
				}
				got, err := svc2.Status("")
				if err != nil {
					t.Fatalf("after restart: status: %v", err)
				}
				if got != mode.wantRestart {
					t.Fatalf("after restart in %s: status = %q, want %q — %s",
						mode.name, got, mode.wantRestart, mode.because)
				}
			})
		}
	}
}

// TestDemoStateLivesInTheCacheDir pins WHERE the throwaway state is, which the restart cycle above
// cannot see: removeDemoState deletes whatever it is handed, so a reset over the wrong directory
// still looks like a working reset. Under the DATA dir it would wipe a real deployment's DB the
// first time anybody ran the binary with --demo.
func TestDemoStateLivesInTheCacheDir(t *testing.T) {
	b := config.Bootstrap{Data: "/data", Cache: "/cache"}
	dbPath, cfgPath := demoStatePaths(b.Cache)

	for _, p := range []string{dbPath, cfgPath} {
		if filepath.Dir(p) != b.Cache {
			t.Errorf("demo state %q is not under the cache dir %q — it is deleted twice per run, so "+
				"anywhere else is a real deployment's data being wiped by a --demo start", p, b.Cache)
		}
	}
	if dbPath == b.DBPath() || cfgPath == b.ConfigPath() {
		t.Error("demo state collides with the REAL db/config path")
	}
}

// TestHTTPServerTimeoutsAreSet is the regression guard for quince#466. Only ReadHeaderTimeout was
// set, which left IdleTimeout inheriting ReadTimeout's zero — documented by net/http as no timeout
// at all — so idle keep-alive connections were never reclaimed.
//
// Asserted field by field rather than as a lump, because the defect was ONE missing field among
// four and a test that checked "some timeout is set" would have passed before the fix.
func TestHTTPServerTimeoutsAreSet(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())

	for _, tc := range []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout},
		{"ReadTimeout", srv.ReadTimeout},
		{"WriteTimeout", srv.WriteTimeout},
		{"IdleTimeout", srv.IdleTimeout},
	} {
		if tc.got <= 0 {
			t.Errorf("%s = %v, want > 0 — a zero here is 'no timeout', not 'a default' (quince#466)", tc.name, tc.got)
		}
	}
}

// TestIdleTimeoutDoesNotInheritZero pins the exact mechanism of quince#466 rather than its symptom.
// net/http: "IdleTimeout … If zero, the value of ReadTimeout is used. If negative, or if zero and
// ReadTimeout is zero or negative, there is no timeout." Both were zero, so there was none.
//
// This fails if someone removes IdleTimeout believing ReadTimeout covers it — the precise mistake
// the original code embodied.
func TestIdleTimeoutDoesNotInheritZero(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	if srv.IdleTimeout == 0 && srv.ReadTimeout == 0 {
		t.Fatal("IdleTimeout and ReadTimeout are both zero — net/http then applies NO idle timeout (quince#466)")
	}
	if srv.IdleTimeout == 0 {
		t.Fatalf("IdleTimeout is 0, silently inheriting ReadTimeout=%v; set it explicitly", srv.ReadTimeout)
	}
}

// TestWriteTimeoutClearsOnHijack records the fact that makes WriteTimeout safe for /api/ws, and
// would catch a Go upgrade that changed it. quince#466 was filed asserting the opposite — that a
// WriteTimeout would break the WebSocket — and it is wrong: net/http's hijackLocked calls
// SetDeadline(time.Time{}), so an upgraded connection carries no server deadline.
//
// Asserted behaviourally rather than by reading the stdlib: the hijacked conn must accept a write
// well after WriteTimeout would have expired.
func TestWriteTimeoutClearsOnHijack(t *testing.T) {
	const writeTO = 150 * time.Millisecond
	done := make(chan error, 1)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		time.Sleep(3 * writeTO) // well past the server's WriteTimeout
		_, err = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
		done <- err
	}))
	srv.Config.WriteTimeout = writeTO
	srv.Start()
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	if err := <-done; err != nil {
		t.Fatalf("write on a hijacked conn %v after WriteTimeout=%v failed: %v — "+
			"the deadline was NOT cleared on hijack, so WriteTimeout would break /api/ws", 3*writeTO, writeTO, err)
	}
}

// applyInsecureTransportOptIn is a security relaxation, so the test asserts BOTH directions:
// it takes effect and says so, and it stays silent and inert when off. A one-direction test
// would pass against a function that always announced, which is the same defect as always
// relaxing — noise that trains the reader to ignore the line.
func TestInsecureTransportOptInAnnouncesItselfAndTakesEffect(t *testing.T) {
	lan := httptest.NewRequest("GET", "http://quince.example:8968/api/health", nil)

	t.Run("off: silent and Secure kept", func(t *testing.T) {
		svc := newDemoAuth(t)
		var out bytes.Buffer
		applyInsecureTransportOptIn(svc, config.Default(), &out)

		if out.Len() != 0 {
			t.Errorf("the default config printed a degraded-mode warning:\n%s", out.String())
		}
		if !svc.Secure(lan) {
			t.Error("Secure was relaxed without the opt-in")
		}
	})

	t.Run("on: announced and Secure relaxed", func(t *testing.T) {
		svc := newDemoAuth(t)
		cfg := config.Default()
		cfg.Sessions.AllowInsecureTransport = true
		var out bytes.Buffer
		applyInsecureTransportOptIn(svc, cfg, &out)

		got := out.String()
		if !strings.Contains(got, "sessions.allow_insecure_transport") {
			t.Errorf("the announcement does not name the setting, so nobody can turn it off:\n%s", got)
		}
		if !strings.Contains(got, "in clear") {
			t.Errorf("the announcement does not say what is unprotected:\n%s", got)
		}
		if svc.Secure(lan) {
			t.Error("the opt-in was announced but did not take effect")
		}
	})
}

// The redirect is what makes one-port routing worth having: the URL the user typed keeps
// working, upgraded in place. It must preserve the HOST AND PORT, because the default is now
// :8968 and a redirect to bare https://host would send them to 443 where nothing listens.
func TestRedirectToHTTPSPreservesHostPortAndPath(t *testing.T) {
	tests := []struct {
		name, host, target, want string
	}{
		{"non-default port", "quince.example:8968", "/devices", "https://quince.example:8968/devices"},
		{"root", "quince.example:8968", "/", "https://quince.example:8968/"},
		{"query preserved", "quince.example:8968", "/jobs?state=failed", "https://quince.example:8968/jobs?state=failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			redirectToHTTPS()(rec, req)

			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tc.want {
				t.Errorf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}

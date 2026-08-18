package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
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
	"github.com/novkostya/quince/core/internal/tlsx"
)

func TestConfigValidateExitCodes(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "good.yml")
	if err := os.WriteFile(valid, []byte("reconcile:\n  interval_minutes: 45\n"), 0o644); err != nil {
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
func demoBoot(t *testing.T, cache string, public bool) (*auth.Service, *demo.Provider, *config.Service, string, func(), func()) {
	t.Helper()
	// prepareDemoState and configureDemo, NOT local copies of what they do: a test that
	// reimplements the sequence it is asserting keeps passing when a step is deleted from serve().
	dbPath, cfgPath, cleanup := prepareDemoState(cache)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	log := discardLog()
	svc := auth.NewService(st, log)
	cfgSvc := config.NewService(cfgPath, log)
	if err := configureDemo(cfgSvc, svc, log, public); err != nil {
		t.Fatalf("configureDemo(public=%v): %v", public, err)
	}
	return svc, demo.NewProvider(bus.New(), log), cfgSvc, cfgPath, func() { _ = st.Close() }, cleanup
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
				svc, prov, _, cfgPath, closeDB, cleanup := demoBoot(t, cache, mode.public)

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
				if err := os.WriteFile(cfgPath, []byte("ui:\n  theme: dark\n"), 0o600); err != nil {
					t.Fatalf("write config edit: %v", err)
				}

				closeDB()
				if tc.graceful {
					cleanup()
				}

				// --- the restart ---
				svc2, prov2, cfgSvc2, _, closeDB2, _ := demoBoot(t, cache, mode.public)
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
				// THE VISITOR'S EDIT IS GONE, but the FILE is not — and that changed with quince#574.
				// It used to assert `demo-config.yml` does not exist, which was true when nothing
				// wrote one; seedDemoStorages now writes a fresh document at every boot. Asserting
				// absence would have been the easy way to keep this green and would have gated the
				// wrong thing: what the reset owes a visitor is that their EDIT does not survive,
				// not that the file is missing.
				// `ui.theme` is the VEHICLE and only the vehicle: any key whose default the edit
				// can differ from would do. It replaced `sessions.ttl_minutes`, which quince#656
				// removed — this assertion is about the RESET, never about the key.
				if theme := cfgSvc2.Current().UI.Theme; theme == "dark" {
					t.Errorf("after restart: ui.theme is still %q — the config edit "+
						"outlived the reset", theme)
				}
				// And the declaration is restored, which is the ruling's own requirement: a visitor
				// may edit or delete these entries, and the next start must put them back or the
				// instance returns to the quince#574 state where its own config cannot be saved.
				if st := cfgSvc2.Current().Storage; st == nil || len(*st) == 0 {
					t.Errorf("after restart: no storage declared — the reset did not re-seed it, so " +
						"Settings would 422 on save again (quince#574)")
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

// TestDemoConfigRoundTripsThroughSave is quince#574: the document `GET /api/config` serves must be
// one `PUT /api/config` accepts. A visitor on the public demo who opened Settings and pressed Save
// got a 422 having changed nothing, because `config.storage` was null in demo mode and `Replace`
// requires a declared storage.
//
// THE DEFECT IS NOW UNREACHABLE BY ITS ORIGINAL MECHANISM, AND THIS TEST SAYS SO RATHER THAN
// PRETENDING OTHERWISE (Operator ruling 2026-08-14, quince#908, following quince#935's gap).
//
// quince#574 was: `config.storage` is null in demo mode, `Replace` refuses any document declaring no
// storage, so pressing Save having changed nothing returned 422. The fix was to SEED demo storages in
// `serve()` — a workaround for a check that refused a STATE. That check now refuses a TRANSITION, and
// a demo that never declared a storage is 0 → 0, which is permitted.
//
// SO THE UNSEEDED HALF FLIPPED FROM `refused` TO `saved`, and that is the ruling working rather than
// a guard being lost: the defect cannot occur by its original mechanism on a seeded or an unseeded
// demo. What this test no longer does is DISTINGUISH the two — deleting the seed from `serve()` would
// now leave both subtests green — so it has stopped being quince#574's regression guard, and saying
// so is the point of this paragraph.
//
// THE SEEDING IS NOT REMOVED, DELIBERATELY. It exists for a second reason the ruling does not touch,
// asserted by the test directly below: the demo must DECLARE the storages it SERVES, or Settings
// shows one thing and the cards show another. That test is now the only thing holding the seed in
// place, which is worth knowing before anybody tidies it.
//
// Asserted through Replace rather than over HTTP because Replace IS the save path: the handler
// decodes and delegates. The ruling turns on Replace staying mode-blind, so the test drives the
// unexempted path deliberately.
func TestDemoConfigRoundTripsThroughSave(t *testing.T) {
	for _, tc := range []struct {
		name     string
		seed     bool
		wantSave bool
	}{
		{"seeded, as serve() does it", true, true},
		// WAS `false` UNTIL 2026-08-14 — this subtest reproduced quince#574 exactly. It now saves,
		// because 0 → 0 is permitted, which is the ruling reaching the defect's root rather than its
		// workaround.
		{"UNSEEDED — quince#574's mechanism, now permitted", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgSvc := config.NewService(filepath.Join(t.TempDir(), "demo-config.yml"), discardLog())
			if tc.seed {
				// configureDemo, not seedDemoStorages: driving serve()'s own entry point is what
				// makes deleting the seed from serve() fail this test rather than pass it.
				if err := configureDemo(cfgSvc, newDemoAuth(t), discardLog(), true); err != nil {
					t.Fatalf("configureDemo: %v", err)
				}
			}

			// Exactly what the UI does on Save: PUT back the document it was served, unmodified.
			errs, _, err := cfgSvc.Replace(cfgSvc.Current(), "test")
			if err != nil {
				t.Fatalf("Replace: %v", err)
			}
			if saved := len(errs) == 0; saved != tc.wantSave {
				t.Fatalf("saving an unmodified fetched config: accepted=%v, want %v (errors %+v) — a "+
					"demo whose own config cannot be saved breaks the surface quince#444 calls the "+
					"reason a live demo beats screenshots", saved, tc.wantSave, errs)
			}
			// The `errs[0].Path == "storage"` assertion that stood here belonged to the refusal, and
			// there is no refusal left on either half. Nothing replaces it: what it guarded — that
			// the seeded half was not passing for some unrelated reason — is now covered by both
			// halves passing for the SAME reason, which is the state the ruling puts them in.
		})
	}
}

// TestSeedDemoStoragesDeclaresWhatTheProviderServes is the ruling's own requirement — the demo
// declares the storages it SERVES — asserted against the config document that actually lands rather
// than against demo.StorageEntries, which is merely the input.
//
// The count check is the one that matters: Settings showing one storage while the cards show two is
// precisely the incoherence the ruling declined the seed-one-throwaway-entry option to avoid.
func TestSeedDemoStoragesDeclaresWhatTheProviderServes(t *testing.T) {
	cfgSvc := config.NewService(filepath.Join(t.TempDir(), "demo-config.yml"), discardLog())
	if err := configureDemo(cfgSvc, newDemoAuth(t), discardLog(), true); err != nil {
		t.Fatalf("configureDemo: %v", err)
	}
	got := cfgSvc.Current().Storage
	if got == nil {
		t.Fatal("storage is still nil after seeding — the whole point of quince#574")
	}
	served := demo.NewProvider(bus.New(), discardLog()).Storages("")
	if len(*got) != len(served) {
		t.Fatalf("declared %d storage(s), the provider serves %d — Settings and the storage cards "+
			"would disagree", len(*got), len(served))
	}
	for i, s := range served {
		if (*got)[i].Name != s.Name || (*got)[i].Path != s.Path {
			t.Errorf("storage %d: declared %q at %q, provider serves %q at %q",
				i, (*got)[i].Name, (*got)[i].Path, s.Name, s.Path)
		}
		if (*got)[i].Default != s.Default {
			t.Errorf("storage %q: declared default=%v, provider serves default=%v",
				s.Name, (*got)[i].Default, s.Default)
		}
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
			redirectToHTTPS(func() bool { return true })(rec, req)

			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tc.want {
				t.Errorf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}

// writeTestPair mints a throwaway self-signed pair on disk, for the serve-path tests below.
// crypto/x509 rather than `openssl`, for the reason tlsx's own helper gives: nothing here is a
// secret, but a key path in argv is the habit the secrets rule exists to prevent.
func writeTestPair(t *testing.T, dir, cn string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, cn+".pem")
	keyFile = filepath.Join(dir, cn+".key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// The plain half decides PER REQUEST, over the two inputs that can now both move under a
// running process (quince#900). All four combinations, because the interesting one — a
// certificate present and the opt-in on — is the case the Operator's ruling turns on, and a
// three-case test would pass against a handler that ignored one input entirely.
func TestPlainHalfDecidesPerRequest(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeTestPair(t, dir, "plainhalf")

	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "app") })

	tests := []struct {
		name         string
		withCert     bool
		optIn        bool
		wantRedirect bool
	}{
		{name: "no certificate, no opt-in: serve — there is nothing to redirect TO", withCert: false, optIn: false, wantRedirect: false},
		{name: "no certificate, opt-in: serve", withCert: false, optIn: true, wantRedirect: false},
		{name: "certificate, no opt-in: redirect", withCert: true, optIn: false, wantRedirect: true},
		{name: "certificate, opt-in: serve — the opt-in beats the redirect", withCert: true, optIn: true, wantRedirect: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keeper := tlsx.NewEmptyKeeper()
			if tc.withCert {
				if err := keeper.SetFiles(certFile, keyFile); err != nil {
					t.Fatal(err)
				}
			}
			h := plainHalf(app, keeper, func() bool { return tc.optIn }, func() bool { return true })

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "http://quince.example:8968/devices", nil))

			if tc.wantRedirect {
				if rec.Code != http.StatusMovedPermanently {
					t.Fatalf("status %d, want 301 — the plain half served the app where it should redirect", rec.Code)
				}
				if got := rec.Header().Get("Location"); got != "https://quince.example:8968/devices" {
					t.Errorf("Location = %q, want the same host, port and path over https", got)
				}
				return
			}
			if rec.Code != http.StatusOK || rec.Body.String() != "app" {
				t.Errorf("status %d body %q, want 200 app — the plain half redirected when it must not",
					rec.Code, rec.Body.String())
			}
		})
	}
}

// The point of deciding per request: BOTH inputs can move between two requests, and the
// answer moves with them. This is what the bind-time choice could not do, and it is the
// difference between a live setting and a settable one.
func TestPlainHalfFollowsBothInputsWithoutRebuilding(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeTestPair(t, dir, "moving")
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "app") })

	keeper := tlsx.NewEmptyKeeper()
	optIn := false
	h := plainHalf(app, keeper, func() bool { return optIn }, func() bool { return true })

	code := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://quince.example:8968/", nil))
		return rec.Code
	}

	if got := code(); got != http.StatusOK {
		t.Fatalf("status %d with no certificate, want 200", got)
	}

	// Turning TLS on, live. The same handler must now redirect.
	if err := keeper.SetFiles(certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	if got := code(); got != http.StatusMovedPermanently {
		t.Errorf("status %d after a certificate was applied, want 301 — the handler is still "+
			"deciding from the state it was built in", got)
	}

	// And the opt-in, live, over the top of it.
	optIn = true
	if got := code(); got != http.StatusOK {
		t.Errorf("status %d after the opt-in went on, want 200 — the opt-in beats the redirect "+
			"(Operator ruling 2026-08-02) and that must hold at request time too", got)
	}

	// Turning TLS back OFF must stop the redirect, which is the direction that matters: a
	// redirect surviving the certificate would send every plain request into a handshake
	// nothing can complete, and there is no channel left to ask for a revert.
	optIn = false
	if err := keeper.SetFiles("", ""); err != nil {
		t.Fatal(err)
	}
	if got := code(); got != http.StatusOK {
		t.Errorf("status %d after the certificate was cleared, want 200 — the plain half is "+
			"redirecting to an https listener that can no longer complete a handshake", got)
	}
}

// The whole serve path, with NO certificate configured, over a real listener: plain http is
// served, and a ClientHello reaches the TLS half and fails there rather than being answered
// as a malformed HTTP request. Before quince#900 this install had no TLS half at all.
//
// Through serveBothProtocols rather than a mux assembled in the test, so what runs here is
// the wiring that ships.
func TestServeBothProtocolsBindsTheMuxWithNoCertificate(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "app") })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serveBothProtocols(ctx, ln, addr, app, tlsx.NewEmptyKeeper(),
			func() bool { return false }, func() bool { return false },
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/") //nolint:noctx // bounded by the client timeout
	if err != nil {
		t.Fatalf("plain http against an install with no certificate: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "app" {
		t.Errorf("status %d body %q, want 200 app", resp.StatusCode, string(body))
	}

	// A ClientHello now reaches a TLS server holding nothing. It must fail the handshake —
	// which is a DIFFERENT failure from the malformed-request rejection it used to get, and
	// the wire-behaviour change quince#900 states as a cost.
	tlsConn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // the point is that the SERVER refuses
	if err == nil {
		_ = tlsConn.Close()
		t.Error("the handshake SUCCEEDED against an install with no certificate configured")
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("shutdown returned %v", err)
	}
}

// The fifth consumer of the config seam: `sessions.allow_insecure_transport` applied live, in
// BOTH directions, with the relaxation surfaced on the way up.
//
// The down direction is the one that did not exist. `applyInsecureTransportOptIn` returned
// before its setter when the opt-in was off, so nothing in a running process could lower it —
// and lowering it is the last step of applying a certificate and keeping it.
func TestInsecureTransportOptInAppliesLiveInBothDirections(t *testing.T) {
	lan := httptest.NewRequest("GET", "http://quince.example:8968/api/health", nil)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")

	cfgSvc := config.NewService(cfgPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Validate refuses a config with no storage declared, and every Replace below goes
	// through it — so the fixture carries one. It is the section this test does not touch.
	base := cfgSvc.Current()
	base.Storage = &[]config.StorageEntry{{Name: "test", Path: filepath.Join(dir, "backups"), Default: true, Backend: "copy"}}
	authSvc := newDemoAuth(t)
	subscribeInsecureTransport(cfgSvc, authSvc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !authSvc.Secure(lan) {
		t.Fatal("Secure was already relaxed before anything was written")
	}

	on := base
	on.Sessions.AllowInsecureTransport = true
	errs, warns, err := cfgSvc.Replace(on, "test")
	if err != nil || len(errs) > 0 {
		t.Fatalf("turning the opt-in on: errs=%v err=%v", errs, err)
	}
	if authSvc.Secure(lan) {
		t.Error("the opt-in was written and did NOT take effect — it still needs a restart")
	}
	var named bool
	for _, w := range warns {
		if w.Path == "sessions.allow_insecure_transport" {
			named = true
		}
	}
	if !named {
		t.Errorf("relaxing the baseline returned no warning naming the setting: %+v — a "+
			"security relaxation that takes effect quietly is indistinguishable from a bug", warns)
	}

	off := base
	off.Sessions.AllowInsecureTransport = false
	if _, warns, err = cfgSvc.Replace(off, "test"); err != nil {
		t.Fatalf("turning the opt-in off: %v", err)
	}
	if !authSvc.Secure(lan) {
		t.Error("the opt-in was turned OFF and the relaxation survived it — the latch this " +
			"change exists to remove is still there")
	}
	for _, w := range warns {
		if w.Path == "sessions.allow_insecure_transport" {
			t.Errorf("turning the degraded mode off still warns about it: %q", w.Message)
		}
	}
}

// `tls.cert_file`/`.key_file` applied LIVE — the last thing quince#900 needs (the mux is
// already bound whether or not a certificate exists). Three transitions, and the middle one is
// the reason the applier warns rather than refuses.
func TestTLSPathsApplyLive(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeTestPair(t, dir, "applied")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfgSvc := config.NewService(filepath.Join(dir, "config.yml"), quiet)
	keeper := tlsx.NewEmptyKeeper()
	subscribeTLS(cfgSvc, keeper, quiet)

	// Validate refuses a config with no storage declared, and every Replace below goes through
	// it — so the fixture carries one. It is the section this test does not touch.
	base := cfgSvc.Current()
	base.Storage = &[]config.StorageEntry{{Name: "test", Path: filepath.Join(dir, "backups"), Default: true, Backend: "copy"}}

	if keeper.HasCertificate() {
		t.Fatal("the keeper started with a certificate; this test cannot observe one arriving")
	}

	// ON — the transition the whole issue exists for.
	on := base
	on.TLS.CertFile, on.TLS.KeyFile = certFile, keyFile
	errs, warns, err := cfgSvc.Replace(on, "test")
	if err != nil || len(errs) > 0 {
		t.Fatalf("turning TLS on: errs=%v err=%v", errs, err)
	}
	if len(warns) > 0 {
		t.Errorf("applying a usable certificate warned: %+v", warns)
	}
	if !keeper.HasCertificate() {
		t.Fatal("the certificate was written and NOT applied — turning TLS on still needs a restart")
	}

	// A BAD EDIT: saved, warned, and the incumbent keeps serving. The applier cannot refuse —
	// the file is already on disk — so the warning is the whole of the honesty here.
	bad := base
	bad.TLS.CertFile, bad.TLS.KeyFile = filepath.Join(dir, "gone.pem"), filepath.Join(dir, "gone.key")
	if _, warns, err = cfgSvc.Replace(bad, "test"); err != nil {
		t.Fatalf("writing an unusable pair: %v", err)
	}
	if !keeper.HasCertificate() {
		t.Error("a bad path edit took TLS DOWN — a config typo must not cost the daemon its https")
	}
	var named bool
	for _, w := range warns {
		if strings.Contains(w.Message, "NOT applied") {
			named = true
		}
	}
	if !named {
		t.Errorf("an unapplied certificate produced no warning: %+v — the file says one thing "+
			"and the process is doing another, silently", warns)
	}

	// OFF — clearing both keys drops the certificate, which is the revert direction.
	off := base
	if _, _, err = cfgSvc.Replace(off, "test"); err != nil {
		t.Fatalf("turning TLS off: %v", err)
	}
	if keeper.HasCertificate() {
		t.Error("clearing tls.cert_file left the certificate in place, so nothing can turn TLS " +
			"back off without a restart")
	}
}

// The two appliers compose into the flow quince#900 exists to unblock: apply a certificate,
// have the plain half start redirecting, then take the plaintext opt-in back down — all in one
// process. Asserted end to end because each half was proved separately above, and the claim
// that matters is that they agree.
func TestCertificateAppliedThenOptInWithdrawnWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeTestPair(t, dir, "flow")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfgSvc := config.NewService(filepath.Join(dir, "config.yml"), quiet)
	authSvc := newDemoAuth(t)
	keeper := tlsx.NewEmptyKeeper()
	subscribeTLS(cfgSvc, keeper, quiet)
	subscribeInsecureTransport(cfgSvc, authSvc, quiet)

	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "app") })
	plain := plainHalf(app, keeper, func() bool { return cfgSvc.Current().Sessions.AllowInsecureTransport }, func() bool { return cfgSvc.Current().TLS.Enabled() })
	code := func() int {
		rec := httptest.NewRecorder()
		plain.ServeHTTP(rec, httptest.NewRequest("GET", "http://quince.example:8968/", nil))
		return rec.Code
	}

	base := cfgSvc.Current()
	base.Storage = &[]config.StorageEntry{{Name: "test", Path: filepath.Join(dir, "backups"), Default: true, Backend: "copy"}}

	// Where a first-run install on a LAN starts: no certificate, and the opt-in on so the
	// cookie survives plain http.
	start := base
	start.Sessions.AllowInsecureTransport = true
	if _, _, err := cfgSvc.Replace(start, "test"); err != nil {
		t.Fatal(err)
	}
	if got := code(); got != http.StatusOK {
		t.Fatalf("status %d before any certificate, want 200", got)
	}
	if authSvc.Secure(httptest.NewRequest("GET", "http://quince.example:8968/", nil)) {
		t.Fatal("the opt-in did not reach the auth service")
	}

	// Apply the certificate. The opt-in still beats the redirect, per the Operator's ruling.
	withCert := start
	withCert.TLS.CertFile, withCert.TLS.KeyFile = certFile, keyFile
	if _, _, err := cfgSvc.Replace(withCert, "test"); err != nil {
		t.Fatal(err)
	}
	if got := code(); got != http.StatusOK {
		t.Errorf("status %d with a certificate AND the opt-in, want 200 — the opt-in beats the "+
			"redirect and applying a certificate must not silently override it", got)
	}

	// Withdraw the opt-in: the last step of keeping the certificate, and the one that was
	// impossible in a running process before quince#900.
	final := withCert
	final.Sessions.AllowInsecureTransport = false
	if _, _, err := cfgSvc.Replace(final, "test"); err != nil {
		t.Fatal(err)
	}
	if got := code(); got != http.StatusMovedPermanently {
		t.Errorf("status %d after the opt-in was withdrawn, want 301", got)
	}
	if !authSvc.Secure(httptest.NewRequest("GET", "http://quince.example:8968/", nil)) {
		t.Error("the cookie relaxation survived the setting being withdrawn — the two halves of " +
			"one security setting have gone out of step, which is the failure this PR pair " +
			"exists to make impossible")
	}
}

// quince#916 review: the not-applied warning must not claim an incumbent that does not exist.
//
// THE SEQUENCE THE APPLIER TEST ABOVE DOES NOT REACH is the ordinary first-run mistake — TLS
// turned on for the FIRST time with a typo in the path. The Keeper holds nothing, so "quince is
// still serving the certificate it had" names a certificate that never existed and "https is
// unchanged" means unchanged from not working at all. A user reading that has no reason to think
// their https is broken, because they believe they have some.
func TestTLSNotAppliedWarningDoesNotInventAnIncumbent(t *testing.T) {
	dir := t.TempDir()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfgSvc := config.NewService(filepath.Join(dir, "config.yml"), quiet)
	keeper := tlsx.NewEmptyKeeper()
	subscribeTLS(cfgSvc, keeper, quiet)

	base := cfgSvc.Current()
	base.Storage = &[]config.StorageEntry{{Name: "test", Path: filepath.Join(dir, "backups"), Default: true, Backend: "copy"}}

	// FIRST configuration, and it is wrong. Nothing has ever been loaded.
	first := base
	first.TLS.CertFile, first.TLS.KeyFile = filepath.Join(dir, "typo.pem"), filepath.Join(dir, "typo.key")
	_, warns, err := cfgSvc.Replace(first, "test")
	if err != nil {
		t.Fatal(err)
	}
	if keeper.HasCertificate() {
		t.Fatal("the keeper loaded something from paths that do not exist")
	}

	msg := tlsWarning(t, warns)
	if strings.Contains(msg, "still serving the certificate it had") {
		t.Errorf("the warning claims an incumbent certificate on a FIRST configuration, where "+
			"there has never been one:\n  %s", msg)
	}
	if strings.Contains(msg, "https is unchanged") {
		t.Errorf("the warning says https is unchanged, which on this path means unchanged from "+
			"not working at all:\n  %s", msg)
	}
	if !strings.Contains(msg, "https is not working") {
		t.Errorf("the warning does not say that https is not working, which is the one fact the "+
			"user needs:\n  %s", msg)
	}

	// …and the OTHER case still says what it always said, because it is true there. A one-sided
	// test would pass against a message that had simply lost the incumbent sentence entirely.
	good := base
	good.TLS.CertFile, good.TLS.KeyFile = writeTestPair(t, dir, "incumbent")
	if _, _, err := cfgSvc.Replace(good, "test"); err != nil {
		t.Fatal(err)
	}
	if !keeper.HasCertificate() {
		t.Fatal("the good pair did not load, so the incumbent case cannot be observed")
	}
	broken := base
	broken.TLS.CertFile, broken.TLS.KeyFile = filepath.Join(dir, "gone.pem"), filepath.Join(dir, "gone.key")
	if _, warns, err = cfgSvc.Replace(broken, "test"); err != nil {
		t.Fatal(err)
	}
	msg = tlsWarning(t, warns)
	if !strings.Contains(msg, "still serving the certificate it had") {
		t.Errorf("with an incumbent loaded the warning no longer says so:\n  %s", msg)
	}
}

// The warning names the key the USER edited. Both keys reach the same loader and the same error,
// so the fault cannot be attributed between them — but which one moved can be, and that is what
// a path on a warning is for (quince#916 review).
func TestTLSWarningNamesTheKeyThatMoved(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeTestPair(t, dir, "named")

	tests := []struct {
		name      string
		old, next config.TLSConfig
		want      string
	}{
		{
			name: "only the key file moved",
			old:  config.TLSConfig{CertFile: certFile, KeyFile: keyFile},
			next: config.TLSConfig{CertFile: certFile, KeyFile: "/tmp/other.key"},
			want: "tls.key_file",
		},
		{
			name: "only the cert file moved",
			old:  config.TLSConfig{CertFile: certFile, KeyFile: keyFile},
			next: config.TLSConfig{CertFile: "/tmp/other.pem", KeyFile: keyFile},
			want: "tls.cert_file",
		},
		{
			name: "both moved — turning TLS on, where either answer is equally true",
			old:  config.TLSConfig{},
			next: config.TLSConfig{CertFile: certFile, KeyFile: keyFile},
			want: "tls.cert_file",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tlsWarningPath(tc.old, tc.next); got != tc.want {
				t.Errorf("tlsWarningPath = %q, want %q", got, tc.want)
			}
		})
	}
}

// tlsWarning returns the one warning under a `tls.*` path, failing if there is not exactly one.
func tlsWarning(t *testing.T, warns []config.Warning) string {
	t.Helper()
	var found []string
	for _, w := range warns {
		if strings.HasPrefix(w.Path, "tls.") {
			found = append(found, w.Message)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one tls warning, got %d: %+v", len(found), warns)
	}
	return found[0]
}

// A CERTIFICATE TRIAL MUST NOT EMIT 301 — Operator ruling 2026-08-17 (quince#1157), amending the
// ruling that chose an unconditional permanent redirect.
//
// WHY IT NEEDS A GATE RATHER THAN A WALK, in the ruling's own words: the regression is invisible in
// a browser until the window closes. A trial serves a certificate and rolls it back BY ITSELF, and
// a browser that cached a permanent upgrade in between keeps upgrading to an origin that has
// stopped existing — with no error naming a cause, because from the browser's side the connection
// simply fails.
//
// THE TRIAL IS REPRODUCED THE WAY `certTrial` PERFORMS ONE: point the keeper at a pair and leave
// `config.yml` alone. That is not a shortcut for the test's convenience — it is the whole mechanism
// (*"we're not going to actually write tls setting entry to config.yml for that"*), and it is why
// the config is the right predicate.
func TestATrialRedirectsTemporarilyAndAConfirmedPairPermanently(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeTestPair(t, dir, "trial")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfgSvc := config.NewService(filepath.Join(dir, "config.yml"), quiet)
	keeper := tlsx.NewEmptyKeeper()
	subscribeTLS(cfgSvc, keeper, quiet)

	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "app") })
	plain := plainHalf(app, keeper,
		func() bool { return false },
		func() bool { return cfgSvc.Current().TLS.Enabled() })
	code := func() int {
		rec := httptest.NewRecorder()
		plain.ServeHTTP(rec, httptest.NewRequest("GET", "http://quince.example:8968/devices", nil))
		return rec.Code
	}

	base := cfgSvc.Current()
	base.Storage = &[]config.StorageEntry{{Name: "test", Path: filepath.Join(dir, "backups"), Default: true, Backend: "copy"}}
	if _, _, err := cfgSvc.Replace(base, "test"); err != nil {
		t.Fatal(err)
	}
	// The control at the bottom: no certificate at all, so nothing redirects and a status
	// assertion below cannot pass because the handler is inert.
	if got := code(); got != http.StatusOK {
		t.Fatalf("status %d with no certificate, want 200 — the app should be served, not redirected", got)
	}

	// THE TRIAL. `SetFiles` directly, with `config.yml` untouched — exactly what `certTrial` does.
	if err := keeper.SetFiles(certFile, keyFile); err != nil {
		t.Fatalf("applying the trial pair: %v", err)
	}
	if !keeper.HasCertificate() {
		t.Fatal("the trial pair did not load, so the redirect below would be proving nothing")
	}
	if got := code(); got != http.StatusTemporaryRedirect {
		t.Errorf("a live trial redirected with %d, want 307. A 301 here is cached permanently, and "+
			"the trial rolls back by itself when the window closes — leaving the browser upgrading to an "+
			"origin that no longer answers (quince#1157)", got)
	}

	// THE CONFIRM. Now `config.yml` names the pair, which is the ruling's predicate — and the
	// state is permanent, so the permanent redirect is correct again.
	confirmed := base
	confirmed.TLS.CertFile, confirmed.TLS.KeyFile = certFile, keyFile
	if _, _, err := cfgSvc.Replace(confirmed, "test"); err != nil {
		t.Fatal(err)
	}
	if got := code(); got != http.StatusMovedPermanently {
		t.Errorf("a configured pair redirected with %d, want 301 — the self-upgrading bookmark is a "+
			"benefit the ruling kept, not one it retreated from", got)
	}

	// AND THE ROLLBACK DIRECTION, because the predicate must move BOTH ways. Clearing the pair
	// from the config puts this back to temporary while the certificate is still loaded, which is
	// the state an unconfirmed pair sits in.
	if _, _, err := cfgSvc.Replace(base, "test"); err != nil {
		t.Fatal(err)
	}
	if got := code(); got != http.StatusOK {
		t.Fatalf("status %d after clearing the pair, want 200 — subscribeTLS should have dropped "+
			"the certificate, so this test's last assertion is about the right thing", got)
	}
}

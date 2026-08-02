package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
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
	if err := svc.SetPassword("attacker-chosen"); !errors.Is(err, auth.ErrAlreadyConfigured) {
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

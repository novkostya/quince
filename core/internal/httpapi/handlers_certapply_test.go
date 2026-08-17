package httpapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
)

// quince#908 slice 5 — TRY A CERTIFICATE, PROVE IT OVER HTTPS, OR HAVE IT DROPPED.
//
// The suite is organised around what a reader cannot check by inspection: the pre-auth bound, that
// the apply writes NO configuration, that the proof must be quince's own TLS half rather than a
// forwarded header, and that the WINDOW rather than the timer decides whether a confirmation is
// still good.

const (
	certApplyPath   = "/api/onboarding/certificate/apply"
	certConfirmPath = "/api/onboarding/certificate/confirm"
)

// writeCertPair mints a self-signed pair IN PROCESS, as `tlsx`'s own suite does and for the reason
// its comment gives: shelling out to `openssl` would put a key path in argv on a project whose
// secrets rule is stdin/pty only.
func writeCertPair(t *testing.T, dir, cn string, notAfter time.Time) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
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

// fakeKeeper records what the running daemon was pointed at, without loading anything. That is the
// whole reason `certKeeper` is an interface: the trial's contract is *the daemon now serves this
// pair*, and proving it needs a record of the calls rather than a real handshake.
type fakeKeeper struct {
	mu    sync.Mutex
	calls [][2]string
	fail  error
}

func (k *fakeKeeper) SetFiles(certFile, keyFile string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail != nil {
		return k.fail
	}
	k.calls = append(k.calls, [2]string{certFile, keyFile})
	return nil
}

// HasCertificate is what `tlsUnusableCode` reads (quince#940 §1). It reports true once the fake has
// been pointed at a pair, which is what the real Keeper does after a load that succeeds.
func (k *fakeKeeper) HasCertificate() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.calls) > 0 && k.calls[len(k.calls)-1][0] != ""
}

func (k *fakeKeeper) last() ([2]string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.calls) == 0 {
		return [2]string{}, false
	}
	return k.calls[len(k.calls)-1], true
}

// fakeTimers stands in for time.AfterFunc so the ten-minute window can be reached without waiting.
//
// IT IS NO LONGER THE AUTHORITY, and the tests say so: since the deadline decides, a test can move
// the CLOCK without firing a timer and vice versa. Both are exercised, because the two failure modes
// they represent are different — a timer that fires late, and a timer that never fires at all.
type fakeTimers struct {
	mu   sync.Mutex
	fns  []func()
	stop []bool
}

func (f *fakeTimers) after(_ time.Duration, fn func()) trialTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fns = append(f.fns, fn)
	f.stop = append(f.stop, false)
	return &fakeTimer{owner: f, idx: len(f.fns) - 1}
}

// expire runs the nth armed timer's function, exactly as the runtime would. It runs it EVEN IF
// STOPPED, on purpose: the case the generation guard exists for is a timer that had already fired
// when `Stop` was called, so a fake that refused would make that path untestable.
func (f *fakeTimers) expire(t *testing.T, n int) {
	t.Helper()
	f.mu.Lock()
	if n >= len(f.fns) {
		f.mu.Unlock()
		t.Fatalf("no timer %d was armed (only %d)", n, len(f.fns))
	}
	fn := f.fns[n]
	f.mu.Unlock()
	fn()
}

func (f *fakeTimers) armed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.fns)
}

type fakeTimer struct {
	owner *fakeTimers
	idx   int
}

func (t *fakeTimer) Stop() bool {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	already := t.owner.stop[t.idx]
	t.owner.stop[t.idx] = true
	return !already
}

// certApplyDeps is a router whose trial the test drives: a fake Keeper, fake timers, and a clock it
// can move.
func certApplyDeps(t *testing.T) (Deps, *fakeKeeper, *fakeTimers, *time.Time) {
	t.Helper()
	deps := testDeps(t)
	keeper := &fakeKeeper{}
	timers := &fakeTimers{}
	now := time.Now()
	trial := newCertTrial(deps.Log, keeper)
	trial.afterFunc = timers.after
	trial.now = func() time.Time { return now }
	deps.Keeper = keeper
	deps.CertTrial = trial
	return deps, keeper, timers, &now
}

// postCertJSON does the double submit a browser does. The apply is NOT `csrfExempt` — it is
// same-origin with the page that sends it, so it holds a token.
func postCertJSON(t *testing.T, h http.Handler, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, "http://quince.example:8968/api/auth/status", nil))
	var token string
	for _, c := range warm.Result().Cookies() {
		if c.Name == auth.CSRFCookieName {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("no CSRF cookie was minted by a pre-auth GET")
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
	req.Header.Set(auth.CSRFHeaderName, token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func applyBody(certFile, keyFile, hostname string) string {
	b, _ := json.Marshal(map[string]string{"cert_file": certFile, "key_file": keyFile, "hostname": hostname})
	return string(b)
}

func decodeApplied(t *testing.T, rec *httptest.ResponseRecorder) certificateApplied {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — body %s", rec.Code, rec.Body.String())
	}
	var got certificateApplied
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — body %s", err, rec.Body.String())
	}
	return got
}

// THE PRE-AUTH BOUND, WHICH IS THE SECURITY CLAIM OF THE WHOLE SLICE.
func TestApplyAndConfirmCloseOnceTheInstallIsClaimed(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))

	for _, tc := range []struct{ name, path, body string }{
		{"a well-formed apply", certApplyPath, applyBody(certFile, keyFile, "quince.example")},
		{"an apply whose body does not parse", certApplyPath, `{"cert_file":`},
		{"an apply with a relative path, otherwise a 422", certApplyPath, `{"cert_file":"c.pem","key_file":"c.key"}`},
		{"a confirm", certConfirmPath, `{"token":"whatever"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _, _ := certApplyDeps(t)
			if err := deps.Auth.SetPassword("test", "127.0.0.1"); err != nil {
				t.Fatalf("SetPassword: %v", err)
			}
			rec := postCertJSON(t, NewRouter(deps), "http://quince.example:8968"+tc.path, tc.body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status %d, want 409 — body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// THE APPLY WRITES NO CONFIGURATION. This is the design in one assertion (Operator, 2026-08-14):
// the daemon is serving the pair and `config.yml` has not been touched, so an abandoned trial leaves
// nothing behind in a file D12 says holds only what the user set.
func TestAnApplyServesThePairAndWritesNoConfig(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps, keeper, timers, _ := certApplyDeps(t)

	got := decodeApplied(t, postCertJSON(t, NewRouter(deps), "http://192.0.2.5:8968"+certApplyPath,
		applyBody(certFile, keyFile, "quince.example")))

	if deps.Config.Current().TLS.Enabled() {
		t.Fatalf("config.yml was written by the APPLY: %+v", deps.Config.Current().TLS)
	}
	if got.ConfigWritten {
		t.Fatal("the response claims the config was written")
	}
	if pair, ok := keeper.last(); !ok || pair[0] != certFile || pair[1] != keyFile {
		t.Fatalf("the daemon was not pointed at the trial pair: %+v", pair)
	}
	if timers.armed() != 1 {
		t.Fatalf("%d timers armed, want 1", timers.armed())
	}
	// THE HOSTNAME IS THE CERTIFICATE'S AND THE PORT IS THE REQUEST'S. Sending the user to the IP
	// they are on now would produce a name mismatch for a certificate that is perfectly good; the
	// certificate's name on port 443 would reach nothing, because both protocols share one listener.
	if got.ConfirmOrigin != "https://quince.example:8968" {
		t.Fatalf("confirm_origin = %q", got.ConfirmOrigin)
	}
	if got.ExpiresSeconds != int(certTrialWindow/time.Second) {
		t.Fatalf("expires_seconds = %d", got.ExpiresSeconds)
	}
}

// THE PORT IS NEVER OMITTED, INCLUDING THE DEFAULT ONE. A bare scheme swap on `http://host` gives
// `https://host` — port 443, where quince is not listening, because both halves share one port.
func TestTheConfirmOriginAlwaysCarriesThePort(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))

	for _, tc := range []struct{ name, host, hostname, want string }{
		{"a default-port install", "quince.example", "quince.example", "https://quince.example:80"},
		{"no hostname given falls back to the request's", "192.0.2.5:8968", "", "https://192.0.2.5:8968"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _, _ := certApplyDeps(t)
			got := decodeApplied(t, postCertJSON(t, NewRouter(deps), "http://"+tc.host+certApplyPath,
				applyBody(certFile, keyFile, tc.hostname)))
			if got.ConfirmOrigin != tc.want {
				t.Fatalf("confirm_origin = %q, want %q", got.ConfirmOrigin, tc.want)
			}
		})
	}
}

// A PAIR THE OFFLINE CHECK WOULD REFUSE IS NEVER SERVED — re-checked here rather than trusted from
// the client, because the file can move between the probe and the apply.
func TestAnUnusablePairIsNeverServed(t *testing.T) {
	dir := t.TempDir()
	expired, expiredKey := writeCertPair(t, dir, "expired.example", time.Now().Add(-time.Minute))
	deps, keeper, timers, _ := certApplyDeps(t)

	rec := postCertJSON(t, NewRouter(deps), "http://quince.example:8968"+certApplyPath,
		applyBody(expired, expiredKey, "expired.example"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 — body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"outcome":"expired"`) {
		t.Fatalf("body does not carry the outcome: %s", rec.Body.String())
	}
	if _, ok := keeper.last(); ok {
		t.Fatal("the daemon was pointed at an expired certificate")
	}
	if timers.armed() != 0 {
		t.Fatal("a trial was started for an apply that did not happen")
	}
}

// THE PROOF IS QUINCE'S OWN TLS HALF. 426 is the code `refuseInsecureOrigin` already uses.
func TestAConfirmOverPlainHTTPIsRefusedAndDoesNotConsumeTheWindow(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps, _, _, _ := certApplyDeps(t)
	h := NewRouter(deps)

	got := decodeApplied(t, postCertJSON(t, h, "http://quince.example:8968"+certApplyPath,
		applyBody(certFile, keyFile, "quince.example")))

	rec := postCertJSON(t, h, "http://quince.example:8968"+certConfirmPath, `{"token":"`+got.ConfirmToken+`"}`)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status %d, want 426 — body %s", rec.Code, rec.Body.String())
	}
	// A REFUSED CONFIRMATION MUST NOT BURN THE ATTEMPT — a user who clicked the wrong link would
	// otherwise have to start again.
	if _, live := deps.CertTrial.pending(); !live {
		t.Fatal("the trial ended because a confirmation was refused")
	}
}

// `X-Forwarded-Proto` IS NOT EVIDENCE HERE, and it is the obvious wrong choice because every other
// "is this secure" question in this product uses `auth.SecureOrigin`, which believes it.
func TestAForwardedProtoHeaderDoesNotConfirmACertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps, _, _, _ := certApplyDeps(t)
	proxies, err := auth.NewTrustedProxies([]string{"192.0.2.1/32"})
	if err != nil {
		t.Fatalf("trusted proxies: %v", err)
	}
	deps.Proxies = proxies
	h := NewRouter(deps)

	got := decodeApplied(t, postCertJSON(t, h, "http://quince.example:8968"+certApplyPath,
		applyBody(certFile, keyFile, "quince.example")))

	req := httptest.NewRequest(http.MethodPost, "http://quince.example:8968"+certConfirmPath,
		strings.NewReader(`{"token":"`+got.ConfirmToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.RemoteAddr = "192.0.2.1:4444"

	// THE TRAP MUST BE ARMED, AND ASSERTING IT IS THE WHOLE VALUE OF THIS TEST. Without this line
	// the 426 below is green whether the handler ignores the header or consults it — because a
	// request the proxy list does NOT trust is insecure by every reading, and the test would be
	// proving that an untrusted header is untrusted.
	if !auth.SecureOrigin(req, proxies) {
		t.Fatal("SecureOrigin is false, so this is not the case under test")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status %d, want 426 — a forwarded header confirmed a certificate that never served", rec.Code)
	}
}

// THE CONFIRM IS WHAT WRITES `config.yml`, and it is the first write in the whole ceremony.
func TestAConfirmOverTheTLSHalfWritesTheConfig(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps, _, timers, _ := certApplyDeps(t)
	h := NewRouter(deps)

	plain := httptest.NewServer(h)
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(h)
	defer tlsSrv.Close()

	start := time.Now()
	got := decodeApplied(t, postCertJSON(t, h, plain.URL+certApplyPath, applyBody(certFile, keyFile, "quince.example")))
	resp, err := tlsSrv.Client().Post(tlsSrv.URL+certConfirmPath, "application/json",
		strings.NewReader(`{"token":"`+got.ConfirmToken+`"}`))
	if err != nil {
		t.Fatalf("confirm over TLS: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if live := deps.Config.Current().TLS; live.CertFile != certFile || live.KeyFile != keyFile {
		t.Fatalf("config.yml = %+v, want the confirmed pair", live)
	}
	// AND THE LATE TIMER CHANGES NOTHING. Firing it now is what the runtime does when `Stop` lost
	// the race; the generation check is what makes it harmless.
	timers.expire(t, 0)
	if live := deps.Config.Current().TLS; live.CertFile != certFile {
		t.Fatalf("a confirmed certificate was undone by its own late timer: %+v", live)
	}

	// THE MEASUREMENT THE RULING ASKED FOR — `certTrialWindow` is ten minutes and the number had to
	// be brought back rather than inherited. REPORTED, not asserted against a threshold: a timing
	// assertion on a shared box is a flake, and the claim is "negligible", not "under N ms".
	t.Logf("apply → https confirm round trip: %v (handshake included) — the ten-minute window is human time, not this",
		elapsed.Round(time.Microsecond))
}

// THE WINDOW DECIDES, NOT THE TIMER — the hole the Operator's passive-expiry question found
// (2026-08-14).
//
// A timer fires LATE: a GC pause, a loaded box, a suspended VM. The first version of `confirm`
// checked only *is a trial live*, so a confirmation arriving after the deadline but before the
// callback ran would SUCCEED and write a certificate whose trial had already expired into
// `config.yml`. Here the clock moves past the deadline and the timer is deliberately NOT fired.
func TestAConfirmAfterTheDeadlineIsRefusedEvenIfTheTimerHasNotRun(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps, _, timers, now := certApplyDeps(t)
	h := NewRouter(deps)
	tlsSrv := httptest.NewTLSServer(h)
	defer tlsSrv.Close()

	got := decodeApplied(t, postCertJSON(t, h, "http://quince.example:8968"+certApplyPath,
		applyBody(certFile, keyFile, "quince.example")))

	*now = now.Add(certTrialWindow + time.Second) // the window closes; the timer does NOT run
	if timers.armed() != 1 {
		t.Fatal("no timer was armed, so this proves nothing about one that has not fired")
	}

	resp, err := tlsSrv.Client().Post(tlsSrv.URL+certConfirmPath, "application/json",
		strings.NewReader(`{"token":"`+got.ConfirmToken+`"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409 — an expired trial was confirmed because its timer was late", resp.StatusCode)
	}
	if deps.Config.Current().TLS.Enabled() {
		t.Fatalf("an expired trial wrote config.yml: %+v", deps.Config.Current().TLS)
	}
	// AND HEALTH AGREES, rather than advertising a window that has closed.
	if _, live := deps.CertTrial.pending(); live {
		t.Fatal("pending() reports a live trial past its deadline")
	}
}

// AN UNCONFIRMED TRIAL PUTS THE DAEMON BACK, AND STILL WRITES NOTHING.
func TestAnUnconfirmedTrialReturnsTheDaemonToTheConfiguredPair(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps, keeper, timers, _ := certApplyDeps(t)

	postCertJSON(t, NewRouter(deps), "http://quince.example:8968"+certApplyPath,
		applyBody(certFile, keyFile, "quince.example"))
	timers.expire(t, 0)

	// A first-run install has no certificate, so "the pair config.yml names" is the empty one —
	// which `Keeper.SetFiles` documents as "turn TLS off".
	if pair, ok := keeper.last(); !ok || pair[0] != "" || pair[1] != "" {
		t.Fatalf("the daemon was left on %+v, want the empty pair", pair)
	}
	if deps.Config.Current().TLS.Enabled() {
		t.Fatal("an abandoned trial wrote config.yml")
	}
}

// A SUPERSEDED TRIAL'S TIMER MUST NOT DROP THE ONE THAT REPLACED IT.
func TestASupersededTimerDoesNotDropTheTrialThatReplacedIt(t *testing.T) {
	dir := t.TempDir()
	one, oneKey := writeCertPair(t, dir, "one.example", time.Now().Add(24*time.Hour))
	two, twoKey := writeCertPair(t, dir, "two.example", time.Now().Add(24*time.Hour))
	deps, keeper, timers, _ := certApplyDeps(t)
	h := NewRouter(deps)

	postCertJSON(t, h, "http://quince.example:8968"+certApplyPath, applyBody(one, oneKey, "one.example"))
	postCertJSON(t, h, "http://quince.example:8968"+certApplyPath, applyBody(two, twoKey, "two.example"))

	timers.expire(t, 0) // the FIRST trial's timer, which nothing is waiting on any more

	if pair, ok := keeper.last(); !ok || pair[0] != two {
		t.Fatalf("the daemon was left on %+v, want the SECOND pair — a stale timer dropped it", pair)
	}
	if _, live := deps.CertTrial.pending(); !live {
		t.Fatal("a stale timer ended the current trial")
	}
}

// A ROUTER WITH NO TLS LISTENER REFUSES HONESTLY — `no silent caps or fallbacks`.
func TestAnApplyWithNoTLSListenerAnswers503(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps := testDeps(t) // no Keeper: NewRouter substitutes the stand-in

	rec := postCertJSON(t, NewRouter(deps), "http://quince.example:8968"+certApplyPath,
		applyBody(certFile, keyFile, "quince.example"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 — body %s", rec.Code, rec.Body.String())
	}
	if !errors.Is(unavailableKeeper{}.SetFiles("a", "b"), errNoKeeper) {
		t.Fatal("the stand-in does not return errNoKeeper, so the 503 above is a coincidence")
	}
}

// THE TRIAL IS VISIBLE, which is what keeps it from being hidden state: for up to ten minutes the
// daemon serves a certificate `config.yml` does not name, and something has to say so.
func TestHealthReportsALiveTrial(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))
	deps, _, _, now := certApplyDeps(t)
	h := NewRouter(deps)

	var before HealthResponse
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://quince.example:8968/api/health", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.TLSTrialExpiresAt != "" {
		t.Fatalf("health reports a trial before one exists: %q", before.TLSTrialExpiresAt)
	}

	postCertJSON(t, h, "http://quince.example:8968"+certApplyPath, applyBody(certFile, keyFile, "quince.example"))

	var during HealthResponse
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://quince.example:8968/api/health", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &during); err != nil {
		t.Fatal(err)
	}
	if during.TLSTrialExpiresAt == "" {
		t.Fatal("health does not report the live trial: the divergence between config and what is served is invisible")
	}
	if _, err := time.Parse(time.RFC3339, during.TLSTrialExpiresAt); err != nil {
		t.Fatalf("tls_trial_expires_at %q is not RFC3339: %v", during.TLSTrialExpiresAt, err)
	}

	// AND IT STOPS REPORTING WHEN THE WINDOW CLOSES, timer or no timer.
	*now = now.Add(certTrialWindow + time.Second)
	var after HealthResponse
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://quince.example:8968/api/health", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.TLSTrialExpiresAt != "" {
		t.Fatalf("health still advertises a closed window: %q", after.TLSTrialExpiresAt)
	}
}

// THE APPLY SAYS WHETHER THE PAIR COVERS THE ADDRESS IT IS SENDING THE USER TO, and it composes that
// address from the same two inputs — so it always could, and never did.
//
// FALSE IS NOT A REFUSAL. The trial still starts: a browser interstitial the user accepts is a
// legitimate install (a self-signed pair, an IP-only LAN), and the ten-minute rollback is what makes
// trying one safe. The field exists so the trial screen can stop asserting coverage it never checked.
func TestTheApplyReportsWhetherTheConfirmOriginIsCovered(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))

	for _, tc := range []struct {
		name, host, hostname, wantOrigin string
		wantCovered                      bool
	}{
		// THE CASE FROM THE WALK: a certificate for a name, applied from an IP with the field left
		// empty, so the confirm link points at an address it cannot cover.
		{"no name given, standing on an IP", "192.0.2.5:8968", "", "https://192.0.2.5:8968", false},
		// AND THE CASE THE FIELD IS OPTIONAL FOR: already on a covered name, nothing to type.
		{"no name given, standing on a covered name", "quince.example:8968", "", "https://quince.example:8968", true},
		{"a covered name typed", "192.0.2.5:8968", "quince.example", "https://quince.example:8968", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _, _ := certApplyDeps(t)
			got := decodeApplied(t, postCertJSON(t, NewRouter(deps), "http://"+tc.host+certApplyPath,
				applyBody(certFile, keyFile, tc.hostname)))

			if got.ConfirmOrigin != tc.wantOrigin {
				t.Fatalf("confirm_origin = %q, want %q", got.ConfirmOrigin, tc.wantOrigin)
			}
			if got.ConfirmHostCovered != tc.wantCovered {
				t.Errorf("confirm_host_covered = %v, want %v for %s",
					got.ConfirmHostCovered, tc.wantCovered, tc.wantOrigin)
			}
		})
	}
}

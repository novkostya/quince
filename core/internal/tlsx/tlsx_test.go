package tlsx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writePair mints a self-signed certificate IN PROCESS and writes the PEM pair.
//
// crypto/x509 rather than shelling out to `openssl`, which the spec's Rule check names as
// the obvious wrong turn: it would put a key path in argv on a project whose secrets rule is
// stdin/pty only. Nothing here is a secret — these are throwaway test keys — but the habit
// is what the rule protects.
func writePair(t *testing.T, dir, cn string) (certFile, keyFile string) {
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
		NotAfter:     time.Now().Add(24 * time.Hour),
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
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// Story 5: an unusable certificate must be refused with the FILE and the REASON named. All
// three cases, because they fail at different layers of tls.LoadX509KeyPair and the standard
// library's message for the third contains no path at all.
func TestNewKeeperRefusesUnusablePairs(t *testing.T) {
	dir := t.TempDir()
	goodCert, goodKey := writePair(t, dir, "good")
	_, otherKey := writePair(t, dir, "other")

	bogus := filepath.Join(dir, "bogus.pem")
	if err := os.WriteFile(bogus, []byte("this is not pem\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		cert, key  string
		wantSubstr string
	}{
		{
			name: "missing file",
			cert: filepath.Join(dir, "nope.pem"), key: goodKey,
			wantSubstr: "cannot read",
		},
		{
			name: "malformed pem",
			cert: bogus, key: goodKey,
			wantSubstr: "not a usable certificate/key pair",
		},
		{
			name: "key does not match cert",
			cert: goodCert, key: otherKey,
			wantSubstr: "not a usable certificate/key pair",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewKeeper(tc.cert, tc.key)
			if err == nil {
				t.Fatal("an unusable pair was accepted")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err, tc.wantSubstr)
			}
			// The mismatch case is the one the standard library reports with no path, so
			// naming a file is the thing this mapping exists to add.
			if tc.name == "key does not match cert" && !strings.Contains(err.Error(), goodCert) {
				t.Errorf("the mismatch error does not name the certificate file: %q", err)
			}
		})
	}
}

// Story 6: the certificate rotates under a running process — files replaced, next handshake
// serves the new one, no restart and no signal.
func TestKeeperRotatesWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "first")

	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewKeeper: %v", err)
	}
	before, err := k.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cn := leafCN(t, before); cn != "first" {
		t.Fatalf("served CN = %q, want first", cn)
	}

	// Rewrite IN PLACE, which is what acme.sh does — same paths, new content.
	newCert, newKey := writePair(t, dir, "second")
	renewInPlace(t, newCert, certFile)
	renewInPlace(t, newKey, keyFile)

	// The premise, asserted rather than assumed: the rewrite is OBSERVABLE. Without renewInPlace's
	// mtime bump both stamps can be unchanged, and then `changed()` is correctly false, the old
	// certificate is correctly served, and the assertion below fails for a reason that has nothing
	// to do with rotation — 13 times in 300 (quince#786). Said here so that if this regresses it
	// reports what actually went wrong.
	if !k.changed() {
		t.Fatal("the rewrite left both (mtime, size) stamps unchanged, so there is nothing for the " +
			"Keeper to notice — this test would be asserting an event that did not happen (quince#786)")
	}

	after, err := k.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after rotation: %v", err)
	}
	if cn := leafCN(t, after); cn != "second" {
		t.Fatalf("after rotation the served CN is %q — still the old certificate, which is "+
			"the defect this exists to prevent: it would serve an expired cert with no log", cn)
	}
}

// A failed reload must keep serving the old certificate rather than failing the handshake.
// Mid-renewal a key can be half-written, and refusing would take the UI down for as long as
// that lasts — a self-inflicted outage on the deployment rotation exists for.
func TestKeeperKeepsServingWhenReloadFails(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "first")
	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	var reported error
	k.OnReloadError = func(e error) { reported = e }

	// Truncate the key, as a half-finished write would.
	if err := os.WriteFile(keyFile, []byte("-----BEGIN EC PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := k.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate refused during a broken rotation: %v", err)
	}
	if cn := leafCN(t, cert); cn != "first" {
		t.Errorf("served CN = %q, want the cached first", cn)
	}
	if reported == nil {
		t.Error("the failed reload was swallowed — OnReloadError never fired, so a genuinely " +
			"broken rotation would leave no trail")
	}
}

// quince#900: an install that has configured NO certificate must still produce a serving
// Keeper. Before this the only constructor loaded a pair or failed, so "no TLS" was expressed
// as a nil *Keeper and a branch at the bind — which is exactly what made turning TLS on need a
// restart.
func TestEmptyKeeperHoldsNothingAndSaysSo(t *testing.T) {
	k := NewEmptyKeeper()

	if k.HasCertificate() {
		t.Error("a Keeper constructed with no pair reports that it has a certificate")
	}
	if _, err := k.GetCertificate(nil); err == nil {
		t.Fatal("GetCertificate returned a certificate from a Keeper that holds none")
	}
	// changed() must not thrash on the empty pair: it stats nothing, so a handshake against an
	// unconfigured install costs no filesystem call at all.
	if k.changed() {
		t.Error("changed() reports a change on a Keeper with no files configured")
	}
}

// The handshake half of the above, over a real client, because "GetCertificate returns an
// error" and "the TLS server refuses the connection" are different claims and the second is
// the one the mux's TLS half now depends on.
func TestEmptyKeeperFailsTheHandshakeRatherThanServing(t *testing.T) {
	k := NewEmptyKeeper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "served") }),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{GetCertificate: k.GetCertificate, MinVersion: tls.VersionTLS12},
	}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // the point is that the SERVER refuses
	}
	resp, err := client.Get("https://" + ln.Addr().String() + "/") //nolint:noctx // t.Cleanup closes it
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the handshake SUCCEEDED against a server holding no certificate — a client " +
			"would take that for a working TLS deployment")
	}
}

// quince#900 item 3: the Keeper learns new PATHS at runtime, which is what makes
// `tls.cert_file`/`.key_file` live. Rotation already worked and is a different thing — same
// paths, new content.
func TestKeeperLearnsNewPathsAtRuntime(t *testing.T) {
	dir := t.TempDir()
	firstCert, firstKey := writePair(t, dir, "first")
	secondCert, secondKey := writePair(t, dir, "second")

	k, err := NewKeeper(firstCert, firstKey)
	if err != nil {
		t.Fatal(err)
	}
	before, err := k.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cn := leafCN(t, before); cn != "first" {
		t.Fatalf("served CN = %q, want first", cn)
	}

	if err := k.SetFiles(secondCert, secondKey); err != nil {
		t.Fatalf("SetFiles to a usable pair: %v", err)
	}
	after, err := k.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cn := leafCN(t, after); cn != "second" {
		t.Errorf("after a path change the served CN is %q, want second — the Keeper is still "+
			"serving the pair it was constructed with", cn)
	}
}

// TLS OFF, LIVE. This is the direction the whole feature is for: an operator applies a
// certificate, it does not work, and the setting goes back. Nothing in the process could lower
// it before.
func TestKeeperSetFilesEmptyTurnsTLSOff(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "going-away")

	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if !k.HasCertificate() {
		t.Fatal("a freshly loaded Keeper reports no certificate")
	}

	if err := k.SetFiles("", ""); err != nil {
		t.Fatalf("clearing the pair returned an error: %v", err)
	}
	if k.HasCertificate() {
		t.Error("the certificate survived being cleared, so nothing in a running process can " +
			"turn TLS back off")
	}
	if _, err := k.GetCertificate(nil); err == nil {
		t.Error("GetCertificate still serves a certificate after the pair was cleared — the " +
			"files are gone from the config and the handshake would still succeed")
	}
	// The files are untouched on disk; it is the CONFIG that stopped naming them.
	if _, err := os.Stat(certFile); err != nil {
		t.Errorf("clearing the config disturbed the certificate file: %v", err)
	}
}

// A live path edit to something unusable must NOT take the daemon's TLS down, and must not be
// swallowed either. The applier that calls this cannot refuse the write — the file is already
// saved — so the error is a warning to surface, and the old certificate goes on serving.
func TestKeeperSetFilesKeepsTheOldCertificateWhenTheNewPairIsUnusable(t *testing.T) {
	dir := t.TempDir()
	goodCert, goodKey := writePair(t, dir, "incumbent")
	k, err := NewKeeper(goodCert, goodKey)
	if err != nil {
		t.Fatal(err)
	}

	missingCert := filepath.Join(dir, "not-yet.pem")
	missingKey := filepath.Join(dir, "not-yet.key")
	err = k.SetFiles(missingCert, missingKey)
	if err == nil {
		t.Fatal("SetFiles accepted a pair that does not exist, so the operator would be told " +
			"nothing while TLS quietly stayed on the old certificate")
	}
	if !strings.Contains(err.Error(), "cannot read") {
		t.Errorf("the error does not name the fault: %q", err)
	}
	cert, err := k.GetCertificate(nil)
	if err != nil {
		t.Fatalf("TLS went down on a bad config edit: %v", err)
	}
	if cn := leafCN(t, cert); cn != "incumbent" {
		t.Errorf("served CN = %q, want the cached incumbent", cn)
	}

	// SELF-HEALING, which is the reason the failed SetFiles keeps the new paths: the operator
	// wrote the config before copying the files in, and copying them in is enough.
	arrivingCert, arrivingKey := writePair(t, dir, "arrived")
	renewInPlace(t, arrivingCert, missingCert)
	renewInPlace(t, arrivingKey, missingKey)

	healed, err := k.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after the files arrived: %v", err)
	}
	if cn := leafCN(t, healed); cn != "arrived" {
		t.Errorf("the served CN is %q — the Keeper never picked up the pair the config names, "+
			"so the operator has to restart after all", cn)
	}
}

// The paths are mutable now, so they are shared state between the config applier's goroutine
// and every handshake. Under `go test -race` this fails if the lock is wrong; without it the
// test still asserts nothing panics and a certificate is always available.
func TestKeeperSetFilesIsSafeBesideConcurrentHandshakes(t *testing.T) {
	dir := t.TempDir()
	certA, keyA := writePair(t, dir, "a")
	certB, keyB := writePair(t, dir, "b")

	k, err := NewKeeper(certA, keyA)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Either pair is a correct answer; a nil certificate with no error is not.
				if c, err := k.GetCertificate(nil); err == nil && c == nil {
					t.Error("GetCertificate returned no certificate and no error")
					return
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if err := k.SetFiles(certB, keyB); err != nil {
			t.Errorf("SetFiles(b): %v", err)
			break
		}
		if err := k.SetFiles(certA, keyA); err != nil {
			t.Errorf("SetFiles(a): %v", err)
			break
		}
	}
	close(stop)
	wg.Wait()
}

// renewInPlace rewrites `to` with the contents of `from` — same path, new content, as acme.sh does
// — and then moves its mtime a minute forward.
//
// THE MTIME BUMP IS THE WHOLE POINT, and this helper's comment used to assert the opposite: "the
// mtime moves, which is what `changed` reads." It does not reliably move (quince#786). `changed()`
// compares (mtime, size) per file, and a test compresses into microseconds an interval that is
// hours or months in production. Two facts then collide:
//
//   - an EC private key PEM is ALWAYS exactly the same size — P-256, fixed-length DER — so the key
//     file can only ever signal a renewal through its mtime;
//   - Linux stamps inodes from the COARSE clock, which advances once per timer tick, so two writes
//     milliseconds apart routinely share an mtime. The filesystem stores nanoseconds; the
//     granularity is the tick, not the precision.
//
// Measured over 3000 iterations in the pinned toolchain container: identical key mtime 77% of the
// time, and BOTH files' stamps unchanged 14% of the time — at which point nothing observable has
// happened and `changed()` is correctly false. That cost 13/300 failures in
// TestKeeperRotatesWithoutRestart and 10/300 in TestCertificateDirectoryIsNeverWrittenTo.
//
// So this restores the test's TIMING to something production-like rather than faking the mechanism:
// `statFile` and the real (mtime, size) trigger stay under test, which is exactly what reaching for
// the `statFn` seam here would have given up.
func renewInPlace(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil { // chmod-preserving rewrite
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(to, later, later); err != nil {
		t.Fatal(err)
	}
}

func leafCN(t *testing.T, c *tls.Certificate) string {
	t.Helper()
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf.Subject.CommonName
}

// The whole point of gap A: both protocols on ONE port. This drives a real TCP listener with
// a real TLS client and a real HTTP client and asserts each reaches its own half.
func TestMuxRoutesBothProtocolsOnOnePort(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "localhost")
	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMux(ln)
	t.Cleanup(func() { _ = mux.Close() })

	tlsSrv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "tls half") }),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{GetCertificate: k.GetCertificate, MinVersion: tls.VersionTLS12},
	}
	plainSrv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "plain half") }),
		ReadHeaderTimeout: 5 * time.Second,
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = tlsSrv.ServeTLS(mux.TLS(), "", "") }()
	go func() { defer wg.Done(); _ = plainSrv.Serve(mux.Plain()) }()
	t.Cleanup(func() {
		_ = tlsSrv.Close()
		_ = plainSrv.Close()
		wg.Wait()
	})

	addr := ln.Addr().String()

	plainClient := &http.Client{Timeout: 5 * time.Second}
	if got := getBody(t, plainClient, "http://"+addr+"/"); got != "plain half" {
		t.Errorf("http request got %q, want plain half", got)
	}

	tlsClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // self-signed test cert
	}
	if got := getBody(t, tlsClient, "https://"+addr+"/"); got != "tls half" {
		t.Errorf("https request got %q, want tls half", got)
	}
}

// Close is called by BOTH http.Servers' Shutdown, through the `side` listeners they were
// given. It must be idempotent or the second one reports a spurious failure — which is the
// shape the gap block warns about under "shutdown coordination without double-closing".
func TestMuxCloseIsIdempotent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMux(ln)
	if err := mux.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := mux.Close(); err != nil {
		t.Errorf("second Close returned %v, want nil — two servers over one listener both close it", err)
	}
	if err := mux.TLS().Close(); err != nil {
		t.Errorf("closing a side after the mux returned %v, want nil", err)
	}
}

// A client that connects and sends nothing must not block anybody else. The accept loop is
// serial, so an inline peek would make one silent socket a complete outage.
func TestMuxSilentConnectionDoesNotBlockOthers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMux(ln)
	t.Cleanup(func() { _ = mux.Close() })

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(mux.Plain()) }()
	t.Cleanup(func() { _ = srv.Close() })

	silent, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = silent.Close() }()

	client := &http.Client{Timeout: 5 * time.Second}
	if got := getBody(t, client, "http://"+ln.Addr().String()+"/"); got != "ok" {
		t.Errorf("a second client got %q while one connection sat silent, want ok", got)
	}
}

func getBody(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// writeWildcard mints a certificate for `*.example.com` — the normal shape for the tier
// bring-your-own-cert exists for, and one whose SAN matches nothing quince is configured with.
func writeWildcard(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "*.example.com"},
		DNSNames:     []string{"*.example.com"},
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
	certFile = filepath.Join(dir, "wildcard.pem")
	keyFile = filepath.Join(dir, "wildcard.key")
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

// G5 / story 7: a wildcard certificate whose SAN does not equal the host quince was reached on
// is SERVED, not rejected.
//
// quince serves what it is given and validates neither CN nor SAN. That is not laxness — a
// bind-mounted wildcard is the NORMAL case for the tier this option exists for, so a hostname
// check would break exactly the deployment the Operator described (quince#446, BYO-cert
// constraint 2). The test asks with an SNI that matches nothing, which is the strongest form
// of the question.
func TestWildcardCertificateIsServedRegardlessOfSNI(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeWildcard(t, dir)
	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatalf("a wildcard pair was REJECTED at load: %v", err)
	}

	for _, sni := range []string{"", "quince.lan", "totally.unrelated.invalid", "example.com"} {
		name := sni
		if name == "" {
			name = "(no SNI)"
		}
		t.Run(name, func(t *testing.T) {
			cert, err := k.GetCertificate(&tls.ClientHelloInfo{ServerName: sni})
			if err != nil {
				t.Fatalf("GetCertificate refused SNI %q: %v — quince must serve what it is given", sni, err)
			}
			if got := leafCN(t, cert); got != "*.example.com" {
				t.Errorf("served CN = %q, want the wildcard", got)
			}
		})
	}
}

// The same claim over a real handshake, because GetCertificate returning a certificate and the
// TLS stack actually completing on a name that does not match are different facts. The client
// skips verification, which is what a browser's click-through — or a correctly-named request
// through a proxy — amounts to here: the point is that the SERVER does not refuse.
func TestWildcardIsServedOverARealHandshake(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeWildcard(t, dir)
	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMux(ln)
	t.Cleanup(func() { _ = mux.Close() })

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "served") }),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{GetCertificate: k.GetCertificate, MinVersion: tls.VersionTLS12},
	}
	go func() { _ = srv.ServeTLS(mux.TLS(), "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,           //nolint:gosec // the point is that the SERVER does not refuse
			ServerName:         "nope.invalid", // matches nothing in the certificate
		}},
	}
	if got := getBody(t, client, "https://"+ln.Addr().String()+"/"); got != "served" {
		t.Errorf("got %q, want served", got)
	}
}

// G4 / story 8: the supplied certificate directory is NEVER written to.
//
// This is the `never mutate` rule's near-miss for this rung, and it constrains the LISTENER
// rather than the generator that slice 7 would have shipped — the listener re-reads that
// directory on every handshake for rotation, so it is the thing with an opportunity to write.
//
// ASSERTED BY OBSERVATION, not by permissions, and that distinction is the honest part. The
// spec says "with it mounted read-only", which is a BIND MOUNT in the deployment this exists
// for; a unit test cannot bind-mount, and `chmod 0555` does not stop root, which is what CI
// runs as. So the directory is made read-only AND its exact contents — including a hash of
// every file — are compared across the cycle. A test that only chmod'd would pass while
// writing happily.
//
// IT MUST GO THROUGH reload(), AND THE FIRST VERSION OF THIS TEST DID NOT. It loaded the pair,
// chmod'd, and called GetCertificate five times — but `changed()` was false every time, because
// nothing had touched the files since NewKeeper recorded their stamps, so `reload()` never ran
// and the test proved that two `stat` calls do not write. True, and never in doubt. **The path
// with the opportunity to write is `reload()`** — the only code here that OPENS and PARSES the
// files — and it was the one path being skipped (review on quince#556).
//
// So the pair is ROTATED first, the snapshot taken after that write, and the handshakes then
// go through a real re-read. The served leaf is asserted to have changed, which is what proves
// `reload()` actually ran: without it a future edit to the stamp logic could quietly return
// this test to the no-op it was.
func TestCertificateDirectoryIsNeverWrittenTo(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "readonly")

	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	// Rotate in place, so `changed()` will be true and GetCertificate must re-read. Minted in
	// a SEPARATE directory so the snapshot below sees only the two files quince was given.
	src := t.TempDir()
	newCert, newKey := writePair(t, src, "rotated")
	renewInPlace(t, newCert, certFile)
	renewInPlace(t, newKey, keyFile)

	// AFTER the rotation, so the test's own write is not what the comparison catches.
	before := snapshotDir(t, dir)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // so t.TempDir can clean up

	var served string
	for range 5 {
		cert, err := k.GetCertificate(&tls.ClientHelloInfo{ServerName: "quince.lan"})
		if err != nil {
			t.Fatalf("GetCertificate failed against a read-only directory: %v", err)
		}
		served = leafCN(t, cert)
	}

	// The guard on the guard: if this is still "readonly", `reload()` never ran and everything
	// below is asserting about a code path the test did not reach.
	if served != "rotated" {
		t.Fatalf("served CN = %q after rotating to \"rotated\" — reload() did not run, so this "+
			"test is back to proving that two stat calls do not write", served)
	}

	if after := snapshotDir(t, dir); after != before {
		t.Errorf("the certificate directory CHANGED across a re-read.\nbefore: %s\nafter:  %s", before, after)
	}
}

// snapshotDir renders a directory's contents as a comparable string: every name, size, modtime
// and CONTENT HASH.
//
// The hash is what makes the claim self-evident rather than an argument. Name and size alone
// would miss a same-length rewrite; modtime closes that in practice — but only on an argument
// about what the code does, and that is the kind of claim that stops being true without anyone
// noticing. sha256 costs three lines and needs no such argument (review on quince#556).
//
// IT STOPPED BEING TRUE, which is why the sentence is now written this way. It used to read
// "because nothing in tlsx calls os.Chtimes", and renewInPlace above now does — deliberately, to
// make a rotation observable (quince#786). Nothing here breaks, because the snapshot is taken
// AFTER that rotation; the point is that the clause doing the reassuring outlived its truth by
// one commit, and the hash is why that cost nothing.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s:%d:%d:%x|", e.Name(), fi.Size(), fi.ModTime().UnixNano(), sha256.Sum256(content))
	}
	return b.String()
}

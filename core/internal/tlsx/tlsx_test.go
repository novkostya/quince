package tlsx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	copyFile(t, newCert, certFile)
	copyFile(t, newKey, keyFile)

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

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	// Chmod-preserving rewrite; the mtime moves, which is what `changed` reads.
	if err := os.WriteFile(to, b, 0o600); err != nil {
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
// runs as. So the directory is made read-only AND its exact contents are compared before and
// after a full load-plus-rotation-check-plus-handshake cycle. A test that only chmod'd would
// pass while writing happily.
func TestCertificateDirectoryIsNeverWrittenTo(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "readonly")

	before := snapshotDir(t, dir)

	k, err := NewKeeper(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // so t.TempDir can clean up

	// Exercise every path that touches those files: the freshness check and the handshake
	// hook, several times, as a live server would.
	for range 5 {
		if _, err := k.GetCertificate(&tls.ClientHelloInfo{ServerName: "quince.lan"}); err != nil {
			t.Fatalf("GetCertificate failed against a read-only directory: %v", err)
		}
	}

	if after := snapshotDir(t, dir); after != before {
		t.Errorf("the certificate directory CHANGED.\nbefore: %s\nafter:  %s", before, after)
	}
}

// snapshotDir renders a directory's contents as a comparable string: every name, size and
// modtime. Names alone would miss a rewrite in place, which is the write most likely to happen
// by accident here — a "helpful" re-save of a parsed certificate.
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
		fmt.Fprintf(&b, "%s:%d:%d|", e.Name(), fi.Size(), fi.ModTime().UnixNano())
	}
	return b.String()
}

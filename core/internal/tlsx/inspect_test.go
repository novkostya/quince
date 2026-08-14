package tlsx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE OFFLINE HALF OF THE CERTIFICATE PROBE (quince#908 §5, slice 4).
//
// EVERY CASE IS GENERATED RATHER THAN COMMITTED. A fixture certificate expires, and a suite that
// bakes one in starts failing on a date nobody chose for a reason unrelated to the code — which is
// especially absurd for the test that checks expiry handling.

// pairFor writes a certificate/key pair with the names and validity window a case needs.
func pairFor(t *testing.T, dir, base string, tmpl x509.Certificate) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl.SerialNumber = big.NewInt(1)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	tmpl.IsCA = true
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certFile = filepath.Join(dir, base+".pem")
	keyFile = filepath.Join(dir, base+".key")
	write(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	write(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return certFile, keyFile
}

func write(t *testing.T, path string, b []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, b, mode); err != nil {
		t.Fatal(err)
	}
}

func TestInspectClassifiesACandidatePair(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	goodCert, goodKey := pairFor(t, dir, "good", x509.Certificate{
		Subject:   pkix.Name{CommonName: "quince.example"},
		DNSNames:  []string{"quince.example", "quince.lan"},
		NotBefore: now.Add(-time.Hour),
		NotAfter:  now.Add(24 * time.Hour),
	})
	expiredCert, expiredKey := pairFor(t, dir, "expired", x509.Certificate{
		DNSNames:  []string{"quince.example"},
		NotBefore: now.Add(-48 * time.Hour),
		NotAfter:  now.Add(-time.Hour),
	})
	futureCert, futureKey := pairFor(t, dir, "future", x509.Certificate{
		DNSNames:  []string{"quince.example"},
		NotBefore: now.Add(24 * time.Hour),
		NotAfter:  now.Add(48 * time.Hour),
	})
	// A SECOND PAIR, so the mismatch case is a real key belonging to a real other certificate rather
	// than a corrupted file — which is what an operator actually does: copies the right cert and the
	// key from the previous renewal.
	_, otherKey := pairFor(t, dir, "other", x509.Certificate{
		DNSNames:  []string{"quince.example"},
		NotBefore: now.Add(-time.Hour),
		NotAfter:  now.Add(24 * time.Hour),
	})

	garbage := filepath.Join(dir, "garbage.pem")
	write(t, garbage, []byte("this is not a certificate\n"), 0o644)

	for _, tc := range []struct {
		name                string
		cert, key, host     string
		wantOutcome         string
		reasonNamesCoverage bool
	}{
		{"a good pair and the name it covers", goodCert, goodKey, "quince.example", OutcomeUsable, false},
		{"a good pair with no name asked about", goodCert, goodKey, "", OutcomeUsable, false},
		// THE ONE THE USER MOST NEEDS SPELLED OUT: the certificate is fine, the name is not on it.
		{"a name the certificate does not cover", goodCert, goodKey, "quince.other", OutcomeWrongHost, true},
		{"an expired certificate", expiredCert, expiredKey, "quince.example", OutcomeExpired, false},
		{"a certificate not yet valid", futureCert, futureKey, "quince.example", OutcomeNotYetValid, false},
		// THE KEY FROM THE PREVIOUS RENEWAL — both files parse, neither is corrupt, and nothing else
		// in quince catches this before a restart.
		{"a key belonging to a different certificate", goodCert, otherKey, "quince.example", OutcomeMismatched, false},
		{"a path that is not there", filepath.Join(dir, "nope.pem"), goodKey, "quince.example", OutcomeUnreadable, false},
		{"a file that is not PEM", garbage, goodKey, "quince.example", OutcomeMalformed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Inspect(tc.cert, tc.key, tc.host, now)

			if got.Outcome != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q — reason %q", got.Outcome, tc.wantOutcome, got.Reason)
			}
			// EVERY REASON NAMES THE FILE (quince#514's rule, and quince#940's whole sweep): a client
			// shows this sentence rather than composing its own, so a reason that omits the path
			// leaves the user guessing which of two files is wrong.
			if !strings.Contains(got.Reason, tc.cert) && !strings.Contains(got.Reason, "does not match") {
				t.Errorf("reason does not name the certificate file: %q", got.Reason)
			}
			// AND `wrong_host` NAMES WHAT IT *DOES* COVER. "Does not cover X" is a status; "covers Y,
			// not X" is something a person can act on.
			if tc.reasonNamesCoverage {
				for _, n := range []string{"quince.example", "quince.lan"} {
					if !strings.Contains(got.Reason, n) {
						t.Errorf("reason omits the name the certificate DOES cover (%s): %q", n, got.Reason)
					}
				}
			}
		})
	}
}

// THE FACTS A UI RENDERS BESIDE THE VERDICT. They are populated on success too — a certificate that
// is usable today and expires in nine days is not a refusal and is exactly what somebody wants to see.
func TestInspectReportsTheNamesDatesAndChainLength(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	cert, key := pairFor(t, dir, "facts", x509.Certificate{
		DNSNames:    []string{"quince.example", "quince.lan"},
		IPAddresses: []net.IP{net.ParseIP("192.0.2.10")},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(24 * time.Hour),
	})

	got := Inspect(cert, key, "quince.example", now)

	if got.Outcome != OutcomeUsable {
		t.Fatalf("outcome = %q, want usable — %s", got.Outcome, got.Reason)
	}
	// IP SANs ARE INCLUDED. A LAN deployment with an IP-only certificate is unusual and real, and
	// omitting them would report "covers nothing" about a certificate that covers something.
	want := []string{"quince.example", "quince.lan", "192.0.2.10"}
	if strings.Join(got.Names, ",") != strings.Join(want, ",") {
		t.Errorf("names = %v, want %v", got.Names, want)
	}
	if got.NotBefore == "" || got.NotAfter == "" {
		t.Errorf("dates are empty on a usable pair: %+v", got)
	}
	// ONE CERTIFICATE IS NOT AN ERROR AND IS OFTEN A PROBLEM — a leaf without its intermediate works
	// on a machine that caches the issuer and fails on a phone that does not. Reported, not judged.
	if got.ChainLength != 1 {
		t.Errorf("chain length = %d, want 1 for a self-signed leaf", got.ChainLength)
	}
}

// AN EXPIRED CERTIFICATE THAT ALSO COVERS THE WRONG NAME HAS TWO PROBLEMS, and the one to report is
// the one that makes it unusable for EVERY name. Ordering asserted directly, because "expired" and
// "wrong host" are different next actions and telling somebody to fix DNS for a dead certificate
// wastes the trip.
func TestExpiryOutranksTheHostname(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	cert, key := pairFor(t, dir, "both", x509.Certificate{
		DNSNames:  []string{"quince.lan"},
		NotBefore: now.Add(-48 * time.Hour),
		NotAfter:  now.Add(-time.Hour),
	})

	if got := Inspect(cert, key, "quince.example", now); got.Outcome != OutcomeExpired {
		t.Errorf("outcome = %q, want expired — a dead certificate is dead for every name", got.Outcome)
	}
}

// NO NETWORK, EVER. The offline half answers what two files can say; reachability is the browser's,
// because quince's own resolver and network namespace say nothing about the phone (quince#908 §5).
// Asserted by inspecting a pair whose name resolves nowhere and would hang if anything dialled it.
func TestInspectTouchesNoNetwork(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	cert, key := pairFor(t, dir, "unreachable", x509.Certificate{
		DNSNames:  []string{"nothing.invalid"},
		NotBefore: now.Add(-time.Hour),
		NotAfter:  now.Add(24 * time.Hour),
	})

	done := make(chan Report, 1)
	go func() { done <- Inspect(cert, key, "nothing.invalid", now) }()
	select {
	case got := <-done:
		if got.Outcome != OutcomeUsable {
			t.Errorf("outcome = %q, want usable — %s", got.Outcome, got.Reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Inspect did not return within 5s — something is dialling the network")
	}
}

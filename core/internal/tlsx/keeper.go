// Package tlsx owns quince serving TLS itself: loading the operator's certificate, keeping
// it fresh under a running process, and splitting one listener into an HTTP half and a TLS
// half so both protocols share the single port QUINCE_LISTEN names (qn.6f slice 4).
//
// The connection splitter lives here rather than in its own package because its only reason
// to exist is TLS — it discriminates on a ClientHello — and a `listenmux` package would
// suggest a general facility this is deliberately not.
package tlsx

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Keeper holds the operator's certificate and re-reads it when the files change on disk.
//
// ROTATION IS THE REASON THIS IS NOT `tls.LoadX509KeyPair` AT BOOT (story 6, and the
// Operator's third BYO-cert constraint on quince#446). `acme.sh` and friends renew on their
// own schedule and rewrite the files in place. A daemon that loaded once would go on serving
// the OLD certificate until someone restarted it — so it serves an expired one, and the
// symptom is a browser error with no server-side log, weeks after the deployment that caused
// it. Nothing in quince would notice.
//
// The freshness check is by MODTIME AND SIZE, not by content hash: it runs on every
// handshake, so it must not read two files per connection. A renewal that produced a
// byte-identical file with an unchanged mtime would be missed, and that is not a renewal.
type Keeper struct {
	certFile string
	keyFile  string

	mu     sync.RWMutex
	cert   *tls.Certificate
	stamps [2]stamp // certFile, keyFile — as observed when `cert` was loaded

	// OnReloadError is called when a rotation re-read fails while a usable certificate is
	// still cached. The caller sets it to log; nil means silence, which is only right in
	// tests. A field rather than a logger dependency, so this package imports nothing of
	// quince's and stays testable without one.
	OnReloadError func(error)

	// statFn is a seam. Tests drive rotation without sleeping and without depending on the
	// filesystem's mtime granularity — one second on some backends, which would make a fast
	// test flaky rather than wrong.
	statFn func(string) (stamp, error)
}

// stamp is the cheap identity of a file: enough to notice a rewrite, cheap enough to check
// on every handshake.
type stamp struct {
	mod  time.Time
	size int64
}

func statFile(path string) (stamp, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return stamp{}, err
	}
	return stamp{mod: fi.ModTime(), size: fi.Size()}, nil
}

// NewKeeper loads the pair once and returns a Keeper, or an error naming WHICH FILE and WHY.
//
// The error is the startup refusal's evidence, so it must survive being printed to an
// operator with no other context — see config.TLSRequirement.
func NewKeeper(certFile, keyFile string) (*Keeper, error) {
	k := &Keeper{certFile: certFile, keyFile: keyFile, statFn: statFile}
	if err := k.reload(); err != nil {
		return nil, err
	}
	return k, nil
}

// reload reads the pair and replaces the cached certificate. Callers hold no lock.
func (k *Keeper) reload() error {
	cert, err := tls.LoadX509KeyPair(k.certFile, k.keyFile)
	if err != nil {
		return classify(k.certFile, k.keyFile, err)
	}
	cs, cerr := k.statFn(k.certFile)
	ks, kerr := k.statFn(k.keyFile)
	if cerr != nil || kerr != nil {
		// The pair parsed a moment ago, so a stat failure now is a race with a renewal
		// rather than a broken config. Serve what we parsed and let the next handshake
		// re-check; zero stamps guarantee it does.
		cs, ks = stamp{}, stamp{}
	}
	k.mu.Lock()
	k.cert, k.stamps = &cert, [2]stamp{cs, ks}
	k.mu.Unlock()
	return nil
}

// GetCertificate is the tls.Config hook. It re-reads the pair when either file has changed
// since the cached copy was loaded, and otherwise returns the cache.
//
// A FAILED RELOAD KEEPS SERVING THE OLD CERTIFICATE, deliberately. Mid-renewal the cert file
// can exist while the key is half-written, and refusing the handshake would take the UI down
// for the seconds that lasts — a self-inflicted outage on the deployment this feature exists
// for. The old certificate is still valid (renewal happens before expiry), so serving it is
// correct as well as available. The failure is not swallowed: it is returned to the caller's
// logger through OnReloadError.
func (k *Keeper) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if k.changed() {
		if err := k.reload(); err != nil && k.OnReloadError != nil {
			k.OnReloadError(err)
		}
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.cert == nil {
		return nil, errors.New("no certificate loaded")
	}
	return k.cert, nil
}

func (k *Keeper) changed() bool {
	cs, cerr := k.statFn(k.certFile)
	ks, kerr := k.statFn(k.keyFile)
	if cerr != nil || kerr != nil {
		return false // unreadable right now: keep serving, do not thrash on reload
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return cs != k.stamps[0] || ks != k.stamps[1]
}

// classify turns tls.LoadX509KeyPair's error into one that names the file and the fault.
//
// The standard library's message is accurate and unhelpful to an operator: a missing file
// gives a bare path with `no such file or directory`, a truncated PEM gives `failed to find
// any PEM data in certificate input`, and a mismatched pair gives `private key does not
// match public key` with NO path at all. Story 5 requires all three to name the file and the
// reason, so the mapping happens here rather than in the message that prints it.
func classify(certFile, keyFile string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("cannot read the certificate or key: %w", err)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("cannot read the certificate or key (permission denied): %w", err)
	}
	// Not a filesystem error, so the files were read and one of them did not parse — or they
	// parsed and do not belong together. LoadX509KeyPair does not expose which, so the
	// message names both paths rather than guessing.
	return fmt.Errorf("%s and %s were read but are not a usable certificate/key pair: %w",
		certFile, keyFile, err)
}

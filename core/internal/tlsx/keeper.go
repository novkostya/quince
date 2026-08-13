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
//
// IT MAY HOLD NOTHING, AND THE PATHS MAY CHANGE UNDER IT (quince#900). A quince that starts
// with no certificate still binds the TLS half, so the Keeper is what an unconfigured
// install hands `tls.Config` — it fails the handshake and says so, rather than the process
// having no TLS half at all. And `tls.cert_file`/`.key_file` are live settings, so the pair
// this holds is the pair the config named MOST RECENTLY, not the one it was constructed
// with.
type Keeper struct {
	// certFile and keyFile MOVE, so they live under mu with everything else. They were
	// construction-time constants until quince#900 and were read unlocked; a live path edit
	// is a write from the config applier's goroutine racing every handshake's read.
	mu       sync.RWMutex
	certFile string
	keyFile  string
	cert     *tls.Certificate
	stamps   [2]stamp // certFile, keyFile — as observed when `cert` was loaded
	// dirty means the config names a pair we are NOT serving — set when the paths move and
	// cleared only by a load that succeeds against those same paths (quince#905 review).
	//
	// WITHOUT IT THE SELF-HEAL IS INFERRED FROM STAMPS, AND CAN FAIL SILENTLY. After a failed
	// path change `stamps` describes the OLD pair while `certFile`/`keyFile` name the new one,
	// so `changed()` is asking "have these files been rewritten" when the question is "are we
	// serving the pair the config names". A `(mtime, size)` collision between the two pairs
	// makes the first answer no and the second answer no as well — and mtime is PRESERVED by
	// `cp -p`, `rsync -a` and a restored archive, so the collision is a thing operators
	// routinely produce rather than a coincidence. Then the daemon serves the old certificate
	// forever and nothing ever says why.
	//
	// The second answer is known exactly rather than inferable, so it is recorded rather than
	// deduced.
	dirty bool

	// OnReloadError is called when a rotation re-read fails while a usable certificate is
	// still cached. The caller sets it to log; nil means silence, which is only right in
	// tests. A field rather than a logger dependency, so this package imports nothing of
	// quince's and stays testable without one.
	//
	// OUTSIDE mu ON PURPOSE, unlike the paths above: it is wired once at startup, before
	// anything serves, and never again. The paths moved under the lock because a config
	// applier writes them while handshakes read them; this has no writer after boot.
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
//
// AN EMPTY PATH IS REFUSED HERE, WHERE `SetFiles` TREATS IT AS "TURN TLS OFF" (quince#905
// review). The two answers are the whole reason there are two constructors, and without this
// guard `NewKeeper("", "")` returned a usable Keeper and a nil error — expressing "nothing is
// configured" through the return value that means "the certificate is fine", which is the
// conflation the doc comment on NewEmptyKeeper claims cannot happen.
//
// It was latent while `config.CheckTLS` was the only caller, since that returns before the
// loader when `!c.TLS.Enabled()`. It stopped being latent when production moved to `SetFiles`:
// this constructor now has NO caller to be guarded by, so its contract has to hold on its own.
func NewKeeper(certFile, keyFile string) (*Keeper, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("no certificate configured: cert_file=%q key_file=%q — "+
			"use NewEmptyKeeper for an install that has not configured TLS", certFile, keyFile)
	}
	k := NewEmptyKeeper()
	if err := k.SetFiles(certFile, keyFile); err != nil {
		return nil, err
	}
	return k, nil
}

// NewEmptyKeeper returns a Keeper holding no certificate, for an install that has not
// configured one.
//
// IT CANNOT FAIL, which is the point of it existing separately (quince#900). `NewKeeper`
// refuses a configured-but-unusable pair, and that refusal is a startup fatal — the silent
// downgrade `config.TLSRequirement` exists to prevent. "Nothing is configured" is not that
// case: it is the ordinary first-run state, and it must produce a serving daemon rather than
// an error. Two constructors keep those two answers from being expressed in one return value.
func NewEmptyKeeper() *Keeper {
	return &Keeper{statFn: statFile}
}

// SetFiles points the Keeper at a new pair and loads it, or — with BOTH paths empty — drops
// the certificate it holds, which is how TLS is turned off in a running process.
//
// THE ERROR RETURN IS A WARNING TO ITS CALLER, NOT A REFUSAL, and that asymmetry with
// `NewKeeper` is deliberate (quince#900). At startup an unusable certificate stops the
// process. Here the config file has ALREADY been written — `config.Applier` cannot refuse,
// by design (config/service.go) — so there is nothing to refuse on behalf of. What this does
// instead is keep serving the certificate it already had and hand the caller an error to
// surface, which is the same choice `GetCertificate` makes for a broken rotation and for the
// same reason: the alternative is a self-inflicted outage.
//
// THE NEW PATHS ARE KEPT EVEN WHEN THE LOAD FAILS, so this SELF-HEALS. `changed()` stats the
// paths now configured; while they are unreadable it reports false and the old certificate
// goes on being served, and the moment they become readable the stamps differ from the old
// pair's and the next handshake picks them up. An operator who writes the config before
// copying the files in gets a working daemon without touching anything again.
//
// A HALF-SET PAIR — one path empty — is treated as "off", matching config.TLSConfig.Enabled.
// Validate has already rejected that case before anything can reach here; the agreement is
// so that if it ever stops doing so, both places still mean the same thing.
func (k *Keeper) SetFiles(certFile, keyFile string) error {
	if certFile == "" || keyFile == "" {
		k.mu.Lock()
		k.certFile, k.keyFile = "", ""
		k.cert, k.stamps, k.dirty = nil, [2]stamp{}, false
		k.mu.Unlock()
		return nil
	}
	k.mu.Lock()
	k.certFile, k.keyFile = certFile, keyFile
	// NOT YET PROVEN to be serving what the config names — reload clears this, and nothing
	// else does. Set BEFORE the load so a load that never stores (the stale-path drop below)
	// leaves the Keeper knowing it is behind rather than believing it is current.
	k.dirty = true
	k.mu.Unlock()
	return k.reload()
}

// HasCertificate reports whether a certificate is loaded and can be served RIGHT NOW.
//
// "Loaded", never "configured", and the difference is the whole reason this is exported: the
// plain half of the mux decides whether to redirect to HTTPS on this answer (quince#900). A
// config naming a certificate that does not parse would, on the other reading, redirect every
// plain request into a handshake that fails — and once that happens the client has no working
// channel left to ask for anything, including a revert.
func (k *Keeper) HasCertificate() bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.cert != nil
}

// files reports the pair currently configured, under the lock.
func (k *Keeper) files() (string, string) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.certFile, k.keyFile
}

// reload reads the currently configured pair and replaces the cached certificate. Callers
// hold no lock.
func (k *Keeper) reload() error {
	certFile, keyFile := k.files()
	if certFile == "" || keyFile == "" {
		return nil // nothing configured: SetFiles has already dropped the cache
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return classify(certFile, keyFile, err)
	}
	cs, cerr := k.statFn(certFile)
	ks, kerr := k.statFn(keyFile)
	if cerr != nil || kerr != nil {
		// The pair parsed a moment ago, so a stat failure now is a race with a renewal
		// rather than a broken config. Serve what we parsed and let the next handshake
		// re-check; zero stamps guarantee it does.
		cs, ks = stamp{}, stamp{}
	}
	k.mu.Lock()
	// THE PATHS MAY HAVE MOVED WHILE THE FILES WERE BEING READ. Loading happens outside the
	// lock — it is filesystem IO on the handshake path — so a config applier can land a new
	// pair in between, and storing this one would leave the Keeper serving a certificate the
	// config no longer names, with stamps that make `changed()` say everything is current.
	// Dropping the stale read is safe: the applier that moved the paths is doing its own
	// reload, and a rotation loses nothing because the next handshake re-checks.
	if k.certFile != certFile || k.keyFile != keyFile {
		k.mu.Unlock()
		return nil
	}
	k.cert, k.stamps, k.dirty = &cert, [2]stamp{cs, ks}, false
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
		// THE ORDINARY STATE OF AN INSTALL THAT HAS NOT CONFIGURED TLS, since quince#900
		// binds the TLS half regardless. The handshake fails — which is the honest answer to
		// a ClientHello aimed at a server holding no certificate — and the plain half goes on
		// serving on the same port, so nothing about the working URL changes.
		return nil, errors.New("no certificate loaded")
	}
	return k.cert, nil
}

func (k *Keeper) changed() bool {
	certFile, keyFile := k.files()
	if certFile == "" || keyFile == "" {
		return false // nothing configured: there is no file whose change could matter
	}
	cs, cerr := k.statFn(certFile)
	ks, kerr := k.statFn(keyFile)
	if cerr != nil || kerr != nil {
		return false // unreadable right now: keep serving, do not thrash on reload
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	// DIRTY BEATS THE STAMPS, and the order matters (quince#905 review). The stamps answer
	// "have these files been rewritten"; `dirty` answers "are we serving the pair the config
	// names", and after a failed path change only the second one is the question. Comparing
	// stamps there compares the NEW paths against the OLD pair's, so a `(mtime, size)`
	// collision — routine, since `cp -p` and `rsync -a` preserve mtime — would report no
	// change and the self-heal would never fire.
	//
	// AFTER the stat guard above, deliberately: a dirty Keeper whose files are still absent
	// has nothing to pick up, and returning true there would re-read and re-log on every
	// single handshake for as long as the config stayed wrong.
	if k.dirty {
		return true
	}
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

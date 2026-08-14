package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// quince#908 slice 5 — THE NARROW `tls.*` WRITE.
//
// Its one claim is the pair of guarantees an unconfirmed apply is built on: the write moves those two
// keys and no others, and it hands back **the pair it displaced** rather than a pair some later reader
// would have to go and find. Nothing here is about a route, a timer or a redirect; those are the
// slices above this one.

// appliedPair records the last TLS pair an applier saw, so a test can tell "written to the file"
// from "handed to the running daemon". The production applier is `subscribeTLS`, which calls
// `Keeper.SetFiles`.
//
// A STRUCT RATHER THAN A RETURNED POINTER, because the closure assigns AFTER this returns — the
// obvious `var seen *TLSConfig; …; return seen` hands back the nil it was born with and every
// assertion against it passes for the wrong reason.
type appliedPair struct{ pair *TLSConfig }

func applied(t *testing.T, svc *Service) *appliedPair {
	t.Helper()
	a := &appliedPair{}
	svc.Subscribe("tls-test", func(old, next Config) []Warning {
		if old.TLS != next.TLS {
			pair := next.TLS
			a.pair = &pair
		}
		return nil
	})
	return a
}

func TestSetTLSFilesMovesThePairAndReturnsWhatItDisplaced(t *testing.T) {
	svc := testService(t)
	seen := applied(t, svc)

	prev, errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate)
	if err != nil || len(errs) > 0 {
		t.Fatalf("first apply: err=%v errs=%v", err, errs)
	}
	// FIRST RUN HAD NO CERTIFICATE, so the pair to go back to is the empty one — which is exactly
	// what `Keeper.SetFiles` documents as "turn TLS off", and is why the revert needs no special
	// case for a user who never had TLS.
	if prev.CertFile != "" || prev.KeyFile != "" {
		t.Fatalf("displaced pair on a first apply = %+v, want empty", prev)
	}
	if got := svc.Current().TLS; got.CertFile != "/etc/quince/one.pem" || got.KeyFile != "/etc/quince/one.key" {
		t.Fatalf("live config after apply = %+v", got)
	}
	if seen.pair == nil {
		t.Fatal("no applier ran: the pair was written but never handed to the running daemon")
	}

	// A SECOND APPLY DISPLACES THE FIRST, not the original. This is the ordering the revert depends
	// on: a user who applies, does not confirm, and applies a different pair must be reverted to the
	// pair the SECOND call replaced — otherwise the undo restores a certificate that was already
	// gone.
	prev2, errs, _, err := svc.SetTLSFiles("/etc/quince/two.pem", "/etc/quince/two.key", SourceApplyCertificate)
	if err != nil || len(errs) > 0 {
		t.Fatalf("second apply: err=%v errs=%v", err, errs)
	}
	if prev2.CertFile != "/etc/quince/one.pem" || prev2.KeyFile != "/etc/quince/one.key" {
		t.Fatalf("displaced pair on the second apply = %+v, want the first pair", prev2)
	}
}

// THE REVERT IS THE SAME CALL WITH THE PAIR IT WAS GIVEN, and an empty pair is a legal argument
// rather than a sentinel meaning "do nothing".
func TestSetTLSFilesAcceptsAnEmptyPairAndTurnsTLSOff(t *testing.T) {
	svc := testService(t)
	if _, errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate); err != nil || len(errs) > 0 {
		t.Fatalf("apply: err=%v errs=%v", err, errs)
	}
	seen := applied(t, svc)

	prev, errs, _, err := svc.SetTLSFiles("", "", SourceRevertCertificate)
	if err != nil || len(errs) > 0 {
		t.Fatalf("revert: err=%v errs=%v", err, errs)
	}
	if prev.CertFile != "/etc/quince/one.pem" {
		t.Fatalf("displaced pair on revert = %+v", prev)
	}
	if got := svc.Current().TLS; got.Enabled() {
		t.Fatalf("TLS still enabled after a revert to the empty pair: %+v", got)
	}
	if seen.pair == nil || seen.pair.CertFile != "" || seen.pair.KeyFile != "" {
		t.Fatalf("applier saw %+v, want the empty pair — a revert nobody applies leaves the daemon serving what it just undid", seen.pair)
	}
}

// HALF A PAIR IS REFUSED, and the refusal is `Validate`'s rather than this function's — which is the
// point of routing the narrow write through `replaceLocked` instead of writing the file itself.
func TestSetTLSFilesRefusesHalfAPairAndChangesNothing(t *testing.T) {
	svc := testService(t)
	before := svc.Current()

	_, errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "", SourceApplyCertificate)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if len(errs) != 1 || errs[0].Path != "tls.key_file" {
		t.Fatalf("errors = %+v, want one on tls.key_file", errs)
	}
	if svc.Current().TLS != before.TLS {
		t.Fatalf("live config moved on a refused write: %+v", svc.Current().TLS)
	}
}

// IT WRITES TWO KEYS AND NOTHING ELSE. The narrowness is a security property here, not a tidiness
// one: the route that reaches this call is pre-auth, so anything it can splice away is something a
// stranger can delete.
func TestSetTLSFilesLeavesTheRestOfTheDocumentAlone(t *testing.T) {
	// Real keys from three OTHER sections. An invented one would be dropped as unknown and the
	// assertion below would fail for a reason that has nothing to do with this write — which is how
	// the first version of this test failed.
	raw := "storage:\n  - name: local\n    path: /backups\n    backend: hardlink\n" +
		"reconcile:\n  interval_minutes: 30\n" +
		"ui:\n  theme: dark\n" +
		"sessions:\n  allow_insecure_transport: true\n"
	svc, path := newServiceOn(t, raw)
	if svc.Discarded() {
		t.Fatal("seed config did not load")
	}

	if _, errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate); err != nil || len(errs) > 0 {
		t.Fatalf("apply: err=%v errs=%v", err, errs)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cert_file: /etc/quince/one.pem", "key_file: /etc/quince/one.key",
		"interval_minutes: 30", "theme: dark", "allow_insecure_transport: true", "path: /backups"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("config.yml lost %q after a tls write:\n%s", want, out)
		}
	}
}

// A DISCARDED DOCUMENT IS REFUSED, because `Current()` on one is `Default()` — so writing through it
// would replace the operator's whole file with defaults plus this pair, from a pre-auth caller.
func TestSetTLSFilesRefusesWhenTheFileOnDiskCouldNotBeRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	// A directory where the file should be: `os.Stat` succeeds, `os.ReadFile` fails, which is
	// `Load`'s unreadable-file discard — and the one that carries NO errors, only warnings.
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	svc := NewService(p, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !svc.Discarded() {
		t.Fatal("the seed did not produce a discarded document, so this proves nothing")
	}

	prev, errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if len(errs) == 0 {
		t.Fatal("a discarded config accepted a tls write")
	}
	// The displaced pair is still meaningful on a refusal: it is what the live document holds, so a
	// caller who armed a revert against this would restore what is already there rather than
	// turning off a certificate that is running.
	if prev != svc.Current().TLS {
		t.Fatalf("displaced pair %+v is not the live pair %+v", prev, svc.Current().TLS)
	}
}

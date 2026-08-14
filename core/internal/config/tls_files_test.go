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
// Its claim is that the write moves those two keys, moves no others, and hands the result to the
// running daemon. Nothing here is about a route, a trial or a timer — and since 2026-08-14 nothing
// here is about a revert either: the trial lives in `tlsx.Keeper` and this function runs only once a
// certificate has proved itself, so `config.yml` is never written for one that did not.

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

func TestSetTLSFilesMovesThePairAndHandsItToTheDaemon(t *testing.T) {
	svc := testService(t)
	seen := applied(t, svc)

	if errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate); err != nil || len(errs) > 0 {
		t.Fatalf("apply: err=%v errs=%v", err, errs)
	}
	if got := svc.Current().TLS; got.CertFile != "/etc/quince/one.pem" || got.KeyFile != "/etc/quince/one.key" {
		t.Fatalf("live config after apply = %+v", got)
	}
	// WRITTEN IS NOT SERVED. `replaceLocked` returns before `notify`, so a write that landed without
	// notifying would leave the file right and the daemon serving whatever it had.
	if seen.pair == nil {
		t.Fatal("no applier ran: the pair was written but never handed to the running daemon")
	}
}

// BOTH PATHS EMPTY IS LEGAL, and it is how an authenticated admin turns TLS off.
func TestSetTLSFilesAcceptsAnEmptyPairAndTurnsTLSOff(t *testing.T) {
	svc := testService(t)
	if errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate); err != nil || len(errs) > 0 {
		t.Fatalf("apply: err=%v errs=%v", err, errs)
	}
	seen := applied(t, svc)

	if errs, _, err := svc.SetTLSFiles("", "", SourceApplyCertificate); err != nil || len(errs) > 0 {
		t.Fatalf("turn off: err=%v errs=%v", err, errs)
	}
	if got := svc.Current().TLS; got.Enabled() {
		t.Fatalf("TLS still enabled after writing the empty pair: %+v", got)
	}
	if seen.pair == nil || seen.pair.CertFile != "" || seen.pair.KeyFile != "" {
		t.Fatalf("applier saw %+v, want the empty pair — the daemon would go on serving what was removed", seen.pair)
	}
}

// HALF A PAIR IS REFUSED, and the refusal is `Validate`'s rather than this function's — which is the
// point of routing the narrow write through `replaceLocked` instead of writing the file itself.
//
// IT SEEDS A REAL PAIR FIRST, AND THAT IS THE WHOLE TEST (quince#977 review). Starting from
// `testService`'s empty `tls:` asserts "a refused write does not write" — true, and not the sentence
// this function's narrowness is defended with. The route that reaches it is PRE-AUTH, so the case
// that matters is **a stranger sends half a pair and knocks TLS out**, and only a document that
// already HOLDS a certificate can fail that way.
func TestSetTLSFilesRefusesHalfAPairAndLeavesTheLIVEPairStanding(t *testing.T) {
	svc := testService(t)
	if errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate); err != nil || len(errs) > 0 {
		t.Fatalf("seed: err=%v errs=%v", err, errs)
	}
	before := svc.Current()
	if !before.TLS.Enabled() {
		t.Fatal("the seed did not take, so a surviving pair cannot be proven")
	}
	seen := applied(t, svc)

	errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "", SourceApplyCertificate)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if len(errs) != 1 || errs[0].Path != "tls.key_file" {
		t.Fatalf("errors = %+v, want one on tls.key_file", errs)
	}
	if svc.Current().TLS != before.TLS {
		t.Fatalf("a refused write destroyed the live pair: %+v, want %+v", svc.Current().TLS, before.TLS)
	}
	// AND NO APPLIER RAN, so the daemon was never told to drop the certificate it is serving. The
	// live config surviving and the Keeper surviving are two facts, and only the second is what a
	// user with a working https connection would notice.
	if seen.pair != nil {
		t.Fatalf("an applier ran for a refused write, with %+v", seen.pair)
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

	if errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate); err != nil || len(errs) > 0 {
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

	errs, _, err := svc.SetTLSFiles("/etc/quince/one.pem", "/etc/quince/one.key", SourceApplyCertificate)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if len(errs) == 0 {
		t.Fatal("a discarded config accepted a tls write")
	}
}

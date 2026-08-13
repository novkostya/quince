package storage

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// quince#912 — the host-key ceremony.

// aHostKeyLine builds a known_hosts line for `addr` from a freshly generated key, the way a scan
// would. Generated rather than fixtured: a checked-in host key is a key, and this file is about
// what quince writes to disk.
func aHostKeyLine(t *testing.T, addr string) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = priv
	signer, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return knownhosts.Line([]string{knownhosts.Normalize(addr)}, signer), signer
}

func TestTrustHostKeyWritesTheLineItIsGiven(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "known_hosts")
	line, _ := aHostKeyLine(t, "nas.example:22")

	if err := TrustHostKey(path, line); err != nil {
		t.Fatalf("TrustHostKey: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the file was not created: %v", err)
	}
	if !strings.Contains(string(got), line) {
		t.Errorf("the confirmed line is not in the file.\nwant: %s\ngot:  %s", line, got)
	}
	// The directory is created as part of the write — /data/keys may not exist on a fresh install,
	// and this is the first thing that would need it if no key had been generated yet.
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || !fi.IsDir() {
		t.Errorf("the parent directory was not created: %v", err)
	}
}

// RECORDING THE SAME KEY TWICE IS A NO-OP, not a second line. An operator who presses the button
// again — or who reloads and repeats the ceremony — must not grow the file, because a known_hosts
// with a hundred identical entries is one nobody will read when it matters.
func TestTrustHostKeyIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	line, _ := aHostKeyLine(t, "nas.example:22")

	for i := range 3 {
		if err := TrustHostKey(path, line); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(got)), "\n") + 1; n != 1 {
		t.Errorf("the file has %d lines, want 1:\n%s", n, got)
	}
}

// A DIFFERENT KEY FOR A HOST ALREADY RECORDED IS REFUSED, and this is the sharpest edge in the file.
//
// It is either a rebuilt storage host — an ordinary event — or something impersonating it. quince
// cannot tell the two apart and must not decide on the operator's behalf: silently replacing the
// entry would make the ceremony worthless, since an attacker's key would be trusted by the same
// button that trusted the real one. The refusal names both possibilities and the file to edit.
func TestTrustHostKeyRefusesAChangedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	first, _ := aHostKeyLine(t, "nas.example:22")
	second, _ := aHostKeyLine(t, "nas.example:22")

	if err := TrustHostKey(path, first); err != nil {
		t.Fatal(err)
	}
	err := TrustHostKey(path, second)
	if err == nil {
		t.Fatal("a changed host key was accepted — it must be refused, not overwritten")
	}
	for _, want := range []struct{ fact, why string }{
		{"DIFFERENT key", "what actually happened"},
		{"rebuilt", "the innocent explanation, which is the common one"},
		{"impersonating", "the other one, which is why quince will not just fix it"},
		{"by hand", "the remedy, since quince refuses to choose"},
	} {
		if !strings.Contains(err.Error(), want.fact) {
			t.Errorf("the refusal does not carry %q — %s.\ngot: %v", want.fact, want.why, err)
		}
	}

	// AND THE ORIGINAL SURVIVED. A refusal that had already truncated the file would be worse than
	// an overwrite, because the operator would be left with no trusted key at all.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), first) {
		t.Error("the refusal did not leave the existing entry intact")
	}
}

// A DIFFERENT HOST IS APPENDED, not confused with the one already there. Two zfs storages on two
// hosts is an ordinary configuration.
func TestTrustHostKeyAppendsASecondHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	a, _ := aHostKeyLine(t, "nas-one.example:22")
	b, _ := aHostKeyLine(t, "nas-two.example:22")

	if err := TrustHostKey(path, a); err != nil {
		t.Fatal(err)
	}
	if err := TrustHostKey(path, b); err != nil {
		t.Fatalf("a second host was refused: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{a, b} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing an entry:\n%s", want)
		}
	}
}

// THE LINE IS VALIDATED AS WHAT IT CLAIMS TO BE. It arrives over the wire and is written to a file
// ssh reads as configuration, so it is parsed rather than trusted because quince produced one a
// moment earlier. `@revoked` is the case worth naming: it parses, and writing it would record the
// host's key as one to REFUSE — locking the operator out with a button labelled "trust".
func TestTrustHostKeyRefusesJunkAndMarkers(t *testing.T) {
	for name, line := range map[string]string{
		"empty":          "",
		"not a line":     "this is not a known_hosts entry",
		"no key":         "nas.example ssh-ed25519",
		"revoked":        "@revoked nas.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB+8lLYbHzKQCP0hM8u6dCP5C0Uc0nsm1qCJqjkTxSCe",
		"cert-authority": "@cert-authority nas.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB+8lLYbHzKQCP0hM8u6dCP5C0Uc0nsm1qCJqjkTxSCe",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			if err := TrustHostKey(path, line); err == nil {
				t.Errorf("%q was accepted", line)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a refused line still created the file")
			}
		})
	}
}

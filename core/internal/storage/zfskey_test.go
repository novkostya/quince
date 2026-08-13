package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// quince#818 piece B — quince generates or FINDS its own key.

// GENERATION PRODUCES SOMETHING SSH WILL ACTUALLY ACCEPT, which is the only claim worth making
// about a key. Parsed back with the same library ssh uses rather than eyeballed for shape.
func TestEnsureZFSKeyGeneratesAUsableKeypair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "zfs")

	k, err := EnsureZFSKey(path)
	if err != nil {
		t.Fatalf("EnsureZFSKey: %v", err)
	}
	if !k.Created {
		t.Fatalf("Created = false for a key that did not exist — the form would say quince FOUND this")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the private half was not written: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		t.Fatalf("what was written is not a private key ssh can load: %v", err)
	}
	// The public half quince SHOWS must be the public half of the key it WROTE. A form that shows
	// one key while the transport uses another fails at the first backup, with an authorized_keys
	// entry the operator pasted correctly.
	if got, want := k.PublicKey, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))+" quince"; got != want {
		t.Fatalf("the shown public key is not the written key's:\n got %q\nwant %q", got, want)
	}
}

// 0600 ON THE FILE, 0700 ON THE DIRECTORY — and ssh is the reason rather than tidiness: it REFUSES a
// key others can read, and it refuses at the first backup rather than here.
func TestEnsureZFSKeyWritesRestrictivePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	path := filepath.Join(dir, "zfs")

	if _, err := EnsureZFSKey(path); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key mode = %#o, want 0600 — ssh refuses a key others can read", perm)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("key directory mode = %#o, want 0700", perm)
	}
}

// DISCOVERY IS THE SAFETY PROPERTY, not an optimisation (quince#818). An existing key's public half
// may already be in an operator's `authorized_keys` on a host quince cannot see, so replacing it
// breaks a working storage SILENTLY — nothing fails until the next backup.
//
// Asserted on the BYTES, because "returned the same public key" would also pass if the file had been
// rewritten with an identical-looking one.
func TestEnsureZFSKeyNeverReplacesAKeyThatIsAlreadyThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "zfs")

	first, err := EnsureZFSKey(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	second, err := EnsureZFSKey(path)
	if err != nil {
		t.Fatalf("EnsureZFSKey on an existing key: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("THE EXISTING PRIVATE KEY WAS REWRITTEN — that silently breaks any host whose "+
			"authorized_keys carries the old public half.\nbefore %d bytes, after %d", len(before), len(after))
	}
	if second.Created {
		t.Fatalf("Created = true for a key quince FOUND — the form would offer to overwrite it")
	}
	if second.PublicKey != first.PublicKey {
		t.Fatalf("the rediscovered public key differs from the generated one:\n%q\n%q", second.PublicKey, first.PublicKey)
	}
}

// A FILE THAT IS NOT A KEY IS A REFUSAL, NOT A REASON TO GENERATE. Replacing it destroys whatever it
// actually was, and "quince overwrote a file I put there" is worse than a refusal naming the path.
func TestEnsureZFSKeyRefusesToOverwriteSomethingThatIsNotAKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zfs")
	const notAKey = "this is not a key, it is somebody's notes\n"
	if err := os.WriteFile(path, []byte(notAKey), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureZFSKey(path); err == nil {
		t.Fatalf("EnsureZFSKey accepted a file that is not a private key")
	} else if !strings.Contains(err.Error(), path) {
		t.Fatalf("the refusal does not name the file: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != notAKey {
		t.Fatalf("the file was overwritten despite not being a key")
	}
}

// THE AUTHORIZED_KEYS LINE CARRIES THE FORCED COMMAND, and that is the whole reason the screen shows
// a line rather than a key. `command="…"` pins the helper regardless of what the client asks for; a
// naked public key pasted into authorized_keys is an unconstrained shell login on the storage host.
func TestTheAuthorizedKeysLinePinsTheHelperAndRestrictsTheSession(t *testing.T) {
	k, err := EnsureZFSKey(filepath.Join(t.TempDir(), "zfs"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(k.AuthorizedKeys, `command="`+ZFSHelperPath+`"`) {
		t.Fatalf("the line does not START with the forced command, so an operator truncating it "+
			"keeps the key and loses the constraint: %s", k.AuthorizedKeys)
	}
	for _, want := range []string{
		"no-port-forwarding", "no-agent-forwarding", "no-pty", "no-X11-forwarding",
		"ssh-ed25519 ", " quince",
	} {
		if !strings.Contains(k.AuthorizedKeys, want) {
			t.Fatalf("the line is missing %q: %s", want, k.AuthorizedKeys)
		}
	}
	// It must be ONE line: a pasted newline splits it into a restriction list and a stray key.
	if strings.Contains(strings.TrimSpace(k.AuthorizedKeys), "\n") {
		t.Fatalf("the authorized_keys line contains a newline: %q", k.AuthorizedKeys)
	}
}

// THE PRIVATE HALF NEVER LEAVES THE FILE. Nothing on the returned struct may carry it — the same
// rule the backup password follows, and the reason everything here is safe to render.
func TestZFSKeyCarriesNoPrivateMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zfs")
	k, err := EnsureZFSKey(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// The PEM body, minus its armour — the bytes that must never appear in anything rendered.
	body := strings.NewReplacer("-----BEGIN OPENSSH PRIVATE KEY-----", "",
		"-----END OPENSSH PRIVATE KEY-----", "", "\n", "").Replace(string(raw))
	if len(body) < 32 {
		t.Fatalf("could not isolate the private body; the test would pass vacuously")
	}

	for name, shown := range map[string]string{
		"PublicKey":      k.PublicKey,
		"AuthorizedKeys": k.AuthorizedKeys,
		"Path":           k.Path,
	} {
		if strings.Contains(shown, body) {
			t.Fatalf("%s carries the private key body", name)
		}
	}
}

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/novkostya/quince/core/internal/config"
)

// quince#818 piece B — quince generates or FINDS its own key.

// GENERATION PRODUCES SOMETHING SSH WILL ACTUALLY ACCEPT, which is the only claim worth making
// about a key. Parsed back with the same library ssh uses rather than eyeballed for shape.
func TestEnsureZFSKeyGeneratesAUsableKeypair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "zfs")

	k, err := EnsureZFSKey(path, "tank/backups")
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

	if _, err := EnsureZFSKey(path, "tank/backups"); err != nil {
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

	first, err := EnsureZFSKey(path, "tank/backups")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	second, err := EnsureZFSKey(path, "tank/backups")
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

	if _, err := EnsureZFSKey(path, "tank/backups"); err == nil {
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
	k, err := EnsureZFSKey(filepath.Join(t.TempDir(), "zfs"), "tank/backups")
	if err != nil {
		t.Fatal(err)
	}

	// THE DATASET IS INSIDE THE FORCED COMMAND SINCE quince#985, and inside the SAME quotes. sshd
	// parses `command="…"` as one option value, so a line that closed the quote after the path would
	// leave the dataset where sshd expects the next option name, and the whole line is rejected.
	if !strings.HasPrefix(k.AuthorizedKeys, `command="`+ZFSHelperPath+` tank/backups"`) {
		t.Fatalf("the line does not START with the forced command and its dataset, so an operator "+
			"truncating it keeps the key and loses the constraint: %s", k.AuthorizedKeys)
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
	k, err := EnsureZFSKey(path, "tank/backups")
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

// A NAME THAT COULD BREAK OUT OF THE QUOTES IS REFUSED, NOT ESCAPED — moved here from the helper
// renderer by quince#985, because this is where the interpolation now happens.
//
// The value lands inside `command="…"` in a file SSHD PARSES. A `"` closes the option value, and
// what follows is read as further options — `command="x" ,pty …` is not a syntax error, it is a
// different set of restrictions. `%q` would escape it rather than refuse, which turns an unusable
// name into an unreadable line an operator would paste anyway.
//
// Refusing is right rather than escaping: every legal ZFS dataset name already matches the pattern,
// so nothing valid is lost, and an escaping routine is a thing that can have a bug.
func TestEnsureZFSKeyRefusesADatasetThatCouldBreakTheLine(t *testing.T) {
	for _, bad := range []string{
		`tank" no-pty="`,
		`tank",pty,command="/bin/sh`,
		"tank/backups with spaces",
		"tank/backups\ncommand=\"/bin/sh\" ssh-ed25519 AAAA",
		"",
		"/leading-slash",
	} {
		path := filepath.Join(t.TempDir(), "zfs")
		_, err := EnsureZFSKey(path, bad)
		if err == nil {
			t.Errorf("EnsureZFSKey accepted the dataset %q — it must refuse before interpolating", bad)
			continue
		}
		if !errors.Is(err, ErrUnsafeDataset) {
			t.Errorf("EnsureZFSKey(%q) = %v, want ErrUnsafeDataset — the endpoint tells a 422 from a "+
				"500 by this sentinel, so a plain error here becomes 'quince could not create a key'", bad, err)
		}
		// AND IT REFUSES BEFORE IT WRITES. A key generated for a request that then fails leaves a
		// keypair on disk whose public half nobody was ever shown.
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("EnsureZFSKey(%q) wrote a key before refusing", bad)
		}
	}
}

// quince#1038 — TYPING WRITES NOTHING PER KEYSTROKE, AND NOTHING REACHES `zfs-*` UNTIL Add.

// THE DEFECT, STATED AS A TEST: asking about twelve prefixes of one dataset must leave ONE key.
//
// The form re-asks on every debounced change, because the `authorized_keys` line carries the dataset
// and a stale line confines the key to the wrong parent. When each dataset derived its own path and
// the endpoint generated into it, that produced a private key per prefix — fourteen on a lab rig
// from typing two names. There is no debounce or validation that fixes it: `labpool` is a legal
// dataset name, so a prefix and a finished name are the same shape.
func TestTypingADatasetNameLeavesOnePendingKey(t *testing.T) {
	dir := t.TempDir()
	name := "labpool/quince"

	var seen []string
	for i := 1; i <= len(name); i++ {
		prefix := name[:i]
		if !datasetPattern.MatchString(prefix) {
			continue // `labpool/` alone is not a legal name; the form would get a 422
		}
		k, err := ZFSKeyFor(dir, prefix)
		if err != nil {
			t.Fatalf("ZFSKeyFor(%q): %v", prefix, err)
		}
		seen = append(seen, k.Fingerprint)
	}
	if len(seen) < 5 {
		t.Fatalf("only %d prefixes were legal names; this test would not exercise the defect", len(seen))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	if len(files) != 1 || files[0] != PendingKeyName {
		t.Fatalf("typing %d prefixes left %v — want exactly [%s]", len(seen), files, PendingKeyName)
	}

	// AND IT IS ONE KEY, NOT ONE PER PREFIX THAT HAPPENED TO OVERWRITE. The material must not change
	// while the operator types: they may have pasted the line already.
	for _, fp := range seen[1:] {
		if fp != seen[0] {
			t.Fatalf("the key changed while typing: %q then %q — a line pasted early would be dead",
				seen[0], fp)
		}
	}
}

// THE LINE FOLLOWS THE DATASET EVEN THOUGH THE KEY DOES NOT. This is what quince#996 needed the
// re-fetch for, and it never required a new keypair.
func TestThePendingLineRendersForWhicheverDatasetIsAsked(t *testing.T) {
	dir := t.TempDir()

	a, err := ZFSKeyFor(dir, "tank/one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ZFSKeyFor(dir, "tank/two")
	if err != nil {
		t.Fatal(err)
	}

	if a.Fingerprint != b.Fingerprint {
		t.Error("two datasets got two pending keys — there is one, by ruling")
	}
	if !strings.Contains(a.AuthorizedKeys, " tank/one\"") {
		t.Errorf("the line for tank/one does not confine to it: %q", a.AuthorizedKeys)
	}
	if !strings.Contains(b.AuthorizedKeys, " tank/two\"") {
		t.Errorf("the line for tank/two does not confine to it: %q", b.AuthorizedKeys)
	}
	// AND IT SAYS WHERE IT WILL LAND, per dataset, so the screen can name a path the operator will
	// recognise later rather than the dot-file it sits in now.
	if a.LandsAt == b.LandsAt || !a.Pending || !b.Pending {
		t.Errorf("lands_at = %q and %q, pending = %v and %v", a.LandsAt, b.LandsAt, a.Pending, b.Pending)
	}
}

// ADD IS WHAT MOVES IT, and afterwards `/data/keys/zfs-*` names exactly the committed storages.
func TestCommitMovesThePendingKeyIntoPlace(t *testing.T) {
	dir := t.TempDir()

	k, err := ZFSKeyFor(dir, "tank/one")
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitZFSKey(dir, "tank/one", k.Fingerprint); err != nil {
		t.Fatalf("CommitZFSKey: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, PendingKeyName)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the pending key survived the move — it is a rename, so nothing should be left behind")
	}
	landed, err := os.Stat(k.LandsAt)
	if err != nil {
		t.Fatalf("the key did not land at %s: %v", k.LandsAt, err)
	}
	if perm := landed.Mode().Perm(); perm != 0o600 {
		t.Errorf("landed mode = %#o, want 0600 — ssh refuses a key others can read", perm)
	}

	// AND THE COMMITTED KEY IS NOW WHAT THE DATASET ANSWERS WITH, no longer pending.
	again, err := ZFSKeyFor(dir, "tank/one")
	if err != nil {
		t.Fatal(err)
	}
	if again.Pending || again.Created || again.Fingerprint != k.Fingerprint {
		t.Errorf("after the move: pending=%v created=%v fingerprint match=%v",
			again.Pending, again.Created, again.Fingerprint == k.Fingerprint)
	}
}

// CONSTRAINT 2 — THE TWO-TAB CASE, which is the single sharp edge of one shared pending key.
//
// Tab A and tab B each render the pending key for a different dataset and both operators paste their
// line on the host. Tab A saves; the key MOVES. Tab B saves — and if quince quietly made a fresh key
// there, the line tab B pasted would be dead and the storage would fail later, somewhere else.
func TestCommitRefusesWhenThePendingKeyIsNotTheOneShown(t *testing.T) {
	dir := t.TempDir()

	shown, err := ZFSKeyFor(dir, "tank/one") // both tabs were shown this key
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitZFSKey(dir, "tank/one", shown.Fingerprint); err != nil { // tab A adds
		t.Fatal(err)
	}

	// Tab B saves for its own dataset, carrying the fingerprint it was shown.
	err = CommitZFSKey(dir, "tank/two", shown.Fingerprint)
	if !errors.Is(err, ErrPendingKeyChanged) {
		t.Fatalf("tab B's save = %v, want ErrPendingKeyChanged — otherwise quince makes a second key "+
			"and the line tab B pasted authenticates nothing", err)
	}
	if _, statErr := os.Stat(config.ZFSKeyPathIn(dir, "tank/two")); statErr == nil {
		t.Error("a key was written for tab B's dataset despite the refusal")
	}

	// AND A SAVE CARRYING NO FINGERPRINT AT ALL IS THE SAME REFUSAL, rather than a silent generate.
	if err := CommitZFSKey(dir, "tank/three", ""); !errors.Is(err, ErrPendingKeyChanged) {
		t.Errorf("an empty fingerprint = %v, want ErrPendingKeyChanged", err)
	}
}

// IT NEVER OVERWRITES A COMMITTED KEY. Discover-before-generate is unchanged: an existing key may
// already be in an `authorized_keys` quince cannot see, so a differing fingerprint is a refusal
// rather than a replacement — and a matching one is a no-op, so a retried save is safe.
func TestCommitNeverOverwritesAKeyAlreadyThere(t *testing.T) {
	dir := t.TempDir()

	first, err := ZFSKeyFor(dir, "tank/one")
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitZFSKey(dir, "tank/one", first.Fingerprint); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(config.ZFSKeyPathIn(dir, "tank/one"))
	if err != nil {
		t.Fatal(err)
	}

	// The same save again — idempotent, because the fingerprint still matches.
	if err := CommitZFSKey(dir, "tank/one", first.Fingerprint); err != nil {
		t.Errorf("a repeated save was refused: %v", err)
	}

	// A save carrying somebody else's fingerprint must not replace it.
	if err := CommitZFSKey(dir, "tank/one", "SHA256:not-the-key-that-is-there"); !errors.Is(err, ErrPendingKeyChanged) {
		t.Errorf("a mismatched save = %v, want ErrPendingKeyChanged", err)
	}
	after, err := os.ReadFile(config.ZFSKeyPathIn(dir, "tank/one"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("THE COMMITTED KEY WAS REWRITTEN — that silently breaks any host whose authorized_keys " +
			"carries the old public half")
	}
}

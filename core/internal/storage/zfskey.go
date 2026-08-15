package storage

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	// ONE DERIVATION, NOT TWO. `config.ZFSKeyPathIn` is what `SSHArgv` composes `-i` from, so the
	// path this package moves a key TO must be the same function rather than a second copy of the
	// escaping rule — the two would drift and the symptom would be a storage whose key quince writes
	// in one place and looks for in another. A one-way edge: `config` imports nothing from here.
	"github.com/novkostya/quince/core/internal/config"
)

// ZFSKey is the keypair quince uses to reach the constrained helper, as the FORM needs it
// (quince#818 piece B).
//
// THE PRIVATE HALF IS NEVER IN THIS STRUCT. It exists only as bytes on their way to a 0600 file
// inside `/data/keys/`, and nothing returns it, renders it or logs it — the same rule the backup
// password follows, for the same reason. Everything here is safe to put on a screen.
type ZFSKey struct {
	// Path is where the private half lives, so the form can say what `ssh_key` would be set to.
	Path string
	// PublicKey is the `ssh-ed25519 AAAA… quince` line.
	PublicKey string
	// AuthorizedKeys is the COMPLETE line to paste on the ZFS host — the forced command included.
	//
	// IT IS THE WHOLE POINT OF SHOWING ANYTHING. `command="…"` is what pins the helper regardless of
	// what the client asks for, so a public key shown WITHOUT it invites an operator to paste a
	// naked key and get an unconstrained shell login on their storage host. The two are one artifact.
	//
	// SINCE quince#985 IT ALSO CARRIES THE DATASET, and that makes this line the only place the
	// confinement is written down. The helper file is now identical on every install; what bounds
	// THIS key to THIS storage is the word after the helper path here.
	AuthorizedKeys string
	// Created distinguishes *quince made this just now* from *quince found the one you already had*.
	// The form must say which: overwriting a key whose public half is already in someone's
	// authorized_keys breaks a working storage silently, and the failure surfaces at the next backup.
	//
	// IT DESCRIBES THE STORAGE, NOT THE FILE (quince#1038). It used to be *did this call write a
	// file*, which the debounced re-fetch made permanently false — the keystroke that finished the
	// dataset name found what an earlier keystroke had made, so the panel said *quince found an ssh
	// key it made earlier* about a key one second old. The question the operator is actually asking
	// is *does this dataset already have a key I may have installed* , and while it is pending it
	// does not.
	Created bool
	// Pending says this key is not yet in `/data/keys/zfs-*` — it is the one `.pending` key, shown
	// for a storage that has not been added.
	//
	// ON THE WIRE BECAUSE THE SCREEN MUST NOT PROMISE A FILE THAT IS NOT THERE. `Path` names where
	// the private half lives *now*; while pending, that is a dot-file the operator should not be
	// told to point `ssh_key` at, because it is about to move.
	Pending bool
	// LandsAt is where a pending key will be moved when the storage is added — set only while
	// Pending, so the form can say where it is going rather than where it is.
	LandsAt string
	// Fingerprint is the `SHA256:…` of the public half.
	//
	// IT IS WHAT THE SAVE CARRIES BACK. One pending key is shared by every open tab, so a save has to
	// prove it is committing the key whose line the operator actually pasted — see CommitZFSKey.
	// Public, derived, and safe to render: it is the same string `ssh-keygen -lf` prints.
	Fingerprint string
}

// ZFSHelperPath is the forced command the authorized_keys line pins — and, since quince#818 piece C,
// what GET /api/storages/zfs/helper tells the operator to save the script AS. Exported so the two
// cannot drift: the path the key is constrained to and the path the instruction names are now one
// constant, and a helper installed anywhere else is simply never reached.
//
// ONE PATH IS NOW CORRECT RATHER THAN A COLLISION (quince#985). It was both, briefly: the constant
// was right about where the file goes and wrong about there being one file, because each install
// rendered its own `PARENT=`. A second zfs storage saved its helper over the first's and the first
// broke at its next commit. The dataset moved into the forced command, so one path is one file
// again.
const ZFSHelperPath = "/usr/local/sbin/quince-zfs-helper"

// authorizedKeysOptions are the restrictions that ride with the forced command, in the order
// deploy/storage.md publishes them so an operator can diff the two by eye.
const authorizedKeysOptions = "no-port-forwarding,no-agent-forwarding,no-pty,no-X11-forwarding"

// ErrUnsafeDataset means the dataset name could not be put into an `authorized_keys` line.
//
// IT IS THE CALLER'S FAULT AND THEREFORE A 422, which is why it is a sentinel rather than a plain
// error: the other way `EnsureZFSKey` fails — a `/data` it cannot write, a file that is not a key —
// is quince's own problem and answers `500`. The endpoint has to tell them apart to name a field.
var ErrUnsafeDataset = errors.New("storage: that is not a usable ZFS dataset name")

// EnsureZFSKey returns the keypair at path, GENERATING ONE ONLY IF THERE IS NOTHING THERE.
//
// DISCOVERY IS NOT AN OPTIMISATION, it is the safety property (quince#818). If `/data/keys/zfs`
// already exists, its public half may already be in an operator's `authorized_keys` on a host quince
// cannot see. Overwriting it would break a working storage **silently** — nothing fails until the
// next backup, hours or a day later. So an existing key is read and offered; it is never replaced,
// and this function has no force flag by design.
//
// ed25519 IN-PROCESS RATHER THAN `ssh-keygen` (build-time decision, quince#818 left it open).
// `golang.org/x/crypto` is already a direct dependency, so the subpackage costs no new module;
// `CLAUDE.md`'s subprocess rules favour not spawning one; and the private half never passes through
// argv, a temp file or another process's memory. `ssh-keygen` IS in the runtime image — measured —
// so this is a choice rather than a necessity.
// PARENT IS AN INPUT SINCE quince#985, and it is what the returned `authorized_keys` line confines
// the key to. It is validated before it is interpolated for the same reason the helper's `PARENT=`
// used to be: the value lands inside `command="…"` in a file sshd parses, and a `"` would close that
// option and turn the remainder into further options. Refused rather than escaped — every legal ZFS
// name already matches `datasetPattern`, so nothing valid is lost.
func EnsureZFSKey(path, parentDataset string) (ZFSKey, error) {
	if path == "" {
		return ZFSKey{}, errors.New("storage: no key path")
	}
	if !datasetPattern.MatchString(parentDataset) {
		return ZFSKey{}, fmt.Errorf("%w: %q", ErrUnsafeDataset, parentDataset)
	}

	switch existing, err := os.ReadFile(path); { //nolint:gosec // the path is config, not user input
	case err == nil:
		k, perr := zfsKeyFromPrivate(path, parentDataset, existing)
		if perr != nil {
			// A FILE THAT IS NOT A KEY IS A REFUSAL, NOT A REASON TO GENERATE. Replacing it would
			// destroy whatever it actually is, and "quince overwrote a file I put there" is a worse
			// outcome than a refusal naming the path.
			return ZFSKey{}, fmt.Errorf("storage: %s exists but is not a usable private key (%w) — "+
				"move it aside if you want quince to make a new one", path, perr)
		}
		return k, nil
	case !errors.Is(err, os.ErrNotExist):
		return ZFSKey{}, fmt.Errorf("storage: cannot read %s: %w", path, err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ZFSKey{}, fmt.Errorf("storage: generating a key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return ZFSKey{}, fmt.Errorf("storage: encoding the key: %w", err)
	}

	// 0700 ON THE DIRECTORY AND 0600 ON THE FILE, because ssh refuses a key whose permissions let
	// anyone else read it — and it refuses at the moment of the first backup, not here.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ZFSKey{}, fmt.Errorf("storage: creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return ZFSKey{}, fmt.Errorf("storage: writing %s: %w", path, err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return ZFSKey{}, fmt.Errorf("storage: deriving the public key: %w", err)
	}
	k := zfsKeyFromPublic(path, parentDataset, sshPub)
	k.Created = true
	return k, nil
}

// zfsKeyFromPrivate re-derives the public half from a private key already on disk. quince keeps no
// `.pub` file: a second artifact can disagree with the first, and the public half is derivable.
func zfsKeyFromPrivate(path, parentDataset string, pemBytes []byte) (ZFSKey, error) {
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return ZFSKey{}, err
	}
	return zfsKeyFromPublic(path, parentDataset, signer.PublicKey()), nil
}

func zfsKeyFromPublic(path, parentDataset string, pub ssh.PublicKey) ZFSKey {
	// `quince` AS THE COMMENT, so an operator scanning a host's authorized_keys can tell at a glance
	// which line is ours — and which to remove when they retire a storage.
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " quince"
	return ZFSKey{
		Path:           path,
		PublicKey:      line,
		AuthorizedKeys: authorizedKeysLine(parentDataset, line),
		Fingerprint:    ssh.FingerprintSHA256(pub),
	}
}

// PendingKeyName is the ONE key quince holds for a storage that has not been added yet.
//
// ONE FILE, NOT ONE PER DATASET (Operator ruling, quince#1038). A pending path derived from the
// dataset would have relocated the litter rather than removed it: typing `l` → `lab` → `labpool`
// would still make three keypairs, just behind a dot. There is one, so there is nothing to sweep.
//
// A DOT PREFIX so it is outside the `zfs-*` audit glob: a listing of `/data/keys/` answers *which
// parents can quince reach* and must not be diluted by a key belonging to no storage.
const PendingKeyName = ".pending"

// ErrPendingKeyChanged means the key the operator was shown is not the key quince now holds.
//
// THE TWO-TAB CASE, and it is the one sharp edge of the single-file shape. Tab A and tab B both
// render the pending key for different datasets and both operators paste their line on the host; tab
// A presses Add, the pending key MOVES, and tab B's `.pending` is gone. Generating a fresh one for
// tab B would leave the line it pasted dead and the storage failing later, somewhere else — so the
// save refuses and says to regenerate and re-paste.
var ErrPendingKeyChanged = errors.New("storage: the key this line was made for is no longer the one quince holds")

// ZFSKeyFor answers *what is the key situation for this dataset* — and WRITES NOTHING per call
// beyond generating the single pending key the first time one is needed (quince#1038).
//
// TWO ANSWERS, and which one you get is whether this dataset has a committed key:
//
//   - a key at the derived path → that key, `Created: false`. The storage already exists here.
//   - nothing there → the PENDING key's line, rendered for this dataset, with `LandsAt` naming
//     where it will be moved on Add.
//
// THE KEY MATERIAL DOES NOT CHANGE WHILE THE OPERATOR TYPES. What changes is the `PARENT` inside
// `command="…"` on the rendered line, which is the only reason quince#996 needed a re-fetch at all —
// it never required a new keypair, and reading it as though it did is what put fourteen private keys
// on a lab rig.
func ZFSKeyFor(dir, parentDataset string) (ZFSKey, error) {
	if dir == "" {
		return ZFSKey{}, errors.New("storage: no key directory")
	}
	if !datasetPattern.MatchString(parentDataset) {
		return ZFSKey{}, fmt.Errorf("%w: %q", ErrUnsafeDataset, parentDataset)
	}

	derived := config.ZFSKeyPathIn(dir, parentDataset)
	switch existing, err := os.ReadFile(derived); { //nolint:gosec // the path is derived, not caller input
	case err == nil:
		return zfsKeyFromPrivate(derived, parentDataset, existing)
	case !errors.Is(err, os.ErrNotExist):
		return ZFSKey{}, fmt.Errorf("storage: cannot read %s: %w", derived, err)
	}

	// NOT COMMITTED YET — show the pending key, and say where it is going.
	k, err := EnsureZFSKey(filepath.Join(dir, PendingKeyName), parentDataset)
	if err != nil {
		return ZFSKey{}, err
	}
	k.Pending = true
	k.LandsAt = derived
	// `Created` DESCRIBES THE STORAGE, NOT THE FILE. A pending key made on the first keystroke is
	// "found" by the second, which is what made this field always-false and its sentence a lie. What
	// the operator needs to know is whether THIS DATASET already has a key — and while it is pending,
	// it does not.
	k.Created = true
	return k, nil
}

// CommitZFSKey moves the pending key to the derived path for a dataset the operator has just added.
//
// THIS IS THE ONLY THING THAT WRITES INTO THE AUDIT SURFACE (Operator ruling, quince#1038). A key
// under `zfs-*` therefore means a storage quince committed to — which is the property quince#1038
// reported as lost, and which a *Make a key* button would not have restored, since a pressed-then-
// abandoned storage still leaves one behind.
//
// `fingerprint` IS THE KEY THE OPERATOR WAS SHOWN, and checking it is constraint 2 of the ruling.
// Without it the two-tab case fails silently and late: tab B saves, quince quietly makes a second
// key, and the line tab B pasted on the host authenticates nothing.
//
// IT NEVER OVERWRITES. Discover-before-generate is unchanged — an existing key at the derived path
// may already be in an `authorized_keys` quince cannot see, so a matching fingerprint is a no-op and
// a differing one is a refusal rather than a replacement.
func CommitZFSKey(dir, parentDataset, fingerprint string) error {
	if dir == "" {
		return errors.New("storage: no key directory")
	}
	if !datasetPattern.MatchString(parentDataset) {
		return fmt.Errorf("%w: %q", ErrUnsafeDataset, parentDataset)
	}
	if fingerprint == "" {
		return fmt.Errorf("%w: the request carried no key fingerprint", ErrPendingKeyChanged)
	}

	derived := config.ZFSKeyPathIn(dir, parentDataset)
	switch existing, err := os.ReadFile(derived); { //nolint:gosec // the path is derived, not caller input
	case err == nil:
		k, perr := zfsKeyFromPrivate(derived, parentDataset, existing)
		if perr != nil {
			return fmt.Errorf("storage: %s exists but is not a usable private key: %w", derived, perr)
		}
		if k.Fingerprint != fingerprint {
			return fmt.Errorf("%w: %s already holds a different key", ErrPendingKeyChanged, derived)
		}
		return nil // already committed, and it is the key they were shown
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("storage: cannot read %s: %w", derived, err)
	}

	pending := filepath.Join(dir, PendingKeyName)
	raw, err := os.ReadFile(pending) //nolint:gosec // a fixed name inside quince's own directory
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: quince holds no pending key", ErrPendingKeyChanged)
		}
		return fmt.Errorf("storage: cannot read %s: %w", pending, err)
	}
	k, err := zfsKeyFromPrivate(pending, parentDataset, raw)
	if err != nil {
		return fmt.Errorf("storage: %s is not a usable private key: %w", pending, err)
	}
	if k.Fingerprint != fingerprint {
		return fmt.Errorf("%w: quince now holds a different pending key", ErrPendingKeyChanged)
	}

	// A RENAME RATHER THAN A COPY: it is atomic within one directory, so there is no window in which
	// both names hold the key, and nothing is left behind to sweep.
	if err := os.Rename(pending, derived); err != nil {
		return fmt.Errorf("storage: moving the pending key to %s: %w", derived, err)
	}
	return nil
}

// authorizedKeysLine composes the one artifact the operator installs beside the helper.
//
// `%q` ON THE WHOLE FORCED COMMAND, PATH AND DATASET TOGETHER — one quoted option value containing a
// space, which is exactly what sshd expects and what the helper's `$1` then receives. Splitting it
// into two quoted strings would not parse; leaving it unquoted would end the option at the space and
// sshd would read the dataset as an option name.
//
// THE CALLER HAS ALREADY VALIDATED THE DATASET. This function is unexported and has one caller for
// that reason: `%q` escapes a `"` rather than refusing it, so a name that could break out would
// arrive here as `\"` and land in the file as something an operator cannot read. The refusal belongs
// upstream, where there is a field to name.
func authorizedKeysLine(parentDataset, publicKey string) string {
	return fmt.Sprintf("command=%q,%s %s",
		ZFSHelperPath+" "+parentDataset, authorizedKeysOptions, publicKey)
}

// ZFSKeyInUse is the key a storage with this parent WOULD authenticate with right now — the
// committed one if there is one, otherwise the pending one. It reads; it never generates.
//
// `Test helper` NEEDS IT AND CANNOT USE THE DERIVED PATH ALONE (quince#1040). The check runs BEFORE
// the storage is added — it is what gates the save — so for a new storage there is nothing at the
// derived path yet: the key the operator was shown, and pasted on their host, is `.pending`. Pointing
// `ssh -i` at the derived path there names a file that does not exist, ssh offers nothing, and sshd
// answers `Permission denied (publickey)` — a refusal about the key that reads exactly like a wrong
// forced command.
//
// IT NEVER GENERATES, unlike `ZFSKeyFor`. A check is a question about what is already there, and a
// press that quietly created key material would be the defect quince#1038 removed, arriving through
// a second door. If there is no key at all the check fails honestly.
func ZFSKeyInUse(dir, parentDataset string) string {
	derived := config.ZFSKeyPathIn(dir, parentDataset)
	if _, err := os.Stat(derived); err == nil {
		return derived
	}
	return filepath.Join(dir, PendingKeyName)
}

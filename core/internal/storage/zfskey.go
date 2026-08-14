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
	Created bool
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
	}
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

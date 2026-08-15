package config

import (
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultZFSKeyDir is where quince keeps the keys it generates or finds for the zfs helper.
//
// INSIDE `/data`, WHICH IS ALREADY MOUNTED BY BOTH SHIPPED COMPOSES — `deploy/compose.nas.yml` and
// `deploy/compose.lab.yml` — so a keypair written here survives a container recreate with no compose
// change and no restart. The documented `hook_cmd` already pointed inside it (`-i /data/keys/zfs`),
// so this is where operators' keys already are rather than a new location (quince#818).
//
// A DIRECTORY RATHER THAN ONE PATH SINCE quince#989. There is one key per PARENT DATASET, because a
// forced command is a property of a key: sshd uses the first `authorized_keys` line whose key
// matches and stops looking, so one key can be confined to exactly one parent. One path for all of
// them was not merely a missing feature — it produced a line pairing the FIRST key with the SECOND
// storage's dataset, which is inert, and left that storage confined to somebody else's parent while
// every probe read healthy.
const DefaultZFSKeyDir = "/data/keys"

// zfsKeySeparator replaces `/` in a dataset name to make one path component.
//
// EXCLUDED BY `datasetPattern` — the charset is `[A-Za-z0-9_.:/-]`, so no legal dataset can contain
// one and the mapping is injective: two different datasets cannot derive one filename. That is the
// guard quince#985 named when it rejected a `/` → `-` scheme, which is NOT injective (`tank/a-b/c`
// and `tank/a/b-c` both give `tank-a-b-c`).
//
// `+` AND NOT `%`, WHICH THE RULING NAMED FIRST AND WHICH DOES NOT WORK. **OpenSSH
// percent-expands `IdentityFile`**, so `ssh -i /data/keys/zfs-labpool%quince` never reaches the
// network — it dies parsing its own argument:
//
//	vdollar_percent_expand: unknown key %q
//	percent_dollar_expand: failed
//
// Measured on the rig, 2026-08-15, against the real client with the real key: `%` fails as above and
// `+` answers `4060418048 124788600832`. Every unit test passed under `%` — the derivation was
// injective, traversal-proof and stable, and produced a path the one tool that consumes it cannot
// open. `%%` would escape it and is worse: the escaped form is what an operator would have to type,
// and `ssh_key` in `config.yml` is not percent-expanded, so the file and the flag would disagree.
const zfsKeySeparator = "+"

// ZFSKeyPathFor derives the key path for one parent dataset (quince#989).
//
// ESCAPING, NOT REAL DIRECTORIES, and the reason is that `datasetPattern` is looser than it looks.
// It accepts `tank/../../etc` — only a LEADING `..` is blocked, since the first character must be
// alphanumeric or `_` — which under a mirrored directory tree resolves to `/data/etc`: a private key
// written outside the key directory, from a name quince's own validator accepts. It also accepts
// `tank/./x`, which normalises onto `tank/x` and reintroduces the collision this function exists to
// remove. Escaping resolves nothing and removes every separator, so neither is guarded against —
// both are unrepresentable.
//
// AND IT FORBIDS NO NAME. A mirrored tree cannot hold `tank/backups` beside `tank/backups/cold` —
// both legal datasets, the first wanting to be a file where the second needs a directory. Git chose
// that layout for loose refs and its only answer is to refuse (`cannot lock ref … 'refs/heads/foo'
// exists`); under ZFS, where nesting IS the model and quince's own canon puts one child dataset per
// device under the parent, that shape sits on the common path rather than in a corner.
//
// THE CALLER HAS ALREADY VALIDATED THE DATASET. This does not re-check: it is a pure derivation, and
// the endpoint that reaches it refuses an unsafe name before calling. A dataset that somehow arrived
// unvalidated would still be confined to one path component by the escape, which is the property
// that matters here.
func ZFSKeyPathFor(parentDataset string) string {
	return ZFSKeyPathIn(DefaultZFSKeyDir, parentDataset)
}

// ZFSKeyPathIn is ZFSKeyPathFor against a caller-chosen directory, so tests are hermetic.
//
// THE DIRECTORY IS QUINCE'S, NEVER A CALLER'S. `Deps.ZFSKeyDir` exists for the same reason the old
// `Deps.ZFSKeyPath` did — a test must not write into `/data` — and it is equally not a retargeting
// knob: nothing on the wire reaches it. Splitting the two makes that visible, where a single
// function reading a package constant would have had to be mocked.
func ZFSKeyPathIn(dir, parentDataset string) string {
	return filepath.Join(dir, "zfs-"+strings.ReplaceAll(parentDataset, "/", zfsKeySeparator))
}

// DefaultZFSKnownHosts is the `known_hosts` quince composes against.
//
// SEPARATE FROM THE SYSTEM FILE ON PURPOSE: the container's own `/etc/ssh/ssh_known_hosts` is part
// of the image and is replaced on every upgrade, so a host key trusted there would silently stop
// being trusted. This lives beside the key, in the volume that persists.
const DefaultZFSKnownHosts = "/data/keys/known_hosts"

// DefaultZFSSSHPort is the port quince composes when the config does not set one.
const DefaultZFSSSHPort = 22

// SSHArgv composes the transport as an ARRAY, never a string (quince#818).
//
// IT REPLACES `strings.Fields(hook_cmd)`. That split a free-text operator string on whitespace, so a
// key path containing a space produced an argv that was silently wrong — and there was no shape in
// which the operator could have escaped it. Building the array removes the question.
//
// THE HOST-KEY OPTIONS ARE PART OF THE TRANSPORT, not decoration, and they are why this composes
// `StrictHostKeyChecking=yes` rather than `accept-new`:
//
//   - `BatchMode=yes` disables every prompt, so an ssh with no `known_hosts` entry REFUSES instead
//     of hanging on a question nobody can answer — a container has no terminal.
//   - `UserKnownHostsFile` points at the volume, per DefaultZFSKnownHosts.
//   - `StrictHostKeyChecking=yes` is what stands between this and a machine-in-the-middle on the
//     operator's backups. `accept-new` trusts whatever answers first, which on a first connect is
//     exactly the moment an attacker would want. The add-storage form seeds `known_hosts` from a
//     fingerprint the operator confirms — `POST /api/storages/zfs/hostkey` then `…/trust`
//     (quince#912) — so by the time this argv runs there IS an entry, and if there is not, failing
//     is the honest outcome rather than trusting one.
//
// THAT LAST CLAUSE WAS FALSE FOR THE WHOLE OF quince#818, and it is why the gap survived. It
// credited the seeding to #818's form, which never touched `known_hosts` — so the refusal was not
// the honest outcome of a step somebody skipped, it was the only outcome there had ever been, on a
// file inside the container that no operator could reach. Anybody auditing this strict-checking
// decision read the claim and concluded the seeding was finished work belonging to someone else.
// Recorded rather than quietly corrected: a comment asserting a mechanism that does not exist is
// this project's most-filed defect, and this instance cost a blocked first run.
//
// `-o` PAIRS ARE TWO ELEMENTS, not one. `-oBatchMode=yes` also works, but the split form is what
// every doc and every operator's muscle memory writes, and an argv is read by people during an
// incident.
// NIL WHEN THERE IS NO TRANSPORT, and that is load-bearing rather than tidy. `WantZFS` asks whether
// a hook is configured, and its `auto` arm treats "a hook is set" as zfs intent. If this returned a
// full argv for an empty `zfs:` block — `ssh -i … @` — then EVERY `backend: auto` storage would
// resolve to zfs and try to reach a host that was never named. `hook_cmd` had this property for
// free, being empty when unset; composing has to preserve it deliberately.
func (z ZFSConfig) SSHArgv() []string {
	if !z.SSHConfigured() {
		return nil
	}
	port := z.SSHPort
	if port == 0 {
		port = DefaultZFSSSHPort
	}
	// AN EXPLICIT `ssh_key` STILL WINS, and it is the escape hatch the derivation must not close: an
	// operator who already has a key with a forced command deployed should not have to move it. What
	// changed in quince#989 is only what the DEFAULT resolves to — one path for every storage, which
	// silently gave a second storage the first one's key and therefore the first one's parent.
	key := z.SSHKey
	if key == "" {
		key = ZFSKeyPathFor(z.ParentDataset)
	}
	return []string{
		"ssh",
		"-i", key,
		"-p", strconv.Itoa(port),
		"-o", "BatchMode=yes",
		"-o", "UserKnownHostsFile=" + DefaultZFSKnownHosts,
		"-o", "StrictHostKeyChecking=yes",
		z.SSHUser + "@" + z.SSHHost,
	}
}

// SSHConfigured reports whether the transport has the two fields that have no default.
//
// PORT AND KEY ARE ABSENT FROM THIS TEST DELIBERATELY: both default, so their being unset says
// nothing about whether the operator declared a transport. User and host are the ones only they can
// supply, which makes them the honest predicate for "is there a helper to reach".
func (z ZFSConfig) SSHConfigured() bool { return z.SSHUser != "" && z.SSHHost != "" }

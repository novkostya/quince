package config

import "strconv"

// DefaultZFSKeyPath is where quince keeps the key it generates or finds for the zfs helper.
//
// INSIDE `/data`, WHICH IS ALREADY MOUNTED BY BOTH SHIPPED COMPOSES — `deploy/compose.nas.yml` and
// `deploy/compose.lab.yml` — so a keypair written here survives a container recreate with no compose
// change and no restart. The documented `hook_cmd` already pointed inside it (`-i /data/keys/zfs`),
// so this is where operators' keys already are rather than a new location (quince#818).
const DefaultZFSKeyPath = "/data/keys/zfs"

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
	key := z.SSHKey
	if key == "" {
		key = DefaultZFSKeyPath
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

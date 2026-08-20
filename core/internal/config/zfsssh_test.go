package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// quince#818 — SSH IS THE TRANSPORT, AND QUINCE COMPOSES IT.

// THE ARGV IS BUILT, NOT SPLIT — the property that deletes a whole bug class. `hook_cmd` reached
// `strings.Fields`, so a key path containing a space became two arguments and there was no spelling
// in which the operator could have escaped it.
func TestSSHArgvKeepsAPathWithSpacesAsONEArgument(t *testing.T) {
	z := ZFSConfig{SSHUser: "zfsuser", SSHHost: "zfshost", SSHKey: "/data/my keys/zfs"}
	argv := z.SSHArgv()

	var found bool
	for i, a := range argv {
		if a == "-i" && i+1 < len(argv) {
			if argv[i+1] != "/data/my keys/zfs" {
				t.Fatalf("-i argument = %q, want the path whole", argv[i+1])
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no -i in argv: %q", argv)
	}
	// And the old failure, stated as the thing that must not happen: the split form would have
	// produced an argument that is only part of the path.
	for _, a := range argv {
		if a == "/data/my" || a == "keys/zfs" {
			t.Fatalf("the key path was split across arguments: %q", argv)
		}
	}
}

// NIL WHEN THERE IS NO TRANSPORT. `WantZFS`'s `auto` arm treats "a hook is configured" as zfs
// intent, so an argv for an empty block would make EVERY auto storage resolve to zfs and try to
// reach a host nobody named. `hook_cmd` had this for free by being empty; composing must preserve it.
func TestSSHArgvIsNilWhenNoTransportIsConfigured(t *testing.T) {
	for _, z := range []ZFSConfig{
		{},
		{SSHUser: "only-user"},
		{SSHHost: "only-host"},
		{ParentDataset: "tank/x"}, // a parent alone is not a transport
	} {
		if argv := z.SSHArgv(); argv != nil {
			t.Fatalf("SSHArgv() = %q for %+v, want nil — an unconfigured transport must not read as zfs intent", argv, z)
		}
	}
}

// THE DEFAULTS ARE APPLIED AT COMPOSE TIME, so the happy path writes neither key into config.yml
// (D12) and the argv is still complete.
func TestSSHArgvDefaultsThePortAndTheKey(t *testing.T) {
	argv := ZFSConfig{SSHUser: "zfsuser", SSHHost: "zfshost", ParentDataset: "tank/backups"}.SSHArgv()
	joined := strings.Join(argv, " ")

	for _, want := range []string{
		// DERIVED FROM THE PARENT since quince#989, rather than one constant for every storage.
		"-i " + ZFSKeyPathFor("tank/backups"),
		"-p 22",
		"zfsuser@zfshost",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q does not contain %q", joined, want)
		}
	}
}

// THE HOST-KEY OPTIONS ARE NOT OPTIONAL, and `StrictHostKeyChecking` must be `yes` rather than
// `accept-new`. `accept-new` trusts whatever answers first, which on a first connect is exactly the
// moment a machine-in-the-middle would want — and `deploy/zfs-helper.md` names this as the property
// standing between that and the operator's backups.
func TestSSHArgvComposesStrictHostKeyChecking(t *testing.T) {
	joined := strings.Join(ZFSConfig{SSHUser: "u", SSHHost: "h"}.SSHArgv(), " ")

	for _, want := range []string{
		"BatchMode=yes",
		"UserKnownHostsFile=" + DefaultZFSKnownHosts,
		"StrictHostKeyChecking=yes",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q is missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "accept-new") {
		t.Fatalf("argv relaxes host-key checking to accept-new: %q", joined)
	}
}

// `hook_cmd` IS REFUSED BY NAME AND THE MESSAGE NAMES ITS SUCCESSORS. Operator ruling, relayed at
// https://github.com/novkostya/quince/issues/818#issuecomment-5245496176.
// `qn.6g`: a remedy the user cannot follow is the same defect as a silent failure, so "retired" on
// its own would not be enough — it has to say what to write instead.
func TestHookCmdIsRefusedAndTheRefusalNamesTheSuccessors(t *testing.T) {
	c := Default()
	c.Storage = &[]StorageEntry{{
		Name: "one", Path: "/backups", Default: true, Backend: "zfs",
		ZFS: ZFSConfig{ParentDataset: "tank/one", Mode: "hook", HookCmd: "ssh -i /k u@h", Seed: "auto"},
	}}

	errs := Validate(c)
	var msg string
	for _, e := range errs {
		if e.Path == "storage[0].zfs.hook_cmd" {
			msg = e.Message
		}
	}
	if msg == "" {
		t.Fatalf("a config carrying hook_cmd was not refused at that path; errs = %v", errs)
	}
	for _, want := range []string{"ssh_user", "ssh_host", "ssh_port", "ssh_key"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal does not name %q — an operator cannot act on it: %s", want, msg)
		}
	}
}

// A ZFS STORAGE WITH NO TRANSPORT IS REFUSED BY `CheckStorageBackendErrors`, NOT BY `Validate` —
// and the layer is the point rather than a detail. `Load` DISCARDS a config that fails `Validate`
// and falls back to `Default()`, so refusing there would trade a refusal naming the storage for a
// daemon running on defaults with no storage at all (quince#508's shape). This path returns the
// error and writes nothing, which is where a missing configuration belongs.
func TestAZFSStorageWithNoTransportIsRefusedWithoutDiscardingTheDocument(t *testing.T) {
	entry := StorageEntry{
		Name: "one", Path: "/backups", Default: true, Backend: "zfs",
		ZFS: ZFSConfig{ParentDataset: "tank/one", Mode: "hook", Seed: "auto"},
	}
	list := []StorageEntry{entry}

	if errs := Validate(Config{Storage: &list}); len(errs) > 0 {
		for _, e := range errs {
			if strings.Contains(e.Path, "ssh_") {
				t.Fatalf("Validate refused the missing transport at %s — that discards the document; "+
					"it belongs in CheckStorageBackendErrors", e.Path)
			}
		}
	}

	probs := CheckStorageBackendErrors(&list)
	if len(probs) != 1 {
		t.Fatalf("CheckStorageBackendErrors = %v, want exactly one problem naming the transport", probs)
	}
	for _, want := range []string{"one", "ssh_user", "ssh_host"} {
		if !strings.Contains(probs[0].Message, want) {
			t.Fatalf("the refusal does not name %q: %s", want, probs[0].Message)
		}
	}
}

// THE REFUSAL MUST NAME THE FIELD IT IS ABOUT, on every branch — `StorageBackendProblem.Field` is
// what a client highlights, and the type's own comment says so.
//
// THIS IS A REGRESSION TEST FOR A REVIEW FINDING, and the finding got through because the test
// above asserts the MESSAGE and never the field. An operator who set `ssh_host` and omitted
// `ssh_user` was told the user was missing while the form highlighted the host they had filled in
// correctly — `qn.6g`'s rule broken by the code that cites it.
func TestTheMissingTransportRefusalNamesTheFieldItIsAbout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		zfs        ZFSConfig
		wantField  string
		wantNamed  string
		wantNotSay string
	}{
		{
			name:       "host set, user missing",
			zfs:        ZFSConfig{ParentDataset: "tank/one", Mode: "hook", Seed: "auto", SSHHost: "nas"},
			wantField:  "zfs.ssh_user",
			wantNamed:  "`zfs.ssh_user`",
			wantNotSay: "`zfs.ssh_host`",
		},
		{
			name:      "user set, host missing",
			zfs:       ZFSConfig{ParentDataset: "tank/one", Mode: "hook", Seed: "auto", SSHUser: "zfsuser"},
			wantField: "zfs.ssh_host",
			wantNamed: "`zfs.ssh_host`",
		},
		{
			name:      "both missing",
			zfs:       ZFSConfig{ParentDataset: "tank/one", Mode: "hook", Seed: "auto"},
			wantField: "zfs.ssh_host",
			wantNamed: "`zfs.ssh_user` and `zfs.ssh_host`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list := []StorageEntry{{Name: "one", Path: "/backups", Default: true, Backend: "zfs", ZFS: tc.zfs}}

			probs := CheckStorageBackendErrors(&list)
			if len(probs) != 1 {
				t.Fatalf("CheckStorageBackendErrors = %v, want exactly one problem", probs)
			}
			wantPath := "storage[0]." + tc.wantField
			if probs[0].Path != wantPath {
				t.Fatalf("Path = %q, want %q — a client highlights the field this names, so pointing "+
					"at one the operator filled in correctly is the defect this guards",
					probs[0].Path, wantPath)
			}
			if !strings.Contains(probs[0].Message, tc.wantNamed) {
				t.Fatalf("message does not name %q: %s", tc.wantNamed, probs[0].Message)
			}
			if tc.wantNotSay != "" && strings.Contains(probs[0].Message, tc.wantNotSay) {
				t.Fatalf("message names %q, which the operator DID set: %s", tc.wantNotSay, probs[0].Message)
			}
		})
	}
}

// quince#989 — ONE KEY PER PARENT DATASET, and the derivation is what makes that true.

// TWO DATASETS CANNOT DERIVE ONE PATH. That is the whole safety property: a collision would hand a
// second storage the first one's key, and with it the first one's forced command and parent — which
// is the bug this replaces, arriving again through the escape instead of through a shared constant.
//
// `%` IS THE SEPARATOR BECAUSE `datasetPattern` EXCLUDES IT (`[A-Za-z0-9_.:/-]`). The pairs below are
// the ones a `/` → `-` scheme collapses, which is why quince#985 rejected that scheme when it was
// proposed for filenames.
func TestZFSKeyPathIsInjective(t *testing.T) {
	for _, pair := range [][2]string{
		{"tank/a-b/c", "tank/a/b-c"},
		{"tank/a", "tank-a"},
		{"tank/a/b", "tank/a-b"},
		{"rpool/quince", "rpool/quince2"},
	} {
		a, b := ZFSKeyPathFor(pair[0]), ZFSKeyPathFor(pair[1])
		if a == b {
			t.Errorf("%q and %q both derive %q — one storage would be handed the other's key",
				pair[0], pair[1], a)
		}
	}
}

// THE DERIVED PATH IS ONE COMPONENT INSIDE THE KEY DIRECTORY, WHATEVER THE DATASET SAYS.
//
// `datasetPattern` is looser than it looks: it ACCEPTS `tank/../../etc`, because only a LEADING `..`
// is blocked — the first character must be alphanumeric or `_`. A scheme that mirrored the dataset
// into real directories would resolve that to `/data/etc`, writing a private key outside the key
// directory from a name quince's own validator accepts. Escaping removes every separator, so this is
// not a guard that could be forgotten; it is unrepresentable.
func TestZFSKeyPathCannotEscapeItsDirectory(t *testing.T) {
	for _, hostile := range []string{
		"tank/../../etc",
		"tank/./x",
		"tank/../../../../../../root/.ssh/authorized_keys",
		"a" + strings.Repeat("/..", 40),
	} {
		got := ZFSKeyPathFor(hostile)
		if dir := filepath.Dir(got); dir != DefaultZFSKeyDir {
			t.Errorf("ZFSKeyPathFor(%q) = %q, which is in %q rather than %q",
				hostile, got, dir, DefaultZFSKeyDir)
		}
		if got != filepath.Clean(got) {
			t.Errorf("ZFSKeyPathFor(%q) = %q, which is not already clean — so it depends on when "+
				"somebody normalises it", hostile, got)
		}
	}
	// AND `tank/./x` STAYS DISTINCT FROM `tank/x`, which mirrored directories would collapse onto
	// one another — the collision this function exists to remove, reappearing through normalisation.
	if ZFSKeyPathFor("tank/./x") == ZFSKeyPathFor("tank/x") {
		t.Error("`tank/./x` and `tank/x` derive one path")
	}
}

// AN EXPLICIT `ssh_key` STILL WINS. It is the escape hatch for an operator who already has a key
// with a forced command deployed, and the derivation must not close it.
func TestSSHArgvPrefersAnExplicitKeyOverTheDerivedOne(t *testing.T) {
	z := ZFSConfig{SSHUser: "quince", SSHHost: "nas", ParentDataset: "tank/backups"}
	argv := strings.Join(z.SSHArgv(), " ")
	if want := "-i " + ZFSKeyPathFor("tank/backups"); !strings.Contains(argv, want) {
		t.Errorf("argv = %q, want it to carry %q", argv, want)
	}

	z.SSHKey = "/data/keys/mine"
	if argv := strings.Join(z.SSHArgv(), " "); !strings.Contains(argv, "-i /data/keys/mine") {
		t.Errorf("an explicit ssh_key was overridden by the derivation: %q", argv)
	}
}

// TWO STORAGES ON ONE HOST GET TWO KEYS; TWO ON ONE PARENT SHARE ONE.
//
// The second half is not an accident to be tolerated — it is correct. A forced command is a property
// of a key and confines it to one parent, so two storages under one parent have identical
// confinement and two keys would buy nothing but a second line to paste.
func TestSSHArgvKeysOneStoragePerParentDataset(t *testing.T) {
	key := func(parent string) string {
		z := ZFSConfig{SSHUser: "quince", SSHHost: "nas", ParentDataset: parent}
		argv := z.SSHArgv()
		for i, a := range argv {
			if a == "-i" && i+1 < len(argv) {
				return argv[i+1]
			}
		}
		t.Fatalf("no -i in %v", argv)
		return ""
	}
	if key("tank/one") == key("tank/two") {
		t.Error("two parents on one host resolve to one key — the second storage would be confined " +
			"to the first's dataset, and every read-only probe would still pass")
	}
	// STABLE ACROSS CALLS, because `discover before generate` rests on it: a path that varied would
	// generate a second keypair every press and offer a line whose public half is not the one on the
	// host. Two variables rather than one inline comparison, which staticcheck reads as a tautology.
	first, again := key("tank/one"), key("tank/one")
	if first != again {
		t.Errorf("one parent resolved to two key paths, %q then %q", first, again)
	}
}

// THE DERIVED PATH MUST SURVIVE `ssh -i`, WHICH IS NOT THE SAME AS BEING A VALID FILENAME.
//
// quince#989 was ruled with `%` or `+` as the separator and named `%` first. `%` produces a path
// OpenSSH cannot open: it percent-expands `IdentityFile`, so `-i /data/keys/zfs-labpool%quince` dies
// parsing its own argument — `vdollar_percent_expand: unknown key %q` — before any connection is
// attempted. Measured on the rig, 2026-08-15; `+` answered on the same host with the same key.
//
// EVERY OTHER TEST IN THIS FILE PASSED UNDER `%`. Injective, traversal-proof, stable, one component
// — and unusable by the single tool that consumes it. This is the assertion that would have caught
// it without a rig, and it exists because the rig caught it instead.
func TestZFSKeyPathIsSafeForSSHIdentityFile(t *testing.T) {
	// The tokens ssh expands in IdentityFile. `%` before any of them is a live expansion; `%` before
	// anything else is a hard parse error. Either way the path quince composed is not the path ssh
	// opens, so the character must not appear at all.
	if strings.Contains(ZFSKeyPathFor("tank/quince"), "%") {
		t.Error("the derived path contains a percent sign, which OpenSSH expands in IdentityFile — " +
			"ssh dies parsing its own argument and never connects")
	}
	// Asserted on the SEPARATOR rather than only on one example, so a dataset whose own characters
	// would reintroduce it fails too.
	for _, parent := range []string{"tank/quince", "tank/a/b/c", "pool/x"} {
		if strings.ContainsAny(ZFSKeyPathFor(parent), "%") {
			t.Errorf("ZFSKeyPathFor(%q) = %q contains `%%`", parent, ZFSKeyPathFor(parent))
		}
	}
}

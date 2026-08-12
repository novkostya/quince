package config

import (
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
	argv := ZFSConfig{SSHUser: "zfsuser", SSHHost: "zfshost"}.SSHArgv()
	joined := strings.Join(argv, " ")

	for _, want := range []string{
		"-i " + DefaultZFSKeyPath,
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
// moment a machine-in-the-middle would want — and `deploy/storage.md` names this as the property
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

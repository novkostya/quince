package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// quince#683 — THE REPRODUCTION, which the ruling recorded as owed and unrun by anyone.
//
// The claim was that `PUT /api/config` accepts a config the daemon refuses to START on: the
// duplicate-`parent_dataset` check had exactly one non-test caller, `main.go`, so the write path had
// no equivalent. Everything on the issue and in its ruling was read from the source; no request was
// ever sent and no daemon was ever started with a colliding pair.
//
// These tests are that measurement. The second one FAILS against the pre-fix tree, which is what
// makes it a regression test rather than a description.

func collidingPair() Config {
	c := Default()
	list := []StorageEntry{
		{Name: "one", Path: "/backups-a", Backend: "zfs", Default: true,
			ZFS: ZFSConfig{ParentDataset: "tank/backups", Mode: "hook", SSHUser: "u", SSHHost: "h"}},
		{Name: "two", Path: "/backups-b", Backend: "zfs",
			ZFS: ZFSConfig{ParentDataset: "tank/backups", Mode: "hook", SSHUser: "u", SSHHost: "h"}},
	}
	for i := range list {
		list[i] = list[i].Resolved()
	}
	c.Storage = &list
	return c
}

func serviceOver(t *testing.T, initial string) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil))), path
}

const oneGoodStorage = `storage:
  - name: one
    path: /backups-a
    default: true
`

// THE REFUSAL, AND THE FILE IS UNCHANGED — asserted on the bytes on disk, not on the return value.
// A refusal that still writes is the failure this gate exists to catch, and it is invisible from the
// API response.
func TestReplaceRefusesACollidingParentDatasetAndWritesNothing(t *testing.T) {
	svc, path := serviceOver(t, oneGoodStorage)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	errs, _, err := svc.Replace(collidingPair(), "test")
	if err != nil {
		t.Fatalf("Replace returned a transport error, want validation errors: %v", err)
	}
	if len(errs) == 0 {
		t.Fatalf("A COLLIDING PAIR WAS ACCEPTED — quince#683. This config makes the daemon refuse " +
			"to start, so saving it produces a file that cannot be booted.")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("THE REFUSAL STILL WROTE THE FILE.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The ruling's CONDITION on the fix: the message must name both storages and the shared dataset,
// matching the duplicate-name and duplicate-path messages in shape and tone — those name the
// colliding peer with `also storage[%d]` and say WHY the rule exists.
//
// A refusal is only not a trap if it says what to change, which is the whole reason
// CheckStorageBackends' bare strings had to be adapted rather than mechanically wrapped.
func TestTheCollisionRefusalNamesBothStoragesAndTheDataset(t *testing.T) {
	svc, _ := serviceOver(t, oneGoodStorage)

	errs, _, err := svc.Replace(collidingPair(), "test")
	if err != nil || len(errs) == 0 {
		t.Fatalf("want validation errors, got errs=%v err=%v", errs, err)
	}
	e := errs[0]

	// The PATH points at the field to change, so a form can highlight it.
	if e.Path != "storage[1].zfs.parent_dataset" {
		t.Errorf("path = %q, want storage[1].zfs.parent_dataset — a client highlights the field it names", e.Path)
	}
	for _, want := range []string{`"one"`, `"two"`, `"tank/backups"`, "also storage[0]"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message does not contain %s — it must name both storages, the shared dataset, "+
				"and the colliding peer.\ngot: %s", want, e.Message)
		}
	}
	// …and WHY, not merely that.
	if !strings.Contains(e.Message, "believe it owned it") {
		t.Errorf("message does not say why the rule exists; got: %s", e.Message)
	}
}

// A zfs storage with no parent dataset is the other arm, and it reaches the write path too.
func TestReplaceRefusesZFSIntentWithNoParentDataset(t *testing.T) {
	svc, _ := serviceOver(t, oneGoodStorage)

	c := Default()
	list := []StorageEntry{
		{Name: "one", Path: "/backups-a", Backend: "zfs", Default: true},
	}
	for i := range list {
		list[i] = list[i].Resolved()
	}
	c.Storage = &list

	errs, _, err := svc.Replace(c, "test")
	if err != nil || len(errs) == 0 {
		t.Fatalf("zfs with no parent_dataset was accepted; errs=%v err=%v", errs, err)
	}
	if !strings.Contains(errs[0].Path, "zfs.parent_dataset") {
		t.Errorf("path = %q, want the parent_dataset field", errs[0].Path)
	}
}

// A COHERENT CONFIG STILL SAVES. The control, and it is not ceremony: a check added to the write
// path is one refusal away from making every save fail, and the two arms above only prove that the
// gate fires.
func TestReplaceStillAcceptsACoherentConfig(t *testing.T) {
	svc, path := serviceOver(t, oneGoodStorage)

	c := Default()
	list := []StorageEntry{
		{Name: "one", Path: "/backups-a", Backend: "zfs", Default: true,
			ZFS: ZFSConfig{ParentDataset: "tank/one", Mode: "hook", SSHUser: "u", SSHHost: "h"}},
		{Name: "two", Path: "/backups-b", Backend: "zfs",
			ZFS: ZFSConfig{ParentDataset: "tank/two", Mode: "hook", SSHUser: "u", SSHHost: "h"}},
	}
	for i := range list {
		list[i] = list[i].Resolved()
	}
	c.Storage = &list

	errs, _, err := svc.Replace(c, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) > 0 {
		t.Fatalf("two zfs storages on DIFFERENT parent datasets were refused: %+v", errs)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "tank/two") {
		t.Fatalf("the accepted config was not written:\n%s", b)
	}
}

// The two renderings are built from one source, so they cannot describe the same config differently.
// main.go keeps the string form for stderr, where a NAME is more use to a human than an index.
func TestBothRenderingsAgree(t *testing.T) {
	c := collidingPair()
	strs := CheckStorageBackends(c.Storage)
	errs := CheckStorageBackendErrors(c.Storage)

	if len(strs) != len(errs) || len(strs) == 0 {
		t.Fatalf("renderings disagree on count: %d strings, %d errors", len(strs), len(errs))
	}
	for i := range strs {
		if strs[i] != errs[i].Message {
			t.Errorf("rendering %d differs:\n string: %s\n  error: %s", i, strs[i], errs[i].Message)
		}
	}
}

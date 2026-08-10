package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func forgetSvc(t *testing.T, entries ...StorageEntry) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if errs, _, err := svc.Replace(withStorages(entries...)); err != nil || len(errs) != 0 {
		t.Fatalf("precondition: seeding the config must succeed; errs=%+v err=%v", errs, err)
	}
	return svc, path
}

func storageNames(c Config) []string {
	if c.Storage == nil {
		return nil
	}
	out := make([]string, 0, len(*c.Storage))
	for _, e := range *c.Storage {
		out = append(out, e.Name)
	}
	return out
}

// G5 — Forget removes the entry from the declaration AND LEAVES EVERY FILE ON DISK.
//
// The tree assertion is the point of the gate, and it is why this test writes real files rather
// than trusting the function not to open anything: "detach-and-forget" is a promise about the
// disk, and a promise about the disk is not proven by a return value. The confirm dialog tells a
// user their backups are not deleted, so the thing under test is that sentence.
func TestForgetStorageLeavesTheTreeAlone(t *testing.T) {
	root := t.TempDir()
	shuttle := filepath.Join(root, "shuttle")
	for _, rel := range []string{
		"latest/Manifest.db",
		"latest/00/0011223344",
		"versions/2026-08-01T00-00-00Z/Manifest.db",
		"quince-storage.json",
	} {
		full := filepath.Join(shuttle, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc, _ := forgetSvc(t,
		StorageEntry{Name: "pool", Path: filepath.Join(root, "pool"), Default: true},
		StorageEntry{Name: "shuttle", Path: shuttle},
	)

	outcome, errs, _, err := svc.ForgetStorage("shuttle", nil)
	if err != nil || outcome != ForgetDone || len(errs) != 0 {
		t.Fatalf("forgetting a non-default storage must succeed; outcome=%v errs=%+v err=%v", outcome, errs, err)
	}

	if got := storageNames(svc.Current()); len(got) != 1 || got[0] != "pool" {
		t.Errorf("the declaration must hold only the survivor, got %v", got)
	}

	// The tree, walked rather than spot-checked: a Forget that deleted ONE file would pass a
	// spot-check on any other.
	var found []string
	if err := filepath.Walk(shuttle, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(shuttle, p)
			found = append(found, rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("the forgotten storage's tree must still be walkable: %v", err)
	}
	if len(found) != 4 {
		t.Errorf("detach-and-forget deletes NOTHING on disk; want 4 files under the forgotten "+
			"storage, found %d: %v", len(found), found)
	}
}

// G5b — the survivors keep their `zfs:` and `retention:` keys, and the CONTRAST is the gate.
//
// Asserting only that the keys survive would pass against an implementation that had no reason to
// exist, so this test also runs the failure mode the endpoint was ruled to prevent: a client that
// reconstructs the storage list from what it rendered and PUTs the whole document. That path
// decodes into a zero-valued Config, and every key no card renders goes to its zero value. If the
// two halves ever agree, the narrow endpoint has stopped earning its place.
func TestForgetStoragePreservesSurvivorsWherePutWouldNot(t *testing.T) {
	keep := &RetentionConfig{KeepRecent: 7, KeepDaily: 14, KeepWeekly: 8}
	pool := StorageEntry{
		Name:    "pool",
		Path:    "/backups",
		Default: true,
		Backend: "zfs",
		ZFS:     ZFSConfig{ParentDataset: "tank/quince", Mode: "hook", HookCmd: "/usr/local/bin/zfs-hook", Seed: "reflink"},
		// A POINTER, and that is what makes this worth pinning: absent differs from zero, so a
		// dropped `retention:` does not read as "keep nothing" — it reads as "the user never
		// wrote it", and the code defaults quietly take over. Silent, and wrong.
		Retention: keep,
	}
	svc, _ := forgetSvc(t, pool, StorageEntry{Name: "shuttle", Path: "/mnt/shuttle"})

	before, err := yaml.Marshal((*svc.Current().Storage)[0])
	if err != nil {
		t.Fatal(err)
	}

	if outcome, errs, _, err := svc.ForgetStorage("shuttle", nil); err != nil || outcome != ForgetDone {
		t.Fatalf("forget must succeed; outcome=%v errs=%+v err=%v", outcome, errs, err)
	}

	survivors := *svc.Current().Storage
	if len(survivors) != 1 {
		t.Fatalf("want one survivor, got %d", len(survivors))
	}
	after, err := yaml.Marshal(survivors[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the survivor must come through byte-for-byte.\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// THE CONTRAST, and it is narrower than fact 6 states — measured here rather than assumed.
	//
	// A client that reconstructs the list rather than splicing a fetched one omits the keys it
	// never rendered. Those do NOT all fail the same way, and only one of them fails silently:
	//
	//   zfs.mode  — omitted decodes to "", which Validate REJECTS (oneOf hook). Loud. A 422.
	//   retention — a POINTER, so omitted decodes to nil, which Validate skips entirely. Silent.
	//
	// So the hazard the narrow endpoint exists to prevent is real and it is `retention:`. The
	// schema comment already says why the field is a pointer — absent must differ from zero,
	// because 0 is a legal value for every Keep* — and that is exactly what makes the loss
	// undetectable: nothing distinguishes "the user asked for no retention" from "a client
	// dropped the key", so the code defaults take over without a word.
	// Built from the FETCHED document with only the storage list reconstructed, because that is
	// the reachable version of the hazard: a wholly zero-valued Config is rejected outright by
	// three other sections (`backup.preferred_transport`, `sessions.ttl_minutes`, `ui.theme` all fail
	// their enum or range checks), so the silent path needs the rest of the document to be
	// well-formed — which, for a client that fetched it and re-sent it, it is.
	naive := svc.Current()
	naive.Storage = &[]StorageEntry{{
		Name: "pool", Path: "/backups", Default: true, Backend: "zfs",
		ZFS: pool.ZFS, // fetched and echoed back; it is the retention key that goes missing
	}}
	errs, _, err := svc.Replace(naive)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("precondition: the naive document must be ACCEPTED — a silent loss is the hazard, "+
			"and a 422 would mean this gate's contrast no longer holds; got %+v", errs)
	}
	switch after := (*svc.Current().Storage)[0].Retention; {
	case after == nil:
		// The live snapshot drops it outright; a reload would then fill CODE defaults. Either
		// way the user's policy is gone and nothing said so.
	case *after == *keep:
		t.Fatal("precondition failed: the PUT path preserved `retention:`. If PUT has been fixed, " +
			"this contrast is stale and the endpoint's justification needs rereading, not this test.")
	}
}

// G6 — forgetting the default is refused, and the refusal SAYS WHY.
//
// Both halves, because the single-storage case reaches the same rule by a different route:
// ResolveStorages marks a lone storage default implicitly, so there is no config in which the
// only storage is not the default. The two messages differ deliberately — "make another storage
// the default first" is the wrong remedy to hand someone who has exactly one disk.
func TestForgetStorageRefusesTheDefault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entries  []StorageEntry
		target   string
		wantWord string
	}{
		{
			name: "two storages, the default one",
			entries: []StorageEntry{
				{Name: "pool", Path: "/backups", Default: true},
				{Name: "shuttle", Path: "/mnt/shuttle"},
			},
			target:   "pool",
			wantWord: "make another storage the default first",
		},
		{
			name:     "one storage, default implicitly",
			entries:  []StorageEntry{{Name: "pool", Path: "/backups"}},
			target:   "pool",
			wantWord: "only storage declared",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, path := forgetSvc(t, tc.entries...)
			beforeFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			outcome, errs, _, err := svc.ForgetStorage(tc.target, nil)
			if err != nil {
				t.Fatalf("a refusal is not an error: %v", err)
			}
			if outcome != ForgetRefused {
				t.Fatalf("forgetting the default must be REFUSED, got outcome %v", outcome)
			}
			if len(errs) != 1 || errs[0].Path != "storage" {
				t.Fatalf("want one 422 at storage:, got %+v", errs)
			}
			if !strings.Contains(strings.ToLower(errs[0].Message), strings.ToLower(tc.wantWord)) {
				t.Errorf("the refusal must name the remedy (%q), got %q", tc.wantWord, errs[0].Message)
			}
			if !strings.Contains(errs[0].Message, tc.target) {
				t.Errorf("the refusal must name the storage %q, got %q", tc.target, errs[0].Message)
			}

			// A REFUSAL WRITES NOTHING. Replace already has this property and this path reaches it
			// before Replace is called at all, so nothing else would catch a future refactor that
			// spliced first and refused afterwards.
			afterFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(beforeFile) != string(afterFile) {
				t.Error("a refused Forget must leave config.yml untouched")
			}
			if got := storageNames(svc.Current()); len(got) != len(tc.entries) {
				t.Errorf("a refused Forget must leave the live snapshot alone, got %v", got)
			}
		})
	}
}

func TestForgetStorageUnknownName(t *testing.T) {
	svc, _ := forgetSvc(t,
		StorageEntry{Name: "pool", Path: "/backups", Default: true},
		StorageEntry{Name: "shuttle", Path: "/mnt/shuttle"},
	)
	outcome, errs, _, err := svc.ForgetStorage("nosuch", nil)
	if err != nil || outcome != ForgetNoSuchStorage || len(errs) != 0 {
		t.Fatalf("an unknown name is a 404 and nothing else; outcome=%v errs=%+v err=%v", outcome, errs, err)
	}
	if got := storageNames(svc.Current()); len(got) != 2 {
		t.Errorf("a 404 must change nothing, got %v", got)
	}
}

// Current() aliases the live slice, so a splice in place would corrupt a snapshot another
// goroutine is already holding. Cheap to get wrong, invisible when it is, and the kind of thing
// -race does not catch on its own because the write is not concurrent — it is just shared.
func TestForgetStorageDoesNotMutateAPriorSnapshot(t *testing.T) {
	svc, _ := forgetSvc(t,
		StorageEntry{Name: "pool", Path: "/backups", Default: true},
		StorageEntry{Name: "shuttle", Path: "/mnt/shuttle"},
		StorageEntry{Name: "nas", Path: "/mnt/nas"},
	)
	held := svc.Current()
	heldNames := storageNames(held)

	if outcome, _, _, err := svc.ForgetStorage("shuttle", nil); err != nil || outcome != ForgetDone {
		t.Fatalf("forget must succeed; outcome=%v err=%v", outcome, err)
	}

	if got := storageNames(held); len(got) != 3 || got[1] != "shuttle" {
		t.Errorf("a snapshot taken before the Forget must still read as it did: was %v, now %v",
			heldNames, got)
	}
}

// TestForgetRestartWarningNamesTheStorageAndTheRemedy IS DELETED WITH THE FUNCTION IT GUARDED
// (qn.6g PR 4). It asserted the notice contained the word "restart"; with the storage applier wired
// there is no restart, so the test could only be kept by keeping a lie. `forget.go` carries why the
// function went. The coverage it stood for moves to `handlers_config_forget_live_test.go`, which
// asserts the opposite property — that a successful forget's warnings mention no restart at all.

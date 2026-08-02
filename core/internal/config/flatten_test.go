package config

import (
	"github.com/novkostya/quince/core/internal/wire"
	"strings"
	"testing"
)

// The flattened `storage:` (qn.6c, quince#473). `perstorage_test.go` stood here and tested
// INHERITANCE — an entry taking the global backend, an entry overriding it, and the `zfs: {}`
// opt-out. All three are gone: with every entry fully specified there is no global to inherit
// from, so those were not tests that broke, they were tests of a mechanism that no longer exists.
//
// What survives from that file is the part that was never about inheritance — the
// duplicate-`parent_dataset` collision, which two fully-specified entries can still produce.
// quince#473's deletion list said `CheckStorageBackends` went with the flattening; it does not,
// and this file is where that is asserted rather than argued.

func parseStorages(t *testing.T, raw string) []StorageEntry {
	t.Helper()
	cfg, _, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Storage == nil {
		t.Fatalf("storage is nil for %q", raw)
	}
	return *cfg.Storage
}

// THE RULED SHORT FORM (quince#504, ruled 2026-08-01 and unbuilt until this change).
func TestASingleStorageIsJustAPath(t *testing.T) {
	entries := parseStorages(t, "storage:\n  - path: /backups\n")
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Name != "/backups" {
		t.Errorf("name = %q, want it defaulted to the path", e.Name)
	}
	if !e.Default {
		t.Error("a lone storage must be default — it is implied, not written")
	}
	if e.Backend != "auto" {
		t.Errorf("backend = %q, want auto", e.Backend)
	}
	if e.ZFS.Mode != "exec" || e.ZFS.Seed != "auto" {
		t.Errorf("zfs defaults not applied: %+v", e.ZFS)
	}
	if e.Retention == nil || *e.Retention != DefaultRetention() {
		t.Errorf("retention = %+v, want the code defaults", e.Retention)
	}
	// And it must VALIDATE, which is the whole of quince#504: it did not, with two errors, while
	// canon documented both keys as required.
	cfg := Default()
	cfg.Storage = &entries
	if errs := Validate(cfg); len(errs) != 0 {
		t.Errorf("the ruled short form does not validate: %+v", errs)
	}
}

// The implication is NARROW — one storage only. With several, an absent default is an error and
// never a pick: order is not intent.
func TestDefaultIsNotImpliedWithSeveralStorages(t *testing.T) {
	entries := parseStorages(t, "storage:\n  - path: /backups\n  - path: /mnt/shuttle\n")
	for i, e := range entries {
		if e.Default {
			t.Errorf("entry %d was silently made default", i)
		}
	}
	cfg := Default()
	cfg.Storage = &entries
	if !hasPath(Validate(cfg), "storage") {
		t.Error("several storages with no default must be an error")
	}
}

// A defaulted name is still a name: it must collide like one, or two unnamed storages under paths
// differing late would quietly share an identity key.
func TestDefaultedNamesStillCollide(t *testing.T) {
	cfg := Default()
	entries := []StorageEntry{
		{Path: "/backups", Default: true}, {Path: "/backups"},
	}
	resolved := ResolveStorages(&entries)
	cfg.Storage = resolved
	errs := Validate(cfg)
	if !hasPath(errs, "storage[1].name") || !hasPath(errs, "storage[1].path") {
		t.Errorf("want both a duplicate name and a duplicate path, got %+v", errs)
	}
}

// Per-entry zfs, with no global anywhere. This is the configuration quince#458 could not express.
func TestEachStorageCarriesItsOwnBackendAndZFS(t *testing.T) {
	entries := parseStorages(t, `storage:
  - name: pool
    path: /backups
    default: true
    backend: zfs
    zfs: {parent_dataset: rpool/quince, mode: hook, hook_cmd: "/x", seed: copy}
  - name: shuttle
    path: /mnt/shuttle
    backend: hardlink
`)
	if entries[0].Backend != "zfs" || entries[0].ZFS.ParentDataset != "rpool/quince" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[0].ZFS.Seed != "copy" || entries[0].ZFS.Mode != "hook" {
		t.Errorf("declared zfs values overwritten by defaults: %+v", entries[0].ZFS)
	}
	// The one that mattered: a namespace storage beside a zfs one gets NOTHING from it.
	if entries[1].Backend != "hardlink" {
		t.Errorf("entry 1 backend = %q", entries[1].Backend)
	}
	if entries[1].ZFS.ParentDataset != "" {
		t.Errorf("zfs bled onto a hardlink storage: %+v — this is quince#458 and it must be "+
			"unconstructible now, not merely guarded", entries[1].ZFS)
	}
	if got := CheckStorageBackends(&entries); len(got) != 0 {
		t.Errorf("coherent config refused: %v", got)
	}
}

// Retention is per-storage, and absent means the CODE defaults rather than zero — a zero policy
// would mean "keep nothing" and silently delete history.
func TestRetentionIsPerStorageAndAbsentMeansDefaults(t *testing.T) {
	entries := parseStorages(t, `storage:
  - name: pool
    path: /backups
    default: true
    retention: {keep_recent: 3, keep_daily: 0, keep_weekly: 1}
  - name: shuttle
    path: /mnt/shuttle
`)
	if got := *entries[0].Retention; got.KeepRecent != 3 || got.KeepDaily != 0 || got.KeepWeekly != 1 {
		t.Errorf("declared retention = %+v", got)
	}
	if got := *entries[1].Retention; got != DefaultRetention() {
		t.Errorf("absent retention = %+v, want the code defaults", got)
	}
	// keep_daily: 0 is a LEGAL declaration, which is why Retention is a pointer: a value type
	// could not tell "keep none" from "not written".
	cfg := Default()
	cfg.Storage = &entries
	if errs := Validate(cfg); len(errs) != 0 {
		t.Errorf("keep_daily: 0 rejected: %+v", errs)
	}
}

func TestNegativeRetentionIsRejectedPerEntry(t *testing.T) {
	cfg := Default()
	entries := []StorageEntry{{Path: "/backups", Retention: &RetentionConfig{KeepRecent: -1}}}
	cfg.Storage = ResolveStorages(&entries)
	if !hasPath(Validate(cfg), "storage[0].retention.keep_recent") {
		t.Errorf("negative retention accepted: %+v", Validate(cfg))
	}
}

// SURVIVES THE FLATTENING — not caused by inheritance. Two fully-specified entries can each spell
// out the same parent dataset, which would create one `<parent>/<udid>` per device with two
// storages each believing they owned it.
func TestTwoZFSStoragesOnOneParentDatasetAreStillRefused(t *testing.T) {
	entries := parseStorages(t, `storage:
  - name: a
    path: /backups
    default: true
    backend: zfs
    zfs: {parent_dataset: rpool/quince}
  - name: b
    path: /mnt/other
    backend: zfs
    zfs: {parent_dataset: rpool/quince}
`)
	got := CheckStorageBackends(&entries)
	if len(got) != 1 {
		t.Fatalf("messages = %v, want exactly one collision", got)
	}
	if !strings.Contains(got[0], "rpool/quince") || !strings.Contains(got[0], `"a"`) ||
		!strings.Contains(got[0], `"b"`) {
		t.Errorf("the refusal must name both storages and the dataset: %q", got[0])
	}
}

func TestTwoZFSStoragesOnDifferentParentsArePermitted(t *testing.T) {
	entries := parseStorages(t, `storage:
  - name: a
    path: /backups
    default: true
    backend: zfs
    zfs: {parent_dataset: rpool/quince}
  - name: b
    path: /mnt/other
    backend: zfs
    zfs: {parent_dataset: tank/quince}
`)
	if got := CheckStorageBackends(&entries); len(got) != 0 {
		t.Errorf("distinct parents refused: %v", got)
	}
}

// THE REMEDY COLLAPSED FROM THREE BRANCHES TO ONE (quince#468, quince#492). Those three existed
// because a zfs backend could arrive by inheritance, from a global, or from the entry's own block,
// and naming the wrong one sent an operator to a key they never wrote. With one possible source
// there is one possible remedy — and it must point at the entry, never at a global that is gone.
func TestZFSWithNoParentNamesTheEntrysOwnBlock(t *testing.T) {
	entries := parseStorages(t, `storage:
  - name: pool
    path: /backups
    default: true
    backend: zfs
`)
	got := CheckStorageBackends(&entries)
	if len(got) != 1 {
		t.Fatalf("messages = %v, want one refusal", got)
	}
	if !strings.Contains(got[0], `"pool"`) {
		t.Errorf("refusal does not name the storage: %q", got[0])
	}
	if strings.Contains(got[0], "storage.zfs.parent_dataset") {
		t.Errorf("refusal points at a GLOBAL key that no longer exists: %q", got[0])
	}
	if !strings.Contains(got[0], "own `zfs:` block") {
		t.Errorf("refusal does not point at the entry's own block: %q", got[0])
	}
}

// `auto` with zfs intent still resolves to zfs — interface fact 4, zfs intent is declared and
// never probed. quince#502 owns removing `auto`; until then it must not become a hole.
func TestAutoWithZFSIntentAndNoParentIsStillRefused(t *testing.T) {
	entries := parseStorages(t, `storage:
  - name: pool
    path: /backups
    default: true
    zfs: {hook_cmd: "/x"}
`)
	if got := CheckStorageBackends(&entries); len(got) != 1 {
		t.Errorf("auto + a hook + no parent must refuse, got %v", got)
	}
}

func hasPath(errs []wire.ConfigError, path string) bool {
	for _, e := range errs {
		if e.Path == path {
			return true
		}
	}
	return false
}

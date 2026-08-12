package storage

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
)

const testUDID = "SYNTHETIC-UDID-0001"

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "quince.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func generousPolicy() RetentionPolicy {
	return RetentionPolicy{KeepRecent: 1000, KeepDaily: 0, KeepWeekly: 0}
}

// newNSManager builds a namespace-backend Manager over a fresh temp /backups + store, with a
// monotonic clock and sequential ids for deterministic assertions.
func newNSManager(t *testing.T, strategy clonetree.Strategy, policy RetentionPolicy) (*Manager, *namespaceBackend, string, *store.Store) {
	t.Helper()
	backups := t.TempDir()
	st := openStore(t)
	name := map[clonetree.Strategy]string{
		clonetree.Reflink: BackendReflink, clonetree.Hardlink: BackendHardlink, clonetree.Copy: BackendCopy,
	}[strategy]
	be := newNamespaceBackend(name, strategy, backups, "test", testLogger())
	seedStorageMarker(t, backups, "", name)
	m := NewManager([]Slot{{Name: "test", Root: backups, Backend: be, BackendName: name, Reachable: true, Retention: policy}},
		st, st, bus.New(), seqID(), testLogger())
	m.now = monotonicClock()
	return m, be, backups, st
}

// fakeZFS simulates the host ZFS (qn.6h in-place model): snapshot = copy the DATASET ROOT →
// .zfs/snapshot/<snap>/ (the tree is written in place, so nothing is exchanged before it),
// rollback = restore the root from a snapshot and REFUSE when a newer one exists, list = enumerate
// .zfs/snapshot/*, destroy = rm the snapshot dir, create = no-op. It records every argv so tests can
// assert exact commands (argv arrays, no shell) and inject failures.
//
// `seed` IS GONE, and it went in the same PR that deleted `seed)` from the reference helper in
// `deploy/storage.md` — a fake must not stop declaring a verb the operator's real script still
// declares, and must not keep declaring one it no longer does.
//
// ITS ONE STRUCTURAL LIE is that snapshots live INSIDE the tree here, where real ZFS keeps them out
// of the dataset entirely. That is the same lie `.zfs` itself tells at snapdir=visible, so the ops
// below skip it explicitly wherever the real thing would simply not see it — which is what makes
// this fixture able to reproduce the walker hazard rather than hide it.
type fakeZFS struct {
	backups string
	parent  string
	calls   [][]string
	failOp  string // if set, run returns an error for this op (e.g. "snapshot")
}

func (f *fakeZFS) run(_ context.Context, argv []string) (string, error) {
	f.calls = append(f.calls, argv)
	if len(argv) < 2 {
		return "", nil
	}
	op := argv[1]
	if op == f.failOp {
		return "injected failure", errFake
	}
	switch op {
	case "create":
		return "", nil
	case "snapshot":
		ds, snap := splitFull(argv[len(argv)-1])
		udid := strings.TrimPrefix(ds, f.parent+"/")
		root := filepath.Join(f.backups, udid)
		dst := filepath.Join(root, ".zfs", "snapshot", snap)
		if _, err := os.Stat(dst); err == nil {
			return "already exists", errFake // idempotency path exercised by callers
		}
		// Copying the root into a directory UNDER the root, so .zfs must be skipped or the copy
		// recurses into the snapshots it is creating.
		if err := copySkippingSnapdir(dst, root); err != nil {
			return err.Error(), err
		}
		return "", nil
	case "rollback":
		return f.rollback(argv[len(argv)-1])
	case "list":
		ds := argv[len(argv)-1]
		udid := strings.TrimPrefix(ds, f.parent+"/")
		snapRoot := filepath.Join(f.backups, udid, ".zfs", "snapshot")
		entries, err := os.ReadDir(snapRoot)
		if err != nil {
			return "does not exist", nil
		}
		var lines []string
		for _, e := range entries {
			if e.IsDir() {
				lines = append(lines, ds+"@"+e.Name())
			}
		}
		return strings.Join(lines, "\n"), nil
	case "destroy":
		ds, snap := splitFull(argv[len(argv)-1])
		udid := strings.TrimPrefix(ds, f.parent+"/")
		return "", os.RemoveAll(filepath.Join(f.backups, udid, ".zfs", "snapshot", snap))
	}
	return "", nil
}

// rollback restores the dataset root from <snap> — and refuses when ANY newer snapshot exists,
// which is what `zfs rollback` does without -r and is qn.6h's measured answer C.
//
// The refusal text is REPRODUCED FROM A REAL ZFS 2026-08-08, not invented, because the production
// code reads it to choose which remedy to name and a paraphrase here would make that assertion prove
// nothing. "Newer" is lexicographic over snapshot names, and includes FOREIGN ones: a host
// snapshotter's `zfs-auto-snap_frequent-*` blocks a rollback exactly as a quince one would, which is
// the whole reason answer C is the likely field case.
func (f *fakeZFS) rollback(full string) (string, error) {
	ds, snap := splitFull(full)
	udid := strings.TrimPrefix(ds, f.parent+"/")
	root := filepath.Join(f.backups, udid)
	src := filepath.Join(root, ".zfs", "snapshot", snap)
	if _, err := os.Stat(src); err != nil {
		return "cannot open '" + full + "': dataset does not exist", errFake
	}
	entries, err := os.ReadDir(filepath.Join(root, ".zfs", "snapshot"))
	if err != nil {
		return "cannot open '" + full + "': dataset does not exist", errFake
	}
	var newer []string
	for _, e := range entries {
		if e.IsDir() && e.Name() > snap {
			newer = append(newer, ds+"@"+e.Name())
		}
	}
	if len(newer) > 0 {
		return "cannot rollback to '" + full + "': more recent snapshots or bookmarks exist\n" +
			"use '-r' to force deletion of the following snapshots and bookmarks:\n" +
			strings.Join(newer, "\n"), errFake
	}
	// Empty the head, then restore it — .zfs is not part of the head on real ZFS, so it survives.
	head, err := os.ReadDir(root)
	if err != nil {
		return err.Error(), err
	}
	for _, e := range head {
		if e.Name() == ".zfs" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return err.Error(), err
		}
	}
	if err := copySkippingSnapdir(root, src); err != nil {
		return err.Error(), err
	}
	return "", nil
}

// copySkippingSnapdir copies src → dst, leaving .zfs behind. Both directions need it: a snapshot
// copies the root into a child of itself, and a rollback copies a snapshot back over a root that
// holds one.
func copySkippingSnapdir(dst, src string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".zfs" {
			continue
		}
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := clonetree.Clone(d, s, clonetree.Copy); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

var errFake = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "fake zfs error" }

func splitFull(full string) (ds, snap string) {
	if i := strings.LastIndex(full, "@"); i >= 0 {
		return full[:i], full[i+1:]
	}
	return full, ""
}

// newZFSManager builds a zfs-backend Manager backed by the fakeZFS (copy seed).
func newZFSManager(t *testing.T, policy RetentionPolicy) (*Manager, *zfsBackend, *fakeZFS, string, *store.Store) {
	return newZFSManagerCfg(t, policy, "copy")
}

// newZFSManagerCfg builds a zfs-backend Manager with a chosen in-container seed strategy.
func newZFSManagerCfg(t *testing.T, policy RetentionPolicy, seed string) (*Manager, *zfsBackend, *fakeZFS, string, *store.Store) {
	t.Helper()
	backups := t.TempDir()
	st := openStore(t)
	parent := "tank/backups/iphone-backup"
	f := &fakeZFS{backups: backups, parent: parent}
	cli := newZFSCLI(parent, []string{"hook-placeholder"})
	cli.run = f.run
	be := newZFSBackend(context.Background(), cli, backups, seed, "test", testLogger())
	seedStorageMarker(t, backups, "", BackendZFS)
	m := NewManager([]Slot{{Name: "test", Root: backups, Backend: be, BackendName: BackendZFS, Reachable: true, Retention: policy}},
		st, st, bus.New(), seqID(), testLogger())
	m.now = monotonicClock()
	return m, be, f, backups, st
}

// seedTree seeds a job and returns the working TREE (working/<udid>) the fake tool writes into
// (idevicebackup2 writes to <target>/<UDID>/; Seed returns the target parent, qn.5b).
func seedTree(t *testing.T, m *Manager, udid, job string) string {
	t.Helper()
	target, err := m.Seed(udid, job)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return filepath.Join(target, udid)
}

// commitGoodTree commits a fresh good encrypted-full tree for udid through Seed→write→CommitJob.
func commitGoodTree(t *testing.T, m *Manager, udid string) {
	t.Helper()
	goodEncryptedFull(t, seedTree(t, m, udid, "job-"+udid))
	if _, err := m.CommitJob(udid, "job-"+udid); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// seedStorageMarker writes the marker a REAL storage always has, so fixtures match production.
//
// Story 7's pre-backup check reads it before every job, and a fixture without one is not a
// simplification — it is a storage in a state buildStorage never produces. Writing it here keeps
// the check honest rather than teaching it to tolerate an absence that cannot happen.
func seedStorageMarker(t *testing.T, root, storageID, backend string) {
	t.Helper()
	if err := WriteStorageMarker(root, StorageMarker{
		StorageID: storageID, Backend: backend,
		CreatedAt: "2026-08-01T00:00:00Z", AppVersion: "test",
	}); err != nil {
		t.Fatalf("seed storage marker: %v", err)
	}
}

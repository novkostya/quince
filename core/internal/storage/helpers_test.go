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
	m := NewManager(be, name, st, st, bus.New(), backups, policy, seqID(), testLogger())
	m.now = monotonicClock()
	return m, be, backups, st
}

// fakeZFS simulates the host ZFS (qn.5b model): snapshot = copy latest/ → .zfs/snapshot/<snap>/
// latest/ (the exchange already moved the verified tree into latest/ before the snapshot),
// list = enumerate .zfs/snapshot/*, destroy = rm the snapshot dir, create = no-op, seed = clone
// latest/ → working/<udid> host-side (the hook `seed` verb). It records every argv so tests can
// assert exact commands (argv arrays, no shell) and inject failures.
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
		src := filepath.Join(f.backups, udid, "latest")
		dst := filepath.Join(f.backups, udid, ".zfs", "snapshot", snap, "latest")
		if _, err := os.Stat(dst); err == nil {
			return "already exists", errFake // idempotency path exercised by callers
		}
		if err := clonetree.Clone(dst, src, clonetree.Copy); err != nil {
			return err.Error(), err
		}
		return "", nil
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
	case "seed":
		// Host-side seed verb (qn.5b): clone latest/ → working/<udid>; verdict COPIED on tmpfs.
		udid := strings.TrimPrefix(argv[len(argv)-1], f.parent+"/")
		mp := filepath.Join(f.backups, udid)
		tree := filepath.Join(mp, "working", udid)
		_ = os.RemoveAll(tree)
		if err := os.MkdirAll(filepath.Join(mp, "working"), 0o755); err != nil {
			return err.Error(), err
		}
		if err := clonetree.Clone(tree, filepath.Join(mp, "latest"), clonetree.Copy); err != nil {
			return err.Error(), err
		}
		return "COPIED", nil // tmpfs → no block sharing
	}
	return "", nil
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

// newZFSManager builds a zfs-backend Manager backed by the fakeZFS (exec mode, copy seed).
func newZFSManager(t *testing.T, policy RetentionPolicy) (*Manager, *zfsBackend, *fakeZFS, string, *store.Store) {
	return newZFSManagerCfg(t, policy, "exec", "copy")
}

// newZFSManagerCfg builds a zfs-backend Manager with a chosen zfs mode + in-container seed strategy.
func newZFSManagerCfg(t *testing.T, policy RetentionPolicy, mode, seed string) (*Manager, *zfsBackend, *fakeZFS, string, *store.Store) {
	t.Helper()
	backups := t.TempDir()
	st := openStore(t)
	parent := "tank/backups/iphone-backup"
	f := &fakeZFS{backups: backups, parent: parent}
	cli := newZFSCLI(parent, mode, "hook-placeholder", "zfs")
	cli.run = f.run
	be := newZFSBackend(context.Background(), cli, backups, seed, "test", testLogger())
	m := NewManager(be, BackendZFS, st, st, bus.New(), backups, policy, seqID(), testLogger())
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

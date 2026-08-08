package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// Backend.Dirty is a pure lift of reset.go's isDirty — this pins that claim.
//
// It exists because the next rung deliberately BREAKS the agreement it asserts. Under qn.6h the zfs
// tree becomes the child dataset root, there is no working/ to stat, and zfs's answer moves to the
// work sentinel in the parent dataset. When that lands, this test fails on the zfs half and has to
// be edited — which is the point: the divergence becomes a visible decision in a diff rather than a
// silent drift.
//
// The failure it guards against is invisible in production. A backend whose Dirty always returns
// false makes RepairWorking answer 202 "nothing to reset" — which is exactly what a genuinely clean
// device answers, so a reset that silently does nothing cannot be told from one that worked.

func TestBothBackendsAgreeWhatDirtyMeansToday(t *testing.T) {
	const udid = "AAAABBBBCCCC"

	t.Run("namespace", func(t *testing.T) {
		_, b, root, _ := newNSManager(t, clonetree.Copy, RetentionPolicy{})
		assertDirtyTracksWorkingDir(t, b, root, udid)
	})

	t.Run("zfs", func(t *testing.T) {
		_, b, _, root, _ := newZFSManager(t, RetentionPolicy{})
		assertDirtyTracksWorkingDir(t, b, root, udid)
	})
}

// assertDirtyTracksWorkingDir drives the ONE observable both backends currently key on, through the
// Backend interface rather than the concrete type — so it is the interface's meaning being pinned.
func assertDirtyTracksWorkingDir(t *testing.T, b Backend, root, udid string) {
	t.Helper()

	if b.Dirty(udid) {
		t.Fatal("a device with no working area reads DIRTY — reset would offer to discard nothing")
	}

	tree := filepath.Join(root, udid, "working", udid)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if !b.Dirty(udid) {
		t.Fatal("a device WITH a working area reads clean — reset would answer 202 'nothing to reset' " +
			"on a head that is mid-transfer, which is indistinguishable from success")
	}

	// The killed-seed case counts as dirty DELIBERATELY: an empty working tree whose seed died is
	// exactly what reset exists for, so this must not start reading the sentinel and skipping it.
	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, udid, "working"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !b.Dirty(udid) {
		t.Error("a working/ with no tree inside reads clean — the killed-seed case is what reset is for")
	}
}

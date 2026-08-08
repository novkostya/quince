package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// THE BACKENDS NOW DISAGREE ABOUT WHAT "DIRTY" MEANS, AND THAT IS THE POINT OF THE METHOD.
//
// This file landed one PR earlier as TestBothBackendsAgreeWhatDirtyMeansToday, pinning the lift of
// reset.go's isDirty and saying in as many words: "the next rung deliberately BREAKS the agreement
// it asserts … when that lands, this test fails on the zfs half and has to be edited — which is the
// point: the divergence becomes a visible decision in a diff rather than a silent drift." This is
// that edit. It failed exactly there, on exactly that assertion.
//
// The failure both halves guard against is invisible in production. A backend whose Dirty always
// returns false makes RepairWorking answer 202 "nothing to reset" — which is precisely what a
// genuinely clean device answers, so a reset that silently does nothing cannot be told from one that
// worked by looking at it.

// The NAMESPACE answer is unchanged, killed-seed case included (story 9: this rung must not move
// reflink / hardlink / copy).
func TestNamespaceDirtyIsAWorkingDirectory(t *testing.T) {
	const udid = "AAAABBBBCCCC"
	_, b, root, _ := newNSManager(t, clonetree.Copy, RetentionPolicy{})

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

// The ZFS answer is THE WORK SENTINEL, in the parent dataset. There is no working/ to stat: the tree
// is the dataset root, so the old question has no answer there and the same stat would read clean
// forever on a device mid-transfer.
func TestZFSDirtyIsTheWorkSentinel(t *testing.T) {
	const udid = "AAAABBBBCCCC"
	_, b, _, root, _ := newZFSManager(t, RetentionPolicy{})

	if b.Dirty(udid) {
		t.Fatal("a device with no work state reads DIRTY")
	}

	// A POPULATED HEAD IS NOT DIRTY BY ITSELF, and this is the assertion that separates the two
	// models. On zfs the dataset root holds the last COMMITTED backup between jobs — the steady
	// state — so "there is content here" cannot mean "there is something to abandon". Only a job
	// having written the sentinel means that.
	tree := deviceDir(root, udid)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	goodEncryptedFull(t, tree)
	if b.Dirty(udid) {
		t.Fatal("a committed dataset head reads DIRTY — reset would offer to roll back a device that " +
			"has simply been backed up, which is every zfs device between jobs")
	}

	if err := writeWorkStateAt(zfsWorkSentinel(root, udid), workState{SeededFromLatest: true}); err != nil {
		t.Fatal(err)
	}
	if !b.Dirty(udid) {
		t.Fatal("a head with a live work sentinel reads clean — reset would answer 202 'nothing to " +
			"reset' on a head that is mid-transfer")
	}

	// And it is read at the PARENT path. A sentinel at the namespace path would be inside the backup
	// tree, where the tool writes and the snapshot captures — so finding it there must not count.
	if err := os.Remove(zfsWorkSentinel(root, udid)); err != nil {
		t.Fatal(err)
	}
	if err := writeWorkStateAt(nsWorkSentinel(root, udid), workState{SeededFromLatest: true}); err != nil {
		t.Fatal(err)
	}
	if b.Dirty(udid) {
		t.Error("zfs read a sentinel from INSIDE the backup tree — that path rides into every snapshot " +
			"and is not where this backend keeps it")
	}
}

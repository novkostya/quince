package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// G1a — TWO STORAGES THAT ARE TWO DIRECTORIES ON ONE FILESYSTEM REPORT THE SAME FIGURES.
//
// This is the gate quince#443 asked ui-e2e to prove and ui-e2e CANNOT: the demo provider fabricates
// both storages and both numbers, and its two paths share no filesystem, so a green e2e there would
// answer "does the card render two numbers" rather than the claim. Two directories in one
// `t.TempDir()` genuinely share a filesystem, so `statfs` genuinely returns the same answer.
//
// It asserts the WIRE claim only. The card renders no filesystem caveat at all — `filesystem_id`
// and a `filesystem_shared` boolean were both offered to the Operator and both declined (gap A,
// ruled 2026-08-03), so there is no conditional UI wording left to check.
func TestFilesystemSpaceIsPerFilesystemNotPerStorage(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "storage-a")
	b := filepath.Join(root, "storage-b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	freeA, totalA, err := FilesystemSpace(a)
	if err != nil {
		t.Fatalf("statfs %s: %v", a, err)
	}
	freeB, totalB, err := FilesystemSpace(b)
	if err != nil {
		t.Fatalf("statfs %s: %v", b, err)
	}

	if totalA != totalB {
		t.Errorf("total differs across two dirs on ONE filesystem: %d vs %d — the field is named "+
			"filesystem_total_bytes precisely because it cannot differ here", totalA, totalB)
	}
	// Free can drift by a block if something else on the box writes between the two calls, so this
	// is a proximity check rather than equality. A per-STORAGE number would not be close at all —
	// it would be a usage figure, orders of magnitude apart from a disk's free space.
	if delta := int64(freeA) - int64(freeB); delta > 1<<30 || delta < -(1<<30) {
		t.Errorf("free differs by more than 1 GiB across two dirs on one filesystem: %d vs %d",
			freeA, freeB)
	}
	if totalA == 0 {
		t.Error("total is 0 — statfs returned nothing usable, so this test proves nothing")
	}
	if freeA > totalA {
		t.Errorf("free %d exceeds total %d", freeA, totalA)
	}
}

// A path that does not exist must ERROR rather than return zeroes: the caller renders null on an
// error and would render "0 bytes free" — a full disk — on a silent zero.
func TestFilesystemSpaceErrorsOnMissingPath(t *testing.T) {
	if _, _, err := FilesystemSpace(filepath.Join(t.TempDir(), "not-there")); err == nil {
		t.Fatal("expected an error for a missing path, got nil — a silent zero would render as a full disk")
	}
}

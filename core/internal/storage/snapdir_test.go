package storage

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// qn.6h D7 + D8 condition 2, landed BEFORE the tree moves.
//
// Both properties are defensive today and become live hazards the moment the zfs backup tree
// becomes the child dataset root. Landing them separately means the lifecycle switch does not
// introduce them in the same commit as everything else.

// D7: browse never resolves to the live tree on zfs. A zfs version's content is only ever inside a
// snapshot — the live tree is the PREVIOUS version until a commit completes, and under qn.6h it is
// the mutable head a backup writes into directly.
func TestBrowseRootNeverResolvesToTheLiveTreeOnZFS(t *testing.T) {
	const root, udid = "/backups", "AAAABBBBCCCC"
	when := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	t.Run("nil snapshot yields no root, even for the newest version", func(t *testing.T) {
		// isLatest=true is the case that used to fall through to latestDir(). It is the dangerous
		// one: "the newest version" is exactly what a browse session asks for.
		if got := browseRoot(root, udid, BackendZFS, nil, true, when); got != "" {
			t.Errorf("browseRoot = %q, want \"\" — anything else is the tree being written", got)
		}
		if got := browseRoot(root, udid, BackendZFS, nil, false, when); got != "" {
			t.Errorf("browseRoot = %q, want \"\"", got)
		}
	})

	// The version's content is the SNAPSHOT ROOT with no trailing component (D7) — the tree is the
	// dataset root, so a snapshot of it IS the tree. It was <snap>/latest until qn.6h, which is why
	// pre-qn.6h snapshots are not browsable: ruled, with no dual-read fallback.
	t.Run("a snapshot resolves to the snapshot ROOT", func(t *testing.T) {
		snap := "tank/b/" + udid + "@quince-2026-08-08-0000"
		want := filepath.Join(root, udid, ".zfs", "snapshot", "quince-2026-08-08-0000")
		if got := browseRoot(root, udid, BackendZFS, &snap, true, when); got != want {
			t.Errorf("browseRoot = %q, want %q", got, want)
		}
	})

	t.Run("the namespace backends are untouched", func(t *testing.T) {
		if got := browseRoot(root, udid, BackendReflink, nil, true, when); got != latestDir(root, udid) {
			t.Errorf("namespace latest = %q, want %q — this rung must not move them", got, latestDir(root, udid))
		}
		if got := browseRoot(root, udid, BackendCopy, nil, false, when); got != nsVersionDir(root, udid, when) {
			t.Errorf("namespace version = %q, want %q", got, nsVersionDir(root, udid, when))
		}
	})
}

// D8 condition 2: .zfs is a child of the dataset root, so once the tree IS that root every walker
// can descend into it — into EVERY snapshot. At snapdir=hidden readdir never returns it and this is
// a no-op; at snapdir=visible, which operators set to browse snapshots by hand, it is returned.
func TestWalkersStepOverTheSnapshotDirectory(t *testing.T) {
	tree := t.TempDir()

	// A device with zero backups, on a dataset whose snapdir is visible.
	snap := filepath.Join(tree, ".zfs", "snapshot", "quince-2026-08-08-0000", "latest", "00")
	if err := os.MkdirAll(snap, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snap, "blob"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("isEmptyDir sees an empty device", func(t *testing.T) {
		// This is the derivation behind Version.kind under qn.6h. Reading non-empty here records a
		// device's FIRST backup as `incremental` — wrong in the field, invisible in CI, and on
		// exactly the value the lab proved Status.plist.IsFullBackup lies about.
		if !isEmptyDir(tree) {
			t.Error("a device with no backups reads NON-EMPTY because .zfs is visible — its first " +
				"backup would be recorded incremental")
		}
	})

	t.Run("dirSize counts no snapshot bytes", func(t *testing.T) {
		// logical_bytes would otherwise grow with the number of versions retained.
		if n := dirSize(tree); n != 0 {
			t.Errorf("dirSize = %d, want 0 — it walked into the snapshots", n)
		}
	})

	t.Run("real content is still counted", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tree, "Manifest.db"), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		if isEmptyDir(tree) {
			t.Error("a device WITH content reads empty — the skip is too broad")
		}
		if n := dirSize(tree); n != 100 {
			t.Errorf("dirSize = %d, want 100 — the skip must not hide real content", n)
		}
	})
}

// D8 condition 2's ASSERTION, as opposed to the skips it announces (quince#747).
//
// The claim under test is narrow and was got wrong once: the probe must key on whether `.zfs` is
// RETURNED BY A DIRECTORY READ, not on whether it can be stat'd. Every zfs dataset can stat it —
// `snapdir=hidden` removes it from listings only — so a stat-based probe says "visible" about every
// dataset in existence, which is what shipped and what a staging log caught.
//
// WHAT THIS FIXTURE CAN AND CANNOT PROVE, said plainly: on an ordinary filesystem a directory entry
// that exists is always listed, so the HIDDEN case — stat succeeds, readdir omits — cannot be built
// here. What it proves is that the probe now reads the listing, which is the property that
// discriminates; that a listing is what ZFS means by `visible` is a fact about ZFS, measured on the
// staging stand rather than asserted here.
func TestSnapdirProbeReadsTheListingRatherThanStatting(t *testing.T) {
	logs := &strings.Builder{}
	be := &zfsBackend{log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))}

	t.Run("a listed .zfs is announced", func(t *testing.T) {
		dev := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dev, snapdirName, "snapshot"), 0o755); err != nil {
			t.Fatal(err)
		}
		logs.Reset()
		be.assertSnapdir(testUDID, dev)
		if !strings.Contains(logs.String(), "snapdir is visible") {
			t.Error("a .zfs that appears in the listing must be announced — that is the case the " +
				"walker skips exist for")
		}
	})

	t.Run("a dataset with no .zfs says nothing", func(t *testing.T) {
		dev := t.TempDir()
		goodEncryptedFull(t, dev)
		logs.Reset()
		be.assertSnapdir(testUDID, dev)
		if strings.Contains(logs.String(), "snapdir") {
			t.Errorf("nothing to announce, but it said: %s", logs.String())
		}
	})

	t.Run("an unreadable device dir is silent, not a crash", func(t *testing.T) {
		logs.Reset()
		be.assertSnapdir(testUDID, filepath.Join(t.TempDir(), "does-not-exist"))
		if logs.String() != "" {
			t.Errorf("an absent dir must produce no claim either way: %s", logs.String())
		}
	})
}

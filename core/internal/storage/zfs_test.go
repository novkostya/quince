package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// qn.6h — zfs writes in place. Everything here is asserted on the FILESYSTEM after the fact rather
// than on the API, because the claim is about where bytes land.
//
// THREE SEED TESTS WERE DELETED WITH THE SEED, and they are named so their absence reads as a
// decision: TestZFSHookSeed, TestZFSSeedInContainerLadder and TestSeedUsesItsOwnGenerousTimeout.
// The last was the regression guard for hardware finding (cs) — a 60 s metadata timeout SIGKILLing a
// 34 GB clone — and it is removed because the clone is gone, not because the finding stopped
// mattering: `zfsSeedTimeout` had exactly one user and it was this backend. The seeding backends
// keep their own guards untouched (story 9).

// G1 + G8: the tool's target is the PARENT, the tree lands in the child dataset root, and a commit
// produces a snapshot of that root — with no clone step and no exchange anywhere.
func TestZFSCommitSnapshotsTheDatasetRoot(t *testing.T) {
	m, _, f, backups, _ := newZFSManager(t, generousPolicy())

	target, err := m.Seed(testUDID, "job1")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if target != backups {
		t.Fatalf("the tool's target = %q, want the PARENT dataset %q — idevicebackup2 appends <UDID> "+
			"itself, and that is what lands the tree in the child dataset root", target, backups)
	}
	tree := filepath.Join(target, testUDID)
	if tree != deviceDir(backups, testUDID) {
		t.Fatalf("<target>/<UDID> = %q, want the child dataset mountpoint %q", tree, deviceDir(backups, testUDID))
	}
	goodEncryptedFull(t, tree)
	if _, err := m.CommitJob(testUDID, "job1"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	vs := m.Versions(testUDID)
	if len(vs) != 1 {
		t.Fatalf("want 1 version, got %d", len(vs))
	}
	v := vs[0]
	if v.Backend != BackendZFS || v.ZFSSnapshot == nil || !strings.Contains(*v.ZFSSnapshot, "@quince-") {
		t.Fatalf("bad zfs version: backend=%s snap=%v", v.Backend, v.ZFSSnapshot)
	}

	// browse_root is the SNAPSHOT ROOT — no trailing component (D7).
	snap := snapName(*v.ZFSSnapshot)
	wantRoot := filepath.Join(backups, testUDID, ".zfs", "snapshot", snap)
	if v.BrowseRoot != wantRoot {
		t.Fatalf("browse_root = %q, want the snapshot root %q with no trailing component", v.BrowseRoot, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(wantRoot, "Manifest.db")); err != nil {
		t.Fatalf("the snapshot root is not the backup tree: %v", err)
	}

	// The marker rode in at the ROOT, and the live head still carries it too.
	if sm, err := ReadMarker(wantRoot); err != nil || sm.VersionID != v.ID {
		t.Fatalf("snapshot root marker = %q err=%v, want %s", sm.VersionID, err, v.ID)
	}
	if hm, err := ReadMarker(tree); err != nil || hm.VersionID != v.ID {
		t.Fatalf("dataset head marker = %q err=%v, want %s", hm.VersionID, err, v.ID)
	}
	assertCleanArgv(t, f.calls, "snapshot")

	// NOTHING WAS CLONED and NOTHING WAS EXCHANGED.
	for _, op := range []string{"seed", "rollback"} {
		if hasOp(f.calls, op) {
			t.Errorf("a commit issued a %q — the in-place path clones nothing and unwinds nothing", op)
		}
	}
}

// G2: the snapshot holds the backup tree and its marker, and nothing else of quince's. Asserted on
// the filesystem, entry by entry.
func TestZFSSnapshotHoldsOnlyTheTree(t *testing.T) {
	m, _, _, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	v := m.Versions(testUDID)[0]
	snapRoot := filepath.Join(backups, testUDID, ".zfs", "snapshot", snapName(*v.ZFSSnapshot))

	entries, err := os.ReadDir(snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "latest", "working":
			t.Errorf("the snapshot contains %q — the old layout rode into a committed version", e.Name())
		case workSentinelName:
			t.Errorf("the snapshot contains the work sentinel; it belongs in the PARENT dataset")
		case journalName:
			t.Errorf("the snapshot contains the commit journal; it belongs in the PARENT dataset")
		}
	}

	// And the sidecars really are in the parent, not merely absent from the snapshot.
	if _, err := os.Stat(zfsWorkSentinel(backups, testUDID)); err == nil {
		t.Error("the work sentinel outlived a successful commit — it is cleared once the snapshot exists")
	}
	if _, err := os.Stat(zfsJournal(backups, testUDID)); !os.IsNotExist(err) {
		t.Errorf("the commit journal must be removed after a successful commit: %v", err)
	}
}

// G3 + story 4: a failed job leaves the head dirty, the sentinel in place, and calls NO rollback.
// The retry resumes into that head and re-transfers nothing.
func TestZFSFailedJobKeepsTheDirtyHeadAndNeverRollsBack(t *testing.T) {
	m, _, f, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)

	tree := seedTree(t, m, testUDID, "jobX")
	writeFile(t, filepath.Join(tree, "PARTIAL"), []byte("half a transfer"))
	note, err := m.Discard(testUDID, "jobX")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "kept dirty for retry") {
		t.Fatalf("discard note = %q, want a kept-dirty report", note)
	}
	if _, err := os.Stat(filepath.Join(tree, "PARTIAL")); err != nil {
		t.Fatalf("the dirty head must survive a failed job so a retry resumes: %v", err)
	}
	if _, err := os.Stat(zfsWorkSentinel(backups, testUDID)); err != nil {
		t.Fatalf("the sentinel must survive a failed job — it is what Dirty() reads: %v", err)
	}
	if hasOp(f.calls, "rollback") {
		t.Fatal("the FAILURE path called rollback — it is abandon-only, reached from Reset alone")
	}

	// The retry resumes: the partial is still there when the next job prepares.
	if _, err := m.Seed(testUDID, "jobY"); err != nil {
		t.Fatalf("retry seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "PARTIAL")); err != nil {
		t.Fatalf("the retry discarded the resumable head: %v", err)
	}
}

// G4's companion at the manager level, and story 5: reset rolls back, and the head afterwards equals
// the newest committed version.
func TestZFSResetRollsBackToTheNewestVersion(t *testing.T) {
	m, _, f, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	v := m.Versions(testUDID)[0]

	tree := seedTree(t, m, testUDID, "jobX")
	writeFile(t, filepath.Join(tree, "PARTIAL"), []byte("abandon me"))

	if err := m.RepairWorkingCopy(testUDID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "PARTIAL")); !os.IsNotExist(err) {
		t.Error("the abandoned partial survived the rollback")
	}
	hm, err := ReadMarker(tree)
	if err != nil || hm.VersionID != v.ID {
		t.Fatalf("head marker after reset = %q err=%v, want the newest version %s", hm.VersionID, err, v.ID)
	}
	if _, err := os.Stat(zfsWorkSentinel(backups, testUDID)); !os.IsNotExist(err) {
		t.Error("the sentinel must be gone after a successful reset")
	}
	if !hasVersion(m, testUDID) {
		t.Fatal("a committed version must survive a Reset")
	}
	if !hasOp(f.calls, "rollback") {
		t.Fatal("reset did not issue a rollback")
	}
}

// G6 / story 6: with NO committed version there is nothing to roll back to, so reset empties the
// head — and it gets there from ListSnapshots rather than from an assumption.
func TestZFSResetWithNoSnapshotEmptiesTheHead(t *testing.T) {
	m, _, f, backups, _ := newZFSManager(t, generousPolicy())
	tree := seedTree(t, m, testUDID, "job1")
	goodEncryptedFull(t, tree)

	if err := m.RepairWorkingCopy(testUDID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !isEmptyDir(tree) {
		t.Error("reset on a device with no committed version must empty the head")
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("the dataset MOUNTPOINT must survive — removing it unmounts quince's storage: %v", err)
	}
	if hasOp(f.calls, "rollback") {
		t.Fatal("there was no snapshot to roll back to; reset must not have tried")
	}
	if !hasOp(f.calls, "list") {
		t.Fatal("reset must ASK whether a committed version exists, not assume it")
	}
	if _, err := os.Stat(zfsWorkSentinel(backups, testUDID)); !os.IsNotExist(err) {
		t.Error("the sentinel must be gone after the head is emptied")
	}

	// And with a snapshot present it never takes this branch.
	commitGoodTree(t, m, testUDID)
	tree2 := seedTree(t, m, testUDID, "job2")
	writeFile(t, filepath.Join(tree2, "PARTIAL"), []byte("x"))
	if err := m.RepairWorkingCopy(testUDID); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if !hasOp(f.calls, "rollback") {
		t.Fatal("with a committed version present reset must roll back, never empty")
	}
}

// G9 / story 12: a pre-qn.6h latest/ or working/ sitting in the dataset root is discarded before any
// snapshot can capture it — and the size goes in the log, because the cost is a full re-transfer.
func TestZFSDiscardsAPreChangeLayoutFromTheDatasetRoot(t *testing.T) {
	m, _, _, backups, _ := newZFSManager(t, generousPolicy())
	root := deviceDir(backups, testUDID)

	// The shape an upgraded install arrives in: a committed latest/, a dirty working/<udid>, and
	// both per-device sidecars at their OLD in-tree paths.
	goodEncryptedFull(t, filepath.Join(root, "latest"))
	goodEncryptedFull(t, filepath.Join(root, "working", testUDID))
	writeFile(t, nsWorkSentinel(backups, testUDID), []byte(`{"seeded_from_latest": true}`))
	writeFile(t, filepath.Join(root, journalName), []byte(`{"phase": "prepared"}`))

	if _, err := m.Seed(testUDID, "job1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, name := range []string{"latest", "working", workSentinelName, journalName} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%q survived into the dataset root — it would ride into every future snapshot", name)
		}
	}
	// Discarded, so this device's next backup is a FULL one. Anything else would be a version
	// recorded `incremental` over content that is no longer there.
	if !isEmptyDir(root) {
		t.Fatal("the root should be empty after the pre-change layout is discarded")
	}
	w, ok, err := readWorkStateAt(zfsWorkSentinel(backups, testUDID))
	if err != nil || !ok {
		t.Fatalf("the new sentinel was not written: ok=%v err=%v", ok, err)
	}
	if w.SeededFromLatest {
		t.Error("after discarding the pre-change layout the head is empty, so the next backup is FULL")
	}
}

// Story 1 both ways: the kind comes from whether the dataset root holds content at job start.
func TestZFSKindComesFromTheDatasetRoot(t *testing.T) {
	m, _, _, backups, _ := newZFSManager(t, generousPolicy())

	tree := seedTree(t, m, testUDID, "job1")
	if w, _, _ := readWorkStateAt(zfsWorkSentinel(backups, testUDID)); w.kindOf() != "full" {
		t.Errorf("a first backup into an empty dataset root is FULL, got %q", w.kindOf())
	}
	goodEncryptedFull(t, tree)
	if _, err := m.CommitJob(testUDID, "job1"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, err := m.Seed(testUDID, "job2"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if w, _, _ := readWorkStateAt(zfsWorkSentinel(backups, testUDID)); w.kindOf() != "incremental" {
		t.Errorf("a backup into a populated dataset root is INCREMENTAL, got %q", w.kindOf())
	}
}

func TestZFSDeleteDestroysSnapshot(t *testing.T) {
	m, _, _, backups, st := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	v := m.Versions(testUDID)[0]
	snap := snapName(*v.ZFSSnapshot)
	if status, err := m.Delete(v.ID); err != nil || status != 202 {
		t.Fatalf("delete: status=%d err=%v", status, err)
	}
	if _, err := os.Stat(filepath.Join(backups, testUDID, ".zfs", "snapshot", snap)); !os.IsNotExist(err) {
		t.Fatal("snapshot not destroyed on delete")
	}
	if _, ok, _ := st.GetVersion(v.ID); ok {
		t.Fatal("version row not deleted")
	}
}

// hasOp reports whether any recorded argv carries this zfs verb.
func hasOp(calls [][]string, op string) bool {
	for _, c := range calls {
		for _, a := range c {
			if a == op {
				return true
			}
		}
	}
	return false
}

// assertCleanArgv finds a call for op and checks it is an argv array with no shell metacharacters
// (secrets/subprocess hygiene: commands are never shell strings — design §6).
func assertCleanArgv(t *testing.T, calls [][]string, op string) {
	t.Helper()
	for _, c := range calls {
		// The op is at index 1 in exec mode (["zfs", op, …]); in hook mode the hook argv
		// precedes it. Find the op anywhere and assert every element is metacharacter-free
		// (the real hygiene invariant: argv arrays, never shell strings — design §6).
		for _, a := range c {
			if a != op {
				continue
			}
			for _, e := range c {
				if strings.ContainsAny(e, " \t;|&$`\n") {
					t.Fatalf("argv element %q contains a shell metacharacter", e)
				}
			}
			return
		}
	}
	t.Fatalf("no %q call recorded in %v", op, calls)
}

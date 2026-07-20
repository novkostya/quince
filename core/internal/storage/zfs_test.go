package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 4: zfs commit creates a @quince-* snapshot, rebuilds latest/ from .zfs, and rows the
// version; commands are argv arrays (no shell).
func TestZFSCommit(t *testing.T) {
	m, _, f, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)

	vs := m.Versions(testUDID)
	if len(vs) != 1 {
		t.Fatalf("want 1 version, got %d", len(vs))
	}
	v := vs[0]
	if v.Backend != BackendZFS || v.ZFSSnapshot == nil || !strings.Contains(*v.ZFSSnapshot, "@quince-") {
		t.Fatalf("bad zfs version: backend=%s snap=%v", v.Backend, v.ZFSSnapshot)
	}
	if !strings.Contains(v.BrowseRoot, filepath.Join(".zfs", "snapshot")) {
		t.Fatalf("zfs browse_root should go through .zfs: %q", v.BrowseRoot)
	}
	snap := snapName(*v.ZFSSnapshot)
	if _, err := os.Stat(filepath.Join(backups, testUDID, ".zfs", "snapshot", snap, "working")); err != nil {
		t.Fatalf("snapshot working path missing: %v", err)
	}
	lm, err := ReadMarker(zfsLatest(backups, testUDID))
	if err != nil || lm.VersionID != v.ID {
		t.Fatalf("latest/ not rebuilt from snapshot: marker=%q err=%v", lm.VersionID, err)
	}
	assertCleanArgv(t, f.calls, "snapshot")
}

func TestZFSDiscardReportsDirty(t *testing.T) {
	m, _, _, _, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	note, err := m.Discard(testUDID, "job-"+testUDID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "working copy dirty, last good") {
		t.Fatalf("discard note = %q, want a dirty-working report", note)
	}
}

func TestZFSRepairWorkingCopy(t *testing.T) {
	m, _, _, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	working := zfsWorking(backups, testUDID)
	if err := os.RemoveAll(working); err != nil { // simulate a corrupt/lost working copy
		t.Fatal(err)
	}
	if err := m.RepairWorkingCopy(testUDID); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if _, err := os.Stat(filepath.Join(working, "Status.plist")); err != nil {
		t.Fatalf("working not rebuilt from snapshot: %v", err)
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

// assertCleanArgv finds a call for op and checks it is an argv array with no shell metacharacters
// (secrets/subprocess hygiene: commands are never shell strings — design §6).
func assertCleanArgv(t *testing.T, calls [][]string, op string) {
	t.Helper()
	for _, c := range calls {
		if len(c) >= 2 && c[1] == op {
			if c[0] != "zfs" {
				t.Fatalf("argv[0] = %q, want zfs", c[0])
			}
			for _, a := range c {
				if strings.ContainsAny(a, " \t;|&$`\n") {
					t.Fatalf("argv element %q contains a shell metacharacter", a)
				}
			}
			return
		}
	}
	t.Fatalf("no %q call recorded in %v", op, calls)
}

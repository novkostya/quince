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

// Stack D5 ladder (i): hook configured → latest/ rebuilt HOST-side via the `mirror` verb.
func TestZFSHookMirror(t *testing.T) {
	m, be, f, backups, _ := newZFSManagerCfg(t, generousPolicy(), "hook", "auto")
	commitGoodTree(t, m, testUDID)
	if _, err := ReadMarker(zfsLatest(backups, testUDID)); err != nil {
		t.Fatalf("latest/ not built via the hook mirror verb: %v", err)
	}
	if be.LastMirror().Mode != MirrorHookReflink {
		t.Fatalf("mirror mode = %q, want %q", be.LastMirror().Mode, MirrorHookReflink)
	}
	// The mirror verb ran as an argv (no shell), through the hook.
	assertCleanArgv(t, f.calls, "mirror")
	t.Logf("hook mirror claim: %q", be.LastMirror().Claim)
}

// Stack D5 ladder (ii)-(iv): hookless → in-container reflink → hardlink-under-matrix → copy,
// self-selecting. On the CI filesystem reflink is unavailable, so it falls through and reports
// an honest non-zero-space claim (never a silent zero-space assertion).
func TestZFSMirrorInContainerLadder(t *testing.T) {
	m, be, _, backups, _ := newZFSManagerCfg(t, generousPolicy(), "exec", "auto")
	commitGoodTree(t, m, testUDID)
	if _, err := ReadMarker(zfsLatest(backups, testUDID)); err != nil {
		t.Fatalf("latest/ not built: %v", err)
	}
	r := be.LastMirror()
	switch r.Mode {
	case MirrorReflink, MirrorHardlink, MirrorCopy:
		t.Logf("in-container ladder selected mode=%q claim=%q", r.Mode, r.Claim)
	default:
		t.Fatalf("unexpected in-container mirror mode %q", r.Mode)
	}
	if r.Claim == "" {
		t.Fatal("mirror claim must be surfaced, never empty")
	}
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

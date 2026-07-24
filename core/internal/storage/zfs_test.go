package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Story 3: zfs commit exchanges the verified working tree into latest/ (atomic) then takes a
// @quince-* snapshot capturing latest/ = the version; commands are argv arrays (no shell).
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
	// browse_root goes through .zfs and points at latest/ (was working/ pre-qn.5b).
	if !strings.Contains(v.BrowseRoot, filepath.Join(".zfs", "snapshot")) || !strings.HasSuffix(v.BrowseRoot, "latest") {
		t.Fatalf("zfs browse_root should be .zfs/snapshot/<snap>/latest: %q", v.BrowseRoot)
	}
	snap := snapName(*v.ZFSSnapshot)
	if _, err := os.Stat(filepath.Join(backups, testUDID, ".zfs", "snapshot", snap, "latest")); err != nil {
		t.Fatalf("snapshot latest/ path missing: %v", err)
	}
	// The live latest/ holds the version (the exchange moved the tree in) and working/ is gone.
	lm, err := ReadMarker(latestDir(backups, testUDID))
	if err != nil || lm.VersionID != v.ID {
		t.Fatalf("latest/ marker = %q err=%v, want %s", lm.VersionID, err, v.ID)
	}
	if _, err := os.Stat(workingParent(backups, testUDID)); !os.IsNotExist(err) {
		t.Fatal("working/ should be gone after a successful zfs commit")
	}
	assertCleanArgv(t, f.calls, "snapshot")
}

func TestZFSDiscardKeepsWorking(t *testing.T) {
	m, _, _, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	goodEncryptedFull(t, seedTree(t, m, testUDID, "jobX")) // a partial attempt
	note, err := m.Discard(testUDID, "jobX")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "kept dirty for retry") {
		t.Fatalf("discard note = %q, want a kept-dirty report", note)
	}
	if isEmptyDir(workingTree(backups, testUDID)) {
		t.Fatal("working/<udid> should be KEPT dirty after discard (resume)")
	}
}

// Reset discards the dirty working so the next backup re-seeds from latest/.
func TestZFSResetWorking(t *testing.T) {
	m, _, _, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	goodEncryptedFull(t, seedTree(t, m, testUDID, "jobX"))
	if err := m.RepairWorkingCopy(testUDID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(workingParent(backups, testUDID)); !os.IsNotExist(err) {
		t.Fatal("working/ should be gone after Reset")
	}
	if !hasVersion(m, testUDID) {
		t.Fatal("committed version must survive a Reset")
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

// qn.5b ladder: hook configured → working/<udid> seeded HOST-side via the `seed` verb at job start.
func TestZFSHookSeed(t *testing.T) {
	m, be, f, _, _ := newZFSManagerCfg(t, generousPolicy(), "hook", "auto")
	commitGoodTree(t, m, testUDID) // v1 → latest/
	// A second job triggers the seed of working/<udid> from latest/ (via the hook `seed` verb).
	target, err := m.Seed(testUDID, "job2")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, testUDID, "Status.plist")); err != nil {
		t.Fatalf("working/<udid> not seeded via the hook seed verb: %v", err)
	}
	if be.LastSeed().Mode != SeedHookReflink {
		t.Fatalf("seed mode = %q, want %q", be.LastSeed().Mode, SeedHookReflink)
	}
	assertCleanArgv(t, f.calls, "seed") // the seed verb ran as an argv (no shell), through the hook
	t.Logf("hook seed claim: %q", be.LastSeed().Claim)
}

// TestSeedUsesItsOwnGenerousTimeout is the regression guard for the hardware finding (cs): the seed
// reflink-clones an ENTIRE backup tree (measured ~17.5 s for 133k files / 34 GB warm, past 60 s
// cold), so it must NOT inherit the 60 s METADATA timeout — doing so SIGKILLed a real 34 GB iPhone
// seed mid-clone and made the device un-backup-able. Asserts the deadline the seed call actually
// runs under, by inspecting the context the hook verb receives.
func TestSeedUsesItsOwnGenerousTimeout(t *testing.T) {
	m, be, _, _, _ := newZFSManagerCfg(t, generousPolicy(), "hook", "auto")
	commitGoodTree(t, m, testUDID) // v1 → latest/, so the next Seed actually clones

	var seedDeadline time.Duration
	prev := be.cli.run
	be.cli.run = func(ctx context.Context, argv []string) (string, error) {
		for _, a := range argv {
			if a == "seed" {
				if dl, ok := ctx.Deadline(); ok {
					seedDeadline = time.Until(dl)
				}
			}
		}
		return prev(ctx, argv)
	}

	if _, err := m.Seed(testUDID, "job2"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if seedDeadline == 0 {
		t.Fatal("the seed verb never ran (no deadline captured)")
	}
	if seedDeadline <= zfsOpTimeout {
		t.Fatalf("seed ran under a %v deadline (<= the %v metadata timeout) — a large tree would be "+
			"SIGKILLed mid-clone, exactly the hardware failure this guards", seedDeadline, zfsOpTimeout)
	}
}

// qn.5b ladder: hookless → in-container reflink → copy, self-selecting (NEVER hardlink for the
// seed, amendment A). On the CI filesystem reflink is unavailable, so it falls through to copy and
// reports an honest claim (never a silent zero-space assertion).
func TestZFSSeedInContainerLadder(t *testing.T) {
	m, be, _, _, _ := newZFSManagerCfg(t, generousPolicy(), "exec", "auto")
	commitGoodTree(t, m, testUDID)
	target, err := m.Seed(testUDID, "job2")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, testUDID, "Status.plist")); err != nil {
		t.Fatalf("working/<udid> not seeded: %v", err)
	}
	r := be.LastSeed()
	switch r.Mode {
	case SeedReflink, SeedCopy:
		t.Logf("in-container seed selected mode=%q claim=%q", r.Mode, r.Claim)
	default:
		t.Fatalf("unexpected in-container seed mode %q (hardlink must never be used for the seed)", r.Mode)
	}
	if r.Claim == "" {
		t.Fatal("seed claim must be surfaced, never empty")
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

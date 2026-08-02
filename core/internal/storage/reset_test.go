package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// qn.6c quince#448 — Reset names its storage, and refuses rather than guessing.

// twoUsable builds a Manager with two reachable storages and returns both roots.
func twoUsable(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m, _, rootA, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID, m.slots[0].Name = "01JSTORAGEA00000000000000", "alpha"
	rootB := t.TempDir()
	m.slots = append(m.slots, Slot{
		StorageID: "01JSTORAGEB00000000000000", Name: "beta", Root: rootB, Reachable: true,
		Backend:     newNamespaceBackend(BackendCopy, clonetree.Copy, rootB, "test", testLogger()),
		BackendName: BackendCopy,
	})
	return m, rootA, rootB
}

func makeDirty(t *testing.T, root, udid string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, udid, "working", udid), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TWO OR MORE DIRTY REFUSES. The ruling's point, and the same answer quince#435 gave a job that
// names no storage: a dirty working/ is a RESUMABLE MULTI-HOUR PARTIAL, so "reset all" would
// discard a transfer on a disk the user was not thinking about.
func TestResetRefusesWhenTwoStoragesAreDirty(t *testing.T) {
	m, rootA, rootB := twoUsable(t)
	makeDirty(t, rootA, testUDID)
	makeDirty(t, rootB, testUDID)

	status, reason := m.RepairWorking(testUDID, "")
	if status != 409 {
		t.Fatalf("two dirty storages must refuse with 409, got %d (%q)", status, reason)
	}
	// It must NAME them — a refusal the user cannot act on is worse than the guess it replaced.
	for _, want := range []string{"alpha", "beta", "storage_id"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the refusal must contain %q so the user can choose: %q", want, reason)
		}
	}
	// And nothing was deleted: refusing means refusing, not refusing-after-doing-one.
	for _, root := range []string{rootA, rootB} {
		if _, err := os.Stat(filepath.Join(root, testUDID, "working")); err != nil {
			t.Errorf("a refusal must not have reset anything: %s is gone", root)
		}
	}
}

// EXACTLY ONE DIRTY resets it and NAMES it, so the user learns which disk was touched from the
// result rather than from the docs.
func TestResetResolvesASingleDirtyStorageAndNamesIt(t *testing.T) {
	m, rootA, rootB := twoUsable(t)
	makeDirty(t, rootB, testUDID) // only beta

	status, reason := m.RepairWorking(testUDID, "")
	if status != 202 {
		t.Fatalf("exactly one dirty storage must be reset, got %d (%q)", status, reason)
	}
	if !strings.Contains(reason, "beta") {
		t.Errorf("the reason must name which storage was reset: %q", reason)
	}
	if _, err := os.Stat(filepath.Join(rootB, testUDID, "working")); !os.IsNotExist(err) {
		t.Error("beta's working copy should be gone")
	}
	// alpha was clean and stays untouched.
	if _, err := os.Stat(filepath.Join(rootA, testUDID, "working")); !os.IsNotExist(err) {
		t.Error("alpha had no working copy and should still have none")
	}
}

// NONE DIRTY is today's idempotent 202, unchanged — the behaviour every existing deployment has.
func TestResetOnNothingDirtyIsStillAccepted(t *testing.T) {
	m, _, _ := twoUsable(t)
	if status, reason := m.RepairWorking(testUDID, ""); status != 202 {
		t.Fatalf("nothing to reset must stay 202, got %d (%q)", status, reason)
	}
}

// A NAMED storage resets exactly that one, even when another is also dirty — which is the whole
// point of being able to name it.
func TestResetByNameTouchesOnlyThatStorage(t *testing.T) {
	m, rootA, rootB := twoUsable(t)
	makeDirty(t, rootA, testUDID)
	makeDirty(t, rootB, testUDID)

	status, _ := m.RepairWorking(testUDID, "01JSTORAGEB00000000000000")
	if status != 202 {
		t.Fatalf("a named storage must be reset, got %d", status)
	}
	if _, err := os.Stat(filepath.Join(rootB, testUDID, "working")); !os.IsNotExist(err) {
		t.Error("beta was named and should be reset")
	}
	if _, err := os.Stat(filepath.Join(rootA, testUDID, "working")); err != nil {
		t.Error("alpha was NOT named and must be untouched")
	}
}

// An unknown id is 404, matching unknown-device — the precedent contracts §1 already set for
// POST /api/jobs.
func TestResetOnAnUnknownStorageIs404(t *testing.T) {
	m, _, _ := twoUsable(t)
	if status, _ := m.RepairWorking(testUDID, "01JSTORAGENOSUCH00000000"); status != 404 {
		t.Errorf("an unknown storage id must be 404, got %d", status)
	}
}

// A named-but-unreachable storage refuses with ITS OWN reason, and matches the job path's 409
// rather than inventing a code for a condition another endpoint already has.
func TestResetOnAnUnreachableStorageRefusesWithItsReason(t *testing.T) {
	m, _, _ := twoUsable(t)
	m.slots[1] = Slot{
		StorageID: "01JSTORAGEB00000000000000", Name: "beta", Root: "/nowhere",
		Reachable: false, UnreachableCode: "missing_medium", UnreachableReason: "the medium is not present",
	}
	status, reason := m.RepairWorking(testUDID, "01JSTORAGEB00000000000000")
	if status != 409 {
		t.Fatalf("an unreachable storage must refuse with 409 (the job path's code), got %d", status)
	}
	if !strings.Contains(reason, "missing_medium") || !strings.Contains(reason, "beta") {
		t.Errorf("it must carry that storage's own reason: %q", reason)
	}
}

// UNREACHABLE STORAGES ARE NAMED, NEVER SILENTLY SKIPPED. One that cannot be read cannot be
// inspected for dirtiness, so a successful reset must not imply the others were checked and found
// clean — "no silent caps" applies to what we could not look at.
func TestResetSaysWhichStoragesItCouldNotInspect(t *testing.T) {
	m, _, rootB := twoUsable(t)
	makeDirty(t, rootB, testUDID)
	m.slots = append(m.slots, Slot{
		StorageID: "01JSTORAGEC00000000000000", Name: "gamma", Root: "/nowhere",
		Reachable: false, UnreachableCode: "path_unreachable",
	})

	status, reason := m.RepairWorking(testUDID, "")
	if status != 202 {
		t.Fatalf("one dirty and one unreachable must still reset the dirty one, got %d (%q)", status, reason)
	}
	if !strings.Contains(reason, "gamma") || !strings.Contains(reason, "NOT inspected") {
		t.Errorf("the result must say which storage was not inspected: %q", reason)
	}
}

// THE KILLED-SEED CASE COUNTS AS DIRTY. A tree whose sentinel still says seed_in_progress is
// exactly what Reset is for, which is why dirtiness stats the directory rather than reading the
// sentinel.
func TestAKilledSeedCountsAsDirty(t *testing.T) {
	m, _, rootB := twoUsable(t)
	// A working/ with no sentinel at all — the shape a killed seed leaves behind.
	if err := os.MkdirAll(filepath.Join(rootB, testUDID, "working"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, reason := m.RepairWorking(testUDID, "")
	if status != 202 || !strings.Contains(reason, "beta") {
		t.Fatalf("a killed seed must count as dirty and be reset, got %d (%q)", status, reason)
	}
}

// The CLI takes a NAME because that is what an operator has in config.yml; the API takes an id
// because that is what a client got from GET /api/storages.
func TestStorageIDByName(t *testing.T) {
	m, _, _ := twoUsable(t)
	if id, err := m.StorageIDByName("beta"); err != nil || id != "01JSTORAGEB00000000000000" {
		t.Errorf("want beta's id, got %q (%v)", id, err)
	}
	// Empty in, empty out — which is how "no --storage" reaches the omitted case.
	if id, err := m.StorageIDByName(""); err != nil || id != "" {
		t.Errorf("empty must pass through as the omitted case, got %q (%v)", id, err)
	}
	if _, err := m.StorageIDByName("nope"); err == nil {
		t.Error("an undeclared name must refuse rather than resolve to the default")
	}
}

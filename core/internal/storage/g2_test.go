package storage

import (
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// G2 (qn.6c story 8) — THE COST IS STATED BEFORE IT IS PAID.
//
// A `(device, storage-B)` pair with no prior version reports full BEFORE the transfer starts, and
// the committed version's `kind` is `full`.
//
// ASSERTED ON BOTH HALVES ON PURPOSE, which is the gate's own wording: "so a UI-only claim cannot
// pass it". The API answer and the committed artifact are produced by different code — `Storages`
// asks the registry, `seedKind` reads the work sentinel — and a rung that warns correctly while
// committing `incremental` has corrupted the version record to make a screen look right.
func TestG2TheFullTransferClaimIsMadeBeforeAndKeptAfter(t *testing.T) {
	m, _, defRoot, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	const defID = "01JSTORAGEDEFAULT0000000"
	m.slots[0].StorageID = defID
	m.slots[0].Name = "internal"
	// The marker must AGREE with the slot — preflight refuses when they disagree, which it
	// just did to this fixture. A real storage never has them out of step.
	seedStorageMarker(t, defRoot, defID, m.slots[0].BackendName)

	// The device already has a backup on the DEFAULT. This is what makes the claim non-trivial:
	// asked per-device rather than per-(device, storage), the answer would be "incremental".
	commitGoodTree(t, m, testUDID)

	// A second storage, with the marker a real one always has.
	secondRoot := t.TempDir()
	const secondID = "01JSTORAGESECOND00000000"
	seedStorageMarker(t, secondRoot, secondID, BackendCopy)
	m.slots = append(m.slots, Slot{
		StorageID: secondID, Name: "shuttle", Root: secondRoot, Reachable: true,
		Backend:     newNamespaceBackend(BackendCopy, clonetree.Copy, secondRoot, "test", testLogger()),
		BackendName: BackendCopy,
	})

	// --- BEFORE: the API says this will be a full transfer -------------------------------------
	list := m.Storages(testUDID)
	if list[0].WillBeFull == nil || *list[0].WillBeFull {
		t.Error("the default already holds a version for this device — it must not claim full")
	}
	if list[1].WillBeFull == nil || !*list[1].WillBeFull {
		t.Fatal("storage B has no prior version for this device: the API must say the next backup " +
			"is FULL, before the user commits to tens of gigabytes")
	}

	// --- THE BACKUP: bound to B, so every write-path call resolves B ----------------------------
	const jobID = "01JOBTOSECOND00000000000"
	if err := m.BindJobStorage(jobID, testUDID, secondID); err != nil {
		t.Fatal(err)
	}
	goodEncryptedFull(t, seedTree(t, m, testUDID, jobID))
	v, err := m.CommitJob(testUDID, jobID)
	if err != nil {
		t.Fatalf("commit to storage B: %v", err)
	}

	// --- AFTER: the committed version agrees with the claim -------------------------------------
	if v.Kind != "full" {
		t.Errorf("the committed version's kind must be full, got %q — the warning was honest and "+
			"the record is not", v.Kind)
	}
	if v.StorageID == nil || *v.StorageID != secondID {
		t.Errorf("the version must be recorded on storage B, got %v", v.StorageID)
	}

	// And the claim FLIPS once B holds a version: the next backup there is incremental, which is
	// what makes will_be_full a fact about the pair rather than a constant.
	after := m.Storages(testUDID)
	if after[1].WillBeFull == nil || *after[1].WillBeFull {
		t.Error("storage B now holds a version for this device — the next backup is not full")
	}
}

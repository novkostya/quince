package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// G1 (qn.6c story 10) — THE ACCEPTANCE CASE, and quince#378's own gate.
//
// One device backed up to storage A, then to storage B. Both commit and verify, and **`latest/`
// under A is byte-identical before and after B's commit**.
//
// It needs no second disk: two directories on one filesystem are two storages, which is the point
// of the storage being an identity rather than a device.
//
// The byte-identity assertion is the one that matters. Every cross-storage defect this rung found —
// browse_root resolving to the wrong root, a commit recorded on the default, seedKind reading the
// wrong sentinel, deleteVersion deleting through the wrong backend — would have been a *write* into
// A while working on B, or a claim about A's contents that A did not make. A tree hash before and
// after is what makes "B did not touch A" a measurement rather than a hope.
func TestG1OneDeviceToTwoStoragesLeavesTheFirstUntouched(t *testing.T) {
	m, _, rootA, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	const idA, idB = "01JSTORAGEA00000000000000", "01JSTORAGEB00000000000000"
	m.slots[0].StorageID = idA
	m.slots[0].Name = "alpha"
	seedStorageMarker(t, rootA, idA, m.slots[0].BackendName)

	rootB := t.TempDir()
	seedStorageMarker(t, rootB, idB, BackendCopy)
	m.slots = append(m.slots, Slot{
		StorageID: idB, Name: "beta", Root: rootB, Reachable: true,
		Backend:     newNamespaceBackend(BackendCopy, clonetree.Copy, rootB, "test", testLogger()),
		BackendName: BackendCopy,
	})

	// --- backup to A ----------------------------------------------------------------------------
	const jobA = "01JOBTOALPHA000000000000"
	if err := m.BindJobStorage(jobA, idA); err != nil {
		t.Fatal(err)
	}
	goodEncryptedFull(t, seedTree(t, m, testUDID, jobA))
	vA, err := m.CommitJob(testUDID, jobA)
	if err != nil {
		t.Fatalf("commit to A: %v", err)
	}
	if rep, ok := m.VerifyVersion(vA.ID); !ok || !rep.OK {
		t.Fatalf("A's version must verify: ok=%v %+v", ok, rep)
	}

	// THE FINGERPRINT, taken after A is committed and before B is touched.
	before := treeHash(t, filepath.Join(rootA, testUDID, "latest"))

	// --- backup to B ----------------------------------------------------------------------------
	const jobB = "01JOBTOBETA0000000000000"
	if err := m.BindJobStorage(jobB, idB); err != nil {
		t.Fatal(err)
	}
	goodEncryptedFull(t, seedTree(t, m, testUDID, jobB))
	vB, err := m.CommitJob(testUDID, jobB)
	if err != nil {
		t.Fatalf("commit to B: %v", err)
	}
	if rep, ok := m.VerifyVersion(vB.ID); !ok || !rep.OK {
		t.Fatalf("B's version must verify: ok=%v %+v", ok, rep)
	}

	// --- A IS UNTOUCHED -------------------------------------------------------------------------
	if after := treeHash(t, filepath.Join(rootA, testUDID, "latest")); after != before {
		t.Fatalf("committing to B changed storage A's latest/ — %s → %s", before, after)
	}

	// Both versions exist, each on its own storage, and each is latest IN ITS OWN GROUP — the
	// is_latest ruling. A single latest across the device would leave one storage's newest version
	// flagged false, with a browse_root pointing at a version dir that does not exist.
	rowA, _, _ := m.reg.GetVersion(vA.ID)
	rowB, _, _ := m.reg.GetVersion(vB.ID)
	if rowA.StorageID == nil || *rowA.StorageID != idA {
		t.Errorf("A's version must be recorded on A, got %v", rowA.StorageID)
	}
	if rowB.StorageID == nil || *rowB.StorageID != idB {
		t.Errorf("B's version must be recorded on B, got %v", rowB.StorageID)
	}
	if !rowA.IsLatest || !rowB.IsLatest {
		t.Errorf("each storage's newest version must be latest in its own group (A=%v B=%v)",
			rowA.IsLatest, rowB.IsLatest)
	}
	// And neither is marked missing after both commits — the state the cross-storage reconciliation
	// bugs produced.
	if rowA.Missing || rowB.Missing {
		t.Errorf("neither version may be missing (A=%v B=%v)", rowA.Missing, rowB.Missing)
	}
}

// treeHash fingerprints a directory tree: every relative path and its content, in sorted order.
// Byte-identity rather than mtime or size, because the defects this guards against write plausible
// files rather than obviously wrong ones.
func treeHash(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			entries = append(entries, "d "+rel)
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		entries = append(entries, "f "+rel+" "+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatalf("hashing %s: %v", root, err)
	}
	sort.Strings(entries)
	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

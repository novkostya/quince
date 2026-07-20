package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
)

var created2 = time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC)

// stageNSCommit builds a v2 commit for a namespace device and leaves it partially done at
// `phase` (as a crash would), with the journal written. v1 must already be the committed latest.
func stageNSCommit(t *testing.T, m *Manager, backups, udid, jobID, vid string, phase CommitPhase) {
	t.Helper()
	work := nsWork(backups, udid, jobID)
	goodEncryptedFull(t, work)
	mustMarker(t, work, vid, jobID, udid, m.backendName)
	dev := deviceDir(backups, udid)
	latest := nsLatest(backups, udid)
	j := Journal{
		VersionID: vid, UDID: udid, Backend: m.backendName, JobID: jobID, Phase: PhasePrepared,
		CreatedAt: fmtRFC(created2), Kind: "full", Encrypted: true, StructureVerifiedAt: fmtRFC(created2),
		JobDir: work,
	}
	if phase == PhasePreviousArchived || phase == PhaseLatestPromoted {
		prevTS := ""
		if pm, err := ReadMarker(latest); err == nil {
			pt, _ := parseRFC(pm.CreatedAt)
			prevTS = tsDir(pt)
		}
		if !isEmptyDir(latest) && prevTS != "" {
			_ = os.MkdirAll(nsVersions(backups, udid), 0o755)
			if err := os.Rename(latest, nsVersionDir(backups, udid, mustParseTSOrNow(prevTS))); err != nil {
				t.Fatal(err)
			}
		}
		j.PrevTS = prevTS
		j.Phase = PhasePreviousArchived
	}
	if phase == PhaseLatestPromoted {
		if err := os.Rename(work, latest); err != nil {
			t.Fatal(err)
		}
		j.Phase = PhaseLatestPromoted
	}
	if err := writeJournal(dev, j); err != nil {
		t.Fatal(err)
	}
}

// Story 5: kill-at-every-namespace-phase → reconciliation rolls forward to a defined state.
func TestReconcileNamespaceKillMatrix(t *testing.T) {
	phases := []CommitPhase{PhasePrepared, PhasePreviousArchived, PhaseLatestPromoted}
	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())
			commitGoodTree(t, m, testUDID) // v1 is latest (id v000001)
			stageNSCommit(t, m, backups, testUDID, "job2", "v2crash", phase)

			if err := m.Reconcile(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			rows, _ := st.ListVersions(testUDID)
			if len(rows) != 2 {
				t.Fatalf("phase %s: want 2 versions after roll-forward, got %d", phase, len(rows))
			}
			if rows[0].ID != "v2crash" || !rows[0].IsLatest {
				t.Fatalf("phase %s: newest should be v2crash+latest, got %s latest=%v", phase, rows[0].ID, rows[0].IsLatest)
			}
			lm, err := ReadMarker(nsLatest(backups, testUDID))
			if err != nil || lm.VersionID != "v2crash" {
				t.Fatalf("phase %s: latest marker = %q err=%v", phase, lm.VersionID, err)
			}
			if journalExists(backups, testUDID) {
				t.Fatalf("phase %s: journal not cleared after reconcile", phase)
			}
		})
	}
}

// Story 5 / design §5: a lost registry write (journal cleared, marker on disk) → re-adopted.
func TestReconcileNamespaceLostRegistryWrite(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID)
	stageNSCommit(t, m, backups, testUDID, "job2", "v2lost", PhaseLatestPromoted)
	// Simulate the fs-consistent-but-registry-lost case: clear the journal.
	if err := removeJournal(deviceDir(backups, testUDID)); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, ok, _ := st.GetVersion("v2lost")
	if !ok {
		t.Fatal("lost version not re-adopted")
	}
	if row.JobID != nil {
		t.Fatalf("re-adopted version should be adopted (job_id nil), got %v", *row.JobID)
	}
	if !row.IsLatest {
		t.Fatal("re-adopted newest should be latest")
	}
}

// Story 6: adopt an on-disk version with no row; mark a row whose artifact vanished as missing.
func TestReconcileAdoptAndMissing(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())

	// A version dir on disk with a valid marker but no registry row → adopted.
	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "adopted-1", "", testUDID, BackendCopy)

	// A registry row whose artifact does not exist → marked missing.
	_ = st.InsertVersion(store.VersionRow{
		ID: "ghost-1", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), IsLatest: false,
	})

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	ad, ok, _ := st.GetVersion("adopted-1")
	if !ok || ad.JobID != nil {
		t.Fatalf("adopted-1 not adopted correctly (ok=%v jobID=%v)", ok, ad.JobID)
	}
	ghost, _, _ := st.GetVersion("ghost-1")
	if !ghost.Missing {
		t.Fatal("ghost-1 should be marked missing, not dropped")
	}
}

// Story 6: a checksum-failing marker is NOT adopted (surfaced, not silently trusted).
func TestReconcileSkipsCorruptMarker(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())
	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "corrupt-1", "", testUDID, BackendCopy)
	// Corrupt the marker (flip a byte in the checksummed body).
	corruptMarker(t, verDir)

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.GetVersion("corrupt-1"); ok {
		t.Fatal("a corrupt-marker version must not be adopted")
	}
}

// --- zfs kill matrix (fake-zfs) ---

func TestReconcileZFSKillMatrix(t *testing.T) {
	phases := []CommitPhase{PhasePrepared, PhaseSnapshotCreated, PhaseLatestRebuilt}
	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			m, be, f, backups, st := newZFSManager(t, generousPolicy())
			if err := be.Provision(testUDID); err != nil {
				t.Fatal(err)
			}
			working := zfsWorking(backups, testUDID)
			goodEncryptedFull(t, working)
			mustMarker(t, working, "zv-crash", "job1", testUDID, BackendZFS)

			snap := snapNameFor("zv-crash", created2)
			full := be.cli.dataset(testUDID) + "@" + snap
			dev := deviceDir(backups, testUDID)
			j := Journal{
				VersionID: "zv-crash", UDID: testUDID, Backend: BackendZFS, JobID: "job1",
				Phase: PhasePrepared, CreatedAt: fmtRFC(created2), Kind: "full", Encrypted: true,
				StructureVerifiedAt: fmtRFC(created2), ZFSSnapshot: full,
			}
			if phase == PhaseSnapshotCreated || phase == PhaseLatestRebuilt {
				if _, err := f.run(context.Background(), []string{"zfs", "snapshot", full}); err != nil {
					t.Fatal(err)
				}
				j.Phase = PhaseSnapshotCreated
			}
			if phase == PhaseLatestRebuilt {
				if err := be.rebuildLatest(testUDID, snap); err != nil {
					t.Fatal(err)
				}
				j.Phase = PhaseLatestRebuilt
			}
			if err := writeJournal(dev, j); err != nil {
				t.Fatal(err)
			}

			if err := m.Reconcile(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			row, ok, _ := st.GetVersion("zv-crash")
			if !ok || !row.IsLatest || row.ZFSSnapshot == nil {
				t.Fatalf("phase %s: zv-crash not reconciled (ok=%v latest=%v snap=%v)", phase, ok, row.IsLatest, row.ZFSSnapshot)
			}
			if journalExists(backups, testUDID) {
				t.Fatalf("phase %s: zfs journal not cleared", phase)
			}
		})
	}
}

// --- helpers ---

func mustMarker(t *testing.T, dir, vid, jobID, udid, backend string) {
	t.Helper()
	if err := WriteMarker(dir, Marker{
		VersionID: vid, JobID: jobID, UDID: udid, Backend: backend, CreatedAt: fmtRFC(created2),
		Kind: "full", Encrypted: true, StructureVerifiedAt: fmtRFC(created2), AppVersion: "test",
	}); err != nil {
		t.Fatal(err)
	}
}

func corruptMarker(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, MarkerName)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.VersionID = "tampered" // body changed, checksum left stale → mismatch
	b2, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(p, b2, 0o644); err != nil {
		t.Fatal(err)
	}
}

func journalExists(backups, udid string) bool {
	_, ok, _ := readJournal(deviceDir(backups, udid))
	return ok
}

func strPtrLocal(s string) *string { return &s }

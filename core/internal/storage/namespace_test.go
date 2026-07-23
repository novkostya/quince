package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// Story 4: namespace commit runs the atomic exchange + archive (latest is newest via a single
// RENAME_EXCHANGE — never an unoccupied instant — previous rotated to versions/<ts>/, working/
// gone), across the safe strategies.

func TestNamespaceCommitRotation(t *testing.T) {
	strategies := []clonetree.Strategy{clonetree.Copy, clonetree.Hardlink}
	if clonetree.ReflinkProbe(t.TempDir()) {
		strategies = append(strategies, clonetree.Reflink)
	}
	for _, strategy := range strategies {
		t.Run(strategy.String(), func(t *testing.T) {
			m, _, backups, st := newNSManager(t, strategy, generousPolicy())

			// First commit → becomes latest.
			goodEncryptedFull(t, seedTree(t, m, testUDID, "job1"))
			v1, err := m.CommitJob(testUDID, "job1")
			if err != nil {
				t.Fatalf("commit 1: %v", err)
			}

			// Second commit → v1 rotates to versions/<ts>/, v2 becomes latest.
			goodEncryptedFull(t, seedTree(t, m, testUDID, "job2"))
			v2, err := m.CommitJob(testUDID, "job2")
			if err != nil {
				t.Fatalf("commit 2: %v", err)
			}

			rows, _ := st.ListVersions(testUDID)
			if len(rows) != 2 {
				t.Fatalf("want 2 versions, got %d", len(rows))
			}
			if rows[0].ID != v2.ID || !rows[0].IsLatest {
				t.Fatalf("newest should be v2 latest; got %s latest=%v", rows[0].ID, rows[0].IsLatest)
			}
			if rows[1].ID != v1.ID || rows[1].IsLatest {
				t.Fatalf("older should be v1 non-latest; got %s latest=%v", rows[1].ID, rows[1].IsLatest)
			}

			// latest/ holds v2's marker.
			lm, err := ReadMarker(latestDir(backups, testUDID))
			if err != nil || lm.VersionID != v2.ID {
				t.Fatalf("latest marker = %q err=%v, want %s", lm.VersionID, err, v2.ID)
			}
			// versions/ holds exactly one rotated version (v1).
			vents, _ := os.ReadDir(nsVersions(backups, testUDID))
			if len(vents) != 1 {
				t.Fatalf("versions/ has %d dirs, want 1", len(vents))
			}
			vm, _ := ReadMarker(filepath.Join(nsVersions(backups, testUDID), vents[0].Name()))
			if vm.VersionID != v1.ID {
				t.Fatalf("rotated version = %s, want v1 %s", vm.VersionID, v1.ID)
			}
			// working/ is gone after a successful commit (between backups only latest/ + versions/).
			if _, err := os.Stat(workingParent(backups, testUDID)); !os.IsNotExist(err) {
				t.Fatal("working/ should be gone after commit")
			}
			// browse_root: latest points at latest/, the rotated one at versions/<ts>.
			wire := m.Versions(testUDID)
			if wire[0].BrowseRoot != latestDir(backups, testUDID) {
				t.Fatalf("latest browse_root = %q", wire[0].BrowseRoot)
			}
			if wire[1].BrowseRoot == latestDir(backups, testUDID) {
				t.Fatalf("rotated browse_root should be versions/<ts>, got %q", wire[1].BrowseRoot)
			}
		})
	}
}

// Story 6 (discard keeps the dirty working): a failed job's working/ is KEPT (unified with zfs, so
// a retry resumes); committed state untouched.
func TestNamespaceDiscardKeepsWorking(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID)
	tree := seedTree(t, m, testUDID, "jobX")
	goodEncryptedFull(t, tree)
	if _, err := m.Discard(testUDID, "jobX"); err != nil {
		t.Fatal(err)
	}
	if isEmptyDir(workingTree(backups, testUDID)) {
		t.Fatal("working/<udid> should be KEPT dirty after discard (resume), not deleted")
	}
	if !hasVersion(m, testUDID) {
		t.Fatal("committed version should survive a discard")
	}
}

// Story 6 (Reset discards): RepairWorkingCopy discards the dirty working so the next backup
// re-seeds; the committed version is untouched.
func TestNamespaceResetWorking(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID)
	// Leave a dirty working from a partial attempt.
	goodEncryptedFull(t, seedTree(t, m, testUDID, "jobX"))
	if err := m.RepairWorkingCopy(testUDID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(workingParent(backups, testUDID)); !os.IsNotExist(err) {
		t.Fatal("working/ should be gone after Reset")
	}
	if !hasVersion(m, testUDID) {
		t.Fatal("committed version should survive a Reset")
	}
}

func hasVersion(m *Manager, udid string) bool { return len(m.Versions(udid)) > 0 }

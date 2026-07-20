package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// Story 3: namespace commit runs the journaled rotation (latest is newest, previous rotated to
// versions/<ts>/, work/ gone), across the three strategies.

func TestNamespaceCommitRotation(t *testing.T) {
	strategies := []clonetree.Strategy{clonetree.Copy, clonetree.Hardlink}
	if clonetree.ReflinkProbe(t.TempDir()) {
		strategies = append(strategies, clonetree.Reflink)
	}
	for _, strategy := range strategies {
		t.Run(strategy.String(), func(t *testing.T) {
			m, _, backups, st := newNSManager(t, strategy, generousPolicy())

			// First commit → becomes latest.
			w1, err := m.Seed(testUDID, "job1")
			if err != nil {
				t.Fatal(err)
			}
			goodEncryptedFull(t, w1)
			v1, err := m.CommitJob(testUDID, "job1")
			if err != nil {
				t.Fatalf("commit 1: %v", err)
			}

			// Second commit → v1 rotates to versions/<ts>/, v2 becomes latest.
			w2, err := m.Seed(testUDID, "job2")
			if err != nil {
				t.Fatal(err)
			}
			goodEncryptedFull(t, w2)
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
			lm, err := ReadMarker(nsLatest(backups, testUDID))
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
			// work/ is empty (both jobs promoted/consumed).
			if !isEmptyDir(filepath.Join(deviceDir(backups, testUDID), "work")) {
				t.Fatal("work/ not empty after commits")
			}
			// browse_root: latest points at latest/, the rotated one at versions/<ts>.
			wire := m.Versions(testUDID)
			if wire[0].BrowseRoot != nsLatest(backups, testUDID) {
				t.Fatalf("latest browse_root = %q", wire[0].BrowseRoot)
			}
			if wire[1].BrowseRoot == nsLatest(backups, testUDID) {
				t.Fatalf("rotated browse_root should be versions/<ts>, got %q", wire[1].BrowseRoot)
			}
		})
	}
}

// Story 3 (discard): a failed job's work is removed; committed state untouched.
func TestNamespaceDiscard(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID)
	w, _ := m.Seed(testUDID, "jobX")
	goodEncryptedFull(t, w)
	if _, err := m.Discard(testUDID, "jobX"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nsWork(backups, testUDID, "jobX")); !os.IsNotExist(err) {
		t.Fatal("work/jobX should be gone after discard")
	}
	if !hasVersion(m, testUDID) {
		t.Fatal("committed version should survive a discard")
	}
}

// Story 11: RepairWorkingCopy reseeds work/ from latest/; fails honestly with no last-good.
func TestNamespaceRepairWorkingCopy(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	// No latest yet → repair fails honestly.
	if err := m.RepairWorkingCopy(testUDID); err == nil {
		t.Fatal("repair with no last-good version should fail")
	}
	commitGoodTree(t, m, testUDID)
	if err := m.RepairWorkingCopy(testUDID); err != nil {
		t.Fatalf("repair: %v", err)
	}
	reseed := filepath.Join(deviceDir(backups, testUDID), "work", "current")
	if _, err := os.Stat(filepath.Join(reseed, "Status.plist")); err != nil {
		t.Fatalf("reseeded work/current missing latest content: %v", err)
	}
}

func hasVersion(m *Manager, udid string) bool { return len(m.Versions(udid)) > 0 }

package storage

import (
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// TestSeedInProgressGuard is the Finding B ((cv)) proof: WorkDir must DISCARD + re-seed a
// working/<udid> left by a seed killed mid-clone (sentinel `seed_in_progress:true`), but RESUME a
// working/<udid> from a completed seed (`seed_in_progress:false`) OR a legacy sentinel that predates
// the field (absent → false → resume, so an upgrade never throws away a resumable 34 GB working).
// The guard must DISCRIMINATE the two — resuming a partial seed could commit a version missing blobs.
//
// IT RAN ON BOTH VERSION MODELS UNTIL qn.6h AND IS NOW NAMESPACE-ONLY. zfs no longer seeds: the tool
// writes into the dataset root, so there is no clone, no partial clone, and nothing for
// `seed_in_progress` to describe. The zfs arm is dropped rather than kept green against a code path
// that cannot run — and workState.SeedInProgress carries the warning that comes with it, since
// repurposing that flag would make this very guard discard a resumable dirty head.
func TestSeedInProgressGuard(t *testing.T) {
	backends := []struct {
		name  string
		build func(t *testing.T) (m *Manager, backups, udid string)
	}{
		{"namespace", func(t *testing.T) (*Manager, string, string) {
			m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
			return m, backups, testUDID
		}},
	}
	scenarios := []struct {
		name       string
		sentinel   func(backups, udid string) // plants the working-tree sentinel
		wantResume bool                       // true = TAG survives (resumed); false = re-seeded
	}{
		{"killed seed → re-seed", func(b, u string) {
			_ = writeWorkStateAt(nsWorkSentinel(b, u), workState{SeededFromLatest: true, SeedInProgress: true})
		}, false},
		{"completed seed → resume", func(b, u string) {
			_ = writeWorkStateAt(nsWorkSentinel(b, u), workState{SeededFromLatest: true, SeedInProgress: false})
		}, true},
		{"legacy sentinel (no field) → resume", func(b, u string) {
			// Old-code sentinel: written post-seed, no seed_in_progress field. Must read complete.
			writeFile(t, nsWorkSentinel(b, u), []byte(`{"seeded_from_latest": true}`))
		}, true},
	}

	for _, be := range backends {
		for _, sc := range scenarios {
			t.Run(be.name+"/"+sc.name, func(t *testing.T) {
				m, backups, udid := be.build(t)
				commitGoodTree(t, m, udid) // latest/ = v1 (something to re-seed FROM)

				// Plant a tagged dirty working/<udid> + the scenario's sentinel.
				tree := workingTree(backups, udid)
				goodEncryptedFull(t, tree)
				writeFile(t, filepath.Join(tree, "TAG"), []byte("do-not-lose-me"))
				sc.sentinel(backups, udid)

				if _, err := m.Seed(udid, "next"); err != nil {
					t.Fatalf("seed: %v", err)
				}

				got := fileExists(filepath.Join(workingTree(backups, udid), "TAG"))
				if got != sc.wantResume {
					if sc.wantResume {
						t.Fatal("TAG gone — a completed/legacy seed was wrongly RE-SEEDED (would lose a resumable working)")
					}
					t.Fatal("TAG survived — a partial/killed seed was wrongly RESUMED (could commit a version missing blobs)")
				}
			})
		}
	}
}

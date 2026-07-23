package storage

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// TestCommitLatestNeverGoesMissing is the qn.5b gate's two-observer proof at the CI level (roadmap
// G-snapshot + G-rclone): a concurrent reader loops reading latest/'s marker for the whole duration
// of a SECOND commit. Because commit swaps working/<udid> into latest/ with one atomic
// RENAME_EXCHANGE — never the old two-rename window — latest/ ALWAYS exists and ALWAYS carries a
// complete version (v1 then v2), never missing. That missing window is exactly what made an
// rclone/snapshot crossing it delete the remote (the (cg) defect). This test FAILS against the
// pre-qn.5b two-rename swap and PASSES against the exchange, on both version models.
func TestCommitLatestNeverGoesMissing(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) (m *Manager, backups, udid string)
	}{
		{"namespace", func(t *testing.T) (*Manager, string, string) {
			m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
			return m, backups, testUDID
		}},
		{"zfs", func(t *testing.T) (*Manager, string, string) {
			m, _, _, backups, _ := newZFSManager(t, generousPolicy())
			return m, backups, testUDID
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, backups, udid := tc.build(t)
			commitGoodTree(t, m, udid) // v1 becomes the committed latest/
			latest := latestDir(backups, udid)

			// Fill a second job's working tree, ready to commit.
			goodEncryptedFull(t, seedTree(t, m, udid, "job2"))

			var missing atomic.Int64
			stop := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					// The snapshot/rclone observer: latest/ must ALWAYS carry a complete version.
					if mk, err := ReadMarker(latest); err != nil || mk.VersionID == "" {
						missing.Add(1) // latest/ absent or torn → the deletion/tear bug
					}
				}
			}()

			v2, err := m.CommitJob(udid, "job2")
			close(stop)
			wg.Wait()
			if err != nil {
				t.Fatalf("commit 2: %v", err)
			}
			if got := missing.Load(); got > 0 {
				t.Fatalf("latest/ was missing/torn %d times during commit — the atomic exchange must keep it continuously valid", got)
			}
			if mk, err := ReadMarker(latest); err != nil || mk.VersionID != v2.ID {
				t.Fatalf("after commit latest/ marker = %q err=%v, want v2 %s", mk.VersionID, err, v2.ID)
			}
		})
	}
}

// TestSeedResumeNoReTransfer is story 6: a failed job's dirty working/<udid> is KEPT, and a retry
// resumes into it WITHOUT re-seeding (no re-transfer of the already-received tree). Proven by
// tagging the dirty tree with a sentinel file and asserting it survives the next WorkDir.
func TestSeedResumeNoReTransfer(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID)

	// A backup fills working/<udid>, then FAILS → Discard keeps it dirty.
	tree := seedTree(t, m, testUDID, "job2")
	goodEncryptedFull(t, tree)
	writeFile(t, filepath.Join(tree, "already-received.marker"), []byte("do not re-transfer me"))
	if _, err := m.Discard(testUDID, "job2"); err != nil {
		t.Fatal(err)
	}

	// The retry seeds again — it must RESUME the dirty tree, not re-clone from latest/.
	target, err := m.Seed(testUDID, "job3-retry")
	if err != nil {
		t.Fatalf("retry seed: %v", err)
	}
	if !fileExists(filepath.Join(target, testUDID, "already-received.marker")) {
		t.Fatal("retry re-seeded from latest/ instead of resuming the dirty working — the partial would be re-transferred")
	}
}

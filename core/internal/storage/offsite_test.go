package storage

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// syncSim walks src (the transfer root) and copies every file NOT excluded by rules into dst —
// a faithful stand-in for `rclone sync --filter` for the specific anchored/unanchored rule
// shapes quince ships (rclone itself is absent in CI; the real binary runs in lab gate 12).
func syncSim(t *testing.T, dst, src string, rules []string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if PathExcluded(filepath.ToSlash(rel), rules) {
			return nil
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		of, err := os.Create(out)
		if err != nil {
			return err
		}
		defer func() { _ = of.Close() }()
		_, err = io.Copy(of, in)
		return err
	})
	if err != nil {
		t.Fatalf("syncSim: %v", err)
	}
}

// buildTransferTree lays out a whole-storage transfer root with quince's subtree: a valid
// latest/ (including a content dir literally named "working" to catch over-match), a rotated
// versions/<ts>/, a work/<job>/, and a device-level working/ (the zfs case). subdir/udid names
// echo the offsite layout.
func buildTransferTree(t *testing.T, root, subdir, udid string) {
	t.Helper()
	dev := filepath.Join(root, subdir, udid)
	goodEncryptedFull(t, filepath.Join(dev, "latest"))
	// A content directory named "working" INSIDE latest/ — the anchored filter must NOT drop it.
	writeFile(t, filepath.Join(dev, "latest", "SubApp", "working", "data.bin"), []byte("real backup content"))
	// Things the offsite copy must exclude:
	writeFile(t, filepath.Join(dev, "working", "dirty.tmp"), []byte("mid-backup"))
	writeFile(t, filepath.Join(dev, "work", "job1", "partial.tmp"), []byte("in flight"))
	writeFile(t, filepath.Join(dev, "versions", "2026-07-01T00-00-00Z", "Status.plist"), []byte("old"))
}

// Story 10: the anchored filter uploads a complete latest/ (incl. nested working/), excludes
// working//work//versions/, and a concurrent backup churning work/ cannot perturb the upload.
func TestOffsiteAnchoredFilterContract(t *testing.T) {
	root := t.TempDir()
	subdir, udid := "iphone-backup", testUDID
	buildTransferTree(t, root, subdir, udid)
	rules := AnchoredFilterRules(subdir)
	dst := filepath.Join(t.TempDir(), "remote")

	// A concurrent "backup" churning work/ during the sync must not affect the upload.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				_ = os.WriteFile(filepath.Join(root, subdir, udid, "work", "job1", "churn"),
					[]byte(time.Now().String()), 0o644)
			}
		}
	}()
	syncSim(t, dst, root, rules)
	close(stop)
	wg.Wait()

	dev := filepath.Join(dst, subdir, udid)
	// latest/ is present and structurally valid (a complete verified backup).
	if r := Verify(filepath.Join(dev, "latest")); !r.OK {
		t.Fatalf("synced latest/ should verify: %s", r.Detail)
	}
	// The nested content dir named "working" survives (NOT over-excluded).
	if !fileExists(filepath.Join(dev, "latest", "SubApp", "working", "data.bin")) {
		t.Fatal("anchored filter wrongly dropped a content dir named working inside latest/")
	}
	// working//work//versions/ are excluded.
	for _, gone := range []string{"working", "work", "versions"} {
		if fileExists(filepath.Join(dev, gone)) {
			t.Fatalf("offsite copy must not contain %s/", gone)
		}
	}
}

// Story 10 (negative): an UNANCHORED working exclude over-matches content inside latest/ —
// proving why the shipped rules must be anchored.
func TestOffsiteUnanchoredFilterOverMatches(t *testing.T) {
	root := t.TempDir()
	subdir, udid := "iphone-backup", testUDID
	buildTransferTree(t, root, subdir, udid)
	dst := filepath.Join(t.TempDir(), "remote")
	syncSim(t, dst, root, []string{"- **/working/**"}) // the WRONG, unanchored rule

	nested := filepath.Join(dst, subdir, udid, "latest", "SubApp", "working", "data.bin")
	if fileExists(nested) {
		t.Fatal("expected the unanchored filter to WRONGLY drop nested latest content — anchoring matters")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

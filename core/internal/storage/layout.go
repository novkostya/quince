package storage

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// tsDirLayout is the filesystem-safe timestamp used for versions/<ts>/ dir names (contracts §2
// example: versions/2026-07-18T02-30-11Z). RFC3339 with ':' → '-'.
const tsDirLayout = "2006-01-02T15-04-05Z"

// snapDateLayout is the date+minute prefix in a zfs snapshot name
// @quince-<YYYY-MM-DDTHH-MM>-<ULID> (qn.5b amendment B, decisions (co)): date-first for
// readable `zfs list` ordering, widened to the minute, with the ULID (== versionID) kept as the
// collision-free tail. The 'T' separator + dash-minutes keep it snapshot-name-safe (no ':').
const snapDateLayout = "2006-01-02T15-04"

func tsDir(t time.Time) string { return t.UTC().Format(tsDirLayout) }

// udidPattern guards any UDID before it reaches a path or an argv (design §6). Same shape as
// deviceops' allowlist — no separators, dots, or shell metacharacters.
var udidPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func validUDID(udid string) bool { return udidPattern.MatchString(udid) }

// deviceDir is <backupsRoot>/<udid> — the device's storage root on every backend.
func deviceDir(backupsRoot, udid string) string { return filepath.Join(backupsRoot, udid) }

// Unified layout (qn.5b — the two version models collapse toward one). Every backend now shares:
//
//	latestDir      <deviceDir>/latest          the newest committed version's live directory;
//	                                            permanent between backups; the sole rclone payload.
//	workingParent  <deviceDir>/working         the per-device staging PARENT handed to
//	                                            idevicebackup2 as its target (it writes the tree
//	                                            into <target>/<UDID> = workingTree, its own
//	                                            convention — so NO symlink is needed); exists only
//	                                            during/after a job, removed on success, KEPT dirty
//	                                            on failure so a retry resumes.
//	workingTree    <deviceDir>/working/<udid>  where idevicebackup2 writes and quince verifies;
//	                                            exchanged into latestDir at commit.
//	workSentinel   <deviceDir>/.quince-work.json   records whether working was seeded from an
//	                                            existing latest/ (⇒ the authoritative full|
//	                                            incremental kind); survives crash/resume; lives
//	                                            OUTSIDE working/ so it never rides into latest/.
//
// Namespace backends additionally keep versions/<ts>/ for rotated-out prior versions (zfs versions
// are snapshots, so there is no versions/ dir — between backups the dataset holds only latest/).
func latestDir(backupsRoot, udid string) string {
	return filepath.Join(deviceDir(backupsRoot, udid), "latest")
}
func workingParent(backupsRoot, udid string) string {
	return filepath.Join(deviceDir(backupsRoot, udid), "working")
}
func workingTree(backupsRoot, udid string) string {
	return filepath.Join(workingParent(backupsRoot, udid), udid)
}
func workSentinel(backupsRoot, udid string) string {
	return filepath.Join(deviceDir(backupsRoot, udid), workSentinelName)
}
func nsVersions(backupsRoot, udid string) string {
	return filepath.Join(deviceDir(backupsRoot, udid), "versions")
}
func nsVersionDir(backupsRoot, udid string, t time.Time) string {
	return filepath.Join(nsVersions(backupsRoot, udid), tsDir(t))
}

// snapName extracts the snapshot short name (after '@') from a full dataset@snap string.
func snapName(full string) string {
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '@' {
			return full[i+1:]
		}
	}
	return full
}

// browseRoot computes contracts §2 Version.browse_root from the committed shape (never stored —
// it moves as a namespace version rotates latest→versions, so it is derived at read time).
func browseRoot(backupsRoot, udid, backend string, zfsSnapshot *string, isLatest bool, createdAt time.Time) string {
	if backend == BackendZFS && zfsSnapshot != nil {
		// qn.5b: the version content lives at latest/ INSIDE the snapshot (was working/) — the
		// commit exchanges the tree into latest/ before snapshotting, so the snapshot IS latest/.
		return filepath.Join(deviceDir(backupsRoot, udid), ".zfs", "snapshot", snapName(*zfsSnapshot), "latest")
	}
	if isLatest {
		return latestDir(backupsRoot, udid)
	}
	return nsVersionDir(backupsRoot, udid, createdAt)
}

// dirSize sums regular-file sizes under root (best-effort; errors → what we could count). Used
// for logical_bytes at commit — never on the read hot path (perf budget).
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// isEmptyDir reports whether dir is absent or contains no entries.
func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	return len(entries) == 0
}

// hexShardDir reports whether name is a two-lowercase-hex-char blob shard dir (ab, cd, …).
var hexShard = regexp.MustCompile(`^[0-9a-f]{2}$`)

func hexShardDir(name string) bool { return hexShard.MatchString(name) }

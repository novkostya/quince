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
// ON ZFS IT RETURNS "" RATHER THAN FALLING THROUGH TO THE LIVE TREE. A zfs version's content is
// only ever inside a snapshot: the live tree is the PREVIOUS version until a commit completes, and
// under qn.6h it becomes the mutable head a backup writes into directly. Falling through would hand
// a browse session a half-transferred tree and present it as a version — silently, because a partial
// listing looks like a small backup rather than like an error.
//
// The nil case is REPRESENTABLE rather than known to occur — ZFSSnapshot is a *string off a registry
// row, and zfs.go handles a nil elsewhere. This refuses instead of arguing it cannot arise, because
// "it cannot happen" is an assumption nobody wrote down (CLAUDE.md, Forbidden).
//
// "" is the caller's cue to surface the version as UNBROWSABLE WITH A REASON — the vocabulary a
// `missing` artifact already uses for "the row exists and the content cannot be served".
func browseRoot(backupsRoot, udid, backend string, zfsSnapshot *string, isLatest bool, createdAt time.Time) string {
	if backend == BackendZFS {
		if zfsSnapshot == nil {
			return ""
		}
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
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return skipSnapdir(d)
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// snapdirName is ZFS's per-dataset snapshot directory. It is a CHILD OF THE DATASET ROOT, so once
// the backup tree becomes that root (qn.6h) every walker over the tree can descend into it.
const snapdirName = ".zfs"

// skipSnapdir tells a filepath.WalkDir to step over ZFS's .zfs directory.
//
// AT snapdir=hidden THIS IS A NO-OP AND EVERYTHING IS SAFE BY LUCK: readdir never returns .zfs, so
// a walker cannot enter it. At snapdir=visible — which operators set precisely so they can browse
// snapshots by hand — it IS returned, and a walk of the tree descends into EVERY SNAPSHOT. Verify
// and logical_bytes would then be wrong in proportion to how many versions exist, silently and only
// on the machines whose owners looked closest.
//
// It is deliberately not conditional on the backend: a `.zfs` directory inside a backup tree is
// never quince's content on any backend, and a walker that skips it unconditionally cannot be got
// wrong by a caller that forgets which backend it is on.
func skipSnapdir(d fs.DirEntry) error {
	if d.IsDir() && d.Name() == snapdirName {
		return filepath.SkipDir
	}
	return nil
}

// isEmptyDir reports whether dir is absent or contains no entries.
//
// .ZFS DOES NOT COUNT AS AN ENTRY. Under qn.6h this answers "has this device been backed up before"
// — the derivation behind Version.kind — over the dataset root. At snapdir=visible a device with
// ZERO backups would otherwise read as non-empty and its first backup would be recorded
// `incremental`: wrong in the field, invisible in CI, and on exactly the value the lab proved
// Status.plist.IsFullBackup lies about.
func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if e.Name() != snapdirName {
			return false
		}
	}
	return true
}

// hexShardDir reports whether name is a two-lowercase-hex-char blob shard dir (ab, cd, …).
var hexShard = regexp.MustCompile(`^[0-9a-f]{2}$`)

func hexShardDir(name string) bool { return hexShard.MatchString(name) }

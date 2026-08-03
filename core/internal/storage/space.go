package storage

import "golang.org/x/sys/unix"

// FilesystemSpace reports the bytes available to an unprivileged writer and the total size of the
// FILESYSTEM containing path — never of the storage.
//
// THE NAMING IS THE CONTRACT, and it is why the wire fields are `filesystem_free_bytes` and
// `filesystem_total_bytes` (contracts §2, ruled 2026-08-03). Two storages that are two directories
// on one disk return identical figures here, and nothing in the result distinguishes them: `statfs`
// answers about a filesystem, and there is no cheap identity for one that survives a remount.
// A caller that presents either number as *this storage's own* is making a claim this function
// cannot support.
//
// Free uses Bavail, not Bfree: root-reserved blocks are not available to quince, so counting them
// would overstate what a backup can use. That matches `backup.statfsFree`, which this deliberately
// does NOT reuse — it is unexported in a device-facing package, returns only the free half, and is
// wired into the A3 low-space watch. Duplicating one syscall is cheaper than exporting a hot path's
// helper to a second consumer with different needs.
func FilesystemSpace(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize) //nolint:unconvert,gosec // Bsize is int64 on linux, int32 on darwin
	return uint64(st.Bavail) * bsize, uint64(st.Blocks) * bsize, nil
}

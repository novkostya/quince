//go:build linux

package storage

import "golang.org/x/sys/unix"

// exchange atomically swaps the directory entries a and b in ONE syscall
// (renameat2 with RENAME_EXCHANGE): neither name is ever unoccupied, unlike the
// two-rename swap it replaces (qn.5b). Both paths MUST exist and be ordinary
// directories on the SAME mounted filesystem — two child datasets in one pool are
// still different filesystems and fail EXDEV, so the per-job layout keeps
// working/<udid> and latest/ as plain dirs inside one device dataset. This is the
// atomic-`latest` primitive: commit exchanges working/<udid> into latest/ with no
// observable gap (verified live on the operator's ZFS, decisions (co); the
// util-linux `exch` CLI does the same). The named symbols are compiler-verified
// against the pinned golang.org/x/sys — never a hardcoded flag value (hard rule).
func exchange(a, b string) error {
	return unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
}

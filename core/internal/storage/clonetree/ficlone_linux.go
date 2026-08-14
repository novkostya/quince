//go:build linux

package clonetree

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// reflinkFile clones src → dst via the FICLONE ioctl (whole-file copy-on-write). The ioctl
// reaches the real filesystem through container bind mounts (only that layer must support it —
// stack D5); busybox userlands are irrelevant since nothing shells out to `cp --reflink`.
func reflinkFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("%w: %v", classifyFICLONE(err), err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	mt := info.ModTime()
	return os.Chtimes(dst, mt, mt)
}

// classifyFICLONE says whether an ioctl failure means "this filesystem cannot" or "not this clone,
// not right now".
//
// ONLY EAGAIN IS TRANSIENT, and the list is short on purpose. EOPNOTSUPP, ENOTTY, EINVAL and EXDEV
// are all settled answers about the filesystem or the pair of files; retrying any of them would
// loop. EAGAIN is the one errno FICLONE uses to mean "ask again" — measured on the lab rig, where
// ZFS block cloning refuses a source not yet in a synced txg with exactly that (quince#790).
//
// AN UNKNOWN ERRNO STAYS `unsupported`, which is the conservative direction: it keeps the strategy
// off a filesystem nobody has characterised, where the opposite default would put backups on one.
func classifyFICLONE(err error) error {
	if errors.Is(err, unix.EAGAIN) {
		return ErrReflinkUnavailable
	}
	return ErrReflinkUnsupported
}

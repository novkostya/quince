//go:build linux

package clonetree

import (
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
		return fmt.Errorf("%w: %v", ErrReflinkUnsupported, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	mt := info.ModTime()
	return os.Chtimes(dst, mt, mt)
}

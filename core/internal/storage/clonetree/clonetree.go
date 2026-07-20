// Package clonetree is quince's one tree-clone implementation, shared by every consumer that
// needs to materialize a copy of a backup tree: namespace Seed (populate work/ from latest/),
// namespace version promotion, and the zfs latest/ mirror (design §5, stack D5). It offers
// three strategies — reflink (FICLONE, independent CoW files), hardlink (shared inodes, guarded
// by the destructive safety matrix), and copy — chosen by the storage probe up front, never
// per file: the strategy is decided once (deterministic, logged) and applied uniformly.
//
// The hardlink strategy NEVER shares an inode for a file class the backup writer may mutate in
// place (MutatesInPlace) — sharing would corrupt the immutable previous version. Reflink and
// copy produce independent files, so the hazard (and its matrix) does not apply to them.
package clonetree

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Strategy selects how regular files are materialized.
type Strategy int

const (
	// Reflink clones via the FICLONE ioctl: independent copy-on-write files, near-instant,
	// zero extra space until divergence. Requires a reflink-capable filesystem (probed).
	Reflink Strategy = iota
	// Hardlink shares inodes except for MutatesInPlace classes, which are copied. Same-fs only.
	Hardlink
	// Copy is a full independent byte copy (preserves mode + mtime).
	Copy
)

func (s Strategy) String() string {
	switch s {
	case Reflink:
		return "reflink"
	case Hardlink:
		return "hardlink"
	case Copy:
		return "copy"
	default:
		return "unknown"
	}
}

// ErrReflinkUnsupported is returned by the reflink path when the filesystem refuses FICLONE.
// The strategy is chosen by a probe before Clone runs, so hitting this mid-clone is a real,
// surfaced error (never a silent fallback — hard rule).
var ErrReflinkUnsupported = errors.New("clonetree: reflink (FICLONE) unsupported on this filesystem")

// Clone recreates the tree rooted at src under dst using strategy for regular files.
// Directories are created with their source mode, symlinks recreated, regular files cloned.
// dst is created if absent; it should be empty (a fresh work/ or mirror dir).
func Clone(dst, src string, strategy Strategy) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("clonetree: stat src: %w", err)
	}
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("clonetree: mkdir dst: %w", err)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // dst root already made
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			return cloneFile(target, path, rel, strategy)
		default:
			// Sockets/devices/fifos never occur in a backup tree; skip loudly-safely.
			return nil
		}
	})
}

func cloneFile(dst, src, rel string, strategy Strategy) error {
	switch strategy {
	case Reflink:
		return reflinkFile(dst, src)
	case Hardlink:
		if MutatesInPlace(rel) {
			return copyFile(dst, src) // never share an inode with a committed version
		}
		if err := os.Link(src, dst); err != nil {
			return fmt.Errorf("clonetree: hardlink %s: %w", rel, err)
		}
		return nil
	case Copy:
		return copyFile(dst, src)
	default:
		return fmt.Errorf("clonetree: unknown strategy %d", strategy)
	}
}

// copyFile makes an independent byte copy preserving mode and mtime (the safety matrix checks
// metadata identity of untouched files across a commit).
func copyFile(dst, src string) error {
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
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	mt := info.ModTime()
	return os.Chtimes(dst, mt, mt)
}

// MutatesInPlace reports whether a backup-relative path is a file class the MobileBackup2
// writer may rewrite/mutate in place — which must therefore be copied (never hard-linked) so a
// committed version's inode is never touched. SQLite databases + their -wal/-shm sidecars and
// the top-level metadata plists are the known classes; the gate-12 destructive matrix validates
// and, on any new finding, extends this list (with a replay fixture — hard rule). Reflink/copy
// trees are exempt (independent files), so this is consulted only for the hardlink strategy.
func MutatesInPlace(rel string) bool {
	base := filepath.Base(rel)
	for _, suf := range []string{
		".db", ".db-wal", ".db-shm", ".db-journal",
		".sqlite", ".sqlite-wal", ".sqlite-shm",
		"-wal", "-shm",
	} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	switch base {
	case "Status.plist", "Info.plist", "Manifest.plist":
		return true
	}
	return false
}

// ReflinkProbe reports whether dir's filesystem supports reflinks with true independence: it
// writes a small source file, FICLONE-clones it, mutates the clone, and verifies the source is
// unchanged. Cleans up after itself. Used by the storage auto-selection probe.
func ReflinkProbe(dir string) bool {
	src := filepath.Join(dir, ".quince-reflink-src")
	dst := filepath.Join(dir, ".quince-reflink-dst")
	defer func() { _ = os.Remove(src); _ = os.Remove(dst) }()
	if err := os.WriteFile(src, []byte("AAAAAAAA"), 0o600); err != nil {
		return false
	}
	if err := reflinkFile(dst, src); err != nil {
		return false
	}
	// Mutate the clone; a true CoW clone leaves the source intact.
	if err := os.WriteFile(dst, []byte("BBBBBBBB"), 0o600); err != nil {
		return false
	}
	got, err := os.ReadFile(src)
	if err != nil {
		return false
	}
	return string(got) == "AAAAAAAA"
}

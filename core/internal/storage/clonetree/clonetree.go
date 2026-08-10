// Package clonetree is quince's one tree-clone implementation, shared by every consumer that
// needs to materialize a copy of a backup tree: namespace Seed (populate work/ from latest/),
// namespace version promotion, and the zfs latest/ mirror (design §5, stack D5). It offers
// three strategies — reflink (FICLONE, independent CoW files), hardlink (shared inodes), and
// copy — chosen by the storage probe up front, never
// per file: the strategy is decided once (deterministic, logged) and applied uniformly.
//
// The hardlink strategy shares an inode for EVERY regular file. Its safety rests on a property of
// the WRITER rather than on anything this package does: `idevicebackup2` unlinks a file before
// creating its replacement, on every path that writes into a backup tree, so a rewrite breaks the
// alias instead of reaching through it into the committed version. Gate 12c measured that on
// hardware (quince#518) — see the Hardlink doc for what it assumes and when to re-check it.
// Reflink and copy produce independent files, so the question does not arise for them.
package clonetree

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Strategy selects how regular files are materialized.
type Strategy int

const (
	// Reflink clones via the FICLONE ioctl: independent copy-on-write files, near-instant,
	// zero extra space until divergence. Requires a reflink-capable filesystem (probed).
	Reflink Strategy = iota
	// Hardlink shares one inode per regular file with the source tree. Same-fs only, and near-free:
	// a 135,183-file / 35 GB backup seeded in 272 MiB of writes where a copy seed cost ~35 GB
	// (measured, quince#518).
	//
	// IT SHARES EVERYTHING, INCLUDING Manifest.db, AND THAT IS A MEASURED CHOICE RATHER THAN AN
	// OVERSIGHT. An earlier version copied a list of "classes the writer may mutate in place"
	// (`MutatesInPlace`: dbs, -wal/-shm sidecars, the top-level plists). Gate 12c showed the list
	// was not what protected the committed version, on two devices with it fully disabled:
	//
	//   - 94,034-file iPad, 35-file incremental  → committed tree unchanged, 29 blobs relinked
	//   - 135,183-file iPhone, 121-file / 4.1 GB incremental, EVERY file aliased including a
	//     266 MB Manifest.db → committed tree unchanged across all 135,183 files
	//
	// The four metadata files came back at link count 1 both times: the tool unlinked and recreated
	// them. The transition was caught live — `latest/Manifest.db` going links=2 → links=1 with its
	// size unmoved, which is unlink-then-create in the act.
	//
	// WHAT THIS ASSUMES, so a future reader knows what would break it: that every path in
	// `idevicebackup2` which writes into the backup tree unlinks first. Three of its four do so
	// explicitly (`remove_file` before create / before rename). The fourth —
	// `mb2_copy_file_by_path`, reached from `DLMessageCopyItem` — does NOT, and its destination is
	// named by the DEVICE at runtime, so no list here could have covered it either. It was not
	// observed firing on either device, with a detector validated by its siblings printing at the
	// same verbosity. **Absence over two devices is not never**: if a backup ever corrupts a
	// committed version, that call is the first place to look, and the fix is upstream rather than
	// here.
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
		// EVERY regular file is linked, including the metadata classes this code used to copy.
		// Gate 12c measured why that list was not what kept the committed version safe
		// (quince#518): `idevicebackup2` UNLINKS before it creates, on every path that writes into
		// the backup tree, so a rewrite breaks the alias instead of reaching through it. See the
		// Hardlink strategy doc for what that assumes and what it costs.
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

// MutatesInPlace IS GONE (quince#518). It listed the file classes the hardlink strategy copied
// instead of linking — `.db`, `-wal`/`-shm` sidecars, and the top-level plists — on the reasoning
// that the MobileBackup2 writer might rewrite them in place and so corrupt the committed version
// through the alias.
//
// GATE 12C MEASURED THAT THE LIST PROTECTED NOTHING. Seeded with the list disabled, on two devices,
// `idevicebackup2` unlinked and recreated every one of those files and the committed tree came back
// byte-identical. What kept it safe was the writer's unlink-first idiom, which covers the content
// blobs the list never named either. The list's own `Manifest.db` entry cost 266 MB of copying per
// seed for no measurable safety.
//
// This tombstone is a GUARD rather than archaeology (quince#595): the list looks obviously prudent,
// and the next reader to worry about in-place writes should know it was tried, measured, and
// removed — and that the residual risk it could never have covered is `DLMessageCopyItem`, which is
// upstream. Delete this comment once that call is patched or the concern is ruled dead.

// ReflinkResult is what ReflinkProbeDetail found. The three values exist because the two
// questions the probe asks have different answers on different filesystems, and quince#747 is
// the record of what collapsing them into a bool costs.
type ReflinkResult int

const (
	// ReflinkUnsupported — do NOT use the reflink strategy here. Either FICLONE refused, or it
	// succeeded and produced a file that shares nothing (a silent full copy), or the clone was
	// not independent of its source.
	ReflinkUnsupported ReflinkResult = iota
	// ReflinkSharing — FICLONE succeeded and the clone DEMONSTRABLY shares extents with its
	// source. This is the only result that earns the space claim stack D5 makes for reflink.
	ReflinkSharing
	// ReflinkSharingUnverifiable — FICLONE succeeded and the clone is independent, but this
	// filesystem cannot report extent sharing, so the space claim is unproven here. Measured on
	// the lab rig: ZFS with block cloning enabled accepts FICLONE and implements no FIEMAP.
	ReflinkSharingUnverifiable
)

// reflinkProbeSize is how large the probe's source file is, and it is load-bearing rather than
// arbitrary. btrfs stores a small file INLINE in its metadata, and an inline extent carries no
// FIEMAP_EXTENT_SHARED flag whether or not the file was cloned — measured on the lab rig, where
// the 8-byte file this probe used to write comes back `inline` on btrfs after a real
// `cp --reflink=always`. 1 MiB is far above any filesystem's inline/tail-packing threshold and
// costs one write, once, per storage at startup.
const reflinkProbeSize = 1 << 20

// ReflinkProbe reports whether dir's filesystem supports the reflink strategy at all. It is the
// bool form of ReflinkProbeDetail and deliberately treats "sharing unverifiable" as supported —
// callers that care about the distinction (and the storage auto-selection probe does, because
// the distinction is the whole of quince#747) must use ReflinkProbeDetail.
func ReflinkProbe(dir string) bool {
	res, _ := ReflinkProbeDetail(dir)
	return res != ReflinkUnsupported
}

// ReflinkProbeDetail probes dir's filesystem for reflink support and returns the result plus a
// sentence naming what was actually observed, for the storage backend's reason string.
//
// IT TESTS SHARING, NOT ONLY INDEPENDENCE, and that is quince#747. The old probe wrote 8 bytes,
// FICLONE-cloned them, mutated the clone and checked the source was intact — which is exactly
// what a plain `cp` also does. Two independent files with identical content behave identically
// under mutation, so the probe could tell a clone from a HARDLINK and could not tell a clone
// from a COPY. `probeNamespace` then selected the reflink backend on that evidence, and stack D5
// chooses reflink FOR SPACE: a FICLONE that succeeded without sharing would have made every
// version a full physical copy, silently, with every gate still green.
//
// So the order is: clone → ask the filesystem whether the clone SHARES its blocks (FIEMAP) →
// then mutate and check independence. Sharing is asked first because the mutation is a
// truncating rewrite, which breaks the sharing it would otherwise be measuring.
//
// Cleans up after itself.
func ReflinkProbeDetail(dir string) (ReflinkResult, string) {
	src := filepath.Join(dir, ".quince-reflink-src")
	dst := filepath.Join(dir, ".quince-reflink-dst")
	defer func() { _ = os.Remove(src); _ = os.Remove(dst) }()
	if err := os.WriteFile(src, probePattern(reflinkProbeSize), 0o600); err != nil {
		return ReflinkUnsupported, fmt.Sprintf("cannot write a probe file: %v", err)
	}
	if err := reflinkFile(dst, src); err != nil {
		return ReflinkUnsupported, fmt.Sprintf("FICLONE refused: %v", err)
	}

	// 1. SHARING — the property the backend is chosen for.
	sharing, why := extentSharing(dst)

	// 2. INDEPENDENCE — still needed, to rule out a hardlink. A true CoW clone leaves the
	// source intact when the clone is rewritten. Measured AFTER sharing, because this rewrite
	// truncates the clone and so destroys the sharing it would otherwise be measuring.
	if err := os.WriteFile(dst, []byte("BBBBBBBB"), 0o600); err != nil {
		return ReflinkUnsupported, fmt.Sprintf("cannot rewrite the probe clone: %v", err)
	}
	got, err := os.ReadFile(src)
	if err != nil {
		return ReflinkUnsupported, fmt.Sprintf("cannot re-read the probe source: %v", err)
	}
	return reflinkVerdict(sharing, why, len(got) == reflinkProbeSize)
}

// reflinkVerdict turns the probe's two measurements into its answer.
//
// PURE, AND THAT IS THE POINT rather than tidiness. The case this whole change exists for — a
// FICLONE that SUCCEEDS and shares nothing — cannot be produced on demand: it needs a filesystem
// that accepts the ioctl and lies, and no tier of the lab rig does (btrfs and XFS share, ext4 and
// exFAT refuse the ioctl outright, ZFS refuses it on a just-written source with EAGAIN). A probe
// whose verdict lives only inside the syscall path would therefore have its most important branch
// covered by nothing, in CI and on hardware alike. Split out, the branch is an ordinary table
// test — see TestReflinkVerdict.
func reflinkVerdict(sharing Sharing, why error, independent bool) (ReflinkResult, string) {
	if sharing == SharingUnshared {
		return ReflinkUnsupported, "FICLONE succeeded but the clone shares no extents with its source — it is a full copy, not a reflink"
	}
	if !independent {
		return ReflinkUnsupported, "rewriting the clone changed the source — these names share an inode, they are not independent files"
	}
	if sharing == SharingShared {
		return ReflinkSharing, "the clone's extents are reported shared (FIEMAP_EXTENT_SHARED) and the clone is independent of its source"
	}
	return ReflinkSharingUnverifiable, fmt.Sprintf("the clone is independent of its source, but this filesystem does not report extent sharing (%v)", why)
}

// probePattern builds n bytes that are neither a hole nor trivially compressible, so the probe
// file gets real extents on a sparse-aware or compressing filesystem. A deterministic xorshift
// rather than crypto/rand: the probe must behave the same on every run.
func probePattern(n int) []byte {
	b := make([]byte, n)
	x := uint32(0x9E3779B9)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

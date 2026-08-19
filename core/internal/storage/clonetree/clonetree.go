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
	"log/slog"
	"os"
	"path/filepath"
	"time"
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
	// `idevicebackup2` which writes into the backup tree unlinks first. Three of its four did so
	// already. The fourth — `mb2_copy_file_by_path`, reached from `DLMessageCopyItem` — did not,
	// and its destination is named by the DEVICE at runtime, so no list here could have covered it.
	// It was never observed firing, but the handler is NOT command-gated (the message loop is
	// guarded only by `cmd != CMD_LEAVE`), so it is reachable during a backup and absence over two
	// devices is not never. **It is now closed by construction rather than by observation**:
	// `deploy/patches/libimobiledevice/0004` adds the missing `remove_file(dst)`, so all four paths
	// unlink and this strategy no longer rests on anything unmeasurable.
	//
	// That patch is the load-bearing dependency. If it is ever dropped, or upstream rewrites that
	// function, this strategy is unsafe again — and the failure is a silently corrupted committed
	// version, found whenever someone next needs a restore.
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

// LogValue makes `"strategy", s` log the word rather than the ordinal. String() is NOT enough and
// the call site cannot show you that: slog's JSON handler marshals a KindAny value with
// encoding/json, which consults json.Marshaler and encoding.TextMarshaler and never fmt.Stringer —
// so a named int type logs as its ordinal, and Reflink being iota means the default backend logged
// `"strategy":0`, which reads as unset (quince#992).
//
// slog resolves a LogValuer before the handler sees it, so this is right for the text handler too,
// where a MarshalText would only fix the JSON one — and it changes nothing about how the type
// serialises anywhere else. Any other named int type that reaches a log key needs the same method;
// there are twelve more in core/internal today and none of them is logged.
func (s Strategy) LogValue() slog.Value { return slog.StringValue(s.String()) }

// ErrReflinkUnsupported is returned by the reflink path when the filesystem refuses FICLONE.
// The strategy is chosen by a probe before Clone runs, so hitting this mid-clone is a real,
// surfaced error (never a silent fallback — hard rule).
var ErrReflinkUnsupported = errors.New("clonetree: reflink (FICLONE) unsupported on this filesystem")

// ErrReflinkUnavailable is FICLONE declining THIS CLONE, RIGHT NOW, on a filesystem that supports
// reflinks. ZFS block cloning refuses a source whose data is not yet in a synced txg and reports
// EAGAIN — measured on the lab rig, ZFS 2.3.2 with `feature@block_cloning` enabled, where the same
// file clones fine once it has settled (quince#790).
//
// IT IS A SEPARATE SENTINEL BECAUSE THE OLD MESSAGE CONTRADICTED ITSELF: every ioctl failure became
// ErrReflinkUnsupported, so a transient refusal read `reflink (FICLONE) unsupported on this
// filesystem: resource temporarily unavailable` — a permanent claim and a transient errno in one
// sentence, about a filesystem that does support reflinks. Neither an operator nor quince can act
// on that.
//
// IT DOES NOT CHANGE WHICH BACKEND IS SELECTED. Whether the probe should settle its source before
// cloning — which would move ZFS-as-namespace-storage from hardlink to reflink — is the open ruling
// on quince#790 and is deliberately untouched here.
var ErrReflinkUnavailable = errors.New("clonetree: reflink (FICLONE) declined this clone right now — the filesystem supports it")

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

// ReflinkResult is what ReflinkProbeDetail found. The four values exist because the questions the
// probe asks have different answers on different filesystems, and quince#747 is the record of what
// collapsing them into a bool costs.
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
	// ReflinkUnavailable — FICLONE declined THIS clone on a filesystem that supports reflinks
	// (EAGAIN; see ErrReflinkUnavailable). Do not use the reflink strategy on this evidence — the
	// probe has not shown a clone working here — but do not report the filesystem as incapable
	// either, because it is not.
	//
	// SEPARATE FROM ReflinkUnsupported RATHER THAN FOLDED INTO IT, so the sentence an operator
	// reads matches what happened. It now means something narrower than it did: the probe RETRIES a
	// transient EAGAIN for ~6 s (quince#790, ruled 2026-08-19), so reaching this outcome means the
	// clone was still declined after waiting out a txg boundary — not that nobody waited.
	ReflinkUnavailable
)

// reflinkProbeSize is how large the probe's source file is, and it is load-bearing rather than
// arbitrary. btrfs stores a small file INLINE in its metadata, and an inline extent carries no
// FIEMAP_EXTENT_SHARED flag whether or not the file was cloned — measured on the lab rig, where
// the 8-byte file this probe used to write comes back `inline` on btrfs after a real
// `cp --reflink=always`. 1 MiB is far above any filesystem's inline/tail-packing threshold and
// costs one write, once, per storage at startup.
const reflinkProbeSize = 1 << 20

// THE RETRY BOUND, AND WHY IT IS FIVE AT ONE SECOND (quince#790, Operator ruling 2026-08-19).
//
// ZFS block cloning refuses a source not yet in a SYNCED transaction group, so the probe's
// write-then-clone loses a race the filesystem resolves on its own within one txg. Measured on the
// lab rig: five retries at one second, identically across three runs, against a default
// `zfs_txg_timeout` of 5 s.
//
// SO THIS WAITS FOR A BOUNDARY, IT DOES NOT FORCE ONE — which is the whole reason the number tracks
// that sysctl rather than being tuned by feel. Lower it without lowering `zfs_txg_timeout` and the
// probe starts reporting `ReflinkUnavailable` on a filesystem that can reflink, which selects
// hardlink and reintroduces exactly the aliasing risk this ruling removes.
//
// The worst case is ~6 s once per storage at startup, bounded, and only on a filesystem that
// answers EAGAIN at all — every other outcome returns on the first attempt.
const (
	reflinkProbeRetries  = 5
	reflinkProbeInterval = time.Second
)

// reflinkProbeSleep is `time.Sleep` in production and a hook in tests, so the retry loop can be
// driven without spending six real seconds per case. A variable rather than a parameter because the
// probe's signature is public API (`ReflinkProbeDetail`) and this is a test seam, not an option.
var reflinkProbeSleep = time.Sleep

// probeClone is the FICLONE the PROBE calls, seamed so the retry loop is testable without a ZFS
// pool. Deliberately separate from `reflinkFile`, which `Clone` uses for the real seed: the retry is
// the probe's alone. A seed's source is `latest/`, written by the previous backup and therefore many
// txgs settled, so the race this waits out should not exist there — and that is REASONING, not a
// measurement (quince#790 asks for the seed to be exercised once reflink is selectable on ZFS).
// Seaming only the probe keeps that question open rather than quietly answering it with a retry.
var probeClone = reflinkFile

// ReflinkProbe reports whether dir's filesystem supports the reflink strategy at all. It is the
// bool form of ReflinkProbeDetail and deliberately treats "sharing unverifiable" as supported —
// callers that care about the distinction (and the storage auto-selection probe does, because
// the distinction is the whole of quince#747) must use ReflinkProbeDetail.
// IT NAMES THE SUPPORTED RESULTS RATHER THAN EXCLUDING THE UNSUPPORTED ONE. `!= ReflinkUnsupported`
// was equivalent while there were three values and silently inverted the moment ReflinkUnavailable
// was added — a transient refusal would have read as reflink support, in the one helper whose whole
// job is to answer that question.
func ReflinkProbe(dir string) bool {
	res, _ := ReflinkProbeDetail(dir)
	return reflinkUsable(res)
}

// reflinkUsable is ReflinkProbe's verdict, split out so it is a table test rather than a claim
// about a filesystem CI does not have.
func reflinkUsable(res ReflinkResult) bool {
	return res == ReflinkSharing || res == ReflinkSharingUnverifiable
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
	// WAIT FOR A TXG BOUNDARY RATHER THAN GIVING UP — Operator ruling 2026-08-19, quince#790.
	//
	// The probe writes its source and clones it immediately, which is precisely the race ZFS block
	// cloning loses: a block not yet in a synced transaction group cannot be cloned, and FICLONE says
	// EAGAIN. Retrying is what turns that into the answer the filesystem would give a moment later.
	//
	// WHY THE RULING WENT THIS WAY, kept because the trade is not the obvious one. Hardlink's safety
	// is CONTINGENT — the seed links every regular file and rests on `idevicebackup2` unlinking
	// before it creates, with one upstream path (`DLMessageCopyItem`) that does not and was never
	// observed firing. Reflink cannot alias at all, by construction. And the downgrade that used to
	// make hardlink the slow-but-safe option is RETIRED (quince#518, gate 12c on hardware
	// 2026-08-10), so hardlink today is fast AND carries the residual risk. Six seconds once per
	// storage at startup buys not needing the argument.
	//
	// NOT `fsync`, AND NOT `sync(2)`. Both are measured wrong and the reason is worth keeping: fsync
	// commits the ZIL, block cloning needs a SYNCED TXG, and those are different events. `sync(2)`
	// initiates one without reliably completing it — 1 success in 4, then 1 in 3, which is what an
	// unreliable barrier looks like rather than a fix.
	//
	// FIVE AT ONE SECOND IS NOT A MAGIC NUMBER. It was measured identically across three runs
	// against a default `zfs_txg_timeout` of 5 s: this WAITS FOR a boundary, it does not force one.
	// Tuning it down without changing that sysctl would reintroduce the failure this removes.
	deadline := reflinkProbeRetries
	var lastErr error
	for attempt := 0; ; attempt++ {
		lastErr = probeClone(dst, src)
		if lastErr == nil {
			break
		}
		if !errors.Is(lastErr, ErrReflinkUnavailable) || attempt >= deadline {
			break
		}
		// Removed between attempts: FICLONE needs a destination it can create, and a failed clone
		// can leave one behind.
		_ = os.Remove(dst)
		reflinkProbeSleep(reflinkProbeInterval)
	}
	if lastErr != nil {
		if errors.Is(lastErr, ErrReflinkUnavailable) {
			// HONEST WHEN IT EXPIRES — quince#936 drew this line and the ruling says not to
			// re-cross it. A probe that gave up reports what it OBSERVED; it does not promote a
			// timeout into "unsupported", which would send an operator to the wrong question and
			// select hardlink on a filesystem that can reflink.
			return ReflinkUnavailable, fmt.Sprintf(
				"FICLONE still declined after %d retries at %s (waiting for a synced txg): %v",
				deadline, reflinkProbeInterval, lastErr)
		}
		return ReflinkUnsupported, fmt.Sprintf("FICLONE refused: %v", lastErr)
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

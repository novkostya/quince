package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// zfsOpTimeout bounds a single host ZFS operation (snapshot/create/list/destroy).
const zfsOpTimeout = 60 * time.Second

// zfsBackend implements the snapshot-native version model (design §5, stack D5): the writer
// mutates working/ in place, a version IS a @quince-* snapshot taken post-verify, and latest/
// is a materialized mirror rebuilt from the new snapshot's .zfs path and atomically swapped —
// the sync-facing consistent view (D5a). Seed/Discard are no-ops on the working copy.
type zfsBackend struct {
	baseCtx    context.Context
	cli        *zfsCLI
	backups    string
	mirrorCfg  string // auto | reflink | hardlink | copy
	appVersion string
	log        *slog.Logger

	mu         sync.Mutex
	lastMirror MirrorReport // surfaced mirror mode + honest space claim (stack D5 (bj)/(bk))
}

func (b *zfsBackend) setMirror(r MirrorReport) {
	b.mu.Lock()
	b.lastMirror = r
	b.mu.Unlock()
	b.log.Info("zfs latest/ mirror built", "mode", r.Mode, "claim", r.Claim)
}

// LastMirror returns the most recent mirror report (for /api/health surfacing).
func (b *zfsBackend) LastMirror() MirrorReport {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastMirror
}

func newZFSBackend(baseCtx context.Context, cli *zfsCLI, backups, mirrorCfg, appVersion string, log *slog.Logger) *zfsBackend {
	return &zfsBackend{baseCtx: baseCtx, cli: cli, backups: backups, mirrorCfg: mirrorCfg,
		appVersion: appVersion, log: log}
}

func (b *zfsBackend) Name() string { return BackendZFS }

func (b *zfsBackend) opCtx() (context.Context, context.CancelFunc) {
	base := b.baseCtx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, zfsOpTimeout)
}

func (b *zfsBackend) Provision(udid string) error {
	if !validUDID(udid) {
		return fmt.Errorf("storage: invalid udid %q", udid)
	}
	ctx, cancel := b.opCtx()
	defer cancel()
	if err := b.cli.CreateDataset(ctx, udid); err != nil {
		return err
	}
	// Visibility probe: the mount must appear inside the container (mount propagation). If it
	// does not, surface the rbind,rslave / `pct set` guidance (design §5) — never silent.
	dev := deviceDir(b.backups, udid)
	if _, err := os.Stat(dev); err != nil {
		b.log.Warn("storage: zfs child dataset not visible in container — check mount propagation "+
			"(recommended: an rbind,rslave lxc.mount.entry; else `pct set -mpN` + restart)",
			"udid", udid, "path", dev, "error", err)
	}
	for _, d := range []string{zfsWorking(b.backups, udid), zfsLatest(b.backups, udid)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// WorkDir returns working/ — zfs seeds by nature (the previous state is already in place), so
// this is design §5's no-op Seed.
func (b *zfsBackend) WorkDir(udid, _ string) (string, error) {
	if !validUDID(udid) {
		return "", fmt.Errorf("storage: invalid udid %q", udid)
	}
	working := zfsWorking(b.backups, udid)
	if err := os.MkdirAll(working, 0o755); err != nil {
		return "", err
	}
	return working, nil
}

func (b *zfsBackend) TreePath(udid, _ string) string { return zfsWorking(b.backups, udid) }

func (b *zfsBackend) Commit(req CommitReq) (Committed, error) {
	working := zfsWorking(b.backups, req.UDID)
	if isEmptyDir(working) {
		return Committed{}, fmt.Errorf("storage: working/ is empty — nothing to commit")
	}
	// Marker is written into working/ BEFORE the snapshot so the immutable version carries it.
	if err := WriteMarker(working, Marker{
		VersionID: req.VersionID, JobID: req.JobID, UDID: req.UDID, Backend: BackendZFS,
		CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind, Encrypted: req.Verify.Encrypted,
		StructureVerifiedAt: fmtRFC(req.CreatedAt), AppVersion: b.appVersion,
	}); err != nil {
		return Committed{}, err
	}
	snap := snapNameFor(req.VersionID, req.CreatedAt)
	dev := deviceDir(b.backups, req.UDID)
	j := Journal{
		VersionID: req.VersionID, UDID: req.UDID, Backend: BackendZFS, JobID: req.JobID,
		Phase: PhasePrepared, CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind,
		Encrypted: req.Verify.Encrypted, StructureVerifiedAt: fmtRFC(req.CreatedAt),
		LogicalBytes: req.Verify.LogicalBytes, ZFSSnapshot: b.cli.dataset(req.UDID) + "@" + snap,
	}
	if err := writeJournal(dev, j); err != nil {
		return Committed{}, err
	}
	if err := b.finishCommit(&j); err != nil {
		return Committed{}, err
	}
	return b.committedFromSnapshot(req.UDID, snap)
}

// finishCommit runs snapshot → latest-rebuild from the journal's phase, idempotently, clearing
// the journal at the end (shared by Commit and ResumeCommit — roll-forward).
func (b *zfsBackend) finishCommit(j *Journal) error {
	dev := deviceDir(b.backups, j.UDID)
	snap := snapName(j.ZFSSnapshot)

	if j.Phase == PhasePrepared {
		ctx, cancel := b.opCtx()
		err := b.cli.Snapshot(ctx, j.UDID, snap)
		cancel()
		if err != nil {
			return err
		}
		j.Phase = PhaseSnapshotCreated
		if err := writeJournal(dev, *j); err != nil {
			return err
		}
	}

	if j.Phase == PhaseSnapshotCreated {
		if err := b.rebuildLatest(j.UDID, snap); err != nil {
			return err
		}
		j.Phase = PhaseLatestRebuilt
		if err := writeJournal(dev, *j); err != nil {
			return err
		}
	}

	return removeJournal(dev)
}

// rebuildLatest materializes latest/ from the just-committed version and atomically swaps it in,
// via the stack D5 mirror ladder ((bi)/(bj)/(bk)). It ALWAYS clones from working/ — NEVER the
// .zfs snapshot mount: reflink-from-snapshot is EXDEV at every layer (cross-superblock, kernel
// behavior), and working/ holds the snapshot's exact content under the per-UDID job lock. The
// chosen mode + honest space claim are recorded (surfaced in logs + /api/health).
func (b *zfsBackend) rebuildLatest(udid, _ string) error {
	working := zfsWorking(b.backups, udid)
	if isEmptyDir(working) {
		return fmt.Errorf("storage: working/ empty at mirror build for %s", udid)
	}
	latest := zfsLatest(b.backups, udid)

	// (i) hook configured → the constrained `mirror` verb rebuilds latest/ HOST-side, where
	// FICLONE works even when the container's unprivileged userns forbids it (gate-12 finding
	// (bi)). It reflinks from working/ under the job lock + atomic-swaps, touching ONLY the
	// derived latest/ (never snapshots) — a bounded blast radius since latest/ is rebuildable.
	if b.cli.mode == "hook" {
		ctx, cancel := b.opCtx()
		defer cancel()
		shared, err := b.cli.Mirror(ctx, udid)
		if err != nil {
			return err
		}
		b.setMirror(MirrorReport{Mode: MirrorHookReflink, Claim: hookClaim(shared)})
		return nil
	}

	// (ii)-(iv) in-container / delegated-exec path: build into staging, then atomic-swap.
	staging := latest + ".new"
	old := latest + ".old"
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	report, err := b.buildMirrorInContainer(staging, working)
	if err != nil {
		return err
	}
	// Atomic swap: latest → latest.old, latest.new → latest, rm latest.old.
	_ = os.RemoveAll(old)
	if !isEmptyDir(latest) {
		if err := os.Rename(latest, old); err != nil {
			return err
		}
	} else {
		_ = os.RemoveAll(latest)
	}
	if err := os.Rename(staging, latest); err != nil {
		return err
	}
	b.setMirror(report)
	return os.RemoveAll(old)
}

// buildMirrorInContainer runs the reflink → hardlink-under-matrix → copy ladder from working/
// (stack D5 (bk)), self-selecting by risk dominance. reflink is attempted first (a non-sharing
// reflink is functionally a copy — same correctness/cost); EPERM/unsupported (the unprivileged-
// userns case, gate-12 finding (bi)) falls through to hardlink, then copy. There is no usable
// in-container measurement channel yet (the hook path measures host-side; the syscall statfs
// channel is a documented follow-up), so a successful reflink is reported UNVERIFIED per (bk) —
// never a silent zero-space claim — and the risky measured-not-sharing→hardlink downgrade is
// NOT taken absent a channel (reflink wins on the risk asymmetry). Every degraded mode is
// surfaced (log + report). Note: the hardlink tier's safety is validated by gate 12c (the
// destructive matrix; owned by this rung, pending on hardware).
func (b *zfsBackend) buildMirrorInContainer(staging, working string) (MirrorReport, error) {
	switch b.mirrorCfg {
	case "hardlink":
		return MirrorReport{MirrorHardlink, "hardlink (explicit) — matrix-gated (gate 12c)"},
			clonetree.Clone(staging, working, clonetree.Hardlink)
	case "copy":
		return MirrorReport{MirrorCopy, "copy (explicit) — full-backup-size write per commit"},
			clonetree.Clone(staging, working, clonetree.Copy)
	}
	// auto / reflink: reflink from working/.
	err := clonetree.Clone(staging, working, clonetree.Reflink)
	if err == nil {
		return MirrorReport{MirrorReflink, claimFor(sharingUnknown)}, nil
	}
	if !errors.Is(err, clonetree.ErrReflinkUnsupported) {
		return MirrorReport{}, err
	}
	_ = os.RemoveAll(staging)
	b.log.Warn("storage: in-container reflink unavailable (EPERM/unsupported — unprivileged userns) → hardlink-under-matrix")
	if err := clonetree.Clone(staging, working, clonetree.Hardlink); err == nil {
		return MirrorReport{MirrorHardlink, "hardlink-under-matrix (reflink unavailable) — gated on gate 12c"}, nil
	}
	_ = os.RemoveAll(staging)
	b.log.Warn("storage: hardlink unavailable → full copy of latest/ (full-backup-size write per commit; surfaced degraded mode)")
	return MirrorReport{MirrorCopy, "copy (reflink+hardlink unavailable) — full-backup-size write per commit"},
		clonetree.Clone(staging, working, clonetree.Copy)
}

// hookClaim renders the honest space claim for a host-side hook mirror given its measured verdict.
func hookClaim(shared sharingResult) string {
	switch shared {
	case sharingYes:
		return "zero-space verified (host-side reflink via hook; pool-level sharing confirmed)"
	case sharingNo:
		return "host-side reflink did not share — full-backup-size write per commit (surfaced)"
	default:
		return "host-side reflink via hook; sharing unverified — budget full-copy space cost"
	}
}

func (b *zfsBackend) ResumeCommit(j Journal) (Committed, bool, error) {
	if j.Phase == PhaseLatestRebuilt {
		_ = removeJournal(deviceDir(b.backups, j.UDID))
		c, err := b.committedFromSnapshot(j.UDID, snapName(j.ZFSSnapshot))
		return c, true, err
	}
	if err := b.finishCommit(&j); err != nil {
		return Committed{}, false, err
	}
	c, err := b.committedFromSnapshot(j.UDID, snapName(j.ZFSSnapshot))
	return c, true, err
}

// Discard leaves working/ dirty (design §5: no unwind; repair-working-copy is the escape hatch)
// and reports the last good version for the UI/log.
func (b *zfsBackend) Discard(udid, _ string) (string, error) {
	last := "none"
	if arts, err := b.Scan(udid); err == nil {
		for _, a := range arts {
			if a.IsLatest {
				last = a.Marker.CreatedAt
			}
		}
	}
	return fmt.Sprintf("working copy dirty, last good version = %s", last), nil
}

func (b *zfsBackend) DeleteArtifact(a Artifact) error {
	if a.ZFSSnapshot == nil {
		return fmt.Errorf("storage: zfs artifact has no snapshot")
	}
	ctx, cancel := b.opCtx()
	defer cancel()
	return b.cli.DestroySnapshot(ctx, a.UDID, snapName(*a.ZFSSnapshot))
}

func (b *zfsBackend) RepairWorkingCopy(udid string) error {
	arts, err := b.Scan(udid)
	if err != nil {
		return err
	}
	var last *Artifact
	for i := range arts {
		if arts[i].IsLatest {
			last = &arts[i]
		}
	}
	if last == nil || last.ZFSSnapshot == nil {
		return fmt.Errorf("storage: no last-good snapshot to rebuild the working copy from")
	}
	src := filepath.Join(deviceDir(b.backups, udid), ".zfs", "snapshot", snapName(*last.ZFSSnapshot), "working")
	working := zfsWorking(b.backups, udid)
	if err := os.RemoveAll(working); err != nil {
		return err
	}
	if err := clonetree.Clone(working, src, clonetree.Copy); err != nil {
		return fmt.Errorf("storage: rebuild working from snapshot: %w", err)
	}
	b.log.Info("storage: rebuilt working copy from last snapshot", "udid", udid, "snapshot", *last.ZFSSnapshot)
	return nil
}

func (b *zfsBackend) Scan(udid string) ([]Artifact, error) {
	ctx, cancel := b.opCtx()
	snaps, err := b.cli.ListSnapshots(ctx, udid)
	cancel()
	if err != nil {
		return nil, err
	}
	var out []Artifact
	var newest string
	for _, s := range snaps {
		working := filepath.Join(deviceDir(b.backups, udid), ".zfs", "snapshot", s, "working")
		m, err := ReadMarker(working)
		if errors.Is(err, ErrMarkerCorrupt) {
			b.log.Warn("storage: snapshot marker failed its checksum — not adopting", "udid", udid, "snapshot", s)
			continue
		}
		if err != nil {
			continue // a foreign or marker-less snapshot is not a quince version we adopt
		}
		full := b.cli.dataset(udid) + "@" + s
		snapCopy := full
		out = append(out, Artifact{UDID: udid, Backend: BackendZFS, ZFSSnapshot: &snapCopy,
			Marker: m, PhysicalBytes: dirSize(working)})
		if m.CreatedAt > newest {
			newest = m.CreatedAt
		}
	}
	for i := range out {
		if out[i].Marker.CreatedAt == newest {
			out[i].IsLatest = true
		}
	}
	return out, nil
}

func (b *zfsBackend) PendingJournals() ([]Journal, error) { return scanJournals(b.backups) }

// SweepWork is a no-op on zfs: working/ is the live in-place copy, never orphaned job dirs.
func (b *zfsBackend) SweepWork(string) error { return nil }

func (b *zfsBackend) committedFromSnapshot(udid, snap string) (Committed, error) {
	working := filepath.Join(deviceDir(b.backups, udid), ".zfs", "snapshot", snap, "working")
	m, err := ReadMarker(working)
	if err != nil {
		return Committed{}, fmt.Errorf("storage: read snapshot marker after commit: %w", err)
	}
	c := committedFromMarker(m, dirSize(working))
	full := b.cli.dataset(udid) + "@" + snap
	c.ZFSSnapshot = &full
	return c, nil
}

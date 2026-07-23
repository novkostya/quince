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

// zfsOpTimeout bounds a single host ZFS operation (snapshot/create/list/destroy/seed).
const zfsOpTimeout = 60 * time.Second

// zfsBackend implements the snapshot-native version model (design §5, stack D5; reworked in
// qn.5b). The writer fills a per-job working/<udid> seeded from latest/ at job start; commit
// verifies it, ATOMICALLY EXCHANGES it into latest/ (renameat2(RENAME_EXCHANGE) — no window), then
// takes the @quince-* snapshot, so the snapshot IS latest/ = the version and browse_root points at
// it. Between backups the dataset holds only latest/. There is no commit-time latest/ mirror any
// more; the reflink moved to seed time (host-side via the hook `seed` verb in the unprivileged
// profile, or the in-container reflink→copy ladder otherwise).
type zfsBackend struct {
	baseCtx    context.Context
	cli        *zfsCLI
	backups    string
	seedCfg    string // auto | reflink | copy (in-container seed strategy; hardlink is never used — amendment A)
	appVersion string
	log        *slog.Logger

	mu       sync.Mutex
	lastSeed SeedReport // surfaced seed mode + honest space claim (stack D5 (bj)/(bk))
}

func (b *zfsBackend) setSeed(r SeedReport) {
	b.mu.Lock()
	b.lastSeed = r
	b.mu.Unlock()
	b.log.Info("zfs working/ seeded from latest", "mode", r.Mode, "claim", r.Claim)
}

// LastSeed returns the most recent seed report (for /api/health surfacing).
func (b *zfsBackend) LastSeed() SeedReport {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastSeed
}

func newZFSBackend(baseCtx context.Context, cli *zfsCLI, backups, seedCfg, appVersion string, log *slog.Logger) *zfsBackend {
	return &zfsBackend{baseCtx: baseCtx, cli: cli, backups: backups, seedCfg: seedCfg,
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
	// Only latest/ is permanent (between backups the dataset holds only latest/); working/ is
	// created per job by WorkDir.
	return os.MkdirAll(latestDir(b.backups, udid), 0o755)
}

// WorkDir returns the idevicebackup2 TARGET (workingParent) after seeding working/<udid> from
// latest/ (design §5 Seed, qn.5b). A non-empty working/<udid> is RESUMED as-is (a prior failed
// attempt); otherwise it is seeded — host-side via the hook `seed` verb where in-container FICLONE
// is blocked, else the in-container reflink→copy ladder — or created empty on a first backup. The
// seed decision (seeded-from-latest ⇒ incremental) is recorded in the work sentinel.
func (b *zfsBackend) WorkDir(udid, _ string) (string, error) {
	if !validUDID(udid) {
		return "", fmt.Errorf("storage: invalid udid %q", udid)
	}
	parent := workingParent(b.backups, udid)
	tree := workingTree(b.backups, udid)
	if !isEmptyDir(tree) {
		b.log.Info("storage: resuming dirty working (zfs)", "udid", udid)
		return parent, nil // already seeded; kind recovered from the sentinel at commit
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	latest := latestDir(b.backups, udid)
	seeded := false
	if !isEmptyDir(latest) {
		if err := b.seedWorking(udid, tree, latest); err != nil {
			return "", err
		}
		seeded = true
	} else if err := os.MkdirAll(tree, 0o755); err != nil {
		return "", err
	}
	if err := writeWorkState(b.backups, udid, workState{SeededFromLatest: seeded}); err != nil {
		return "", err
	}
	return parent, nil
}

// seedWorking clones latest/ → working/<udid>. Hook mode delegates to the host-side `seed` verb
// (where FICLONE works despite the unprivileged userns, gate-12 (bi)); otherwise the in-container
// reflink→copy ladder runs (never hardlink — amendment A). The chosen mode + honest space claim
// are surfaced.
func (b *zfsBackend) seedWorking(udid, tree, latest string) error {
	if b.cli.mode == "hook" {
		ctx, cancel := b.opCtx()
		defer cancel()
		shared, err := b.cli.Seed(ctx, udid)
		if err != nil {
			return fmt.Errorf("storage: hook seed working from latest: %w", err)
		}
		b.setSeed(SeedReport{Mode: SeedHookReflink, Claim: hookClaim(shared)})
		return nil
	}
	report, err := b.seedInContainer(tree, latest)
	if err != nil {
		return fmt.Errorf("storage: seed working from latest: %w", err)
	}
	b.setSeed(report)
	return nil
}

// seedInContainer runs the reflink → copy ladder from latest/ (qn.5b amendment A: the hardlink
// tier is disabled-to-copy for the seed until gate 12c). reflink is attempted first; EPERM/
// unsupported (the unprivileged-userns case, gate-12 (bi)) falls through to a full copy, surfaced.
// There is no usable in-container sharing-measurement channel yet, so a successful reflink is
// reported UNVERIFIED ((bk)) — never a silent zero-space claim.
func (b *zfsBackend) seedInContainer(tree, latest string) (SeedReport, error) {
	if b.seedCfg == "copy" {
		return SeedReport{SeedCopy, "copy (explicit) — full-backup-size seed"},
			clonetree.Clone(tree, latest, clonetree.Copy)
	}
	// auto / reflink: reflink from latest/.
	err := clonetree.Clone(tree, latest, clonetree.Reflink)
	if err == nil {
		return SeedReport{SeedReflink, claimFor(sharingUnknown)}, nil
	}
	if !errors.Is(err, clonetree.ErrReflinkUnsupported) {
		return SeedReport{}, err
	}
	_ = os.RemoveAll(tree)
	b.log.Warn("storage: in-container reflink unavailable (EPERM/unsupported — unprivileged userns) → full copy seed (surfaced degraded mode)")
	return SeedReport{SeedCopy, "copy (reflink unavailable) — full-backup-size seed"},
		clonetree.Clone(tree, latest, clonetree.Copy)
}

func (b *zfsBackend) TreePath(udid, _ string) string { return workingTree(b.backups, udid) }

func (b *zfsBackend) Commit(req CommitReq) (Committed, error) {
	tree := workingTree(b.backups, req.UDID)
	if isEmptyDir(tree) {
		return Committed{}, fmt.Errorf("storage: working tree is empty — nothing to commit")
	}
	// Marker is written into working/<udid> BEFORE the exchange so it rides into latest/ and the
	// immutable snapshot carries it.
	if err := WriteMarker(tree, Marker{
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
		LogicalBytes: req.Verify.LogicalBytes, JobDir: tree,
		ZFSSnapshot: b.cli.dataset(req.UDID) + "@" + snap,
	}
	if err := writeJournal(dev, j); err != nil {
		return Committed{}, err
	}
	if err := b.finishCommit(&j); err != nil {
		return Committed{}, err
	}
	return b.committedFromSnapshot(req.UDID, snap)
}

// finishCommit runs exchange → snapshot from the journal's phase, idempotently (roll-forward,
// shared by Commit and ResumeCommit). The exchange is NOT idempotent (re-running it reverses the
// swap), so it is guarded by the version-id marker on latest/: a resume that finds latest/ already
// carrying this version's id skips straight to the snapshot.
func (b *zfsBackend) finishCommit(j *Journal) error {
	dev := deviceDir(b.backups, j.UDID)
	snap := snapName(j.ZFSSnapshot)
	latest := latestDir(b.backups, j.UDID)
	tree := j.JobDir

	if j.Phase == PhasePrepared {
		if !latestHasVersion(latest, j.VersionID) {
			if err := os.MkdirAll(latest, 0o755); err != nil { // exchange needs both dirs to exist
				return err
			}
			if err := exchange(tree, latest); err != nil {
				return fmt.Errorf("storage: exchange working into latest: %w", err)
			}
		}
		j.Phase = PhaseExchanged
		if err := writeJournal(dev, *j); err != nil {
			return err
		}
	}

	if j.Phase == PhaseExchanged {
		// working/ now holds the OLD latest content (already in the previous snapshot) — drop it
		// so the dataset (and this snapshot) hold only latest/.
		_ = os.RemoveAll(workingParent(b.backups, j.UDID))
		removeWorkState(b.backups, j.UDID)
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

	return removeJournal(dev)
}

func (b *zfsBackend) ResumeCommit(j Journal) (Committed, bool, error) {
	if j.Phase == PhaseSnapshotCreated {
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

// Discard keeps working/ dirty (design §5 / qn.5b: no unwind — a retry resumes into it; Reset is
// the explicit discard) and reports the last good version for the UI/log.
func (b *zfsBackend) Discard(udid, _ string) (string, error) {
	last := "none"
	if arts, err := b.Scan(udid); err == nil {
		for _, a := range arts {
			if a.IsLatest {
				last = a.Marker.CreatedAt
			}
		}
	}
	return fmt.Sprintf("working copy kept dirty for retry; last good version = %s", last), nil
}

func (b *zfsBackend) DeleteArtifact(a Artifact) error {
	if a.ZFSSnapshot == nil {
		return fmt.Errorf("storage: zfs artifact has no snapshot")
	}
	ctx, cancel := b.opCtx()
	defer cancel()
	return b.cli.DestroySnapshot(ctx, a.UDID, snapName(*a.ZFSSnapshot))
}

// RepairWorkingCopy is the qn.5b Reset op: DISCARD the dirty working area so the next backup
// re-seeds cleanly from latest/ (the previous "rebuild working from the last snapshot" is
// superfluous now — seeding does that at job start). Losing only the partial, never a version.
func (b *zfsBackend) RepairWorkingCopy(udid string) error {
	if err := os.RemoveAll(workingParent(b.backups, udid)); err != nil {
		return err
	}
	removeWorkState(b.backups, udid)
	b.log.Info("storage: reset — discarded dirty working copy (zfs)", "udid", udid)
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
		// qn.5b: the version content lives at latest/ inside the snapshot. Pre-qn.5b snapshots
		// held it at working/ and are treated as disposable lab data (decisions (co), decision 4):
		// ReadMarker(.../latest) simply fails to find a marker for them and they are skipped
		// gracefully — never adopted at a stale path, never a crash.
		snapLatest := filepath.Join(deviceDir(b.backups, udid), ".zfs", "snapshot", s, "latest")
		m, err := ReadMarker(snapLatest)
		if errors.Is(err, ErrMarkerCorrupt) {
			b.log.Warn("storage: snapshot marker failed its checksum — not adopting", "udid", udid, "snapshot", s)
			continue
		}
		if err != nil {
			continue // a foreign, marker-less, or pre-qn.5b snapshot is not a version we adopt here
		}
		full := b.cli.dataset(udid) + "@" + s
		snapCopy := full
		out = append(out, Artifact{UDID: udid, Backend: BackendZFS, ZFSSnapshot: &snapCopy,
			Marker: m, PhysicalBytes: dirSize(snapLatest)})
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

// SweepWork is a no-op on zfs: a dirty working/ is first-class resumable state (a retry resumes
// into it), not an orphan — Reset is the only discard (qn.5b).
func (b *zfsBackend) SweepWork(string) error { return nil }

func (b *zfsBackend) committedFromSnapshot(udid, snap string) (Committed, error) {
	snapLatest := filepath.Join(deviceDir(b.backups, udid), ".zfs", "snapshot", snap, "latest")
	m, err := ReadMarker(snapLatest)
	if err != nil {
		return Committed{}, fmt.Errorf("storage: read snapshot marker after commit: %w", err)
	}
	c := committedFromMarker(m, dirSize(snapLatest))
	full := b.cli.dataset(udid) + "@" + snap
	c.ZFSSnapshot = &full
	return c, nil
}

// latestHasVersion reports whether latest/ already carries the given version's marker — the
// idempotency guard for the non-idempotent exchange (a re-run must not swap twice).
func latestHasVersion(latest, versionID string) bool {
	m, err := ReadMarker(latest)
	return err == nil && m.VersionID == versionID
}

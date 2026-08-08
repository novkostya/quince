package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// zfsOpTimeout bounds a single host ZFS METADATA operation (snapshot/create/list/destroy). These
// are effectively O(1) — a snapshot is a metadata write, not a data walk — so a tight bound is right.
const zfsOpTimeout = 60 * time.Second

// zfsBackend implements the snapshot-native version model (design §5, stack D5; reworked in qn.5b,
// then again in qn.6h — Operator ruling 2026-08-08).
//
// THE TREE IS THE DATASET ROOT. idevicebackup2's target is the PARENT dataset and it writes into
// <target>/<UDID> by its own convention, which is already the child dataset's mountpoint — so the
// backup lands in the dataset root with no seed, no working copy, no symlink and no exchange.
// Commit is verify → write the marker → `zfs snapshot`, and the snapshot IS the version: browse
// reads .zfs/snapshot/<snap>/ with no trailing component. Between backups the dataset holds only
// the backup tree and its marker; quince's per-job bookkeeping (the work sentinel, the commit
// journal) lives in the PARENT, where a snapshot cannot capture it.
//
// What that buys is the whole point of the rung: a backup starts transferring as soon as the user
// has entered their passcode instead of after a clone of the previous one, and the operator's
// host-side helper stops containing quince's lifecycle.
type zfsBackend struct {
	baseCtx context.Context
	cli     *zfsCLI
	backups string
	// seedCfg is auto | reflink | copy. NOTHING ON THIS BACKEND READS IT ANY MORE — there is no
	// seed to strategise — and it is kept rather than deleted because it stays valid and meaningful
	// for the namespace backends, so what a zfs storage should REPORT for it (ignored, omitted, or
	// an explicit not-applicable) is a contracts §6 shape question. qn.6h open question 2; deciding
	// it silently is how a config key ends up claiming to do something.
	seedCfg    string
	appVersion string
	log        *slog.Logger
}

func newZFSBackend(baseCtx context.Context, cli *zfsCLI, backups, seedCfg, appVersion string, log *slog.Logger) *zfsBackend {
	return &zfsBackend{baseCtx: baseCtx, cli: cli, backups: backups, seedCfg: seedCfg,
		appVersion: appVersion, log: log}
}

func (b *zfsBackend) Name() string { return BackendZFS }

func (b *zfsBackend) opCtx() (context.Context, context.CancelFunc) {
	return b.ctxWithin(zfsOpTimeout)
}

func (b *zfsBackend) ctxWithin(d time.Duration) (context.Context, context.CancelFunc) {
	base := b.baseCtx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, d)
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
	//
	// IT MATTERS MORE SINCE qn.6h, because the dataset root is now the tool's write destination: an
	// invisible mount means idevicebackup2 creates a plain <backups>/<udid> directory in the
	// container's own filesystem and transfers the whole backup into it.
	dev := deviceDir(b.backups, udid)
	if _, err := os.Stat(dev); err != nil {
		b.log.Warn("storage: zfs child dataset not visible in container — check mount propagation "+
			"(recommended: an rbind,rslave lxc.mount.entry; else `pct set -mpN` + restart); "+
			"until it is, a backup would be written into the container's own filesystem, not the dataset",
			"udid", udid, "path", dev, "error", err)
		return nil
	}
	// NOTHING IS CREATED HERE ANY MORE. `zfs create` made the dataset and its mountpoint IS the
	// tree; there is no latest/ to mkdir and working/ no longer exists (qn.6h D1).
	b.assertSnapdir(udid, dev)
	return nil
}

// assertSnapdir is D8 condition 2's assertion, done at the visibility probe because that is where
// this backend already looks at the mount.
//
// At the `hidden` default readdir never returns .zfs and every walker is safe BY LUCK; at
// snapdir=visible — which operators set in order to browse snapshots by hand — .zfs is a child of
// the backup tree and a naive walk descends into EVERY snapshot. quince skips it explicitly
// (skipSnapdir, isEmptyDir), so this is not a warning about a broken state: it records that the
// condition the skips exist for is live on this dataset, so a reader of the logs can tell the
// "safe by luck" case from the "safe because we skip" one.
//
// IT MUST BE A readdir, NOT A stat, AND THAT IS THE WHOLE CORRECTNESS OF THIS FUNCTION. `.zfs` is
// reachable by explicit path on EVERY zfs dataset — `snapdir=hidden` removes it from directory
// LISTINGS and nothing else — so `os.Stat` succeeds regardless of the setting and cannot tell the
// two cases apart. This shipped as a stat in quince#745 and printed "snapdir is visible" against a
// `hidden` dataset on the staging stand within the hour. Measured there 2026-08-08:
//
//	ls -d <dev>/.zfs            → <dev>/.zfs      (stat succeeds)
//	ls -a <dev> | grep -c .zfs  → 0               (not listed: hidden)
//
// A listing is also what "visible" MEANS to ZFS and what the hazard depends on: a walker finds `.zfs`
// by reading the directory, so a `.zfs` that no readdir returns cannot be walked into.
//
// Reading the ZFS property itself would need a `get` verb the constrained helper does not have, and
// adding one would cost an operator hand-edit to learn something the filesystem already answers.
func (b *zfsBackend) assertSnapdir(udid, dev string) {
	entries, err := os.ReadDir(dev)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != snapdirName {
			continue
		}
		b.log.Info("storage: zfs snapdir is visible on this dataset — quince's tree walkers skip "+
			"the snapshot directory explicitly (qn.6h condition 2), so verify, logical_bytes and the "+
			"first-backup check do not descend into snapshots",
			"udid", udid, "snapdir", filepath.Join(dev, snapdirName))
		return
	}
}

// WorkDir returns the idevicebackup2 TARGET, which on zfs is the PARENT dataset: the tool appends
// <UDID> itself and that lands the tree in the child dataset root, where quince wants it. There is
// no clone, so WorkDir and PrepareWork are the same call.
func (b *zfsBackend) WorkDir(udid, jobID string) (string, error) {
	target, _, err := b.PrepareWork(udid, jobID)
	return target, err
}

// PrepareWork records what the job needs to know and hands back the target. It never seeds, so
// seedPending is ALWAYS false and the engine takes the plain `supervise` branch with an empty
// gatePath — which is why --gate is not passed and Info.plist needs no capture/restore on this
// backend (qn.6h D5).
//
// It is safe on a dirty head: a failed job's tree is first-class resumable state and the retry
// re-transfers nothing. There is no killed-seed case to discriminate here, because there is no seed.
func (b *zfsBackend) PrepareWork(udid, _ string) (string, bool, error) {
	if !validUDID(udid) {
		return "", false, fmt.Errorf("storage: invalid udid %q", udid)
	}
	tree := deviceDir(b.backups, udid)
	if err := b.discardPreChangeLayout(udid, tree); err != nil {
		return "", false, err
	}
	// THE AUTHORITATIVE full-vs-incremental SIGNAL (finding #9(a)): is the dataset root non-empty at
	// job start. Same question the seeding backends ask of latest/, asked at the path that now holds
	// the content — and never Status.plist.IsFullBackup, which the lab proved lies (a first 33 GB
	// backup writes IsFullBackup:false). isEmptyDir ignores .zfs, which is what stops a device with
	// ZERO backups reading as non-empty at snapdir=visible and recording its first backup as
	// `incremental`.
	seeded := !isEmptyDir(tree)
	// SeedInProgress stays false: see workState.SeedInProgress on why it must not be repurposed.
	if err := writeWorkStateAt(zfsWorkSentinel(b.backups, udid), workState{SeededFromLatest: seeded}); err != nil {
		return "", false, err
	}
	if seeded {
		b.log.Info("storage: backing up into the existing dataset head — incremental, nothing cloned", "udid", udid)
	}
	return b.backups, false, nil
}

// SeedWork cannot be reached on this backend: PrepareWork never reports a pending seed, and the
// engine calls this only when it does. Kept as a no-op rather than an error because the Backend
// contract is WorkDir == PrepareWork + SeedWork, and a backend that has nothing to do in the second
// half should say so by doing nothing.
func (b *zfsBackend) SeedWork(string, string) error { return nil }

// discardPreChangeLayout removes a pre-qn.6h layout from the dataset root, LOUDLY (story 12).
//
// Before this rung the child dataset was a CONTAINER holding latest/, working/ and the two
// per-device sidecars. Afterwards the dataset root IS the backup tree, so those are no longer
// siblings of the tree — they are inside it. Left alone they ride into every future snapshot and
// show up to any external reader of what is otherwise an iTunes-layout backup.
//
// DISCARDED RATHER THAN MIGRATED, by Operator note 2026-08-08: sole user, no v0.1 tag, migration off
// the table. The cost is that the next backup is a full transfer, which is why the size goes in the
// log line rather than being left for someone to infer from a long night.
func (b *zfsBackend) discardPreChangeLayout(udid, tree string) error {
	for _, name := range []string{"latest", "working", workSentinelName, journalName} {
		p := filepath.Join(tree, name)
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		bytes := fi.Size()
		if fi.IsDir() {
			bytes = dirSize(p)
		}
		b.log.Warn("storage: discarding a pre-qn.6h entry from the zfs dataset root — the backup tree "+
			"IS this dataset now, so leaving it would ride it into every future snapshot; the next "+
			"backup for this device is a full transfer",
			"udid", udid, "entry", name, "bytes", bytes)
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

func (b *zfsBackend) TreePath(udid, _ string) string { return deviceDir(b.backups, udid) }

func (b *zfsBackend) Commit(req CommitReq) (Committed, error) {
	tree := deviceDir(b.backups, req.UDID)
	if isEmptyDir(tree) {
		return Committed{}, fmt.Errorf("storage: dataset head is empty — nothing to commit")
	}
	// The marker goes into the dataset root BEFORE the snapshot, so the immutable snapshot carries
	// it. Writing it is what makes the tree a candidate version; the snapshot is what makes it one.
	if err := WriteMarker(tree, Marker{
		VersionID: req.VersionID, JobID: req.JobID, UDID: req.UDID, Backend: BackendZFS,
		CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind, Encrypted: req.Verify.Encrypted,
		StructureVerifiedAt: fmtRFC(req.CreatedAt), AppVersion: b.appVersion,
	}); err != nil {
		return Committed{}, err
	}
	snap := snapNameFor(req.VersionID, req.CreatedAt)
	j := Journal{
		VersionID: req.VersionID, UDID: req.UDID, Backend: BackendZFS, JobID: req.JobID,
		Phase: PhasePrepared, CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind,
		Encrypted: req.Verify.Encrypted, StructureVerifiedAt: fmtRFC(req.CreatedAt),
		LogicalBytes: req.Verify.LogicalBytes, JobDir: tree,
		ZFSSnapshot: b.cli.dataset(req.UDID) + "@" + snap,
	}
	if err := writeJournal(zfsJournal(b.backups, req.UDID), j); err != nil {
		return Committed{}, err
	}
	if err := b.finishCommit(&j); err != nil {
		return Committed{}, err
	}
	return b.committedFromSnapshot(req.UDID, snap)
}

// finishCommit does ONE thing where it used to do three: take the snapshot. In place there is
// nothing to exchange and nothing to clean up before snapshotting — working/ does not exist and the
// sentinel lives in the parent, outside the snapshot's path entirely — so canon's "between backups
// the dataset holds only the backup" is satisfied by construction rather than by ordering.
//
// ROLL-FORWARD NEEDS NO NEW CODE: zfscli.Snapshot already tolerates `already exists`, so a resume
// that crashed after the snapshot and before the journal write repeats it harmlessly.
//
// THE latestHasVersion GUARD IS GONE ON PURPOSE, and it is not a missing safety. It existed because
// the EXCHANGE is not idempotent — re-running one reverses the swap — so a resume had to be stopped
// from swapping twice. Nothing reverses now: the marker write is idempotent by content. Restoring
// the guard would be guarding an operation that no longer happens.
func (b *zfsBackend) finishCommit(j *Journal) error {
	jp := zfsJournal(b.backups, j.UDID)

	if j.Phase == PhasePrepared {
		ctx, cancel := b.opCtx()
		err := b.cli.Snapshot(ctx, j.UDID, snapName(j.ZFSSnapshot))
		cancel()
		if err != nil {
			return err
		}
		j.Phase = PhaseSnapshotCreated
		if err := writeJournal(jp, *j); err != nil {
			return err
		}
	}

	// Cleared AFTER the snapshot: the head is a committed version from here on, so a crash in this
	// window leaves Dirty()==true against a head that equals the newest snapshot — where a reset
	// would roll back to the snapshot just taken and change nothing.
	removeWorkStateAt(zfsWorkSentinel(b.backups, j.UDID))
	return removeJournal(jp)
}

func (b *zfsBackend) ResumeCommit(j Journal) (Committed, bool, error) {
	if j.Phase == PhaseSnapshotCreated {
		removeWorkStateAt(zfsWorkSentinel(b.backups, j.UDID))
		_ = removeJournal(zfsJournal(b.backups, j.UDID))
		c, err := b.committedFromSnapshot(j.UDID, snapName(j.ZFSSnapshot))
		return c, true, err
	}
	if err := b.finishCommit(&j); err != nil {
		return Committed{}, false, err
	}
	c, err := b.committedFromSnapshot(j.UDID, snapName(j.ZFSSnapshot))
	return c, true, err
}

// Discard keeps the dirty head (design §5 / qn.5b: no unwind — a retry resumes into it; Reset is
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
	return fmt.Sprintf("dataset head kept dirty for retry; last good version = %s", last), nil
}

func (b *zfsBackend) DeleteArtifact(a Artifact) error {
	if a.ZFSSnapshot == nil {
		return fmt.Errorf("storage: zfs artifact has no snapshot")
	}
	ctx, cancel := b.opCtx()
	defer cancel()
	return b.cli.DestroySnapshot(ctx, a.UDID, snapName(*a.ZFSSnapshot))
}

// zfsNewerSnapshotMarker is the substring `zfs rollback` returns when a snapshot newer than the
// target exists — qn.6h answer C, MEASURED on real ZFS 2026-08-08:
//
//	cannot rollback to '<ds>@quince-…': more recent snapshots or bookmarks exist
//	use '-r' to force deletion of the following snapshots and bookmarks: …
//
// Matched to pick the REMEDY, never to decide whether the operation failed — that is the exit code's
// job. A message that names the wrong remedy is a state-honesty failure, and B's remedy (stop the
// container) does nothing at all under C.
const zfsNewerSnapshotMarker = "more recent snapshots"

// RepairWorkingCopy is Reset on zfs, and since the 2026-08-08 ruling it is a `zfs rollback` to the
// newest @quince-* snapshot: the head returns to the last committed version, and the dirty
// in-place tree is what is lost. Abandon-only — this is rollback's ONLY caller, it is never reached
// after verify, and it is never a failure default. A failed JOB keeps its head and resumes.
//
// THREE OUTCOMES, all designed for, and only two were ever observed:
//
//	A  it succeeds. The measured case: a mounted dataset with held read fds, a child's fd, a cwd
//	   inside the tree, an active writer and a held WRITE fd were none of them an obstacle.
//	B  it fails or times out (a busy mount). NEVER OBSERVED, kept because one host is not a proof.
//	C  a snapshot NEWER than the target exists, so ZFS refuses. Measured, and the likely field case
//	   unless the host snapshotter has been told to leave quince's datasets alone.
//
// Answer A is safe here because of quince's own guard, NOT because of ZFS: a rollback removes files
// under an active writer with no error to either side, and engine.go refuses reset with 409 while a
// backup runs. If that guard regressed the symptom would be a rolled-back tree instantly re-dirtied
// with nothing logged anywhere.
func (b *zfsBackend) RepairWorkingCopy(udid string) error {
	ctx, cancel := b.opCtx()
	snaps, err := b.cli.ListSnapshots(ctx, udid)
	cancel()
	if err != nil {
		return fmt.Errorf("storage: reset could not list this device's snapshots, so it cannot know "+
			"what to roll back to: %w", err)
	}
	if len(snaps) == 0 {
		return b.emptyHead(udid)
	}

	// Newest by lexicographic order, which is not a coincidence: the name is
	// quince-<YYYY-MM-DDTHH-MM>-<ULID>, date-first precisely so `zfs list` and a plain sort agree,
	// and the ULID tail sorts by time within a minute. A wrong pick here is FAIL-SAFE rather than
	// destructive — a plain `zfs rollback` refuses anything but the most recent snapshot, so the
	// worst case is answer C's refusal, not a destroyed version.
	newest := snaps[0]
	for _, s := range snaps[1:] {
		if s > newest {
			newest = s
		}
	}

	ctx, cancel = b.opCtx()
	err = b.cli.Rollback(ctx, udid, newest)
	cancel()
	if err != nil {
		return b.rollbackRefusal(udid, newest, err)
	}

	// The rollback removed them: neither was in the snapshot. Clearing the sentinel again is
	// belt-and-braces for the case where it lives on a filesystem the rollback did not touch.
	removeWorkStateAt(zfsWorkSentinel(b.backups, udid))
	b.log.Info("storage: reset — rolled the dataset back to the newest committed version (zfs)",
		"udid", udid, "snapshot", newest)
	return nil
}

// rollbackRefusal turns a failed rollback into a message an operator can act on, with zfs's own
// words quoted verbatim rather than paraphrased — the operator's next action depends on which
// failure it was, and a paraphrase is where that distinction gets lost.
//
// NOTHING IS HALF-UNDONE and nothing is retried: the head is still dirty, the sentinel is still
// there, the job's resume state survives, and a retry would be the identical call against the
// identical mount. If the cause is transient the operator repeats the action; quince does not decide
// that on their behalf.
func (b *zfsBackend) rollbackRefusal(udid, snap string, err error) error {
	b.log.Warn("storage: reset — zfs refused the rollback; the dataset head is unchanged and still resumable",
		"udid", udid, "snapshot", snap, "error", err)
	if strings.Contains(err.Error(), zfsNewerSnapshotMarker) {
		// ANSWER C. Stopping the container does nothing here, and saying so would be worse than
		// saying nothing. quince cannot force past it and should not: -r is the only escape and it
		// destroys committed versions, which is why the helper discards flags.
		return fmt.Errorf("a snapshot newer than the version quince would restore exists, so ZFS "+
			"refuses to roll back — and quince will not force past it, because the only flag that "+
			"would (`-r`) destroys committed versions. Destroy the intervening snapshots on the host, "+
			"or do nothing: the dataset head is still dirty and still RESUMABLE, so a retry of the "+
			"backup resumes from it and re-transfers nothing. To stop this recurring, exclude quince's "+
			"datasets from the host snapshotter (`zfs set com.sun:auto-snapshot=false <parent-dataset>` "+
			"for zfs-auto-snapshot; sanoid/zrepl/pve-zsync each need their own exclusion). zfs said: %w", err)
	}
	// ANSWER B. There is no in-product remedy, and saying so is the whole of quince's job here.
	return fmt.Errorf("the rollback did not complete. Stop or restart the container so the dataset is "+
		"not mounted, then reset again; quince does not retry this automatically. zfs said: %w", err)
}

// emptyHead is reset on a device with NO committed version: there is no snapshot to roll back to,
// so the abandon is to clear the head. Reached only after ListSnapshots said so — story 6's "having
// asserted, not having assumed".
//
// It removes the dataset root's ENTRIES rather than the root: the root is the mountpoint, and
// removing it would unmount quince's own storage. .zfs is skipped for the same reason every walker
// skips it — at snapdir=visible it is a child of the tree and it is not quince's to delete.
func (b *zfsBackend) emptyHead(udid string) error {
	root := deviceDir(b.backups, udid)
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("storage: reset could not read the dataset head: %w", err)
	}
	for _, e := range entries {
		if e.Name() == snapdirName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	removeWorkStateAt(zfsWorkSentinel(b.backups, udid))
	b.log.Info("storage: reset — emptied the dataset head; this device has no committed version to "+
		"roll back to", "udid", udid)
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
		// qn.6h: the version content is the SNAPSHOT ROOT. Older snapshots hold it one level down
		// (pre-qn.6h at <snap>/latest/, pre-qn.5b at <snap>/working/) and are NOT browsable — ruled
		// 2026-08-08 with no dual-read fallback. A marker read here simply finds nothing for them.
		snapRoot := zfsSnapRoot(b.backups, udid, s)
		m, err := ReadMarker(snapRoot)
		if errors.Is(err, ErrMarkerCorrupt) {
			b.log.Warn("storage: snapshot marker failed its checksum — not adopting", "udid", udid, "snapshot", s)
			continue
		}
		if err != nil {
			// SKIPPING IS RULED; SKIPPING QUIETLY IS NOT (story 14). An unbrowsable version that says
			// nothing is indistinguishable from one that was never taken, which is precisely what "no
			// silent caps or fallbacks" forbids. ListSnapshots already filtered to quince-*, so this
			// is one of quince's own snapshots from before the layout moved — not a foreign one.
			b.log.Warn("storage: skipping a quince snapshot with no marker at its root — it predates "+
				"qn.6h (content at <snap>/latest/ or <snap>/working/) and is NOT browsable; the "+
				"snapshot itself is untouched",
				"udid", udid, "snapshot", s, "looked_in", snapRoot)
			continue
		}
		full := b.cli.dataset(udid) + "@" + s
		snapCopy := full
		out = append(out, Artifact{UDID: udid, Backend: BackendZFS, ZFSSnapshot: &snapCopy,
			Marker: m, PhysicalBytes: dirSize(snapRoot)})
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

// PendingJournals reads the PARENT-level journals (qn.6h): the device dirs are backup trees now, so
// there is nothing of quince's to descend into.
func (b *zfsBackend) PendingJournals() ([]Journal, error) { return scanFlatJournals(b.backups) }

// SweepWork is a no-op on zfs: a dirty head is first-class resumable state (a retry resumes into
// it), not an orphan — Reset is the only discard (qn.5b). After qn.6h there is no working/ to sweep
// at all. quince#731 makes reconciliation scheduled and nothing sequences the two: if such a sweep
// were ever generalised to "job scaffolding" it would reach the sentinel in the PARENT dataset and
// clear a live job's dirty marker, making a resumable head look clean — whichever lands second owes
// the other a check (qn.6h open question 5).
func (b *zfsBackend) SweepWork(string) error { return nil }

func (b *zfsBackend) committedFromSnapshot(udid, snap string) (Committed, error) {
	snapRoot := zfsSnapRoot(b.backups, udid, snap)
	m, err := ReadMarker(snapRoot)
	if err != nil {
		return Committed{}, fmt.Errorf("storage: read snapshot marker after commit: %w", err)
	}
	c := committedFromMarker(m, dirSize(snapRoot))
	full := b.cli.dataset(udid) + "@" + snap
	c.ZFSSnapshot = &full
	return c, nil
}

// Capacity satisfies the optional capacityReporter (quince#585, Operator ruling 2026-08-03).
//
// `statfs` on a zfs storage root reports the PARENT dataset's own used and excludes the per-device
// children that hold every backup — measured on the staging stand as 256 K against seventeen
// backups. zfs `used` on a parent includes descendants, so this measures what gap A already ruled
// the field to mean. No contract text changed: the meaning was right, the instrument was wrong.
//
// Bounded by opCtx like every other hook call, so a dead SSH target cannot hang a render.
func (b *zfsBackend) Capacity() (free, total uint64, err error) {
	ctx, cancel := b.opCtx()
	defer cancel()
	return b.cli.Capacity(ctx)
}

// Dirty: THE WORK SENTINEL EXISTS — a job wrote into the dataset head and no snapshot has been taken
// since. It means what it always meant (this device has an abandonable work state on this storage);
// what changed in qn.6h is what answers it, because there is no working/ left to stat and that same
// stat would return false forever, leaving Reset silently reporting nothing to do on a device whose
// head is mid-transfer.
//
// The killed-seed case the namespace answer protects has NO zfs analogue: there is no seed, so there
// is no partial clone, and the sentinel is written before the tool starts rather than around a clone.
func (b *zfsBackend) Dirty(udid string) bool {
	_, err := os.Stat(zfsWorkSentinel(b.backups, udid))
	return err == nil
}

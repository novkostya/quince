package storage

import (
	"fmt"
	"log/slog"
	"os"
)

// THIS FILE IS THE NAMESPACE BACKENDS' SEED LIFECYCLE. It was shared by both models until qn.6h;
// zfs now has no seed, no working copy and no latest/ to clone from — it writes into the dataset
// root — so it prepares its own work state in zfs.go and reaches none of this. Kept as it was rather
// than generalised: reflink / hardlink / copy are explicitly out of that rung's scope.
//
// prepareWorkDir is that lifecycle (qn.5b design §5 + Finding B, (cv)): resume a trustworthy dirty
// working/<udid>, or (re)seed it — bracketing the clone with a `seed_in_progress` sentinel so a seed
// killed mid-flight is never silently resumed into.
//
// It returns the idevicebackup2 TARGET (workingParent). seedFn does the backend's clone of
// latest/ → working/<udid> (clonetree via the safe strategy); it is invoked ONLY when latest/ is
// non-empty.
//
// Resume vs re-seed: a non-empty working/<udid> is normally a resumable dirty working (a prior
// FAILED backup — keep it so a retry resumes, no re-transfer). The EXCEPTION (Finding B) is a tree
// left by a KILLED SEED: its sentinel still says `seed_in_progress`, meaning the clone never
// finished, so the tree is a partial and resuming it could commit a version missing blobs — discard
// and re-seed instead. The guard discriminates the two by the flag alone; legacy/absent sentinels
// read false → resume (see workState.SeedInProgress).
func prepareWorkDir(backups, udid string, log *slog.Logger, seedFn func() error) (string, error) {
	target, seedPending, err := prepareWorkDirPhase1(backups, udid, log)
	if err != nil {
		return "", err
	}
	if seedPending {
		if err := finishSeed(backups, udid, seedFn); err != nil {
			return "", err
		}
	}
	return target, nil
}

// prepareWorkDirPhase1 is the FAST half of prepareWorkDir (qn.6b gated seed): resume-or-prepare the
// target and report whether a clone is still pending. It creates the empty target dir and writes the
// `seed_in_progress` sentinel for a cold seed, but does NOT clone — the caller launches idevicebackup2
// gated, then calls finishSeed while the tool waits. seedPending=false means resume (dirty working
// kept) or first-backup (empty tree) — nothing to clone, no gate needed.
func prepareWorkDirPhase1(backups, udid string, log *slog.Logger) (target string, seedPending bool, err error) {
	if !validUDID(udid) {
		return "", false, fmt.Errorf("storage: invalid udid %q", udid)
	}
	parent := workingParent(backups, udid)
	tree := workingTree(backups, udid)

	if !isEmptyDir(tree) {
		st, ok, _ := readWorkStateAt(nsWorkSentinel(backups, udid))
		if ok && st.SeedInProgress {
			// A seed was in progress → this tree is a partial clone (killed mid-seed). Discard it
			// and re-seed; resuming a partial could commit a version missing blobs (Finding B).
			log.Warn("storage: discarding a partial working — a seed was killed mid-clone; re-seeding from latest",
				"udid", udid)
			if err := os.RemoveAll(parent); err != nil {
				return "", false, err
			}
		} else {
			// Completed seed (or a legacy sentinel with no seed_in_progress) → a legit dirty
			// working; a retry resumes into it with no re-transfer.
			log.Info("storage: resuming dirty working", "udid", udid)
			return parent, false, nil
		}
	}

	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", false, err
	}
	seeded := !isEmptyDir(latestDir(backups, udid))
	// Mark IN PROGRESS before cloning — a crash/kill before finishSeed clears it leaves this true so
	// the next WorkDir catches the partial (above) instead of resuming it.
	if err := writeWorkStateAt(nsWorkSentinel(backups, udid), workState{SeededFromLatest: seeded, SeedInProgress: true}); err != nil {
		return "", false, err
	}
	if !seeded {
		// First/full backup: an empty tree, nothing to clone. Clear the sentinel and finish here.
		if err := os.MkdirAll(tree, 0o755); err != nil {
			return "", false, err
		}
		if err := writeWorkStateAt(nsWorkSentinel(backups, udid), workState{SeededFromLatest: false, SeedInProgress: false}); err != nil {
			return "", false, err
		}
		return parent, false, nil
	}
	return parent, true, nil // clone pending; the sentinel stays seed_in_progress until finishSeed
}

// finishSeed is the SLOW half: run the backend's clone (latest/ → working/<udid>) and clear the
// `seed_in_progress` sentinel so the tree becomes a trustworthy resume target. Called only when
// prepareWorkDirPhase1 reported seedPending.
//
// It first clears working/<udid>: on the qn.6b gated path (candidate C) idevicebackup2 has already
// created working/<udid> and written Info.plist into it before the gate, and the clone expects a
// clean dst (clonetree overlays and reflink needs a fresh file). No-op on the non-gated path, where
// the tree is still absent. The caller captures anything worth preserving (Info.plist) beforehand.
func finishSeed(backups, udid string, seedFn func() error) error {
	if err := os.RemoveAll(workingTree(backups, udid)); err != nil {
		return err
	}
	if err := seedFn(); err != nil {
		return err
	}
	return writeWorkStateAt(nsWorkSentinel(backups, udid), workState{SeededFromLatest: true, SeedInProgress: false})
}

package storage

import (
	"fmt"
	"log/slog"
	"os"
)

// prepareWorkDir is the Seed lifecycle shared by BOTH backends (qn.5b design §5 + Finding B, (cv)):
// resume a trustworthy dirty working/<udid>, or (re)seed it — bracketing the clone with a
// `seed_in_progress` sentinel so a seed killed mid-flight is never silently resumed into.
//
// It returns the idevicebackup2 TARGET (workingParent). seedFn does the backend-specific clone of
// latest/ → working/<udid> (namespace: clonetree via the safe strategy; zfs: host-side hook `seed`
// verb or the in-container reflink→copy ladder); it is invoked ONLY when latest/ is non-empty.
//
// Resume vs re-seed: a non-empty working/<udid> is normally a resumable dirty working (a prior
// FAILED backup — keep it so a retry resumes, no re-transfer). The EXCEPTION (Finding B) is a tree
// left by a KILLED SEED: its sentinel still says `seed_in_progress`, meaning the clone never
// finished, so the tree is a partial and resuming it could commit a version missing blobs — discard
// and re-seed instead. The guard discriminates the two by the flag alone; legacy/absent sentinels
// read false → resume (see workState.SeedInProgress).
func prepareWorkDir(backups, udid string, log *slog.Logger, seedFn func() error) (string, error) {
	if !validUDID(udid) {
		return "", fmt.Errorf("storage: invalid udid %q", udid)
	}
	parent := workingParent(backups, udid)
	tree := workingTree(backups, udid)

	if !isEmptyDir(tree) {
		st, ok, _ := readWorkState(backups, udid)
		if ok && st.SeedInProgress {
			// A seed was in progress → this tree is a partial clone (killed mid-seed). Discard it
			// and re-seed; resuming a partial could commit a version missing blobs (Finding B).
			log.Warn("storage: discarding a partial working — a seed was killed mid-clone; re-seeding from latest",
				"udid", udid)
			if err := os.RemoveAll(parent); err != nil {
				return "", err
			}
		} else {
			// Completed seed (or a legacy sentinel with no seed_in_progress) → a legit dirty
			// working; a retry resumes into it with no re-transfer.
			log.Info("storage: resuming dirty working", "udid", udid)
			return parent, nil
		}
	}

	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	seeded := !isEmptyDir(latestDir(backups, udid))
	// Mark IN PROGRESS before cloning — a crash/kill during seedFn leaves this true so the next
	// WorkDir catches the partial (above) instead of resuming it.
	if err := writeWorkState(backups, udid, workState{SeededFromLatest: seeded, SeedInProgress: true}); err != nil {
		return "", err
	}
	if seeded {
		if err := seedFn(); err != nil {
			return "", err
		}
	} else if err := os.MkdirAll(tree, 0o755); err != nil { // first/full backup: an empty tree
		return "", err
	}
	// Seed complete — clear the flag; the tree is now a trustworthy resume target.
	if err := writeWorkState(backups, udid, workState{SeededFromLatest: seeded, SeedInProgress: false}); err != nil {
		return "", err
	}
	return parent, nil
}

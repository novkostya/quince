package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// journalName is the per-device commit journal's NAMESPACE name (design §5: commit phases persist to
// disk so a crash mid-commit reconciles deterministically). It lives in the device dir and exists
// only while a commit is in flight; a fresh commit removes it on success, reconciliation completes
// and removes any it finds (roll-forward).
//
// ON ZFS IT LIVES IN THE PARENT DATASET — zfsJournal — because after qn.6h the device dir IS the
// backup tree and the journal is on disk at the moment `zfs snapshot` runs, so this path would put
// quince's bookkeeping inside every committed version. Which is why the functions below take a PATH.
const journalName = ".quince-commit.json"

// nsJournal is the namespace journal path.
func nsJournal(deviceDir string) string { return filepath.Join(deviceDir, journalName) }

// CommitPhase names a journaled commit step (design §5's two phase sequences).
type CommitPhase string

const (
	// THE TWO MODELS NO LONGER SHARE A PIVOT (qn.6h). This block said "both models share the atomic
	// exchange as their pivot"; that is now true of the namespace backends alone.
	//
	//   namespace: prepared → exchanged → archived        (old working content → versions/<prev-ts>/)
	//   zfs:       prepared → snapshot_created            (the tree IS the dataset root; nothing moves)
	//
	// Namespace still writes the tree to working/<udid>, writes its marker in, then EXCHANGES it into
	// latest/ in one syscall — marker-guarded for idempotency, because a re-run that sees latest/
	// already carrying this version's id must not swap twice. zfs writes in place, so there is
	// nothing to exchange and PhaseExchanged does not occur on it. Per-backend phase sets are already
	// the design, so the enum keeps its shape.
	PhasePrepared        CommitPhase = "prepared"         // marker written: namespace working/<udid>, zfs the dataset root
	PhaseExchanged       CommitPhase = "exchanged"        // namespace only: working/<udid> ⇄ latest/ done (atomic)
	PhaseArchived        CommitPhase = "archived"         // namespace: prev latest → versions/<prev-ts>/
	PhaseSnapshotCreated CommitPhase = "snapshot_created" // zfs: @quince-<date>-<id> exists
)

// Journal is the on-disk commit progress record for one device. Reconciliation reads it to
// roll a half-done commit forward.
type Journal struct {
	VersionID           string      `json:"version_id"`
	UDID                string      `json:"udid"`
	Backend             string      `json:"backend"`
	JobID               string      `json:"job_id"`
	Phase               CommitPhase `json:"phase"`
	CreatedAt           string      `json:"created_at"` // RFC3339 UTC
	Kind                string      `json:"kind"`
	Encrypted           bool        `json:"encrypted"`
	StructureVerifiedAt string      `json:"structure_verified_at"`
	LogicalBytes        int64       `json:"logical_bytes"`
	JobDir              string      `json:"job_dir"`      // namespace: work/<job> path
	PrevTS              string      `json:"prev_ts"`      // namespace: archived previous latest's ts dir
	ZFSSnapshot         string      `json:"zfs_snapshot"` // zfs: full snapshot name
	DeviceDir           string      `json:"device_dir"`   // where this journal lives
}

func writeJournal(path string, j Journal) error {
	j.DeviceDir = filepath.Dir(path)
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func readJournal(path string) (Journal, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Journal{}, false, nil
	}
	if err != nil {
		return Journal{}, false, err
	}
	var j Journal
	if err := json.Unmarshal(b, &j); err != nil {
		return Journal{}, false, err
	}
	j.DeviceDir = filepath.Dir(path)
	return j, true, nil
}

func removeJournal(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// scanJournals walks the immediate device subdirs of backupsRoot and returns every commit journal
// found — the NAMESPACE shape, where each device dir is a container holding its own journal.
func scanJournals(backupsRoot string) ([]Journal, error) {
	entries, err := os.ReadDir(backupsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Journal
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		j, ok, err := readJournal(nsJournal(filepath.Join(backupsRoot, e.Name())))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, j)
		}
	}
	return out, nil
}

// scanFlatJournals reads the parent-level per-device journals the zfs backend writes (qn.6h). It
// looks at FILES in the parent rather than descending, which is the whole point: on zfs the device
// subdirs are the backup trees and nothing of quince's is in them but the marker.
//
// The udid comes from the journal's own payload, never from the filename — the file names a device
// for a human's benefit, and parsing it back would be a second, weaker source of truth.
func scanFlatJournals(backupsRoot string) ([]Journal, error) {
	entries, err := os.ReadDir(backupsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Journal
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, zfsSidecarPrefix+"commit-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		j, ok, err := readJournal(filepath.Join(backupsRoot, name))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, j)
		}
	}
	return out, nil
}

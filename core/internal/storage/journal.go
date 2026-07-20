package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// journalName is the per-device commit journal (design §5: commit phases persist to disk so a
// crash mid-commit reconciles deterministically). It lives in the device dir and exists only
// while a commit is in flight; a fresh commit removes it on success, reconciliation completes
// and removes any it finds (roll-forward).
const journalName = ".quince-commit.json"

// CommitPhase names a journaled commit step (design §5's two phase sequences).
type CommitPhase string

const (
	// namespace (reflink/hardlink/copy): prepared → previous_archived → latest_promoted.
	PhasePrepared         CommitPhase = "prepared"          // marker written into work/<job>
	PhasePreviousArchived CommitPhase = "previous_archived" // latest/ → versions/<prev-ts>/
	PhaseLatestPromoted   CommitPhase = "latest_promoted"   // work/<job>/ → latest/
	// zfs: prepared → snapshot_created → latest_rebuilt.
	PhaseSnapshotCreated CommitPhase = "snapshot_created" // @quince-<id>-<ts> exists
	PhaseLatestRebuilt   CommitPhase = "latest_rebuilt"   // latest/ rebuilt from .zfs + swapped
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

func writeJournal(deviceDir string, j Journal) error {
	j.DeviceDir = deviceDir
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(deviceDir, journalName), b, 0o644)
}

func readJournal(deviceDir string) (Journal, bool, error) {
	b, err := os.ReadFile(filepath.Join(deviceDir, journalName))
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
	j.DeviceDir = deviceDir
	return j, true, nil
}

func removeJournal(deviceDir string) error {
	err := os.Remove(filepath.Join(deviceDir, journalName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// scanJournals walks the immediate device subdirs of backupsRoot and returns every commit
// journal found (used by both namespace and zfs PendingJournals).
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
		j, ok, err := readJournal(filepath.Join(backupsRoot, e.Name()))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, j)
		}
	}
	return out, nil
}

package storage

import (
	"encoding/json"
	"errors"
	"os"
)

// workSentinelName is the per-device work-state sidecar (qn.5b). It lives in the device dir,
// NEVER inside working/<udid> (which is exchanged into latest/, and which the zfs hook `seed` verb
// blows away with rm -rf + cp -a), so it cannot pollute a committed version and it survives the
// seed. It records two facts the resume path cannot otherwise recover: whether working/ was seeded
// from an existing latest/ (the authoritative full-vs-incremental signal, finding #9(a), (cj)/(ck)),
// and whether a seed was IN PROGRESS when quince last touched it (Finding B, (ct)/(cv)).
const workSentinelName = ".quince-work.json"

// workState is the workSentinelName payload.
type workState struct {
	// SeededFromLatest is true when WorkDir cloned an existing latest/ into working/<udid>
	// (⇒ incremental); false when working/ started empty (a first/full backup).
	SeededFromLatest bool `json:"seeded_from_latest"`
	// SeedInProgress is written true BEFORE the seed clone and cleared to false on success (Finding
	// B, (cv)). A non-empty working/<udid> whose sentinel still says true is a PARTIAL clone from a
	// seed killed mid-flight (e.g. the (cs) seed-timeout SIGKILL, or a crash) — resuming into it
	// could commit a version missing blobs, so WorkDir discards it and re-seeds. LEGACY-SAFE by Go's
	// zero value: an old-code sentinel (written post-seed = complete) has no `seed_in_progress` field
	// → decodes to false → resume, so an upgrade never discards a resumable 34 GB working/.
	SeedInProgress bool `json:"seed_in_progress"`
}

// kindOf maps the seed decision to the authoritative Version.kind.
func (w workState) kindOf() string {
	if w.SeededFromLatest {
		return "incremental"
	}
	return "full"
}

func writeWorkState(backupsRoot, udid string, w workState) error {
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(workSentinel(backupsRoot, udid), b, 0o644)
}

// readWorkState reads the sentinel; ok=false when it is absent (no seed happened / already
// cleaned). A present-but-unreadable sentinel is reported so the caller can fall back to
// recomputing the kind from the presence of a prior version (never silently guessing).
func readWorkState(backupsRoot, udid string) (w workState, ok bool, err error) {
	b, rerr := os.ReadFile(workSentinel(backupsRoot, udid))
	if errors.Is(rerr, os.ErrNotExist) {
		return workState{}, false, nil
	}
	if rerr != nil {
		return workState{}, false, rerr
	}
	if uerr := json.Unmarshal(b, &w); uerr != nil {
		return workState{}, false, uerr
	}
	return w, true, nil
}

func removeWorkState(backupsRoot, udid string) {
	_ = os.Remove(workSentinel(backupsRoot, udid))
}

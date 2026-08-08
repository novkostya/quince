package storage

import (
	"encoding/json"
	"errors"
	"os"
)

// workSentinelName is the per-device work-state sidecar's NAMESPACE name (qn.5b). It lives in the
// device dir, NEVER inside working/<udid> (which is exchanged into latest/, and which the zfs hook
// `seed` verb blows away with rm -rf + cp -a), so it cannot pollute a committed version and it
// survives the seed. It records two facts the resume path cannot otherwise recover: whether working/
// was seeded from an existing latest/ (the authoritative full-vs-incremental signal, finding #9(a),
// (cj)/(ck)), and whether a seed was IN PROGRESS when quince last touched it (Finding B, (ct)/(cv)).
//
// ON ZFS THE PATH IS DIFFERENT AND THE PAYLOAD IS THE SAME — zfsWorkSentinel, in the parent dataset
// (qn.6h D3). Which is why every function below takes a PATH: the file is the same file, and only
// its owner knows where it goes.
const workSentinelName = ".quince-work.json"

// workState is the workSentinelName payload.
type workState struct {
	// SeededFromLatest is true when WorkDir cloned an existing latest/ into working/<udid>
	// (⇒ incremental); false when working/ started empty (a first/full backup).
	//
	// ON ZFS NOTHING IS CLONED AND THE MEANING IS UNCHANGED (qn.6h D3): the derivation becomes "was
	// the dataset root non-empty at job start" — the same question about the same content, asked at
	// the path that now holds it. The field keeps its name because it is what makes Version.kind
	// authoritative (finding #9(a): the lab proved Status.plist.IsFullBackup lies), and renaming it
	// would break the legacy-safe decode below for no gain.
	SeededFromLatest bool `json:"seeded_from_latest"`
	// SeedInProgress is written true BEFORE the seed clone and cleared to false on success (Finding
	// B, (cv)). A non-empty working/<udid> whose sentinel still says true is a PARTIAL clone from a
	// seed killed mid-flight (e.g. the (cs) seed-timeout SIGKILL, or a crash) — resuming into it
	// could commit a version missing blobs, so WorkDir discards it and re-seeds. LEGACY-SAFE by Go's
	// zero value: an old-code sentinel (written post-seed = complete) has no `seed_in_progress` field
	// → decodes to false → resume, so an upgrade never discards a resumable 34 GB working/.
	//
	// IT IS ALWAYS FALSE ON ZFS AND MUST NOT BE REPURPOSED to mean "a transfer is in flight" (qn.6h
	// D3). There is no seed there, so there is no partial clone for it to describe — and the name
	// invites the mistake. Setting it true would make prepareWorkDirPhase1's guard discard a
	// RESUMABLE DIRTY HEAD, which is exactly what "a failed job keeps its dirty working so a retry
	// resumes" forbids: a multi-hour Wi-Fi transfer, thrown away by a field name.
	SeedInProgress bool `json:"seed_in_progress"`
}

// kindOf maps the seed decision to the authoritative Version.kind.
func (w workState) kindOf() string {
	if w.SeededFromLatest {
		return "incremental"
	}
	return "full"
}

func writeWorkStateAt(path string, w workState) error {
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// readWorkStateAt reads the sentinel; ok=false when it is absent (no seed happened / already
// cleaned). A present-but-unreadable sentinel is reported so the caller can fall back to
// recomputing the kind from the presence of a prior version (never silently guessing).
func readWorkStateAt(path string) (w workState, ok bool, err error) {
	b, rerr := os.ReadFile(path)
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

func removeWorkStateAt(path string) {
	_ = os.Remove(path)
}

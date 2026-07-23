package storage

import (
	"encoding/json"
	"errors"
	"os"
)

// workSentinelName is the per-device work-state sidecar (qn.5b). It lives in the device dir,
// NEVER inside working/<udid> (which is exchanged into latest/), so it cannot pollute a committed
// version. It records the ONE fact the commit cannot re-derive after the tree has moved: whether
// working/ was seeded from an existing latest/ — the authoritative full-vs-incremental signal
// (finding #9(a), (cj)/(ck)) — and it survives a crash or a resume-into-dirty-working so the
// recovered kind is correct.
const workSentinelName = ".quince-work.json"

// workState is the workSentinelName payload.
type workState struct {
	// SeededFromLatest is true when WorkDir cloned an existing latest/ into working/<udid>
	// (⇒ incremental); false when working/ started empty (a first/full backup).
	SeededFromLatest bool `json:"seeded_from_latest"`
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

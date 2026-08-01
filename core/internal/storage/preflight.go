package storage

import (
	"errors"
	"fmt"
	"os"
)

// PreflightStorage re-checks a job's storage IMMEDIATELY BEFORE anything is written to it
// (qn.6c story 7).
//
// `jobSlot` already refuses a storage that was unusable AT STARTUP. This asks the different and
// later question: is it still the same storage, right now? Between startup and a backup a disk can
// be unmounted, swapped, or remounted from a different filesystem — and a transfer that begins
// against a path which is no longer the storage quince thinks it is writes tens of gigabytes to the
// wrong place.
//
// IT NEVER RE-PROBES A BACKEND, and that distinction is what keeps G5b intact (quince#438). It reads
// the marker and compares it against what the slot ALREADY knows; it creates nothing, selects
// nothing and writes nothing. Re-selecting a backend here is precisely the operation that would
// turn a bare mountpoint back into a new empty storage.
//
// Three distinguishable, actionable failures, never a downgrade and never a re-create:
//
//	unreachable       the root cannot be read at all
//	missing medium    the path is readable and the marker is GONE — an unplugged disk's bare
//	                  mountpoint, which must never be treated as an empty new storage
//	backend mismatch  the marker and the slot disagree about what this storage is
func (m *Manager) PreflightStorage(jobID string) error {
	s, err := m.jobSlot(jobID)
	if err != nil {
		return err // unreachable, or a binding that no longer resolves
	}
	return s.preflight()
}

func (s Slot) preflight() error {
	marker, err := ReadStorageMarker(s.Root)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		// MISSING MEDIUM. The path is readable and the marker is not there, which is exactly an
		// unplugged disk's mountpoint. Refusing here is the difference between "your disk is not
		// mounted" and silently filling the system disk it was mounted on.
		return fmt.Errorf(
			"storage %q at %s has no quince storage marker — the medium is not mounted. "+
				"Mount it and try again; quince will not write a backup to a bare mountpoint",
			s.Name, s.Root)
	case errors.Is(err, ErrStorageMarkerCorrupt):
		return fmt.Errorf(
			"storage %q at %s has an unreadable storage marker: %v — refusing rather than "+
				"re-creating it, because a marker quince cannot read is not one it may overwrite",
			s.Name, s.Root, err)
	default:
		return fmt.Errorf("storage %q at %s could not be read: %w", s.Name, s.Root, err)
	}

	// THE MEDIUM CHANGED UNDER US. A different storage_id at the same path means this is another
	// storage entirely — a swapped disk, or a mountpoint that now resolves elsewhere. Writing this
	// device's backup here would mix two storages' contents under one identity.
	if s.StorageID != "" && marker.StorageID != s.StorageID {
		return fmt.Errorf(
			"storage %q at %s now identifies as %s, not %s — the medium at this path is not the "+
				"storage quince opened at startup; refusing rather than writing to it",
			s.Name, s.Root, marker.StorageID, s.StorageID)
	}

	// BACKEND MISMATCH, compared against the backend this slot ALREADY holds — never re-probed.
	// Versions already committed here were made with the recorded backend, so adopting a different
	// one silently changes what a `latest/` exchange means.
	if bad, why := marker.Mismatch(s.BackendName); bad {
		return fmt.Errorf("storage %q: %s", s.Name, why)
	}
	return nil
}

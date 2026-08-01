package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Resolution is what a declared storage turned out to be when quince looked at it.
type Resolution string

const (
	// ResolutionCreated — quince has never seen this storage and found a reachable path with no
	// marker, so this IS its creation moment: the backend was probed and frozen, and the marker
	// written. Loud and user-visible by design (see ResolveStorage).
	ResolutionCreated Resolution = "created"
	// ResolutionOpened — the marker was read and did NOT DISAGREE with the probe. The ordinary
	// case. Note the wording: whether the comparison actually ran is StorageState.Verified, not
	// this value, because a probe that cannot determine a backend is not a disagreement.
	ResolutionOpened Resolution = "opened"
	// ResolutionMissingMedium — the path is reachable and has NO marker, for a storage the DB
	// already knows. An unplugged disk's bare mountpoint. REFUSED, never re-created.
	ResolutionMissingMedium Resolution = "missing_medium"
	// ResolutionBackendMismatch — the marker and the probe disagree. A remount. REFUSED, never
	// re-selected.
	ResolutionBackendMismatch Resolution = "backend_mismatch"
	// ResolutionUnreachable — the path itself could not be read.
	ResolutionUnreachable Resolution = "unreachable"
	// ResolutionCorruptMarker — a marker is present and fails its own checksum.
	ResolutionCorruptMarker Resolution = "corrupt_marker"
)

// OK reports whether a storage in this state may be backed up to.
func (r Resolution) OK() bool { return r == ResolutionCreated || r == ResolutionOpened }

// StorageState is the outcome of resolving one declared storage.
type StorageState struct {
	Name       string
	Path       string
	Resolution Resolution
	StorageID  string // set when Resolution.OK()
	Backend    string // set when Resolution.OK()
	// Reason is prose for a human: observation, consequence, remedy (preflight's idiom).
	//
	// ALWAYS set when !OK. ALSO set on the one OK path worth saying out loud — an unverified open
	// (OK() && !Verified), where quince proceeds on the marker alone. So a non-empty Reason does
	// NOT imply a refusal: read Resolution and Verified for that.
	Reason string
	// Verified reports whether the backend comparison ACTUALLY RAN.
	//
	// It exists because "checked and agrees" and "could not check" are different facts, and
	// Resolution alone cannot carry both: a probe that returns nothing is not a disagreement
	// (Mismatch declines to manufacture one), so an unprobeable storage that already has a marker
	// opens on the strength of the marker alone. That is the right BEHAVIOUR — opening freezes
	// nothing, and refusing every backup because a probe hiccuped is worse than the problem — but
	// ResolutionOpened must not be read as evidence a comparison happened.
	//
	// This is quince#363's shape one layer down: that ruling split `wifi_sync_unconfirmed`
	// (accepted, and the read-back could not RUN) from `wifi_sync_not_applied` (accepted, and the
	// state is UNCHANGED), because conflating "unverifiable" with a verified outcome reported a
	// write that had worked as one that had not. Same distinction, same reason.
	//
	// false + OK() means: proceed, but nothing confirmed the medium is what the marker says.
	Verified bool
}

// KnownStorage is the DB half ResolveStorage consults. Defined here, consumer-side, so the
// storage package does not depend on the store package's row type.
type KnownStorage struct {
	Known     bool
	StorageID string
}

// StorageLookup answers "has quince created this storage before?" for a config entry name.
type StorageLookup func(name string) (KnownStorage, error)

// ResolveStorage decides what a declared storage is, and is the rung's load-bearing rule.
//
// CREATION REQUIRES A REACHABLE PATH, NO MARKER, **AND NO DB ROW**. The last clause is the one that
// took a review to find (quince#381), and without it an unmounted mountpoint is created as a new
// storage: /mnt/backup-disk is a readable, empty directory on the ROOT filesystem while the disk is
// unplugged, because the marker is on the disk. Contents-only, quince would probe `copy` instead of
// the disk's `zfs`, write a NEW uuid into the mountpoint, have that marker SHADOWED (not deleted)
// when the disk mounts over it, and then accept backups onto the root filesystem while the user
// believes they are going to the removable one. Silent, and a removable disk is this rung's
// motivating case.
//
// So: a reachable path with no marker, for a storage the DB already knows, is a MISSING MEDIUM and
// refuses. Never re-create, never re-probe.
//
// THE RESIDUAL IS REAL AND DELIBERATELY NOT ENGINEERED AWAY: the very first startup after declaring
// a storage whose medium is absent has neither marker nor row, and is indistinguishable from a
// genuine creation. It is carried by a written requirement — *declare a storage with its medium
// present* — plus the fact that creation is a LOUD, user-visible event, so the one remaining silent
// case is not silent. Closing it mechanically would mean recording an expected filesystem or device
// id at creation; that is named in the spec's Known gaps rather than built.
func ResolveStorage(name, path string, probe func(string) string, known StorageLookup,
	now func() time.Time, appVersion string, newID func() string) (StorageState, error) {

	st := StorageState{Name: name, Path: path}

	if !reachable(path) {
		st.Resolution = ResolutionUnreachable
		st.Reason = fmt.Sprintf("storage %q: path %q cannot be read — if this is removable media, it is not mounted; quince will not back up to it and will not create anything there", name, path)
		return st, nil
	}

	marker, err := ReadStorageMarker(path)
	switch {
	case err == nil:
		// A marker exists. Compare rather than re-select: the backend was chosen once.
		probed := probe(path)
		if bad, why := marker.Mismatch(probed); bad {
			st.Resolution, st.Reason = ResolutionBackendMismatch, "storage "+name+": "+why
			return st, nil
		}
		st.Resolution, st.StorageID, st.Backend = ResolutionOpened, marker.StorageID, marker.Backend
		// Verified only if the probe produced something to compare AGAINST. An empty probe is
		// declined as a mismatch (deliberately — see Mismatch), so without this the caller would
		// be told a comparison succeeded when none ran.
		st.Verified = probed != ""
		if !st.Verified {
			st.Reason = fmt.Sprintf("storage %q: opened on its %s alone — the backend could not be probed at %q, so nothing confirmed the medium is still %s. Proceeding, because opening freezes nothing; recorded as UNVERIFIED rather than reported as agreement.",
				name, StorageMarkerName, path, marker.Backend)
		}
		return st, nil

	case errors.Is(err, ErrStorageMarkerCorrupt):
		st.Resolution = ResolutionCorruptMarker
		st.Reason = fmt.Sprintf("storage %q: %s at %q failed its own checksum — refusing rather than guessing at its identity; the file is damaged, not absent", name, StorageMarkerName, path)
		return st, nil

	case !errors.Is(err, os.ErrNotExist):
		return st, fmt.Errorf("storage %q: reading %s: %w", name, StorageMarkerName, err)
	}

	// No marker. Whether that is a creation or an absent medium is the DB's answer, not the
	// directory's.
	k, err := known(name)
	if err != nil {
		return st, fmt.Errorf("storage %q: checking whether it is known: %w", name, err)
	}
	if k.Known {
		st.Resolution = ResolutionMissingMedium
		st.Reason = fmt.Sprintf("storage %q: %q is readable but has no %s, and quince created this storage before (%s). Its medium is ABSENT — a mountpoint with nothing mounted on it looks exactly like this. Refusing rather than creating a second storage here, which would put backups on the wrong filesystem. Mount it and start again.",
			name, path, StorageMarkerName, k.StorageID)
		return st, nil
	}

	// Creation moment.
	backend := probe(path)
	if backend == "" {
		st.Resolution = ResolutionUnreachable
		st.Reason = fmt.Sprintf("storage %q: could not determine a backend for %q, so refusing to create a storage whose backend would be a guess", name, path)
		return st, nil
	}
	m := StorageMarker{
		StorageID:  newID(),
		Backend:    backend,
		CreatedAt:  now().UTC().Format(time.RFC3339),
		AppVersion: appVersion,
	}
	if err := WriteStorageMarker(path, m); err != nil {
		return st, fmt.Errorf("storage %q: writing %s: %w", name, StorageMarkerName, err)
	}
	// Verified: creation just probed and froze the result, so the marker and the medium agree by
	// construction. The refusal above is why — a creation with an undetermined backend never gets
	// this far, because that guess would be permanent.
	st.Resolution, st.StorageID, st.Backend, st.Verified = ResolutionCreated, m.StorageID, m.Backend, true
	return st, nil
}

// reachable reports whether path is a readable directory. Deliberately not a writability probe:
// "cannot write here" is a different problem with a different remedy, and conflating them would
// report a full or read-only disk as an absent one.
func reachable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

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
	// ResolutionOpened — the marker was read and agrees with what was probed. The ordinary case.
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
	Reason     string // always set when !OK — observation, consequence, remedy
}

// knownStorage is the DB half ResolveStorage consults. Defined here, consumer-side, so the
// storage package does not depend on the store package's row type.
type knownStorage struct {
	Known     bool
	StorageID string
}

// StorageLookup answers "has quince created this storage before?" for a config entry name.
type StorageLookup func(name string) (knownStorage, error)

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
		if bad, why := marker.Mismatch(probe(path)); bad {
			st.Resolution, st.Reason = ResolutionBackendMismatch, "storage "+name+": "+why
			return st, nil
		}
		st.Resolution, st.StorageID, st.Backend = ResolutionOpened, marker.StorageID, marker.Backend
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
	st.Resolution, st.StorageID, st.Backend = ResolutionCreated, m.StorageID, m.Backend
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

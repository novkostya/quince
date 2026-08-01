package store

import (
	"database/sql"
	"time"
)

// StorageRow is quince's record of a storage it has resolved before (migration 0006).
//
// The DISK is still the source of truth for a storage's identity — the UUID lives in its
// quince-storage.json. This table is what lets quince tell "a storage I have created before, whose
// medium is absent" from "a path I have never seen", which is the whole basis of the
// unmounted-mountpoint guard: without a row to check, an unplugged disk's bare mountpoint reads as
// a brand-new storage and quince creates one on the root filesystem.
//
// Keyed on Name — the config entry's stable label — NOT on Path. A path moves when a disk is
// remounted elsewhere; the name does not.
type StorageRow struct {
	Name string
	// StorageID is the UUID from quince-storage.json. nil until the creation moment has run,
	// which is the state that says "declared, never yet reached".
	StorageID *string
	// Backend is frozen at creation. nil alongside StorageID.
	Backend   *string
	Path      string // last known; informational, never an identity
	CreatedAt *time.Time
	SeenAt    time.Time
}

// UpsertStorage records (or refreshes) a storage by name. It never clears StorageID or Backend
// once set: those are frozen at the creation moment, and an update that could blank them would
// make a later startup treat a known storage as new — the exact confusion this table prevents.
func (s *Store) UpsertStorage(r StorageRow) error {
	_, err := s.db.Exec(`INSERT INTO storages (name, storage_id, backend, path, created_at, seen_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			storage_id = COALESCE(storages.storage_id, excluded.storage_id),
			backend    = COALESCE(storages.backend,    excluded.backend),
			path       = excluded.path,
			created_at = COALESCE(storages.created_at, excluded.created_at),
			seen_at    = excluded.seen_at`,
		r.Name, nullStr(r.StorageID), nullStr(r.Backend), r.Path,
		nullTime(r.CreatedAt), fmtTime(r.SeenAt))
	return err
}

// GetStorage returns the row for a config entry's name; ok=false when quince has never seen it.
//
// ok=false is the ONLY thing that permits a creation moment. A row that exists with a nil
// StorageID means "declared before, never successfully reached" — still not a licence to create,
// because the medium may simply have been absent every time.
func (s *Store) GetStorage(name string) (StorageRow, bool, error) {
	row := s.db.QueryRow(
		`SELECT name, storage_id, backend, path, created_at, seen_at FROM storages WHERE name = ?`, name)
	r, err := scanStorage(row)
	if err == sql.ErrNoRows {
		return StorageRow{}, false, nil
	}
	if err != nil {
		return StorageRow{}, false, err
	}
	return r, true, nil
}

// ListStorages returns every known storage, name order.
func (s *Store) ListStorages() ([]StorageRow, error) {
	rows, err := s.db.Query(
		`SELECT name, storage_id, backend, path, created_at, seen_at FROM storages ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StorageRow
	for rows.Next() {
		r, err := scanStorage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AttributeVersions sets storage_id on every version for a udid that has none yet.
//
// It only ever fills a NULL — an already-attributed version is left alone, so this can run on
// every startup without rewriting history. That matters more than it looks: `storage_id` is a fact
// about where a committed backup lives, and a committed backup is data that cannot be regenerated.
func (s *Store) AttributeVersions(udid, storageID string) (int64, error) {
	res, err := s.db.Exec(
		`UPDATE versions SET storage_id = ? WHERE udid = ? AND storage_id IS NULL`, storageID, udid)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountUnattributedVersions reports how many version rows still carry a NULL storage_id.
//
// This exists for the gate the nullability ruling made mandatory: `null` means "not yet
// attributed" and is TRANSITIONAL, and a nullable-with-meaning field whose meaning is "temporary"
// decays into a permanent unknown unless something asserts otherwise. This is that something.
func (s *Store) CountUnattributedVersions() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM versions WHERE storage_id IS NULL`).Scan(&n)
	return n, err
}

func scanStorage(sc rowScanner) (StorageRow, error) {
	var (
		r       StorageRow
		sid     sql.NullString
		backend sql.NullString
		created sql.NullString
		seen    string
	)
	if err := sc.Scan(&r.Name, &sid, &backend, &r.Path, &created, &seen); err != nil {
		return StorageRow{}, err
	}
	r.StorageID = strPtrOrNil(sid)
	r.Backend = strPtrOrNil(backend)
	var err error
	if r.CreatedAt, err = timePtrOrNil(created); err != nil {
		return StorageRow{}, err
	}
	if r.SeenAt, err = parseTime(seen); err != nil {
		return StorageRow{}, err
	}
	return r, nil
}

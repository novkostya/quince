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

// AttributeVersion sets storage_id on ONE version that has none yet.
//
// It replaced a `(udid, storageID)` form that filled every NULL row for a device with a single id
// (quince#439). That signature could not be made correct once more than one storage is declared:
// the caller had to pick a storage before knowing where each artifact was, and picking the default
// silently recorded existing backups as living on a disk they are not on. The right moment to answer
// "which storage" is while reconciling one, because `Scan` has just walked that root — so the
// caller now names a VERSION, not a device.
//
// `storage_id IS NULL` in the WHERE is the whole guarantee: attribution FILLS, it never rewrites.
// An already-attributed version records where a committed backup lives, and a committed backup is
// data that cannot be regenerated — so moving it is not something a startup scan may do, even by
// mistake.
func (s *Store) AttributeVersion(id, storageID string) error {
	_, err := s.db.Exec(
		`UPDATE versions SET storage_id = ? WHERE id = ? AND storage_id IS NULL`, storageID, id)
	return err
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

// DeleteStorage removes a storage's row by name. It is the second half of forgetting a storage,
// and its absence was the whole of quince#1525: `ForgetStorage` splices the entry out of
// `config.yml` and nothing removed the row, so `GetStorage(name)` kept answering `Known` for a
// storage nobody had declared for weeks. A reachable path plus no marker plus a known row is
// MISSING MEDIUM, which refuses — so the path stayed claimed and could not be re-added, and the
// only lever that appeared to exist was deleting `quince-storage.json` by hand, which moves the
// state sideways rather than freeing it.
//
// KEYED ON NAME, like every other operation on this table, and that is what makes it safe to run
// after the config write: the row is only ever consulted for a name the config declares, so a row
// whose entry has just been removed is unreachable until someone re-declares that name — which is
// precisely the case this exists to unblock.
//
// IT DOES NOT TOUCH `versions`. Rows there join to `storages` by `storage_id`, and removing a
// storage that still has versions would orphan that join and silently detach a real backup history
// from the disk holding it. The refusal that prevents it is the CALLER'S, because it must produce
// a sentence naming what blocks it rather than a bare no — see the forget handler. This function
// is deliberately unguarded so that the guard lives where it can be explained.
//
// AND NOTHING DOWNSTREAM WOULD CATCH A MISTAKE HERE. `versions.storage_id` is a plain `TEXT`
// column added by `ALTER TABLE` (`0006_storage.sql:43`) with NO `FOREIGN KEY`, so `PRAGMA
// foreign_keys=ON` has nothing to enforce on this join. A delete that should not have happened
// detaches a backup history silently and permanently. The caller's guard is the ONLY guard
// (quince#1534 review), which is why it refuses when it cannot check rather than proceeding.
//
// Missing rows are not an error: forgetting a storage quince has never resolved is a no-op, not a
// failure, and a caller that had to distinguish them would be handling a case with no remedy.
func (s *Store) DeleteStorage(name string) error {
	_, err := s.db.Exec(`DELETE FROM storages WHERE name = ?`, name)
	return err
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

package store

import (
	"database/sql"
	"time"
)

// VersionRow is one committed-version registry row (contracts §2 Version, minus the derived
// browse_root — the storage layer computes that from backend + is_latest + created_at, which
// is why it isn't a column). The DISK is the source of truth; this is quince's record of what
// it committed. Pointer/`*time.Time` fields are the contract's nullables.
type VersionRow struct {
	ID                  string
	UDID                string
	Backend             string
	ZFSSnapshot         *string
	CreatedAt           time.Time
	JobID               *string // nil = adopted
	Kind                string
	Encrypted           bool
	IsLatest            bool
	StructureVerifiedAt *time.Time
	ContentVerifiedAt   *time.Time
	LogicalBytes        int64
	PhysicalBytes       int64
	Missing             bool
	// StorageID is which storage this version lives on. nil = NOT YET ATTRIBUTED (qn.6c,
	// migration 0006). Unlike JobID, whose nil (= adopted) is permanent and correct, this one
	// is TRANSITIONAL: a pre-qn.6c version starts nil and is attributed once its storage has a
	// quince-storage.json marker. Treat a lingering nil as a defect, not a steady state.
	StorageID *string
}

// InsertVersion records a committed (or adopted) version. It does NOT enforce single-latest;
// call PromoteLatest after a commit to make this row the sole latest for its (udid, storage).
func (s *Store) InsertVersion(v VersionRow) error {
	_, err := s.db.Exec(`INSERT INTO versions
		(id, udid, backend, zfs_snapshot, created_at, job_id, kind, encrypted, is_latest,
		 structure_verified_at, content_verified_at, logical_bytes, physical_bytes, missing, storage_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.UDID, v.Backend, nullStr(v.ZFSSnapshot), fmtTime(v.CreatedAt), nullStr(v.JobID),
		v.Kind, boolInt(v.Encrypted), boolInt(v.IsLatest),
		nullTime(v.StructureVerifiedAt), nullTime(v.ContentVerifiedAt),
		v.LogicalBytes, v.PhysicalBytes, boolInt(v.Missing), nullStr(v.StorageID))
	return err
}

// PromoteLatest makes id the sole is_latest=1 row for its `(udid, storage)` (single UPDATE, so a
// crash can't leave two latests within a group). Called at the end of a commit's registry phase.
//
// SCOPED TO THE STORAGE, not just the device — Operator ruling 2026-08-01 (quince#378).
// `Version.is_latest` means "the newest committed version of this device ON ITS STORAGE", so a
// device backed up to two storages has two rows flagged, one each. Without the storage clause,
// committing to storage B demoted storage A's latest, and `browse_root` then resolved A's newest
// version to a `versions/<ts>/` directory that does not exist — its artifact is still in `latest/`.
//
// `storage_id IS ?` rather than `= ?`, because SQL `=` never matches NULL and unattributed rows
// must still get a latest AMONG THEMSELVES. Excluding them was considered and rejected: a device
// whose rows are all NULL would then have NO latest at all, which produces the same unresolvable
// `browse_root` by the opposite route. `IS ?` fails safe — every group always has exactly one.
func (s *Store) PromoteLatest(udid, id string, storageID *string) error {
	_, err := s.db.Exec(
		`UPDATE versions SET is_latest = CASE WHEN id = ? THEN 1 ELSE 0 END
		 WHERE udid = ? AND storage_id IS ?`,
		id, udid, nullStr(storageID))
	return err
}

// ListVersions returns versions for a udid ("" = all), newest first. Missing rows are
// included (state honesty — the UI shows them as gone, never silently dropped).
func (s *Store) ListVersions(udid string) ([]VersionRow, error) {
	var (
		rows *sql.Rows
		err  error
	)
	const sel = `SELECT id, udid, backend, zfs_snapshot, created_at, job_id, kind, encrypted,
		is_latest, structure_verified_at, content_verified_at, logical_bytes, physical_bytes, missing,
		storage_id
		FROM versions`
	if udid == "" {
		rows, err = s.db.Query(sel + ` ORDER BY created_at DESC, id DESC`)
	} else {
		rows, err = s.db.Query(sel+` WHERE udid = ? ORDER BY created_at DESC, id DESC`, udid)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []VersionRow
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UDIDsWithVersions returns the distinct UDIDs that have at least one version row (missing or not),
// newest-activity first. It is the offline-device set (qn.6a): a device is remembered because it has
// backups, even when no muxer currently sees it. Missing rows are included — a device whose artifacts
// all vanished still has a history the user should see, rendered dead.
func (s *Store) UDIDsWithVersions() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT udid FROM versions GROUP BY udid ORDER BY MAX(created_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetVersion returns one version by id; ok=false when absent.
func (s *Store) GetVersion(id string) (VersionRow, bool, error) {
	row := s.db.QueryRow(`SELECT id, udid, backend, zfs_snapshot, created_at, job_id, kind,
		encrypted, is_latest, structure_verified_at, content_verified_at, logical_bytes,
		physical_bytes, missing, storage_id FROM versions WHERE id = ?`, id)
	v, err := scanVersion(row)
	if err == sql.ErrNoRows {
		return VersionRow{}, false, nil
	}
	if err != nil {
		return VersionRow{}, false, err
	}
	return v, true, nil
}

// DeleteVersion removes a version row (the artifact deletion is the storage layer's job).
func (s *Store) DeleteVersion(id string) error {
	_, err := s.db.Exec(`DELETE FROM versions WHERE id = ?`, id)
	return err
}

// MarkVersionMissing flags (or clears) a row whose on-disk artifact reconciliation could not
// find — the row survives, honestly, rather than being dropped (design §5).
func (s *Store) MarkVersionMissing(id string, missing bool) error {
	_, err := s.db.Exec(`UPDATE versions SET missing = ? WHERE id = ?`, boolInt(missing), id)
	return err
}

// SetContentVerified stamps content_verified_at (set by a qn.8 unlock's canary decrypt).
func (s *Store) SetContentVerified(id string, t time.Time) error {
	_, err := s.db.Exec(`UPDATE versions SET content_verified_at = ? WHERE id = ?`,
		fmtTime(t), id)
	return err
}

// rowScanner is the shared surface of *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanVersion(sc rowScanner) (VersionRow, error) {
	var (
		v       VersionRow
		snap    sql.NullString
		created string
		job     sql.NullString
		enc     int
		latest  int
		sVer    sql.NullString
		cVer    sql.NullString
		missing int
		storage sql.NullString
	)
	if err := sc.Scan(&v.ID, &v.UDID, &v.Backend, &snap, &created, &job, &v.Kind, &enc, &latest,
		&sVer, &cVer, &v.LogicalBytes, &v.PhysicalBytes, &missing, &storage); err != nil {
		return VersionRow{}, err
	}
	v.ZFSSnapshot = strPtrOrNil(snap)
	v.JobID = strPtrOrNil(job)
	v.Encrypted = enc != 0
	v.IsLatest = latest != 0
	v.Missing = missing != 0
	v.StorageID = strPtrOrNil(storage)
	t, err := parseTime(created)
	if err != nil {
		return VersionRow{}, err
	}
	v.CreatedAt = t
	if v.StructureVerifiedAt, err = timePtrOrNil(sVer); err != nil {
		return VersionRow{}, err
	}
	if v.ContentVerifiedAt, err = timePtrOrNil(cVer); err != nil {
		return VersionRow{}, err
	}
	return v, nil
}

// --- nullable helpers ---

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return fmtTime(*p)
}

func strPtrOrNil(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func timePtrOrNil(n sql.NullString) (*time.Time, error) {
	if !n.Valid {
		return nil, nil
	}
	t, err := parseTime(n.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// StorageCounts is how many versions a storage holds and how many distinct devices they belong to.
// Both are for the storage-card surface (qn.6d gap A, ruled 2026-08-03).
type StorageCounts struct {
	Backups int
	Devices int
}

// CountVersionsByStorage returns per-storage counts keyed by storage_id, in ONE query.
//
// MISSING ROWS ARE INCLUDED, which is a ruled choice and not an oversight (qn.6d rung-ruled
// decision 3). It follows UDIDsWithVersions' reasoning — a version whose artifact has vanished is
// still history the user should see — and it deliberately differs from Slot.hasVersionFor, which
// EXCLUDES missing rows because "will the next backup be full" depends on a usable artifact. Two
// questions, two answers; the divergence is intentional.
//
// Rows with a NULL storage_id are omitted: unattributed means the server has not worked out which
// storage they belong to, and counting them against any storage would be a guess. A storage with no
// rows at all is absent from the map, so callers must treat a miss as zero rather than as unknown.
func (s *Store) CountVersionsByStorage() (map[string]StorageCounts, error) {
	rows, err := s.db.Query(
		`SELECT storage_id, COUNT(*), COUNT(DISTINCT udid) FROM versions
		 WHERE storage_id IS NOT NULL GROUP BY storage_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]StorageCounts)
	for rows.Next() {
		var id string
		var c StorageCounts
		if err := rows.Scan(&id, &c.Backups, &c.Devices); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

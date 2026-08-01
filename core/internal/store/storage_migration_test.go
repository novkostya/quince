package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// These are qn.6c gate G6. The claim is narrow and load-bearing: `0006_storage` is applied to a
// database that already holds committed versions, and **no existing row changes**.
//
// contracts.md's "breaking is cheap here" clause explicitly does NOT reach persisted state — a
// SQLite row behind a committed backup has no commit to ship with. So the property to assert is
// not "the migration runs" but "the migration ran and the data is untouched".

// openPre0006 builds a database at the 0005 schema — the shape a real install upgrading into
// qn.6c actually has — by applying the migrations and then undoing 0006. Undoing rather than
// hand-writing the old schema keeps this honest: a hand-rolled fixture drifts from what the
// earlier migrations really produced, and would then be testing a shape nobody ever ran.
func openPre0006(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quince.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.db.Exec(`DROP INDEX idx_versions_storage`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := st.db.Exec(`ALTER TABLE versions DROP COLUMN storage_id`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if _, err := st.db.Exec(`DROP TABLE storages`); err != nil {
		t.Fatalf("drop storages: %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM schema_migrations WHERE version = '0006_storage'`); err != nil {
		t.Fatalf("unrecord: %v", err)
	}
	return st, path
}

func TestMigration0006UpgradesAnExistingDatabaseWithoutTouchingItsRows(t *testing.T) {
	st, _ := openPre0006(t)

	// A version as a pre-qn.6c install would hold it.
	created := time.Date(2026, 7, 20, 3, 30, 0, 0, time.UTC)
	job := "01J0B0000000000000000000"
	snap := "rpool/x/00008140-000A1B2C3D4E5F60@quince-2026-07-20T03-30-01J0"
	want := VersionRow{
		ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
		Backend: "zfs", ZFSSnapshot: &snap, CreatedAt: created, JobID: &job,
		Kind: "full", Encrypted: true, IsLatest: true,
		LogicalBytes: 42400000000, PhysicalBytes: 3400000000,
	}
	if _, err := st.db.Exec(`INSERT INTO versions
		(id, udid, backend, zfs_snapshot, created_at, job_id, kind, encrypted, is_latest,
		 logical_bytes, physical_bytes, missing)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,0)`,
		want.ID, want.UDID, want.Backend, snap, fmtTime(created), job, want.Kind,
		1, 1, want.LogicalBytes, want.PhysicalBytes); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	if err := st.migrate(); err != nil {
		t.Fatalf("migrate to 0006: %v", err)
	}

	got, ok, err := st.GetVersion(want.ID)
	if err != nil || !ok {
		t.Fatalf("version gone after migrate: ok=%v err=%v", ok, err)
	}

	// Every pre-existing field is byte-for-byte what it was. This is the data-at-rest claim.
	if got.UDID != want.UDID || got.Backend != want.Backend || got.Kind != want.Kind ||
		got.Encrypted != want.Encrypted || got.IsLatest != want.IsLatest ||
		got.LogicalBytes != want.LogicalBytes || got.PhysicalBytes != want.PhysicalBytes ||
		got.Missing {
		t.Errorf("migration altered an existing row:\n got %+v\nwant %+v", got, want)
	}
	if got.ZFSSnapshot == nil || *got.ZFSSnapshot != snap {
		t.Errorf("zfs_snapshot changed: %v", got.ZFSSnapshot)
	}
	if got.JobID == nil || *got.JobID != job {
		t.Errorf("job_id changed: %v", got.JobID)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("created_at changed: %v want %v", got.CreatedAt, created)
	}

	// And the new column is NULL — not "", not a fabricated id. `null` means NOT YET ATTRIBUTED
	// (Operator ruling 2026-08-01): the storage's UUID lives in its quince-storage.json, which is
	// written at a later rung's creation moment, so there is nothing truthful to backfill with.
	if got.StorageID != nil {
		t.Errorf("storage_id must be NULL for a pre-qn.6c version, got %q", *got.StorageID)
	}
}

func TestMigration0006IsAdditiveOnly(t *testing.T) {
	st, _ := openPre0006(t)

	before := columns(t, st, "versions")
	if err := st.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	after := columns(t, st, "versions")

	// Every pre-existing column survives with the same name, type and NOT NULL/default — nothing
	// renamed, retyped, reordered or dropped. Compared by PRAGMA rather than by the stored schema
	// TEXT, because SQLite rewrites that string on its own for reasons unrelated to the migration
	// (the first version of this test compared strings and failed on exactly that).
	for name, def := range before {
		got, ok := after[name]
		if !ok {
			t.Errorf("column %q disappeared", name)
			continue
		}
		if got != def {
			t.Errorf("column %q changed definition: %q → %q", name, def, got)
		}
	}
	// ...and exactly one column is new.
	if len(after) != len(before)+1 {
		t.Errorf("want exactly one new column, got %d → %d", len(before), len(after))
	}
	if _, ok := after["storage_id"]; !ok {
		t.Error("storage_id was not added")
	}
	if tableSQL(t, st, "storages") == "" {
		t.Error("storages table was not created")
	}
}

// columns reports name → "type|notnull|default" for a table, which is enough to catch a rename,
// retype, or a NOT NULL/default change on an existing column.
func columns(t *testing.T, st *Store, table string) map[string]string {
	t.Helper()
	rows, err := st.db.Query(`SELECT name, type, "notnull", ifnull(dflt_value,'') FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%q): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var name, typ, notNull, dflt string
		if err := rows.Scan(&name, &typ, &notNull, &dflt); err != nil {
			t.Fatal(err)
		}
		out[name] = typ + "|" + notNull + "|" + dflt
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMigration0006IsIdempotent guards the property migrate() already claims but that a new
// ALTER is the most likely thing to break: re-running must not error on a duplicate column.
func TestMigration0006IsIdempotent(t *testing.T) {
	st := openTemp(t) // already fully migrated
	if err := st.migrate(); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}

func tableSQL(t *testing.T, st *Store, name string) string {
	t.Helper()
	var s string
	err := st.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&s)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("read schema for %q: %v", name, err)
	}
	return s
}

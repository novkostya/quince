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
//
// It undoes 0006 ONLY, so every later migration stays recorded and the `migrate()` under test
// applies exactly one. That scoping is load-bearing: both tests below assert a property OF 0006
// ("no row changed", "exactly one new column"), and 0010 drops a column — left pending, it would
// be measured as 0006's doing and the second test would fail on a claim it never made. 0010 has
// its own test, which reconstructs its own predecessor shape the same way.
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
		LogicalBytes: 42400000000,
	}
	if _, err := st.db.Exec(`INSERT INTO versions
		(id, udid, backend, zfs_snapshot, created_at, job_id, kind, encrypted, is_latest,
		 logical_bytes, missing)
		VALUES (?,?,?,?,?,?,?,?,?,?,0)`,
		want.ID, want.UDID, want.Backend, snap, fmtTime(created), job, want.Kind,
		1, 1, want.LogicalBytes); err != nil {
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
		got.LogicalBytes != want.LogicalBytes || got.Missing {
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

// openPre0010 builds a database at the 0009 schema by applying the migrations and then putting
// `physical_bytes` back, exactly as 0002 declared it. Same discipline as openPre0006: reconstruct
// by undoing rather than by hand-writing a schema nobody ran.
//
// THIS FIXTURE IS ALSO THE GUARD, AND THAT IS THE POINT OF THE CHECK BELOW (quince#1049). `Open()`
// runs every migration, 0010 included, so by the time this function resumes the column must already
// be gone. If 0010 stopped dropping it, the test's own
// `if _, ok := after["physical_bytes"]; ok` would never be reached — the failure lands HERE, two
// functions from the assertion that names the claim.
//
// So the check is explicit rather than left to the raw `ALTER TABLE` error. A bare re-add does fail
// (`duplicate column name`), which is why the protection is real today; it fails as a SQL error
// about a fixture, which reads like a broken test rather than like the migration having stopped
// working. **Do not make this tolerant of an absent drop** — an `IF NOT EXISTS`, or hand-writing the
// pre-0010 schema instead of undoing, would silently retire this test's primary claim while leaving
// its assertion looking load-bearing.
func openPre0010(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "quince.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, ok := columns(t, st, "versions")["physical_bytes"]; ok {
		t.Fatal("0010 did not drop versions.physical_bytes — the migration under test is a no-op, " +
			"and this fixture is what noticed rather than the assertion named for it (quince#1049)")
	}
	if _, err := st.db.Exec(`ALTER TABLE versions ADD COLUMN physical_bytes INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("re-add column: %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM schema_migrations WHERE version = '0010_drop_physical_bytes'`); err != nil {
		t.Fatalf("unrecord: %v", err)
	}
	return st
}

// quince#442. 0010 drops a column that HOLDS DATA on every existing install, so the claim is the
// same one G6 makes for 0006 read from the other side: the column goes, and nothing else moves.
// A version row behind a committed backup has no commit to ship with, so "breaking is cheap here"
// does not reach it (contracts.md) — a migration that took a neighbouring value with it would be
// destroying the record of a real backup.
func TestMigration0010DropsPhysicalBytesAndTouchesNothingElse(t *testing.T) {
	st := openPre0010(t)

	created := time.Date(2026, 7, 20, 3, 30, 0, 0, time.UTC)
	job := "01J0B0000000000000000000"
	want := VersionRow{
		ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
		Backend: "reflink", CreatedAt: created, JobID: &job,
		Kind: "full", Encrypted: true, IsLatest: true, LogicalBytes: 42400000000,
	}
	// physical_bytes seeded with the value a real install holds: THE SAME NUMBER AS logical_bytes,
	// because both came from one dirSize walk. That equality is the bug, and seeding a distinct
	// number would let a mix-up between the two columns pass unnoticed here.
	if _, err := st.db.Exec(`INSERT INTO versions
		(id, udid, backend, created_at, job_id, kind, encrypted, is_latest,
		 logical_bytes, physical_bytes, missing)
		VALUES (?,?,?,?,?,?,?,?,?,?,0)`,
		want.ID, want.UDID, want.Backend, fmtTime(created), job, want.Kind,
		1, 1, want.LogicalBytes, want.LogicalBytes); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	before := columns(t, st, "versions")
	if err := st.migrate(); err != nil {
		t.Fatalf("migrate to 0010: %v", err)
	}
	after := columns(t, st, "versions")

	if _, ok := after["physical_bytes"]; ok {
		t.Error("physical_bytes survived the migration")
	}
	if len(after) != len(before)-1 {
		t.Errorf("want exactly one column removed, got %d → %d", len(before), len(after))
	}
	for name, def := range before {
		if name == "physical_bytes" {
			continue
		}
		got, ok := after[name]
		if !ok {
			t.Errorf("column %q disappeared alongside physical_bytes", name)
			continue
		}
		if got != def {
			t.Errorf("column %q changed definition: %q → %q", name, def, got)
		}
	}

	got, ok, err := st.GetVersion(want.ID)
	if err != nil || !ok {
		t.Fatalf("version gone after migrate: ok=%v err=%v", ok, err)
	}
	if got.UDID != want.UDID || got.Backend != want.Backend || got.Kind != want.Kind ||
		got.Encrypted != want.Encrypted || got.IsLatest != want.IsLatest ||
		got.LogicalBytes != want.LogicalBytes || got.Missing {
		t.Errorf("migration altered an existing row:\n got %+v\nwant %+v", got, want)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("created_at changed: %v want %v", got.CreatedAt, created)
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

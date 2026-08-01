package store

import (
	"testing"
	"time"
)

func ts(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
}

func TestStorageUpsertAndGet(t *testing.T) {
	st := openTemp(t)
	if _, ok, err := st.GetStorage("local"); err != nil || ok {
		t.Fatalf("unknown storage must be ok=false: ok=%v err=%v", ok, err)
	}

	id, backend := "01JS00000000000000000000", "zfs"
	created := ts(t)
	if err := st.UpsertStorage(StorageRow{
		Name: "local", StorageID: &id, Backend: &backend, Path: "/backups",
		CreatedAt: &created, SeenAt: created,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, ok, err := st.GetStorage("local")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.StorageID == nil || *got.StorageID != id || got.Backend == nil || *got.Backend != backend {
		t.Errorf("identity lost: %+v", got)
	}
	if got.Path != "/backups" || !got.SeenAt.Equal(created) {
		t.Errorf("unexpected row: %+v", got)
	}
}

// The identity is FROZEN at creation. An upsert that could blank storage_id or backend would make
// a later startup treat a known storage as new — which is precisely the unmounted-mountpoint bug
// this table exists to prevent, arriving through the write path instead of the read path.
func TestStorageUpsertNeverClearsAFrozenIdentity(t *testing.T) {
	st := openTemp(t)
	id, backend := "01JS00000000000000000000", "zfs"
	created := ts(t)
	if err := st.UpsertStorage(StorageRow{
		Name: "local", StorageID: &id, Backend: &backend, Path: "/backups",
		CreatedAt: &created, SeenAt: created,
	}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}

	// A later sighting that knows only the path — e.g. the medium was absent, so nothing was read.
	later := created.Add(time.Hour)
	if err := st.UpsertStorage(StorageRow{Name: "local", Path: "/mnt/moved", SeenAt: later}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	got, _, err := st.GetStorage("local")
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageID == nil || *got.StorageID != id {
		t.Errorf("storage_id was cleared by a later upsert: %+v", got.StorageID)
	}
	if got.Backend == nil || *got.Backend != backend {
		t.Errorf("backend was cleared by a later upsert: %+v", got.Backend)
	}
	if got.CreatedAt == nil || !got.CreatedAt.Equal(created) {
		t.Errorf("created_at moved: %+v", got.CreatedAt)
	}
	// Path and seen_at DO move — a path is informational and a disk can be remounted elsewhere.
	if got.Path != "/mnt/moved" || !got.SeenAt.Equal(later) {
		t.Errorf("path/seen_at should track the latest sighting: %+v", got)
	}
}

func TestListStorages(t *testing.T) {
	st := openTemp(t)
	now := ts(t)
	for _, n := range []string{"usb", "local"} {
		if err := st.UpsertStorage(StorageRow{Name: n, Path: "/" + n, SeenAt: now}); err != nil {
			t.Fatalf("upsert %s: %v", n, err)
		}
	}
	rows, err := st.ListStorages()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "local" || rows[1].Name != "usb" {
		t.Errorf("want name order, got %+v", rows)
	}
}

// --- the no-permanent-nulls gate the ruling made MANDATORY ---
//
// `storage_id` null means "not yet attributed" and is TRANSITIONAL. A nullable-with-meaning field
// whose meaning is "temporary" decays into a permanent unknown unless something asserts otherwise.
// These are that something.

func seedVersion(t *testing.T, st *Store, id, udid string) {
	t.Helper()
	if err := st.InsertVersion(VersionRow{
		ID: id, UDID: udid, Backend: "zfs", CreatedAt: ts(t), Kind: "full",
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestAttributeVersionsFillsOnlyNulls(t *testing.T) {
	st := openTemp(t)
	seedVersion(t, st, "01JV1", "DEV-A")
	seedVersion(t, st, "01JV2", "DEV-A")
	seedVersion(t, st, "01JV3", "DEV-B")

	if n, err := st.CountUnattributedVersions(); err != nil || n != 3 {
		t.Fatalf("precondition: want 3 unattributed, got %d (%v)", n, err)
	}

	n, err := st.AttributeVersions("DEV-A", "01JSTORAGE-A")
	if err != nil {
		t.Fatalf("attribute: %v", err)
	}
	if n != 2 {
		t.Errorf("want 2 rows attributed, got %d", n)
	}

	// Re-running must be a no-op: an already-attributed version is a fact about where a committed
	// backup lives, and this runs on every startup.
	again, err := st.AttributeVersions("DEV-A", "01JSTORAGE-DIFFERENT")
	if err != nil {
		t.Fatalf("re-attribute: %v", err)
	}
	if again != 0 {
		t.Errorf("attribution must never overwrite; %d rows were rewritten", again)
	}
	v, _, err := st.GetVersion("01JV1")
	if err != nil {
		t.Fatal(err)
	}
	if v.StorageID == nil || *v.StorageID != "01JSTORAGE-A" {
		t.Errorf("an attributed version was reattributed: %v", v.StorageID)
	}

	// DEV-B is untouched, so the count reflects real remaining work rather than going quiet.
	if n, err := st.CountUnattributedVersions(); err != nil || n != 1 {
		t.Errorf("want 1 still unattributed, got %d (%v)", n, err)
	}
}

func TestCountUnattributedReachesZero(t *testing.T) {
	st := openTemp(t)
	seedVersion(t, st, "01JV1", "DEV-A")
	if _, err := st.AttributeVersions("DEV-A", "01JSTORAGE-A"); err != nil {
		t.Fatal(err)
	}
	n, err := st.CountUnattributedVersions()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the transitional null must be able to reach zero; %d remain", n)
	}
}

// --- is_latest is per (udid, storage) — Operator ruling 2026-08-01, quince#378 ---

func insertVer(t *testing.T, st *Store, id, udid string, storageID *string, at time.Time) {
	t.Helper()
	if err := st.InsertVersion(VersionRow{
		ID: id, UDID: udid, Backend: "copy", Kind: "full", CreatedAt: at, StorageID: storageID,
	}); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func latestIDs(t *testing.T, st *Store, udid string) []string {
	t.Helper()
	rows, err := st.ListVersions(udid)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.IsLatest {
			out = append(out, r.ID)
		}
	}
	return out
}

// THE ASSERTION THE RULING EXISTS TO PROTECT: one device, two storages, BOTH flagged latest.
//
// The architect expected this could not be written until the registry lands. It can at this layer
// — what needs the registry is the browse_root half, which is asserted with story 3's G1. This is
// the half that pins the SQL, and it is the half that would silently regress.
func TestPromoteLatestIsScopedToTheStorage(t *testing.T) {
	st := openTemp(t)
	a, b := "01JSTORAGE-A", "01JSTORAGE-B"
	base := ts(t)

	insertVer(t, st, "01VA1", "DEV", &a, base)
	insertVer(t, st, "01VB1", "DEV", &b, base.Add(time.Hour)) // newer, different storage

	if err := st.PromoteLatest("DEV", "01VA1", &a); err != nil {
		t.Fatal(err)
	}
	if err := st.PromoteLatest("DEV", "01VB1", &b); err != nil {
		t.Fatal(err)
	}

	got := latestIDs(t, st, "DEV")
	if len(got) != 2 {
		t.Fatalf("a device on two storages must have TWO latest rows, one each; got %v", got)
	}

	// And the load-bearing negative: promoting on B must NOT demote A. Before the storage clause
	// this is exactly what happened, and A's newest version then resolved browse_root to a
	// versions/<ts>/ directory that does not exist.
	if err := st.PromoteLatest("DEV", "01VB1", &b); err != nil {
		t.Fatal(err)
	}
	if got := latestIDs(t, st, "DEV"); len(got) != 2 {
		t.Errorf("promoting on storage B demoted storage A's latest; got %v", got)
	}
}

// The NULL group, which the ruling settled toward `IS ?` rather than exclusion: a pre-qn.6c row
// must still get a latest AMONG ITS OWN GROUP. Excluding NULLs would leave such a device with no
// latest at all — the same unresolvable browse_root, reached from the other side.
func TestPromoteLatestGivesUnattributedRowsTheirOwnLatest(t *testing.T) {
	st := openTemp(t)
	attributed := "01JSTORAGE-A"
	base := ts(t)

	insertVer(t, st, "01VNULL1", "DEV", nil, base)
	insertVer(t, st, "01VNULL2", "DEV", nil, base.Add(time.Hour))
	insertVer(t, st, "01VATTR1", "DEV", &attributed, base.Add(2*time.Hour))

	if err := st.PromoteLatest("DEV", "01VNULL2", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.PromoteLatest("DEV", "01VATTR1", &attributed); err != nil {
		t.Fatal(err)
	}

	got := latestIDs(t, st, "DEV")
	if len(got) != 2 {
		t.Fatalf("the NULL group and the attributed group each get a latest; got %v", got)
	}

	// `storage_id = ?` never matches NULL, so a naive fix would silently promote nothing here and
	// leave the device with no resolvable newest version at all.
	v, _, err := st.GetVersion("01VNULL2")
	if err != nil {
		t.Fatal(err)
	}
	if !v.IsLatest {
		t.Error("an unattributed row must be promotable within the NULL group (IS, not =)")
	}
}

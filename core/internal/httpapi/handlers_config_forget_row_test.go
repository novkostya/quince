package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// quince#1525: `ForgetStorage` spliced the entry out of `config.yml` and nothing removed the
// `storages` row, so `ResolveStorage` — which asks the DB first on every path — kept answering
// Known for a storage nobody declared. A reachable path with no marker plus a known row is MISSING
// MEDIUM, which refuses, so the path stayed claimed and could not be re-added from any interface.
//
// THE ASSERTION IS ON `GetStorage`, NOT ON THE RESPONSE, because ok=false is the only state that
// permits a creation moment. A 200 that left the row behind is exactly the bug.
func TestForgetReleasesTheStorageRowSoThePathCanBeReAdded(t *testing.T) {
	d := testDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	id, backend, when := "01JS00000000000000000000", "copy", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := d.Store.UpsertStorage(store.StorageRow{
		Name: "shuttle", StorageID: &id, Backend: &backend, Path: "/mnt/shuttle",
		CreatedAt: &when, SeenAt: when,
	}); err != nil {
		t.Fatalf("seeding the row: %v", err)
	}
	// The control: the row must be there, or a pass below proves nothing.
	if _, ok, err := d.Store.GetStorage("shuttle"); err != nil || !ok {
		t.Fatalf("control failed — no row to release: ok=%v err=%v", ok, err)
	}

	if code, body := deleteStorage(t, srv, c, "shuttle"); code != http.StatusOK {
		t.Fatalf("forget = %d, want 200: %s", code, body)
	}

	if _, ok, err := d.Store.GetStorage("shuttle"); err != nil || ok {
		t.Fatalf("the row survived the forget — quince#1525: ok=%v err=%v", ok, err)
	}
}

// The guard: a storage whose backups are still recorded must NOT be forgotten, because
// `versions.storage_id` joins against its marker UUID and removing the row would orphan that join
// — silently detaching a real backup history from the disk holding it.
//
// AND THE REFUSAL MUST NAME WHAT BLOCKS IT (Operator, quince#940). A bare "cannot forget" leaves a
// user with no next action; the counts are what decide what they do.
func TestForgetIsRefusedWhileVersionsReferenceTheStorage(t *testing.T) {
	d := testDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	id, backend, when := "01JS00000000000000000000", "copy", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := d.Store.UpsertStorage(store.StorageRow{
		Name: "shuttle", StorageID: &id, Backend: &backend, Path: "/mnt/shuttle",
		CreatedAt: &when, SeenAt: when,
	}); err != nil {
		t.Fatalf("seeding the row: %v", err)
	}
	for _, v := range []store.VersionRow{
		{ID: "01V1", UDID: "U1", Backend: "copy", CreatedAt: when, StorageID: &id},
		{ID: "01V2", UDID: "U2", Backend: "copy", CreatedAt: when.Add(time.Hour), StorageID: &id},
	} {
		if err := d.Store.InsertVersion(v); err != nil {
			t.Fatalf("seeding a version: %v", err)
		}
	}

	code, body := deleteStorage(t, srv, c, "shuttle")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("forget with versions = %d, want 422: %s", code, body)
	}
	for _, want := range []string{"2 backups", "2 devices", "Nothing on the disk is deleted"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the refusal does not name %q — it must say what blocks it: %s", want, body)
		}
	}

	// AND THE REFUSAL MUST LEAVE BOTH SIDES ALONE. A 422 that had already written the config, or
	// already dropped the row, would be the worst of both.
	if _, ok, err := d.Store.GetStorage("shuttle"); err != nil || !ok {
		t.Fatalf("a refused forget dropped the row anyway: ok=%v err=%v", ok, err)
	}
}

// The neighbouring case, and it is the one a user most wants to forget: a storage quince has
// DECLARED but never reached carries a row with a nil StorageID, so no version can reference it.
// A guard that read a missing count as "unknown, refuse" would block exactly that.
func TestForgetIsAllowedForAStorageQuinceHasNeverReached(t *testing.T) {
	d := testDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	when := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := d.Store.UpsertStorage(store.StorageRow{
		Name: "shuttle", Path: "/mnt/shuttle", SeenAt: when,
	}); err != nil {
		t.Fatalf("seeding the row: %v", err)
	}

	if code, body := deleteStorage(t, srv, c, "shuttle"); code != http.StatusOK {
		t.Fatalf("forget of a never-reached storage = %d, want 200: %s", code, body)
	}
	if _, ok, err := d.Store.GetStorage("shuttle"); err != nil || ok {
		t.Fatalf("the row survived: ok=%v err=%v", ok, err)
	}
}

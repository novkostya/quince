package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/store"
)

// quince#652 — resolveSlot IS THE SEAM THE FIX HAS TO CROSS, so it is asserted here rather than only
// at ResolveStorage.
//
// WHY THIS FILE EXISTS AT ALL, stated because the alternative looked sufficient and was not. The
// first version of this fix's coverage asserted at the wire — build a Manager, append an unreachable
// Slot, read `Storages()` and check the counts. That test PASSES ON THE UNFIXED CODE: it constructs
// the Slot with `StorageID` already set, so it exercises how `storageToWire` keys the count map and
// never touches the resolver that was losing the id. Mutation testing caught it; reading it did not.
//
// It is kept, in unreachable_test.go, because keying-by-id is a real regression to guard. But the
// claim "an unplugged disk keeps its identity" lives here, on the path a real one takes:
// config entry → resolveSlot → ResolveStorage → the DB → Slot.

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedKnownStorage records the storage the way a successful creation would have, so the DB knows an
// id for this name — which is the whole premise: SQLite is reachable when the disk is not.
func seedKnownStorage(t *testing.T, st *store.Store, name, path, id string) {
	t.Helper()
	sid, backend := id, storage.BackendCopy
	if err := st.UpsertStorage(store.StorageRow{
		Name: name, StorageID: &sid, Backend: &backend, Path: path,
		SeenAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed storage row: %v", err)
	}
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// THE REPORTED CASE. An unplugged USB whose mountpoint still exists is a readable DIRECTORY, so
// reachable() passes and the failure comes from the marker read. Staged here by making the marker
// path a directory, which fails with neither ErrNotExist nor a checksum error — the limb that
// returns an error, and the limb the Operator's `input/output error` came down.
func TestResolveSlotKeepsTheIDWhenTheMarkerCannotBeRead(t *testing.T) {
	st := testStore(t)
	root := t.TempDir()
	const want = "01JUSB000000000000000000"
	seedKnownStorage(t, st, "usb", root, want)

	if err := os.Mkdir(filepath.Join(root, storage.StorageMarkerName), 0o755); err != nil {
		t.Fatalf("mkdir marker-as-dir: %v", err)
	}

	slot := resolveSlot(context.Background(), config.StorageEntry{Name: "usb", Path: root}, st, quietLog())

	if slot.Reachable {
		t.Fatal("an unreadable marker must not resolve as reachable")
	}
	if slot.StorageID != want {
		t.Errorf("StorageID = %q, want %q — this is the join key for versions.storage_id, so losing "+
			"it makes the storage page report 0 backups and claim quince has never reached the path",
			slot.StorageID, want)
	}
}

// The mountpoint itself is gone: reachable() refuses before any marker read. Different limb, same
// requirement.
func TestResolveSlotKeepsTheIDWhenThePathIsGone(t *testing.T) {
	st := testStore(t)
	gone := filepath.Join(t.TempDir(), "not-mounted")
	const want = "01JUSB000000000000000000"
	seedKnownStorage(t, st, "usb", gone, want)

	slot := resolveSlot(context.Background(), config.StorageEntry{Name: "usb", Path: gone}, st, quietLog())

	if slot.Reachable {
		t.Fatal("a path that does not exist must not resolve as reachable")
	}
	if slot.StorageID != want {
		t.Errorf("StorageID = %q, want %q", slot.StorageID, want)
	}
}

// THE OTHER HALF, and the reason this is a narrowing rather than a widening: a storage the DB has
// never known still carries no id. "" keeps meaning NEVER CREATED, which is what the UI renders as
// "quince has never reached this path" — a sentence that is only safe to print because of this test.
func TestResolveSlotInventsNoIDForAStorageItHasNeverKnown(t *testing.T) {
	st := testStore(t)
	gone := filepath.Join(t.TempDir(), "not-mounted")

	slot := resolveSlot(context.Background(), config.StorageEntry{Name: "usb", Path: gone}, st, quietLog())

	if slot.StorageID != "" {
		t.Errorf("StorageID = %q, want empty — fabricating an identity for a disk quince has never "+
			"seen would make the UI claim a history that does not exist", slot.StorageID)
	}
}

// qn.6e PR 9a — ZERO STORAGES IS A LEGITIMATE STARTUP STATE.
//
// `buildStorage` used to return an error here, with a comment calling the case "unreachable past
// config.CheckStorages … if it ever gets here the guard upstream has stopped working". The Operator
// ruling of 2026-08-07 (option (a)) makes it reachable on purpose: a first run has no `storage:` key
// at all, and quince serves so the storage can be added from the UI.
//
// THIS IS THE ASSERTION THAT WOULD HAVE CAUGHT THE HALF-DONE VERSION. Relaxing only `main.go` moves
// the exit here — same dead daemon, different error string — so the claim worth gating is that a
// Manager BUILDS and ANSWERS on zero, not merely that the startup check was loosened.
func TestBuildStorageServesWithNoStoragesDeclared(t *testing.T) {
	cfgSvc := config.NewService(filepath.Join(t.TempDir(), "config.yml"), quietLog())
	st := testStore(t)

	mgr, err := buildStorage(context.Background(), config.Bootstrap{}, cfgSvc, st, nil, quietLog())
	if err != nil {
		t.Fatalf("buildStorage refused an empty declaration: %v — zero storages is the first-run "+
			"state since qn.6e, not a configuration error", err)
	}
	if mgr == nil {
		t.Fatal("buildStorage returned no manager and no error")
	}

	// AND IT ANSWERS HONESTLY on zero rather than merely existing. These are qn.6g's empty-list
	// guards, re-asserted through the constructor this rung newly routes into them.
	if got := mgr.Storages(""); len(got) != 0 {
		t.Errorf("Storages() on an empty manager returned %d entries", len(got))
	}
	// A job that names nowhere to go is REFUSED, which is what keeps "serving" from meaning
	// "pretending to work".
	if _, status, _ := mgr.ResolveChoice(""); status != 409 {
		t.Errorf("ResolveChoice on an empty manager = %d, want 409", status)
	}
}

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

	mgr, _, err := buildStorage(context.Background(), config.Bootstrap{}, cfgSvc, st, nil, quietLog(), scanSynchronous)
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

// quince#569 — THE WIRE CODE IS TRANSLATED, NOT STRINGIFIED, AND THESE ARE THE PATHS THAT PROVE IT.
//
// WHY THESE TESTS AND NOT THE TWO THAT ALREADY EXISTED. `reset_test.go` and `StorageSelect.test.tsx`
// both pinned `path_unreachable` and both CONSTRUCTED the slot by hand, so they asserted the value
// the contract declares against a fixture their author wrote. Nothing drove a real unreadable
// directory to the wire, which is why a daemon emitting `unreachable` sat green for a whole rung.
// The architect's ruling makes that the condition on any fix: a real path, through resolveSlot, to a
// Slot. Reading the mapping function proves the mapping; only this proves what the daemon emits.
//
// Each case names the branch of ResolveStorage it takes, because the codes are only distinguishable
// by which limb produced them and a test that reached the wrong limb would still pass.

// A path that does not exist: ResolveStorage's FIRST branch, `!reachable(path)`, which is
// ResolutionUnreachable. THE MOST COMMON FAILURE IN THE WHOLE MODEL — an unplugged removable disk —
// and the one that shipped an undeclared code.
func TestResolveSlotEmitsPathUnreachableForAPathThatIsNotThere(t *testing.T) {
	st := testStore(t)
	gone := filepath.Join(t.TempDir(), "not-mounted")

	slot := resolveSlot(context.Background(), config.StorageEntry{Name: "usb", Path: gone}, st, quietLog())

	if slot.Reachable {
		t.Fatal("a path that does not exist must not resolve as reachable")
	}
	if slot.UnreachableCode != "path_unreachable" {
		t.Errorf("UnreachableCode = %q, want %q — the daemon emitted the internal Resolution instead "+
			"of the declared wire code, which is quince#569 returning. contracts.md, wire/objects.go "+
			"and ui/src/lib/types.ts all say path_unreachable; a client branching on the union gets no "+
			"match and falls through to a default for an unplugged disk",
			slot.UnreachableCode, "path_unreachable")
	}
}

// A marker that fails its own checksum: the ErrStorageMarkerCorrupt branch, ResolutionCorruptMarker.
// RULED A FOURTH CODE on 2026-08-02 rather than folded into path_unreachable, because the disk here
// is PRESENT AND READABLE and saying "the path cannot be read" about it would be false.
func TestResolveSlotEmitsCorruptMarkerForAMarkerThatFailsItsChecksum(t *testing.T) {
	st := testStore(t)
	root := t.TempDir()
	// A well-formed marker whose checksum does not match its contents. Written by hand rather than
	// through WriteStorageMarker, which would compute a VALID one — the corruption is the fixture.
	const bad = `{"storage_id":"01JUSB000000000000000000","backend":"copy",` +
		`"created_at":"2026-01-01T00:00:00Z","app_version":"0.0.0-dev","checksum":"not-the-real-sum"}`
	if err := os.WriteFile(filepath.Join(root, storage.StorageMarkerName), []byte(bad), 0o644); err != nil {
		t.Fatalf("stage a corrupt marker: %v", err)
	}

	slot := resolveSlot(context.Background(), config.StorageEntry{Name: "usb", Path: root}, st, quietLog())

	if slot.Reachable {
		t.Fatal("a storage whose marker failed its checksum must not resolve as reachable")
	}
	if slot.UnreachableCode != "corrupt_marker" {
		t.Errorf("UnreachableCode = %q, want %q — the disk is present and readable and quince simply "+
			"cannot confirm WHICH storage it is. The remedy differs from path_unreachable's *plug it "+
			"in* and is dangerous to guess at, so the two must stay distinguishable",
			slot.UnreachableCode, "corrupt_marker")
	}
}

// A readable path with NO marker, for a storage the DB already knows: the missing-medium branch.
// The control for the two above — it was already correct, so it is what shows the mapping did not
// break the codes that happened to agree with their internal spelling.
func TestResolveSlotStillEmitsMissingMediumForAKnownStorageWithNoMarker(t *testing.T) {
	st := testStore(t)
	root := t.TempDir() // reachable, empty: a bare mountpoint
	seedKnownStorage(t, st, "usb", root, "01JUSB000000000000000000")

	slot := resolveSlot(context.Background(), config.StorageEntry{Name: "usb", Path: root}, st, quietLog())

	if slot.Reachable {
		t.Fatal("a known storage with no marker must not resolve as reachable")
	}
	if slot.UnreachableCode != "missing_medium" {
		t.Errorf("UnreachableCode = %q, want %q", slot.UnreachableCode, "missing_medium")
	}
}

// THE DEFAULT, WHICH IS THE POINT OF MAPPING AT ALL. The ruling's condition is that an unmapped
// internal state produces a logged error and an OBVIOUSLY WRONG wire value — never `""` and never
// the nearest plausible neighbour, because a default that guesses reproduces quince#569 one layer
// down, with a UI rendering a confident wrong remedy instead of failing to match.
//
// The input is a Resolution that does not exist. That is the whole scenario: this arm exists for the
// SEVENTH value somebody adds to the enum without touching the three declaration sites.
func TestWireUnreachableCodeRefusesToGuessAtAnUnmappedResolution(t *testing.T) {
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelError}))

	got := wireUnreachableCode(storage.Resolution("a_state_nobody_declared"), "usb", log)

	if got == "" {
		t.Error(`UnreachableCode = "" — an empty code is indistinguishable from "reachable, no cause" ` +
			`on the wire, which is the silent fallthrough the ruling forbids by name`)
	}
	for _, plausible := range []string{"path_unreachable", "missing_medium", "backend_mismatch", "corrupt_marker"} {
		if got == plausible {
			t.Errorf("UnreachableCode = %q for an unmapped state — the default picked a plausible "+
				"neighbour, so a client renders a confident WRONG remedy and nobody learns the "+
				"vocabulary is incomplete", got)
		}
	}
	if got != "unmapped" {
		t.Errorf("UnreachableCode = %q, want %q — the declared sentinel", got, "unmapped")
	}
	// The log line is half the mechanism: the wire value says something is wrong, and only this says
	// WHICH state was missed, which is what a maintainer needs to fix it.
	if s := logged.String(); !strings.Contains(s, "a_state_nobody_declared") {
		t.Errorf("the error log does not name the unmapped resolution, so the wire value is the only "+
			"evidence and it does not say what was missed; got: %s", s)
	}
}

// AND THE MAPPING IS TOTAL OVER THE STATES THAT CAN REACH IT. Every Resolution that is not OK() must
// have an arm — this is what turns "somebody adds a seventh value" from a silent wire change into a
// failing test, which is the guarantee the boundary mapping was ruled FOR.
func TestEveryUnreachableResolutionHasADeclaredWireCode(t *testing.T) {
	declared := map[string]bool{
		"path_unreachable": true, "missing_medium": true, "backend_mismatch": true, "corrupt_marker": true,
	}
	// Enumerated by hand because Go has no way to range over a string-const enum. THAT IS THE WEAK
	// LINK AND IT IS NAMED: a value added to storage.Resolution and not added here is not caught by
	// this test. It is still worth having — it catches an arm DELETED from the switch, and it fails
	// loudly if a mapping starts returning something undeclared.
	for _, r := range []storage.Resolution{
		storage.ResolutionUnreachable, storage.ResolutionMissingMedium,
		storage.ResolutionBackendMismatch, storage.ResolutionCorruptMarker,
	} {
		if r.OK() {
			t.Errorf("%q reports OK() — it cannot reach the unreachable branch and should not be here", r)
			continue
		}
		got := wireUnreachableCode(r, "usb", quietLog())
		if !declared[got] {
			t.Errorf("wireUnreachableCode(%q) = %q, which is not a code declared in contracts.md, "+
				"wire/objects.go and ui/src/lib/types.ts", r, got)
		}
	}
}

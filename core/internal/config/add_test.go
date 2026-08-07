package config

import (
	"os"
	"strings"
	"testing"
)

// qn.6e PR 5a — config.AddStorage, Forget's mirror. The endpoint over it is a separate PR; this
// proves the splice, the refusals and what they do to the file.

func newEntry(over func(*StorageEntry)) StorageEntry {
	e := StorageEntry{Name: "second", Path: "/backups-b", Backend: "reflink"}
	if over != nil {
		over(&e)
	}
	return e
}

func TestAddStorageSplicesWithoutTouchingSiblings(t *testing.T) {
	// THE WHOLE REASON THIS IS NOT A FULL-DOCUMENT PUT: the existing entry carries `zfs:` and
	// `retention:` keys that no UI surface renders. A client reconstructing the list would drop
	// them; a server-side splice cannot.
	svc, path := serviceOver(t, `storage:
  - name: one
    path: /backups-a
    default: true
    backend: zfs
    zfs:
      parent_dataset: tank/one
      mode: hook
      hook_cmd: ssh h
    retention:
      keep_recent: 3
      keep_daily: 7
      keep_weekly: 2
`)

	outcome, errs, _, err := svc.AddStorage(newEntry(nil))
	if err != nil || outcome != AddDone {
		t.Fatalf("add refused: outcome=%v errs=%+v err=%v", outcome, errs, err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"tank/one", "hook_cmd", "keep_recent: 3", "keep_weekly: 2", "/backups-b"} {
		if !strings.Contains(got, want) {
			t.Errorf("THE SPLICE LOST %q — a sibling's unrendered keys must survive an add.\n%s", want, got)
		}
	}
}

// The first storage stays default; the added one does not steal it.
func TestAddStorageDoesNotStealTheDefault(t *testing.T) {
	svc, _ := serviceOver(t, oneGoodStorage)

	if _, errs, _, err := svc.AddStorage(newEntry(nil)); err != nil || len(errs) > 0 {
		t.Fatalf("add refused: %+v %v", errs, err)
	}
	list := *svc.Current().Storage
	if len(list) != 2 {
		t.Fatalf("want 2 storages, got %d", len(list))
	}
	if !list[0].Default || list[1].Default {
		t.Fatalf("the default moved: [0].Default=%v [1].Default=%v", list[0].Default, list[1].Default)
	}
}

// An entry claiming `default` is refused outright rather than silently ignored — accepting it and
// dropping the field would be the silent acceptance the hard rules forbid, and honouring it would
// re-point every backup that names no storage.
func TestAddStorageRefusesAClaimedDefault(t *testing.T) {
	svc, _ := serviceOver(t, oneGoodStorage)

	_, errs, _, err := svc.AddStorage(newEntry(func(e *StorageEntry) { e.Default = true }))
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) == 0 || errs[0].Path != "default" {
		t.Fatalf("want a refusal at `default`, got %+v", errs)
	}
}

// AN EMPTY BACKEND IS REFUSED, NOT DEFAULTED. Resolved() would turn it into `auto`, and the add flow
// exists to write the concrete backend it just showed. Defaulting it would hide a client bug and
// reintroduce `auto` as stored state through the back door (quince#502).
func TestAddStorageRefusesAnEmptyOrAutoBackend(t *testing.T) {
	svc, _ := serviceOver(t, oneGoodStorage)

	for _, backend := range []string{"", "auto", "nonsense"} {
		t.Run("backend="+backend, func(t *testing.T) {
			_, errs, _, err := svc.AddStorage(newEntry(func(e *StorageEntry) { e.Backend = backend }))
			if err != nil {
				t.Fatal(err)
			}
			if len(errs) == 0 || errs[0].Path != "backend" {
				t.Fatalf("backend %q was accepted or misreported: %+v", backend, errs)
			}
		})
	}
}

// The ADDED entry carries a concrete backend — asserted on the file, because that is this PR's
// claim.
func TestAddStorageWritesAConcreteBackendForTheAddedEntry(t *testing.T) {
	svc, path := serviceOver(t, oneGoodStorage)

	if _, errs, _, err := svc.AddStorage(newEntry(nil)); err != nil || len(errs) > 0 {
		t.Fatalf("add refused: %+v %v", errs, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "backend: reflink") {
		t.Fatalf("the concrete backend was not written:\n%s", b)
	}
}

// MEASURED, AND IT FALSIFIES A CLAIM THE qn.6e SPEC MAKES.
//
// The spec's reachable goal for `auto` reads: *"quince never writes `auto`; a human still may."*
// The first half is FALSE, and not because of the add path — `replaceLocked` marshals the whole
// RESOLVED document, so any save materialises Resolved()'s defaults for every entry, including ones
// the user never touched. A hand-written minimal `storage: - path: /backups` acquires
// `backend: auto` and `zfs.seed: auto` the first time anything saves the config.
//
// This test PINS THE ACTUAL BEHAVIOUR rather than asserting the spec's wording, so the next reader
// meets the truth instead of a claim. Nothing here is broken: the materialised value is identical in
// meaning to the omitted one, and `auto` is legal by ruling. What is wrong is the sentence, and the
// correction is owed to the spec rather than to this code.
//
// The intent behind that sentence IS satisfied: a storage ADDED through the flow records the
// concrete backend that was probed and shown, which is the property the rung actually wanted.
func TestSavingMaterialisesAutoForPreexistingEntries(t *testing.T) {
	svc, path := serviceOver(t, oneGoodStorage) // declares no `backend:` at all

	if _, errs, _, err := svc.AddStorage(newEntry(nil)); err != nil || len(errs) > 0 {
		t.Fatalf("add refused: %+v %v", errs, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "backend: auto") {
		// FAILS rather than skips, deliberately. A skip is a silent pass, and the point of this test
		// is that a change here obliges a spec correction: if quince genuinely stops materialising
		// `auto`, delete this test AND fix the qn.6e sentence in the same diff.
		t.Fatalf("quince no longer materialises `auto` on save. If that is deliberate, delete this "+
			"test and correct the qn.6e spec in the same diff — it claims quince never wrote it, "+
			"which was false when this test was written.\n%s", b)
	}
}

// Duplicate name and duplicate path are refused AT THE FIELD THE CALLER TYPED, not at an index in
// the merged list. Validate reports the same rule as `storage[i].name`, and a caller adding one
// entry cannot map `i` to their own input.
func TestAddStorageRefusesDuplicatesAtTheCallersField(t *testing.T) {
	svc, path := serviceOver(t, oneGoodStorage)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, errs, _, err := svc.AddStorage(newEntry(func(e *StorageEntry) { e.Name = "one" }))
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) == 0 || errs[0].Path != "name" {
		t.Fatalf("duplicate name: want a refusal at `name`, got %+v", errs)
	}

	_, errs, _, err = svc.AddStorage(newEntry(func(e *StorageEntry) { e.Path = "/backups-a" }))
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) == 0 || errs[0].Path != "path" {
		t.Fatalf("duplicate path: want a refusal at `path`, got %+v", errs)
	}

	// AND NOTHING WAS WRITTEN. A refusal that still writes is invisible from the return value.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("A REFUSED ADD WROTE THE FILE.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The name defaults to the path, so an unnamed entry can still collide with a storage named after
// that path — compared on the RESOLVED name for exactly that reason.
func TestAddStorageCatchesACollisionThroughTheNameDefault(t *testing.T) {
	svc, _ := serviceOver(t, `storage:
  - name: /backups-b
    path: /backups-a
    default: true
`)

	_, errs, _, err := svc.AddStorage(newEntry(func(e *StorageEntry) { e.Name = "" }))
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) == 0 || errs[0].Path != "name" {
		t.Fatalf("an unnamed entry colliding through the name default was not caught: %+v", errs)
	}
}

// A non-absolute path is refused here, matching the probe endpoint and validate.go — so the form's
// refusal, the probe's refusal and the config's refusal cannot disagree about one string.
func TestAddStorageRefusesARelativePath(t *testing.T) {
	svc, _ := serviceOver(t, oneGoodStorage)

	_, errs, _, err := svc.AddStorage(newEntry(func(e *StorageEntry) { e.Path = "relative/dir" }))
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) == 0 || errs[0].Path != "path" {
		t.Fatalf("want a refusal at `path`, got %+v", errs)
	}
}

// THE ADD INHERITS quince#683 RATHER THAN CARRYING A COPY. It writes through replaceLocked, so a
// colliding zfs parent dataset is refused by the check that lives there — which is what makes both
// doors one door.
func TestAddStorageInheritsTheParentDatasetCheck(t *testing.T) {
	svc, _ := serviceOver(t, `storage:
  - name: one
    path: /backups-a
    default: true
    backend: zfs
    zfs:
      parent_dataset: tank/shared
      mode: hook
      hook_cmd: ssh h
`)

	_, errs, _, err := svc.AddStorage(newEntry(func(e *StorageEntry) {
		e.Backend = "zfs"
		e.ZFS = ZFSConfig{ParentDataset: "tank/shared", Mode: "hook", HookCmd: "ssh h"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) == 0 {
		t.Fatalf("A COLLIDING PARENT DATASET WAS ACCEPTED through the add path — the inherited " +
			"check did not run, which means the add endpoint would need its own copy")
	}
	if !strings.Contains(errs[0].Message, "believe it owned it") {
		t.Errorf("the inherited refusal lost its reasoning: %+v", errs)
	}
}

// THE FIRST STORAGE, WHICH NO OTHER TEST IN THIS FILE ADDS.
//
// Every case above seeds `oneGoodStorage` and adds a SECOND storage — where an existing
// `default: true` already satisfies validateStorages' exactly-one-default rule. So all of them
// passed while the FIRST add was impossible, which is the one the entire first-run path is made of.
//
// The defect: AddStorage appended `entry.Resolved()`, which fills an entry's own defaults but not
// the rule that is a property of the LIST — a lone storage is default by implication, and that lives
// in ResolveStorages at parse time. A one-entry list assembled in memory therefore carried
// `Default: false` and was refused with "exactly one storage must be marked `default: true`".
//
// FOUND BY RUNNING A REAL STORAGELESS CONTAINER, not by reading the code. This test is that
// measurement brought back in-tree, and it FAILS on the unfixed version.
func TestAddStorageCanAddTheVeryFirstOne(t *testing.T) {
	// No `storage:` key at all — a fresh install, which is what quince now starts on.
	svc, path := serviceOver(t, "backup:\n  preferred_transport: usb\n")

	outcome, errs, _, err := svc.AddStorage(newEntry(nil))
	if err != nil {
		t.Fatal(err)
	}
	if outcome != AddDone || len(errs) > 0 {
		t.Fatalf("THE FIRST STORAGE WAS REFUSED: %+v — a fresh install cannot finish setup, which "+
			"is the whole of the first-run path", errs)
	}

	list := *svc.Current().Storage
	if len(list) != 1 {
		t.Fatalf("want 1 storage, got %d", len(list))
	}
	// AND IT IS THE DEFAULT, by implication rather than by the caller claiming it — `validateAddition`
	// refuses an entry that sets `default` itself, so quince has to infer it or nobody can.
	if !list[0].Default {
		t.Errorf("the only storage is not default; a backup naming no storage would resolve to nothing")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "default: true") {
		t.Errorf("the implied default was not written to the file:\n%s", b)
	}
}

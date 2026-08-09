package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/store"
)

// qn.6g PR 4 — the storage applier, asserted THROUGH THE REAL SEAM rather than by calling
// ApplyStorages directly.
//
// WHY THAT MATTERS HERE, and it is the reason this file is not three lines long: calling
// `storageMgr.ApplyStorages(rebuilt)` in a test proves the Manager, which PR 3 already proved. The
// claim of THIS PR is that a write to `config.yml` reaches the Manager — `Replace` → notify → the
// applier registered in `buildStorage` → `ApplyStorages` — so a test has to enter at the config
// service and leave at the storage list, with every link in between the real one. A test that
// skipped the seam would pass on a build where `Subscribe` was never called.
//
// NOTHING HERE ADDS A TEST-ONLY ACCESSOR TO `storage.Manager`. The first draft of this file wanted
// `SlotsForTest()` and `ReconcileCountForTest()`, and both would have asserted that the applier did
// some INTERNAL thing rather than that a user sees a different answer. Retention is therefore
// asserted through `Prune`, which is the only thing retention is for, and the hot-add reconcile
// through an adopted version appearing in `Versions`.

const applierUDID = "SYNTHETIC-UDID-6G4B" // udidPattern: [A-Za-z0-9-]{8,64}

// wiredStorage builds a config service over a real config.yml and the real storage Manager wired to
// it, exactly as `serve` does.
//
// It hands back the store as well, because two of these tests have to seed or read the registry
// directly: "what Prune kept" and "what a hot add adopted" are both registry facts, and neither is
// reachable from the config surface that triggers them.
func wiredStorage(t *testing.T, entries []config.StorageEntry) (*config.Service, *storage.Manager, *store.Store) {
	t.Helper()
	svc, mgr, _, st := wiredStorageMode(t, entries, scanSynchronous)
	return svc, mgr, st
}

// wiredStorageMode is wiredStorage with the scan mode exposed, handing back the RUNNER as well.
//
// `serve` is the only mode with both a runner and a live config surface, so the storage-added trigger
// is observable only under `scanDeferred` (qn.6i PR 4). Tests that assert what an ADD does need this;
// tests that assert what the applier does to the slot list do not, and keep the simpler helper.
func wiredStorageMode(t *testing.T, entries []config.StorageEntry, mode scanMode) (
	*config.Service, *storage.Manager, *storage.Runner, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")

	cfg := config.Default()
	cfg.Storage = &entries
	data, err := config.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal seed config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	cfgSvc := config.NewService(path, quietLog())
	st := testStore(t)
	mgr, runner, err := buildStorage(context.Background(), config.Bootstrap{}, cfgSvc, st, bus.New(), quietLog(), mode)
	if err != nil {
		t.Fatalf("buildStorage: %v", err)
	}
	return cfgSvc, mgr, runner, st
}

// entry is the short form a test writes; Resolved() fills the rest, the same way load does.
// `copy` rather than `auto` so no test depends on what the CI filesystem happens to support.
func entry(t *testing.T, name string, isDefault bool) config.StorageEntry {
	t.Helper()
	return config.StorageEntry{
		Name: name, Path: t.TempDir(), Default: isDefault, Backend: "copy",
	}.Resolved()
}

// storageNames is what the wire says, which is what a browser would see.
func storageNames(mgr *storage.Manager) []string {
	var out []string
	for _, s := range mgr.Storages("") {
		out = append(out, s.Name)
	}
	return out
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// replaceStorage writes a new storage list through the config service — the same call
// `PUT /api/config` makes — and fails on a refusal, since these tests are about what happens AFTER
// a valid write.
func replaceStorage(t *testing.T, svc *config.Service, entries []config.StorageEntry) {
	t.Helper()
	cfg := svc.Current()
	cfg.Storage = &entries
	errs, _, err := svc.Replace(cfg)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("Replace refused a config this test needs to be valid: %+v", errs)
	}
}

// G1 — a storage added through the config endpoint is served by the SAME process.
//
// Asserted on `Storages("")`, the wire read, rather than on the Manager's internals: "the user can
// see it" is the claim, and an internal slice that holds it while the wire does not would be the
// same defect wearing a different shape.
func TestAStorageAddedThroughConfigIsServedWithoutARestart(t *testing.T) {
	alpha := entry(t, "alpha", true)
	svc, mgr, _ := wiredStorage(t, []config.StorageEntry{alpha})

	if got := storageNames(mgr); has(got, "beta") {
		t.Fatalf("precondition: beta must not be present before the write; got %v", got)
	}

	beta := entry(t, "beta", false)
	replaceStorage(t, svc, []config.StorageEntry{alpha, beta})

	if got := storageNames(mgr); !has(got, "beta") {
		t.Errorf("after adding beta, Storages() = %v — a storage declared in config.yml and not "+
			"served is exactly the restart-required defect this rung removes", got)
	}
}

// G2 — a forgotten storage stops being served, AND ITS TREE IS UNTOUCHED.
//
// The filesystem assertion is not decoration. `never mutate a committed version` is the rule this
// rung comes nearest to breaking: a forget is a list edit, and an implementation that "cleaned up"
// the root would be catastrophic and would still pass an API-only test. So a file is written before
// and read after.
func TestAForgottenStorageStopsBeingServedAndItsTreeSurvives(t *testing.T) {
	alpha := entry(t, "alpha", true)
	beta := entry(t, "beta", false)
	svc, mgr, _ := wiredStorage(t, []config.StorageEntry{alpha, beta})

	canary := filepath.Join(beta.Path, "a-committed-backup-lives-here")
	if err := os.WriteFile(canary, []byte("do not touch"), 0o644); err != nil {
		t.Fatalf("seed canary: %v", err)
	}

	outcome, errs, _, err := svc.ForgetStorage("beta", nil)
	if err != nil {
		t.Fatalf("ForgetStorage: %v", err)
	}
	if outcome != config.ForgetDone {
		t.Fatalf("ForgetStorage(beta) = %v, errs %+v — want it to succeed", outcome, errs)
	}

	if got := storageNames(mgr); has(got, "beta") {
		t.Errorf("after forgetting beta, Storages() = %v — it is no longer declared and must no "+
			"longer be served", got)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Errorf("the forgotten storage's tree was disturbed: %v — forgetting a storage removes it "+
			"from a list and must touch no disk", err)
	}
}

// G3 — forgetting the default is still refused, and live-apply opened no path around it.
//
// Here rather than left to the config package's own suite because what is asserted is that THIS
// rung did not weaken it: with the applier wired, a refusal that leaked would take effect
// immediately rather than at the next restart.
func TestForgettingTheDefaultIsStillRefusedWithTheApplierWired(t *testing.T) {
	alpha := entry(t, "alpha", true)
	beta := entry(t, "beta", false)
	svc, mgr, _ := wiredStorage(t, []config.StorageEntry{alpha, beta})

	outcome, errs, _, err := svc.ForgetStorage("alpha", nil)
	if err != nil {
		t.Fatalf("ForgetStorage: %v", err)
	}
	if outcome != config.ForgetRefused {
		t.Fatalf("ForgetStorage(alpha) = %v, want ForgetRefused — alpha is the default", outcome)
	}
	if len(errs) == 0 {
		t.Error("a refusal with no error to render is a silent one")
	}
	if got := storageNames(mgr); !has(got, "alpha") {
		t.Errorf("Storages() = %v — a REFUSED forget must leave the storage served; anything else "+
			"means the applier ran on a write that never happened", got)
	}
}

// seedPrunableVersion inserts a quince-created, non-latest version — the only kind retention
// touches — ATTRIBUTED to a storage.
//
// The attribution is load-bearing and cost a run to learn. `Prune` will happily *select* an
// unattributed row, resolving it under the default slot's policy, and then `deleteVersion` refuses
// it `409`: *"not attributed to any configured storage, so quince cannot say which disk its data is
// on"*. Correct, and unrelated to this rung — but a test seeded with nil ids fails inside Prune for
// a reason that looks like the applier and is not.
//
// No artifact on disk, and that is sound rather than lazy: the copy backend's `DeleteArtifact` is
// `os.RemoveAll(dir)`, which succeeds on a path that was never there. What is under test is WHICH
// rows Prune selects, and a directory would not change that answer.
func seedPrunableVersion(t *testing.T, st *store.Store, storageID, id string, created time.Time) {
	t.Helper()
	job := "job-" + id
	if err := st.InsertVersion(store.VersionRow{
		ID: id, UDID: applierUDID, Backend: storage.BackendCopy, CreatedAt: created,
		JobID: &job, Kind: "full", Encrypted: true, IsLatest: false, StorageID: &storageID,
	}); err != nil {
		t.Fatalf("seed version %s: %v", id, err)
	}
}

// G4 — a retention edit reaches Prune with no restart.
//
// Retention rides the storage applier because `policyFor` reads it off the slot list, so this is
// also the assertion that the ride works. Note what the edit does NOT change: no name, no path, no
// backend, no order — so a `sameStorageDeclaration` comparing only those would skip the rebuild and
// this test would fail, which is the point of asserting it here rather than on the helper.
//
// The BEFORE prune is half the test. Without it, a bug that pruned everything under any policy
// would pass: the assertion is that the same call keeps three versions and then keeps one, with
// nothing between them but a config write.
func TestARetentionEditChangesWhatTheNextPruneKeeps(t *testing.T) {
	alpha := entry(t, "alpha", true)
	svc, mgr, st := wiredStorage(t, []config.StorageEntry{alpha})

	// The id quince minted for alpha when it claimed the (markerless) temp root at startup. Read
	// rather than invented: `policyFor` matches a version's storage_id against the SLOT's, so a
	// made-up id would fall through to the unattributed branch and prove nothing about the rebuild.
	alphaID, err := mgr.StorageIDByName("alpha")
	if err != nil {
		t.Fatalf("alpha has no storage id: %v", err)
	}

	// Three distinct ISO weeks, so KeepWeekly cannot rescue them once it is turned down.
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"01V000A", "01V000B", "01V000C"} {
		seedPrunableVersion(t, st, alphaID, id, base.AddDate(0, 0, -7*i))
	}

	if err := mgr.Prune(applierUDID); err != nil {
		t.Fatalf("prune under the default policy: %v", err)
	}
	if got := len(mgr.Versions(applierUDID)); got != 3 {
		t.Fatalf("under the default policy (keep_recent 10) Prune kept %d of 3 — the premise of "+
			"this test is that it keeps them all before the edit", got)
	}

	edited := alpha
	edited.Retention = &config.RetentionConfig{KeepRecent: 1, KeepDaily: 0, KeepWeekly: 0}
	replaceStorage(t, svc, []config.StorageEntry{edited})

	if err := mgr.Prune(applierUDID); err != nil {
		t.Fatalf("prune under the edited policy: %v", err)
	}
	if got := len(mgr.Versions(applierUDID)); got != 1 {
		t.Errorf("after editing retention to keep_recent 1, Prune kept %d versions, want 1 — "+
			"retention reaches Prune only through ApplyStorages, so a retention-only edit that does "+
			"not rebuild the slot list is invisible until a restart", got)
	}
}

// G7 — a storage added hot whose root ALREADY HOLDS committed backups shows them.
//
// The easiest thing in this PR to get wrong, and the spec says so: `ApplyStorages` alone leaves the
// new disk listed and EMPTY, so its history stays invisible until a restart — this rung's own
// defect one layer along. Startup runs Reconcile for exactly this reason; the applier has to too.
//
// The fixture is one directory and one marker, which is all `Scan` reads. `WriteMarker` rather than
// a hand-written JSON file: `ReadMarker` rejects a bad checksum by SKIPPING the directory, so a
// hand-rolled marker would make this test pass or fail for a reason that has nothing to do with
// the applier.
func TestAHotAddedStorageShowsTheBackupsItAlreadyHolds(t *testing.T) {
	alpha := entry(t, "alpha", true)
	svc, mgr, runner, _ := wiredStorageMode(t, []config.StorageEntry{alpha}, scanDeferred)
	runner.Start(context.Background())

	beta := entry(t, "beta", false)
	created := time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC)
	verDir := filepath.Join(beta.Path, applierUDID, "versions", created.Format("2006-01-02T15-04-05Z"))
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := storage.WriteMarker(verDir, storage.Marker{
		VersionID: "01VONBETA", UDID: applierUDID, Backend: storage.BackendCopy,
		CreatedAt: created.Format(time.RFC3339), Kind: "full", Encrypted: true,
		StructureVerifiedAt: created.Format(time.RFC3339), AppVersion: "test",
	}); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if got := len(mgr.Versions(applierUDID)); got != 0 {
		t.Fatalf("precondition: nothing is known about %s before beta is declared; got %d",
			applierUDID, got)
	}

	// THE PROMISE MOVED FROM `AT THE RESPONSE` TO `SHORTLY AFTER IT` (qn.6i PR 4, quince#715). The
	// applier used to run the scan inline, inside the HTTP handler and under `writeMu`; it now enqueues
	// one. So the test waits for the pass instead of reading straight after the write — and waits on
	// the runner rather than sleeping, because a sleep long enough to be reliable is slow and one short
	// enough to be fast is a flake.
	//
	// What must NOT change is the user-visible outcome: no restart. Contracts §6 now says *shortly
	// after* rather than *without a restart*, and this is what makes that wording true rather than a
	// hope.
	done := runner.WaitForPass()
	replaceStorage(t, svc, []config.StorageEntry{alpha, beta})
	<-done

	vs := mgr.Versions(applierUDID)
	if len(vs) != 1 {
		t.Fatalf("after declaring beta hot, Versions() = %d, want 1 — a disk that already holds "+
			"backups must not be listed with an empty history until quince restarts", len(vs))
	}
	if vs[0].ID != "01VONBETA" {
		t.Errorf("adopted version id = %q, want 01VONBETA", vs[0].ID)
	}
}

// A FORGET DOES NOT RECONCILE, which is the other half of the reconcile-on-add rule.
//
// Nothing was added, so there is nothing to scan, and walking every declared tree on every removal
// is work for no answer. Asserted by the observable that would change if it ran: a version sitting
// unadopted on the REMAINING storage stays unadopted.
func TestAForgetDoesNotTriggerAReconcile(t *testing.T) {
	alpha := entry(t, "alpha", true)
	beta := entry(t, "beta", false)
	svc, mgr, runner, _ := wiredStorageMode(t, []config.StorageEntry{alpha, beta}, scanDeferred)

	created := time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)
	verDir := filepath.Join(alpha.Path, applierUDID, "versions", created.Format("2006-01-02T15-04-05Z"))
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := storage.WriteMarker(verDir, storage.Marker{
		VersionID: "01VLATEALPHA", UDID: applierUDID, Backend: storage.BackendCopy,
		CreatedAt: created.Format(time.RFC3339), Kind: "full", Encrypted: true,
		StructureVerifiedAt: created.Format(time.RFC3339), AppVersion: "test",
	}); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if _, _, _, err := svc.ForgetStorage("beta", nil); err != nil {
		t.Fatalf("ForgetStorage: %v", err)
	}

	// ASSERTED AT THE TRIGGER NOW, WHICH IS SHARPER THAN THE OBSERVABLE IT REPLACES (qn.6i PR 4).
	//
	// The runner is deliberately NOT started, so nothing can consume a trigger: if the forget queued
	// one it is still queued, and `Reconciling()` says so. The old form — "no version was adopted" —
	// still holds and is kept below, but on its own it would now pass for the wrong reason, because an
	// unstarted runner adopts nothing whether or not it was triggered.
	if runner.Reconciling() {
		t.Error("a forget queued a reconciliation pass — nothing was added, so there is nothing to " +
			"scan, and under a schedule a spurious pass per removal is a full walk for no answer")
	}
	if got := len(mgr.Versions(applierUDID)); got != 0 {
		t.Errorf("a forget reconciled (%d versions adopted) — nothing was added, so there is "+
			"nothing to scan", got)
	}
}

// sameStorageDeclaration's ORDER-SENSITIVITY, which is the one property of it that looks like a bug.
//
// Position IS the default (`slots[0]`), so two lists with identical members in a different order
// are a real change — the user made a different disk the default. A set comparison would call this
// "same" and silently keep the old default.
func TestSameStorageDeclarationTreatsAReorderAsAChange(t *testing.T) {
	a := entry(t, "alpha", true)
	b := entry(t, "beta", false)

	if !sameStorageDeclaration([]config.StorageEntry{a, b}, []config.StorageEntry{a, b}) {
		t.Error("an identical list must compare same, or every unrelated config write re-resolves " +
			"every storage")
	}
	if sameStorageDeclaration([]config.StorageEntry{a, b}, []config.StorageEntry{b, a}) {
		t.Error("a REORDER must compare different: slots[0] is the default, so swapping two " +
			"storages changes where an unnamed backup goes")
	}
}

// addedStorage is by NAME, and a path edit on a known name is not an addition.
//
// It decides whether the reconcile runs, so getting it wrong in the generous direction costs a walk
// of every declared tree on every path typo, and in the mean direction costs an adopted history.
func TestAddedStorageIsByNameSoAPathEditIsNotAnAddition(t *testing.T) {
	a := entry(t, "alpha", true)
	moved := a
	moved.Path = t.TempDir()

	if addedStorage([]config.StorageEntry{a}, []config.StorageEntry{moved}) {
		t.Error("a path change on an existing name is an EDIT — it re-resolves through " +
			"ApplyStorages either way, and calling it an addition scans every tree for nothing")
	}
	if !addedStorage([]config.StorageEntry{a}, []config.StorageEntry{a, entry(t, "beta", false)}) {
		t.Error("a genuinely new name must read as an addition, or its existing backups stay " +
			"invisible until a restart")
	}
}

// qn.6i PR 4 — THE ADD RETURNS WITHOUT WAITING FOR THE SCAN (quince#715).
//
// This is the PR's whole claim, and it needs an assertion that can distinguish "enqueued" from "ran",
// which no observable about adopted versions can: both end with the version adopted, differing only
// in when. So the runner is deliberately NOT started. The write returns, a pass is queued, and NOTHING
// HAS SCANNED — which is precisely the state the old inline code could never be in.
//
// WHY IT MATTERS beyond latency: the applier runs inside the HTTP handler with `writeMu` held, so the
// ~48-second walk did not merely hang the button — it queued every following config write behind it,
// including a Forget. Enqueuing releases the lock in milliseconds.
func TestAddingAStorageEnqueuesTheScanRatherThanRunningIt(t *testing.T) {
	alpha := entry(t, "alpha", true)
	svc, mgr, runner, _ := wiredStorageMode(t, []config.StorageEntry{alpha}, scanDeferred)

	beta := entry(t, "beta", false)
	created := time.Date(2026, 7, 20, 5, 0, 0, 0, time.UTC)
	verDir := filepath.Join(beta.Path, applierUDID, "versions", created.Format("2006-01-02T15-04-05Z"))
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := storage.WriteMarker(verDir, storage.Marker{
		VersionID: "01VENQUEUED", UDID: applierUDID, Backend: storage.BackendCopy,
		CreatedAt: created.Format(time.RFC3339), Kind: "full", Encrypted: true,
		StructureVerifiedAt: created.Format(time.RFC3339), AppVersion: "test",
	}); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	replaceStorage(t, svc, []config.StorageEntry{alpha, beta})

	if !runner.Reconciling() {
		t.Fatal("adding a storage queued no reconciliation — a disk that already holds backups would " +
			"stay invisible until something unrelated triggered a pass")
	}
	if got := len(mgr.Versions(applierUDID)); got != 0 {
		t.Fatalf("the add SCANNED before returning (%d versions adopted) — that is quince#715: the "+
			"handler holding writeMu across the walk, so the button hangs and the next config write "+
			"queues behind it", got)
	}

	// And the enqueued pass really does the work once something runs it.
	done := runner.WaitForPass()
	runner.Start(context.Background())
	<-done
	if got := len(mgr.Versions(applierUDID)); got != 1 {
		t.Fatalf("after the queued pass ran, Versions() = %d, want 1 — enqueuing is only honest if "+
			"the pass actually happens", got)
	}
}

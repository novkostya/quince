package config

// The reload seam's own suite (qn.6q, quince#1094). Stories 3–7 of the spec live here; stories 1, 2
// and 8 belong to the poller and its wiring, which is the next slice.
//
// EVERY CASE DRIVES A REAL FILE. `Reload` exists to notice what somebody else did to `config.yml`,
// so a test that stubbed the read would be asserting the half that cannot break.

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// oneStorage is the minimal document that survives Validate + CheckStorages, which a bare `Config{}`
// does not — a save with no storage is refused (quince#394).
func oneStorage(path string) string {
	return "storage:\n  - name: main\n    path: " + path + "\n    backend: copy\n    default: true\n"
}

// newLoadedService writes doc to a temp config.yml and returns a Service that has loaded it.
func newLoadedService(t *testing.T, doc string) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfgPath, quiet())
	if svc.Discarded() {
		t.Fatalf("fixture did not load: warnings=%v", svc.warnings)
	}
	return svc, cfgPath
}

// countingApplier records how many times it ran and what it last saw.
type countingApplier struct {
	mu    sync.Mutex
	calls int
	last  Config
	prev  Config
}

func (a *countingApplier) apply(old, next Config) []Warning {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	a.prev, a.last = old, next
	return nil
}

func (a *countingApplier) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// STORY 4 — a no-op edit applies nothing.
//
// This is the case a debounce cannot get right and a content comparison gets right for free: the
// file is rewritten, its mtime moves, and the bytes are identical.
func TestReloadUnchangedBytesApplyNothing(t *testing.T) {
	doc := oneStorage(t.TempDir())
	svc, path := newLoadedService(t, doc)
	var a countingApplier
	svc.Subscribe("test", a.apply)

	// Rewrite with identical content — a `touch`, or a save that changed nothing.
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, warns := svc.Reload()
	if outcome != ReloadUnchanged {
		t.Errorf("outcome = %v, want ReloadUnchanged", outcome)
	}
	if got := a.count(); got != 0 {
		t.Errorf("applier ran %d times, want 0 — a no-op edit must apply nothing", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

// STORY 3 — quince's own write does not reload.
//
// ASSERTED BY APPLIER CALL COUNT, not by the absence of a symptom (spec gate G2). `Replace` runs the
// appliers once; the poll that follows must add nothing. Counting is what distinguishes "the reload
// was suppressed" from "the reload happened and changed nothing visible".
func TestReloadDoesNotReactToOurOwnWrite(t *testing.T) {
	store := t.TempDir()
	svc, _ := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)

	next := svc.Current()
	next.Backup.RequireEncryption = !next.Backup.RequireEncryption
	if errs, _, err := svc.Replace(next, SourcePutConfig); err != nil || len(errs) > 0 {
		t.Fatalf("Replace: err=%v errs=%v", err, errs)
	}
	if got := a.count(); got != 1 {
		t.Fatalf("applier ran %d times after Replace, want 1", got)
	}

	outcome, _ := svc.Reload()
	if outcome != ReloadUnchanged {
		t.Errorf("outcome = %v, want ReloadUnchanged — quince's own write must not read as a hand-edit", outcome)
	}
	if got := a.count(); got != 1 {
		t.Errorf("applier ran %d times total, want 1 — the poll after our own save re-applied it", got)
	}
}

// STORY 3, THE HARD HALF (spec gate G3) — the self-write record must survive the round-trip guard's
// FULL-DOCUMENT fallback.
//
// `replaceLocked` may discard its tidy bytes and write `Marshal(c)` instead. Recording the ATTEMPT
// rather than what LANDED would make that degradation indistinguishable from somebody editing the
// file, and the next poll would re-apply. Asserted against the file itself rather than against the
// guard's internals: whatever `Replace` put on disk is what `lastBytes` must hold.
func TestReloadRecordIsWhatLandedOnDisk(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))

	next := svc.Current()
	next.Reconcile.IntervalMinutes = 42
	if errs, _, err := svc.Replace(next, SourcePutConfig); err != nil || len(errs) > 0 {
		t.Fatalf("Replace: err=%v errs=%v", err, errs)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	svc.mu.RLock()
	recorded := svc.lastBytes
	svc.mu.RUnlock()
	if string(recorded) != string(onDisk) {
		t.Errorf("lastBytes does not match the file:\n recorded=%q\n on disk =%q", recorded, onDisk)
	}
}

// STORY 1/2 at the seam — a hand-edit applies, and the appliers are told with the right old/next.
//
// The poller is a later slice; what this proves is that the seam it will drive does the work.
func TestReloadAppliesAHandEdit(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)

	before := svc.Current().Backup.RequireEncryption
	edited := oneStorage(store) + "backup:\n  require_encryption: " + boolStr(!before) + "\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, _ := svc.Reload()
	if outcome != ReloadApplied {
		t.Fatalf("outcome = %v, want ReloadApplied", outcome)
	}
	if got := svc.Current().Backup.RequireEncryption; got == before {
		t.Errorf("require_encryption = %v, unchanged — the hand-edit did not reach the snapshot", got)
	}
	if got := a.count(); got != 1 {
		t.Fatalf("applier ran %d times, want 1", got)
	}
	a.mu.Lock()
	prev, last := a.prev, a.last
	a.mu.Unlock()
	if prev.Backup.RequireEncryption != before {
		t.Errorf("applier's `old` = %v, want the pre-edit value %v", prev.Backup.RequireEncryption, before)
	}
	if last.Backup.RequireEncryption == before {
		t.Error("applier's `next` still carries the pre-edit value")
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// storageCount counts declared storages. `Config.Storage` is a POINTER so that an absent key is
// distinguishable from an empty list, which is why this cannot be a bare len().
func storageCount(c Config) int {
	if c.Storage == nil {
		return 0
	}
	return len(*c.Storage)
}

// STORY 5 — a bad hand-edit keeps last-good, and NO applier runs.
//
// D12: "an invalid edit never crashes the app (keep running on last-good, show a UI banner naming the
// bad key)." The applier count is the load-bearing assertion: calling appliers with old == next would
// be a lie about what happened, and it is the easy mistake.
func TestReloadRefusesABadEditAndKeepsLastGood(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)
	good := svc.Current()

	if err := os.WriteFile(path, []byte("storage: [ this is not: valid: yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, warns := svc.Reload()
	if outcome != ReloadRefused {
		t.Fatalf("outcome = %v, want ReloadRefused", outcome)
	}
	if got := a.count(); got != 0 {
		t.Errorf("applier ran %d times on a refused edit, want 0", got)
	}
	if storageCount(svc.Current()) != storageCount(good) {
		t.Error("the running configuration changed on a refused edit — last-good did not stand")
	}
	if !svc.Discarded() {
		t.Error("Discarded() = false after the file on disk was refused")
	}
	if len(warns) == 0 {
		t.Error("a refused edit returned no warnings — the cause must reach the UI")
	}
}

// STORY 5, THE LOOP GUARD — a refused edit is Loaded ONCE, not once per poll.
//
// Without recording the bad bytes, every later poll re-reads the same broken file, re-parses and
// re-logs at the poll interval, forever.
func TestReloadRefusalIsNotRepeated(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))

	if err := os.WriteFile(path, []byte("storage: [ bad: yaml: here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadRefused {
		t.Fatalf("first reload: outcome = %v, want ReloadRefused", outcome)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadUnchanged {
		t.Errorf("second reload: outcome = %v, want ReloadUnchanged — the same broken file was re-read", outcome)
	}
}

// STORY 6 — fixing the file recovers, with no restart.
//
// The property that makes the banner tolerable: nothing has to be restarted to escape it.
func TestReloadRecoversWhenTheFileIsFixed(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)

	if err := os.WriteFile(path, []byte("storage: [ bad: yaml: here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadRefused {
		t.Fatal("setup: the bad edit was not refused")
	}

	fixed := oneStorage(store) + "reconcile:\n  interval_minutes: 15\n"
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, _ := svc.Reload()
	if outcome != ReloadApplied {
		t.Fatalf("outcome = %v, want ReloadApplied after the file was repaired", outcome)
	}
	if svc.Discarded() {
		t.Error("Discarded() is still true after the file was repaired — the banner cannot be cleared")
	}
	if got := svc.Current().Reconcile.IntervalMinutes; got != 15 {
		t.Errorf("interval_minutes = %d, want 15 — the repaired document did not take effect", got)
	}
	if got := a.count(); got != 1 {
		t.Errorf("applier ran %d times, want 1 — only the recovery applies", got)
	}
}

// STORY 7 — a DELETED config.yml keeps last-good rather than falling back to defaults.
//
// Removing the file is not an instruction to adopt Default(): that would silently disable every
// declared storage, and it is far more likely to be an accident mid-edit. The restored file is picked
// up by the next poll.
func TestReloadKeepsLastGoodWhenTheFileIsDeleted(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)
	want := storageCount(svc.Current())

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outcome, _ := svc.Reload()
	if outcome != ReloadUnchanged {
		t.Errorf("outcome = %v, want ReloadUnchanged for a vanished file", outcome)
	}
	if got := storageCount(svc.Current()); got != want {
		t.Errorf("storage count = %d, want %d — a deleted file must not disable storage", got, want)
	}
	if got := a.count(); got != 0 {
		t.Errorf("applier ran %d times for a vanished file, want 0", got)
	}

	// …and the replacement is adopted.
	restored := oneStorage(store) + "reconcile:\n  interval_minutes: 30\n"
	if err := os.WriteFile(path, []byte(restored), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadApplied {
		t.Errorf("outcome = %v, want ReloadApplied once the file came back", outcome)
	}
	if got := svc.Current().Reconcile.IntervalMinutes; got != 30 {
		t.Errorf("interval_minutes = %d, want 30", got)
	}
}

// A RELOAD MUST NOT WRITE (spec decision D4). The file the operator left is the file that stays —
// byte for byte, including formatting a canonical re-marshal would destroy.
func TestReloadNeverWritesTheFile(t *testing.T) {
	store := t.TempDir()
	// Deliberately un-canonical: comments, blank lines and an order `Marshal` would not produce.
	hand := "# my notes\n\nstorage:\n\n  - name: main\n    path: " + store +
		"\n    backend: copy\n    default: true\n\n# trailing comment\n"
	svc, path := newLoadedService(t, hand)

	edited := hand + "\nreconcile:\n  interval_minutes: 20\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadApplied {
		t.Fatal("setup: the edit was not applied")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != edited {
		t.Errorf("Reload rewrote the operator's file:\n before=%q\n after =%q", edited, after)
	}
	if !strings.Contains(string(after), "# my notes") {
		t.Error("the operator's comments were destroyed by a reload")
	}
}

// A reload on a fresh install with NO file must not invent one, and must not panic on a nil record.
func TestReloadWithNoFileEverIsQuiet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	svc := NewService(path, quiet())
	var a countingApplier
	svc.Subscribe("test", a.apply)

	outcome, _ := svc.Reload()
	if outcome != ReloadUnchanged {
		t.Errorf("outcome = %v, want ReloadUnchanged when there is no file at all", outcome)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Reload created config.yml on a fresh install — it must never write")
	}
	if got := a.count(); got != 0 {
		t.Errorf("applier ran %d times, want 0", got)
	}
}

// THE SURFACE-CHANGE BROADCAST (quince#1162, Operator ruling 2026-08-17 option C).
//
// Three transitions must fire it and one must not, and the interesting one is the REFUSED edit —
// the running configuration is unchanged there, so no applier runs, and an event scoped to
// "the config changed" would leave the banner state silent.

// countingAnnouncer records how many times the broadcast fired.
type countingAnnouncer struct {
	mu sync.Mutex
	n  int
}

func (a *countingAnnouncer) fire()      { a.mu.Lock(); a.n++; a.mu.Unlock() }
func (a *countingAnnouncer) count() int { a.mu.Lock(); defer a.mu.Unlock(); return a.n }

func TestSurfaceChangeFiresOnAnAppliedHandEdit(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var a countingAnnouncer
	svc.OnSurfaceChange(a.fire)

	edited := oneStorage(store) + "reconcile:\n  interval_minutes: 33\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadApplied {
		t.Fatalf("outcome = %v, want ReloadApplied", outcome)
	}
	if got := a.count(); got != 1 {
		t.Errorf("broadcast fired %d times, want 1", got)
	}
}

// THE ONE THAT WOULD BE MISSED. No applier runs on a refusal, so registering the broadcast as an
// Applier would leave this silent — and this is the state whose banner an open page must draw.
func TestSurfaceChangeFiresOnAREFUSEDHandEdit(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var applier countingApplier
	svc.Subscribe("test", applier.apply)
	var a countingAnnouncer
	svc.OnSurfaceChange(a.fire)

	if err := os.WriteFile(path, []byte("storage: [ bad: yaml: here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadRefused {
		t.Fatalf("outcome = %v, want ReloadRefused", outcome)
	}
	if got := applier.count(); got != 0 {
		t.Fatalf("an applier ran on a refusal (%d) — the premise of this test is gone", got)
	}
	if got := a.count(); got != 1 {
		t.Errorf("broadcast fired %d times on a REFUSED edit, want 1 — the banner state is silent", got)
	}
	if !svc.Discarded() {
		t.Error("Discarded() is false, so there would be nothing for the page to redraw")
	}
}

func TestSurfaceChangeFiresOnAWrite(t *testing.T) {
	store := t.TempDir()
	svc, _ := newLoadedService(t, oneStorage(store))
	var a countingAnnouncer
	svc.OnSurfaceChange(a.fire)

	next := svc.Current()
	next.Reconcile.IntervalMinutes = 77
	if errs, _, err := svc.Replace(next, SourcePutConfig); err != nil || len(errs) > 0 {
		t.Fatalf("Replace: err=%v errs=%v", err, errs)
	}
	if got := a.count(); got != 1 {
		t.Errorf("broadcast fired %d times on a write, want 1", got)
	}
}

// AN UNCHANGED FILE IS NOT A SURFACE CHANGE. Without this the poll would broadcast every 2s forever
// and every open page would refetch on a timer — option A, arrived at by accident.
func TestSurfaceChangeIsSilentWhenNothingChanged(t *testing.T) {
	store := t.TempDir()
	doc := oneStorage(store)
	svc, path := newLoadedService(t, doc)
	var a countingAnnouncer
	svc.OnSurfaceChange(a.fire)

	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil { // identical bytes
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadUnchanged {
		t.Fatal("setup: the no-op edit was not suppressed")
	}
	if got := a.count(); got != 0 {
		t.Errorf("broadcast fired %d times with nothing changed, want 0", got)
	}
}

// A Service with no broadcast registered — every CLI — must not panic.
func TestSurfaceChangeIsOptional(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	if err := os.WriteFile(path, []byte(oneStorage(store)+"reconcile:\n  interval_minutes: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := svc.Reload(); outcome != ReloadApplied {
		t.Errorf("outcome = %v, want ReloadApplied with no announcer registered", outcome)
	}
}

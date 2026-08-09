package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE RUNG'S HEADLINE CLAIM, as a test: a save writes back what the user wrote plus what they
// changed. This is quince#728's opening measurement inverted — the issue recorded 50 bytes in and
// 641 out, and the same scenario now has to come back small.

func serviceOn(t *testing.T, raw string) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return NewService(path, slog.New(slog.NewTextHandler(os.Stderr, nil))), path
}

// quince#728's exact scenario: hand-write the minimal declaration, change Theme in the UI, save.
func TestASaveKeepsTheFileTheUserWrote(t *testing.T) {
	svc, path := serviceOn(t, "storage:\n    - path: /backups\n      backend: hardlink\n")

	next := svc.Current()
	next.UI.Theme = "dark"
	if errs, _, err := svc.Replace(next); err != nil || len(errs) > 0 {
		t.Fatalf("replace: errs=%+v err=%v", errs, err)
	}

	got := readFile(t, path)
	const want = "storage:\n    - path: /backups\n      backend: hardlink\nui:\n    theme: dark\n"
	if got != want {
		t.Fatalf("the save did not keep the user's file.\n got: %q\nwant: %q", got, want)
	}
	// The issue's own numbers, so a regression is legible rather than a diff nobody reads.
	if len(got) > 120 {
		t.Errorf("file is %d bytes; quince#728 reported 641 for this document and 50 for the input", len(got))
	}
}

// NOT A ONE-SHOT: the SECOND save must not re-inflate what the first one tidied. This is what the
// `s.declared` assignment in replaceLocked buys, and without it the set still describes the document
// as READ, so every key the first write added looks undeclared again.
func TestASecondSaveDoesNotReInflate(t *testing.T) {
	svc, path := serviceOn(t, "storage:\n    - path: /backups\n")

	for i, theme := range []string{"dark", "light"} {
		next := svc.Current()
		next.UI.Theme = theme
		if errs, _, err := svc.Replace(next); err != nil || len(errs) > 0 {
			t.Fatalf("replace %d: errs=%+v err=%v", i, errs, err)
		}
	}

	got := readFile(t, path)
	const want = "storage:\n    - path: /backups\nui:\n    theme: light\n"
	if got != want {
		t.Fatalf("the second save re-inflated the file.\n got: %q\nwant: %q", got, want)
	}
}

// A key the user set to a NON-default value is written even though nothing in the file declared it,
// because this write changed it — clause 2. Without clause 2 a UI change would save nothing.
func TestAChangedKeyIsWrittenEvenIfTheFileNeverHadIt(t *testing.T) {
	svc, path := serviceOn(t, "storage:\n    - path: /backups\n")

	next := svc.Current()
	next.Backup.RequireEncryption = false // the default is TRUE, so this is a real choice
	if errs, _, err := svc.Replace(next); err != nil || len(errs) > 0 {
		t.Fatalf("replace: errs=%+v err=%v", errs, err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "require_encryption: false") {
		t.Fatalf("a deliberate `false` was not written — this is the field `omitempty` would have "+
			"erased, and clause 2 is what keeps it:\n%s", got)
	}
	if strings.Contains(got, "preferred_transport") {
		t.Errorf("an untouched sibling key came along for the ride:\n%s", got)
	}
}

// THE FILE STILL LOADS. Every tidy write must produce a document the daemon can start on — the
// failure mode quince#683 is about, reached from the other direction.
func TestWhatIsSavedStillLoads(t *testing.T) {
	svc, path := serviceOn(t, "storage:\n    - path: /backups\n")

	next := svc.Current()
	next.UI.Theme = "dark"
	if errs, _, err := svc.Replace(next); err != nil || len(errs) > 0 {
		t.Fatalf("replace: errs=%+v err=%v", errs, err)
	}

	l := Load(path)
	if !l.OK {
		t.Fatalf("the written file does not load: %+v", l.Errors)
	}
	if !SameConfig(l.Config, svc.Current()) {
		t.Errorf("reloaded config differs from the live one\n got: %+v\nwant: %+v", l.Config, svc.Current())
	}
}

// THE RUNTIME GUARD IS SILENT IN NORMAL OPERATION (spec decision 3). It fires only when a write
// would lose something, and an ordinary save must never see it — a guard that cries on the happy
// path teaches the reader to ignore it.
func TestTheRoundTripGuardDoesNotFireOnAnOrdinarySave(t *testing.T) {
	svc, _ := serviceOn(t, "storage:\n    - path: /backups\n      backend: hardlink\n")

	next := svc.Current()
	next.UI.Theme = "dark"
	_, warns, err := svc.Replace(next)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warns {
		if strings.Contains(w.Message, "short form") {
			t.Fatalf("the round-trip guard fired on an ordinary save: %s", w.Message)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// THE ADD DOOR WRITES WHAT THE CALLER SUPPLIED, NOT WHAT RESOLUTION FILLED IN (spec PR 5).
//
// quince#759 measured this at 292 bytes — eleven keys for the two a caller sends. It is the FIRST
// FILE a new install produces and the one most users will ever read, so it matters more than the
// save path it followed.
func TestAddingTheFirstStorageWritesOnlyWhatWasSupplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml") // NO file: a genuine fresh install
	svc := NewService(path, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	outcome, errs, _, err := svc.AddStorage(StorageEntry{Path: "/backups", Backend: "hardlink"})
	if err != nil || len(errs) > 0 || outcome != AddDone {
		t.Fatalf("add refused: outcome=%v errs=%+v err=%v", outcome, errs, err)
	}

	got := readFile(t, path)
	const want = "storage:\n    - path: /backups\n      backend: hardlink\n"
	if got != want {
		t.Fatalf("the first-run file carries keys nobody set.\n got: %q\nwant: %q", got, want)
	}
	if l := Load(path); !l.OK {
		t.Fatalf("the first-run file does not load: %+v", l.Errors)
	}
}

// THE SHARP EDGE (spec D3): a lone storage's `default: true` is implied and unwritten. Add a second
// and the implication STOPS APPLYING, so the incumbent's default must be materialised at that moment
// or the next parse finds two storages and no default — a config the daemon will not start on.
//
// This is the one case where quince must write a value nobody set, and it is the opposite of every
// other rule in this rung.
func TestASecondStorageMaterialisesTheIncumbentsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if _, errs, _, err := svc.AddStorage(StorageEntry{Path: "/backups", Backend: "hardlink"}); err != nil || len(errs) > 0 {
		t.Fatalf("first add: %+v %v", errs, err)
	}
	if strings.Contains(readFile(t, path), "default:") {
		t.Fatalf("a lone storage should not carry `default:` — it is implied:\n%s", readFile(t, path))
	}

	_, errs, warns, err := svc.AddStorage(StorageEntry{Name: "second", Path: "/backups-b", Backend: "reflink"})
	if err != nil || len(errs) > 0 {
		t.Fatalf("second add: %+v %v", errs, err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "default: true") {
		t.Fatalf("the incumbent's default was not materialised when the second storage arrived — "+
			"the next parse finds two storages and no default:\n%s", got)
	}

	// THE FALLBACK MUST NOT BE WHAT SAVED THIS, and asserting so is the only way this test guards D3.
	//
	// Measured with D3 disabled: the tidy write loses `storage[/backups].default`, the round-trip
	// guard catches it (`differing=storage[/backups].default`), and the FULL document is written —
	// so the file still loads, still contains `default: true`, and every assertion below still
	// passes. **The guard covers for the missing rule**, which is decision 3 working exactly as
	// designed and is the first time it has fired on a real defect rather than a contrived one.
	//
	// It also means "the file loads" cannot be this test's guarantee. The file must be TIDY as well,
	// which is what the fallback does not produce.
	for _, w := range warns {
		if strings.Contains(w.Message, "short form") {
			t.Fatalf("the round-trip guard fired — the fallback wrote this file, so D3 is not what "+
				"made it valid: %s", w.Message)
		}
	}
	for _, fat := range []string{"zfs:", "retention:", "preferred_transport"} {
		if strings.Contains(got, fat) {
			t.Fatalf("the file carries %q, so this is the fallback's full document rather than a "+
				"tidy write:\n%s", fat, got)
		}
	}
	// The assertion that matters: the file must LOAD, which is what validateStorages would refuse.
	l := Load(path)
	if !l.OK {
		t.Fatalf("the written file does not load — this is the unstartable config D3 exists to "+
			"prevent: %+v\n%s", l.Errors, got)
	}
	defaults := 0
	for _, e := range *l.Config.Storage {
		if e.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("want exactly one default after the reload, got %d:\n%s", defaults, got)
	}
}

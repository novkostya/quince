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

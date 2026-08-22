package msgfixture_test

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"
	"github.com/novkostya/ios-backup-parser/messages"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/messages/msgfixture"
	"github.com/novkostya/quince/core/internal/vault/parserfs"
)

// The captured log must be LIVE, and the control is that the main file alone is INCOMPLETE.
// Without this, a "-wal fixture" could be an empty file beside a fully-checkpointed database
// and every test using it would pass while proving nothing.
func TestCapturedWALHoldsPagesTheMainFileDoesNot(t *testing.T) {
	got, err := msgfixture.BuildWithWAL(t.TempDir(), msgfixture.Spec{})
	if err != nil {
		t.Fatalf("BuildWithWAL: %v", err)
	}
	if len(got.WAL) == 0 {
		t.Fatal("empty -wal")
	}

	// The control: open the main file WITHOUT its log. If the database were already
	// checkpointed this would find every message, and the fixture would be inert.
	withoutLog := openBytes(t, got.Main, nil)
	withLog := openBytes(t, got.Main, got.WAL)

	if withLog <= withoutLog {
		t.Errorf("with-log=%d without-log=%d — the log holds nothing the main file lacks, so this fixture proves nothing",
			withLog, withoutLog)
	}
	if withLog != 8 {
		t.Errorf("with the log the parser sees %d messages, want 8", withLog)
	}
}

// openBytes places main (and optionally its -wal) in a backup and returns how many messages
// the parser can see. A main file with no readable schema yields 0 rather than failing the
// test, because "cannot open" is a legitimate answer for a log-less half-written database.
func openBytes(t *testing.T, main, wal []byte) int {
	t.Helper()
	files := []fixture.File{
		{Domain: msgfixture.Domain, RelativePath: msgfixture.RelativePath, Flags: 1, Data: main},
	}
	if wal != nil {
		files = append(files, fixture.File{
			Domain: msgfixture.Domain, RelativePath: msgfixture.RelativePath + "-wal", Flags: 1, Data: wal,
		})
	}
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: files}); err != nil {
		t.Fatalf("build backup: %v", err)
	}
	v, err := vault.OpenUnencrypted(dir)
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	defer func() { _ = v.Close() }()
	if _, err := v.Unlock(t.Context(), ""); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	fsys, err := parserfs.New(v, t.TempDir())
	if err != nil {
		t.Fatalf("parserfs: %v", err)
	}
	m, err := messages.Open(fsys)
	if err != nil {
		return 0
	}
	defer func() { _ = m.Close() }()
	n := 0
	for _, err := range m.Messages() {
		if err != nil {
			continue
		}
		n++
	}
	return n
}

// THE HARD RULE, MECHANICALLY. Opening a database with a live -wal REPLAYS it — a write. If
// parserfs handed the parser a path into the backup rather than into a private copy, that
// write would land on a committed version.
//
// So: hash every file in the backup tree, open the domain through the real parserfs, hash
// again. Any difference is *never mutate a committed version* being broken.
func TestOpeningThroughParserfsLeavesTheBackupBytesUnCHANGED(t *testing.T) {
	got, err := msgfixture.BuildWithWAL(t.TempDir(), msgfixture.Spec{})
	if err != nil {
		t.Fatalf("BuildWithWAL: %v", err)
	}
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: []fixture.File{
		{Domain: msgfixture.Domain, RelativePath: msgfixture.RelativePath, Flags: 1, Data: got.Main},
		{Domain: msgfixture.Domain, RelativePath: msgfixture.RelativePath + "-wal", Flags: 1, Data: got.WAL},
	}}); err != nil {
		t.Fatalf("build backup: %v", err)
	}

	before := hashTree(t, dir)

	v, err := vault.OpenUnencrypted(dir)
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	if _, err := v.Unlock(t.Context(), ""); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	fsys, err := parserfs.New(v, t.TempDir())
	if err != nil {
		t.Fatalf("parserfs: %v", err)
	}
	m, err := messages.Open(fsys)
	if err != nil {
		t.Fatalf("messages.Open: %v", err)
	}
	n := 0
	for _, err := range m.Messages() {
		if err != nil {
			continue
		}
		n++
	}
	_ = m.Close()
	_ = v.Close()

	if n == 0 {
		t.Fatal("control failed: the parser read nothing, so nothing would have been mutated either")
	}

	after := hashTree(t, dir)
	for path, sum := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("%s: disappeared from the backup tree", path)
			continue
		}
		if got != sum {
			t.Errorf("%s: bytes CHANGED — a committed version was mutated", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s: appeared in the backup tree — nothing may be written beside a committed version", path)
		}
	}
}

func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = string(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("hashed nothing — the walk found no files, so a comparison would be vacuous")
	}
	return out
}

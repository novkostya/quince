package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A CONCURRENT READER NEVER SEES A TORN CONFIG — quince#768.
//
// WHAT IS ACTUALLY BEING ASSERTED, since the mechanism is one layer below the thing under test.
// `FileText` reads `config.yml` at request time holding NO write lock, and `GET /api/config` calls it
// on every request including ones that land while a save is in flight. That is safe for exactly one
// reason: `AtomicWrite` builds a temp file and `os.Rename`s it over the path, so a reader gets the
// old bytes or the new bytes and never a prefix of either.
//
// BEFORE qn.6j PR 7 THAT WAS A HYPOTHESIS. `AtomicWrite`'s comment said *"so a reader never sees a
// half-written config"* while no such reader existed. The panel gave it one, and neither file
// mentioned the other — so a future truncate-and-write, which looks like a simplification and passes
// every test that does not race, would break the panel intermittently under load. This is the guard
// that turns those two comments into something enforced.
//
// WHY THE TWO DOCUMENTS DIFFER SHARPLY IN LENGTH. A torn read is only detectable if a prefix of one
// document is not also a valid whole document. The long one is padded with comment lines so that any
// truncation of it is neither of the two accepted answers, and the assertion is EQUALITY against a
// closed set rather than a parse — a partially-written YAML file can still parse, so "it parsed" is
// not the property.
//
// IT DRIVES `AtomicWrite` DIRECTLY rather than `Replace`. The coupling quince#768 names is between
// these two functions; going through `Replace` would add validation, resolution and the declared-set
// machinery to a test about a rename, and would make a failure ambiguous about which layer tore.
func TestAConcurrentReaderNeverSeesATornConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	short := []byte("storage:\n    - path: /backups\n")
	long := []byte("storage:\n    - path: /backups\n" +
		strings.Repeat("# padding so that a truncated write cannot equal the short document\n", 400))

	// The file exists before either goroutine starts, so "" is not an accepted answer below and a
	// read that returns it is a real failure rather than a race with the first write.
	if err := os.WriteFile(path, short, 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(path, slog.New(slog.NewTextHandler(nil, nil)))

	const rounds = 300
	var wg sync.WaitGroup
	wg.Add(2)

	writeErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			data := short
			if i%2 == 0 {
				data = long
			}
			if err := AtomicWrite(path, data); err != nil {
				select {
				case writeErr <- err:
				default:
				}
				return
			}
		}
	}()

	bad := make(chan string, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			got := svc.FileText()
			if got == string(short) || got == string(long) {
				continue
			}
			select {
			case bad <- got:
			default:
			}
			return
		}
	}()

	wg.Wait()

	select {
	case err := <-writeErr:
		t.Fatalf("AtomicWrite failed, so the reader below was not exercised: %v", err)
	default:
	}
	select {
	case got := <-bad:
		t.Fatalf("FileText returned a document that is neither of the two written — %d bytes, "+
			"beginning %q. The unlocked read is safe only because AtomicWrite RENAMES; a "+
			"truncate-and-write serves exactly this (quince#768).", len(got), head(got))
	default:
	}
}

// head bounds what a failure prints: the long document is ~27 KB and dumping it would bury the one
// number that identifies the defect, which is its length.
func head(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

package capability

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"
	_ "modernc.org/sqlite"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/parserfs"
)

// build makes a backup holding exactly the given files and returns a capability cache over
// it, through the real parserfs — not a stub, because the states this reports are produced by
// the parser reacting to real bytes and a stub would assert my beliefs about it.
func build(t *testing.T, files []fixture.File) *Cache {
	t.Helper()
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: files}); err != nil {
		t.Fatal(err)
	}
	v, err := vault.OpenUnencrypted(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if _, err := v.Unlock(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	f, err := parserfs.New(v, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewCache(f)
}

func states(r *Report) map[string]State {
	m := map[string]State{}
	for _, d := range r.Domains {
		m[d.Domain] = d.State
	}
	return m
}

// An empty backup: every domain is ABSENT, and none is reported as a failure. This is the
// state the ruling did not name and the one most likely to be rendered as an error.
func TestEmptyBackupReportsEveryDomainAbsent(t *testing.T) {
	c := build(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/Preferences/x.plist", Flags: 1, Data: []byte("x")},
	})
	got := states(c.Report())
	if len(got) != len(Names()) {
		t.Fatalf("report has %d domains, want %d (%v)", len(got), len(Names()), Names())
	}
	for _, name := range Names() {
		if got[name] != StateAbsent {
			t.Errorf("%s = %q on a backup with no domain databases, want %q", name, got[name], StateAbsent)
		}
	}
}

// Cached for the lifetime of the cache, and computed once.
func TestReportIsComputedOnceAndCached(t *testing.T) {
	c := build(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/Preferences/x.plist", Flags: 1, Data: []byte("x")},
	})
	first := c.Report()
	second := c.Report()
	if first != second {
		t.Error("Report returned a different pointer on the second call — it is recomputing, " +
			"which is the fixed cost the laziness ruling exists to avoid")
	}
}

// The enumeration is quince's, is sorted, and is FIVE until a parser release carries more.
func TestEnumerationIsTheTagsFive(t *testing.T) {
	want := []string{"calendar", "calls", "contacts", "messages", "notes"}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v — reminders and safari are main-only (spec fact 9b)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names()[%d] = %q, want %q (sorted)", i, got[i], want[i])
		}
	}
}

// The report is ordered, so a consumer's output is deterministic.
func TestReportIsOrdered(t *testing.T) {
	c := build(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/Preferences/x.plist", Flags: 1, Data: []byte("x")},
	})
	r := c.Report()
	for i := 1; i < len(r.Domains); i++ {
		if r.Domains[i-1].Domain >= r.Domains[i].Domain {
			t.Errorf("report not sorted: %q then %q", r.Domains[i-1].Domain, r.Domains[i].Domain)
		}
	}
}

// wrongSchemaDB returns the bytes of a VALID SQLite database whose tables are not any
// domain's schema. This is what produces a genuine ErrUnsupportedSchema, with a fingerprint —
// as distinct from garbage bytes, which are not a database at all.
func wrongSchemaDB(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wrong.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE NotTheSchemaYouAreLookingFor (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO NotTheSchemaYouAreLookingFor (v) VALUES ('x')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A VALID database with the WRONG schema is StateUnsupported and carries a fingerprint —
// which is the evidence a schema-support issue needs, and the only thing separating this from
// "your backup is damaged".
func TestValidDatabaseWithWrongSchemaIsUnsupportedWithFingerprint(t *testing.T) {
	c := build(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/CallHistoryDB/CallHistory.storedata",
			Flags: 1, Data: wrongSchemaDB(t)},
	})
	var calls *Domain
	for i := range c.Report().Domains {
		if c.Report().Domains[i].Domain == "calls" {
			calls = &c.Report().Domains[i]
		}
	}
	if calls == nil {
		t.Fatal("no calls row in the report")
	}
	if calls.State != StateUnsupported {
		t.Fatalf("calls = %q for a valid database with an unrecognised schema, want %q",
			calls.State, StateUnsupported)
	}
	if calls.Fingerprint == "" {
		t.Error("Fingerprint is empty — without it 'unsupported' is a dead end for whoever " +
			"has to add support, and it is what distinguishes this from an unreadable file")
	}
}

// GARBAGE BYTES ARE NOT AN UNRECOGNISED SCHEMA. They take a different path and get a
// different state, because the remedies differ: one invites a schema-support issue, the
// other says the backup is damaged.
func TestGarbageBytesAreUnreadableNotUnsupported(t *testing.T) {
	c := build(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/CallHistoryDB/CallHistory.storedata",
			Flags: 1, Data: []byte("this is not a sqlite database")},
	})
	got := states(c.Report())
	if got["calls"] == StateUnsupported {
		t.Error("calls = unsupported_schema for bytes that are not a database — this sends " +
			"someone to file a schema-support issue against a corrupt file")
	}
	if got["calls"] != StateUnreadable {
		t.Errorf("calls = %q, want %q", got["calls"], StateUnreadable)
	}
	// Control: a domain genuinely missing still reads absent, so the three are distinguished
	// rather than everything collapsing to one non-absent bucket.
	if got["notes"] != StateAbsent {
		t.Errorf("notes = %q, want %q", got["notes"], StateAbsent)
	}
}

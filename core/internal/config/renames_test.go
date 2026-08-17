package config

import (
	"strings"
	"testing"
)

// quince#401 — A RENAMED KEY NAMES ITS SUCCESSOR AND ECHOES WHAT WAS LOST.
//
// The defect is not that the typo guard is wrong; it is that "unknown config key (ignored)" is the
// same sentence for a key nobody ever knew and one that was RENAMED, where the user's setting is
// silently not in force. Found in the wild, on an instance where the value happened to match the new
// default — which is exactly why it survived.

// THE CLAIM, DRIVEN THROUGH A REAL Parse rather than by calling the helper. A test that called
// `renameWarning` directly would pass whether or not `unknownKeys` ever consults it — and the
// indexed-path trap below is precisely the way that wiring silently fails.
func TestARenamedStorageKeyNamesItsSuccessorAndItsValue(t *testing.T) {
	_, _, warnings, err := Parse([]byte("storage:\n  - path: /backups\n    zfs:\n      mirror: copy\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var msg string
	for _, w := range warnings {
		if strings.Contains(w.Path, "mirror") {
			msg = w.Message
		}
	}
	if msg == "" {
		t.Fatalf("no warning mentions the retired key at all; got %+v", warnings)
	}
	for _, want := range []string{
		"storage.zfs.seed", // the successor — without it the reader cannot act
		`"copy"`,           // THE VALUE, which is the half that says something was lost
		"NOT in force",     // and says so in as many words
		"qn.5b",            // when, so a reader can date their config
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the renamed-key warning is missing %q:\n%s", want, msg)
		}
	}
	// AND IT MUST NOT STILL READ AS A PLAIN TYPO. The old sentence is what this replaces; both at
	// once would be the fix sitting beside the defect.
	if strings.Contains(msg, "unknown config key") {
		t.Errorf("the warning still reads as an unrecognised key:\n%s", msg)
	}
}

// THE INDEX TRAP, AND IT IS THE ONE THAT WOULD HAVE SHIPPED DEAD. `unknownKeys` reports a storage
// entry's key as `storage[0].zfs.mirror`, and every rename worth recording so far lives inside
// `storage:` — so a table keyed on `storage.zfs.mirror` matches NOTHING unless the index is
// stripped. A table that never fires is indistinguishable from no table, and every test that did not
// use a real indexed config would still pass.
func TestTheRenameTableMatchesAnIndexedPath(t *testing.T) {
	if got := keyIndex("storage[0].zfs.mirror"); got != "storage.zfs.mirror" {
		t.Errorf("keyIndex(%q) = %q, want the schema path", "storage[0].zfs.mirror", got)
	}
	if got := keyIndex("storage[11].zfs.mirror"); got != "storage.zfs.mirror" {
		t.Errorf("a two-digit index is not stripped: %q", got)
	}
	// The SECOND entry, not just the first — an off-by-one in the regex would pass the case above.
	_, _, warnings, err := Parse([]byte(
		"storage:\n  - path: /a\n  - path: /b\n    zfs:\n      mirror: copy\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	hit := false
	for _, w := range warnings {
		if strings.Contains(w.Message, "storage.zfs.seed") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("the rename was not recognised on storage[1]; got %+v", warnings)
	}
}

// A KEY THAT WAS NEVER RENAMED KEEPS THE OLD SENTENCE. The typo guard is not being replaced, and a
// rename table that swallowed ordinary unknown keys would be a worse bug than the one it fixes.
func TestAnOrdinaryUnknownKeyIsUnchanged(t *testing.T) {
	_, _, warnings, err := Parse([]byte("storage:\n  - path: /backups\n    zfs:\n      mirrorr: copy\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Path, "mirrorr") {
			found = true
			if !strings.Contains(w.Message, "unknown config key") {
				t.Errorf("a genuine typo lost the typo-guard sentence: %s", w.Message)
			}
			if strings.Contains(w.Message, "renamed") {
				t.Errorf("a genuine typo was reported as a rename: %s", w.Message)
			}
		}
	}
	if !found {
		t.Errorf("the typo was not reported at all; got %+v", warnings)
	}
}

// IT IS A WARNING, NEVER AN ERROR, and the document still loads. D12's rule is that an unknown key
// is a warning; a rename is a MORE informative warning, not a new refusal — and a config discarded
// over a retired key would be a far bigger change than this issue asked for.
func TestARenamedKeyDoesNotRefuseTheDocument(t *testing.T) {
	cfg, _, warnings, err := Parse([]byte(
		"storage:\n  - path: /backups\n    zfs:\n      mirror: copy\n"))
	if err != nil {
		t.Fatalf("a retired key must not fail the parse: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("the rename produced no warning at all")
	}
	if cfg.Storage == nil || len(*cfg.Storage) != 1 {
		t.Fatalf("the rest of the document did not survive: %+v", cfg.Storage)
	}
	if (*cfg.Storage)[0].Path != "/backups" {
		t.Errorf("the entry carrying the retired key lost its other settings: %+v", (*cfg.Storage)[0])
	}
}

// A VALUE THAT IS NOT A STRING STILL ECHOES AS THE OPERATOR WROTE IT. `%q` on an `any` holding an
// int prints Go syntax rather than the file's text, which is the kind of detail that turns a helpful
// message into a confusing one.
func TestANonStringValueIsEchoedAsWritten(t *testing.T) {
	msg, ok := renameWarning("storage[0].zfs.mirror", 3)
	if !ok {
		t.Fatal("the rename was not recognised")
	}
	if !strings.Contains(msg, `"3"`) {
		t.Errorf("a numeric value is not echoed readably: %s", msg)
	}
}

// EVERY ROW POINTS AT A KEY THE SCHEMA ACTUALLY HAS. A successor that was itself renamed, or typo'd
// when the row was added, sends the operator to a key that does not exist — advice worse than the
// "unknown key" line this replaces, because it sounds authoritative.
func TestEveryRenameSuccessorIsARealKey(t *testing.T) {
	if len(renamedKeys) == 0 {
		t.Fatal("the rename table is empty, so every assertion here is vacuous")
	}
	for old, r := range renamedKeys {
		// Drive the successor through Parse as a real key and assert it is NOT reported unknown.
		// Building the YAML from the path keeps this honest for rows added later.
		doc, ok := yamlForPath(r.successor, "auto")
		if !ok {
			t.Errorf("cannot build a document for successor %q of %q — add a case to yamlForPath "+
				"rather than dropping the assertion", r.successor, old)
			continue
		}
		_, _, warnings, err := Parse([]byte(doc))
		if err != nil {
			t.Errorf("successor %q of %q does not parse: %v", r.successor, old, err)
			continue
		}
		for _, w := range warnings {
			if strings.Contains(w.Message, "unknown config key") {
				t.Errorf("successor %q of %q is not a key the schema knows: %s", r.successor, old, w.Message)
			}
		}
		if r.since == "" {
			t.Errorf("rename %q has no `since` — a reader cannot date their config against it", old)
		}
	}
}

// yamlForPath builds a minimal document declaring one dotted schema path. It knows only the shapes
// the table needs; an unknown shape returns false so the assertion above FAILS LOUDLY rather than
// skipping, which is the difference between a guard and a decoration.
func yamlForPath(path, value string) (string, bool) {
	switch path {
	case "storage.zfs.seed":
		return "storage:\n  - path: /backups\n    zfs:\n      seed: " + value + "\n", true
	}
	return "", false
}

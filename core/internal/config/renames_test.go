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
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the renamed-key warning is missing %q:\n%s", want, msg)
		}
	}
	// NO RUNG NUMBER. `qn.5b` dates nothing for the person reading this message — they have never
	// seen a rung and there is no release that contains the rename. The successor and the lost
	// value are what make it actionable, and they are asserted above.
	if strings.Contains(msg, "qn.") {
		t.Errorf("the renamed-key warning cites a rung at the operator:\n%s", msg)
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
		// NO `since` GUARD. The field is gone: it only ever fed a rung number into a message the
		// operator reads, and a rung dates nothing for them. What the successor must satisfy is
		// asserted above — that it parses, and that the schema knows it.
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

// qn.12 — A RENAMED SECTION SAYS SO, AND ECHOES THE CHILD KEYS.
//
// This is the shape `renames.go` recorded as unsolved: `unknownKeys` never recurses into a key it
// does not recognise, so for a section the only path it ever offers is the PARENT and the only value
// is the whole map beneath it. A leaf-keyed row can never match, and reusing the leaf FORMATTER
// would render Go map syntax at the operator.
//
// Driven through a real Parse for the same reason as the test above: calling the helper directly
// would pass whether or not `unknownKeys` consults it.
func TestARenamedSectionNamesItsSuccessorAndEchoesItsChildren(t *testing.T) {
	_, _, warnings, err := Parse([]byte("automation:\n  staleness_days: 7\n  reminder_cooldown_hours: 12\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var msg string
	for _, w := range warnings {
		if w.Path == "automation" {
			msg = w.Message
		}
	}
	if msg == "" {
		t.Fatalf("no warning is reported against the renamed section at all; got %+v", warnings)
	}
	for _, want := range []string{
		"notifications",           // the successor section — without it the reader cannot act
		"staleness_days: \"7\"",   // THE CHILDREN AND THEIR VALUES, which is what says something was lost
		"reminder_cooldown_hours", // both of them, not just the first
		"NOT in force",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the renamed-section warning is missing %q:\n%s", want, msg)
		}
	}
	// NO RUNG NUMBER, for the reason given on the leaf-rename test above.
	if strings.Contains(msg, "qn.") {
		t.Errorf("the renamed-section warning cites a rung at the operator:\n%s", msg)
	}
	if strings.Contains(msg, "unknown config key") {
		t.Errorf("the warning still reads as an unrecognised key:\n%s", msg)
	}
	// THE FAILURE THAT WOULD OTHERWISE SHIP: `%v` over a map[string]any prints GO MAP SYNTAX at the
	// operator. (It is SORTED -- fmt has sorted map keys since Go 1.12, measured at go1.26.5 -- so the
	// hazard is the syntax, not the order. The ordering hazard is real and lives in the `range` loop
	// that builds the name list, which the test below pins.)
	if strings.Contains(msg, "map[") {
		t.Errorf("the section's value was rendered as a Go map rather than as the user's keys:\n%s", msg)
	}
}

// SORTED, BECAUSE GO RANDOMISES MAP ITERATION PER RUN. An unsorted echo produces a different warning
// on every load of the SAME file, which a reader cannot diff and a test cannot pin. Asserting the
// order once here is cheaper than a flake nobody can reproduce.
func TestTheRenamedSectionEchoIsDeterministicallyOrdered(t *testing.T) {
	in := []byte("automation:\n  staleness_days: 7\n  reminder_cooldown_hours: 12\n  zz_unknown: 1\n")
	first := ""
	for i := 0; i < 8; i++ {
		_, _, warnings, err := Parse(in)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var msg string
		for _, w := range warnings {
			if w.Path == "automation" {
				msg = w.Message
			}
		}
		if i == 0 {
			first = msg
			continue
		}
		if msg != first {
			t.Fatalf("the same file produced two different warnings:\n%s\n%s", first, msg)
		}
	}
	if !strings.Contains(first, "reminder_cooldown_hours") ||
		strings.Index(first, "reminder_cooldown_hours") > strings.Index(first, "staleness_days") {
		t.Errorf("children are not in sorted order:\n%s", first)
	}
}

// AN EMPTY SECTION STILL GETS A SENTENCE, and not a dangling one. `automation:` with nothing under it
// is a real thing to find in a file somebody has been editing, and the successor is still the thing
// they need to know.
func TestARenamedSectionWithNoChildrenStillNamesTheSuccessor(t *testing.T) {
	msg, ok := renameSectionWarning("automation", nil)
	if !ok {
		t.Fatalf("an empty renamed section produced no warning")
	}
	if !strings.Contains(msg, "notifications") || !strings.Contains(msg, "IGNORED") {
		t.Errorf("the empty-section sentence does not carry the successor:\n%s", msg)
	}
	if strings.Contains(msg, "your settings  are") {
		t.Errorf("the empty-section sentence has a dangling echo:\n%s", msg)
	}
}

// THE RENAME MUST NOT HAVE LEFT THE NEW SECTION UNREACHABLE. A rename that spells the successor
// wrong in the struct tag would produce a perfect warning pointing at a key that is ALSO unknown —
// the defect one level down, and invisible to every test above.
func TestTheSuccessorSectionActuallyParses(t *testing.T) {
	cfg, _, warnings, err := Parse([]byte("notifications:\n  staleness_days: 9\n  backup_completed: true\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, w := range warnings {
		if strings.Contains(w.Path, "notifications") {
			t.Errorf("the successor section is itself unknown: %+v", w)
		}
	}
	if cfg.Notifications.StalenessDays != 9 {
		t.Errorf("staleness_days = %d, want 9", cfg.Notifications.StalenessDays)
	}
	if !cfg.Notifications.BackupCompleted {
		t.Errorf("backup_completed was not read back as true")
	}
	// The keys the file did NOT carry keep their defaults rather than zeroing.
	if cfg.Notifications.OverdueDays != 14 {
		t.Errorf("overdue_days = %d, want the default 14", cfg.Notifications.OverdueDays)
	}
	if !cfg.Notifications.ActionRequired {
		t.Errorf("action_required defaulted to false; the four that matter default ON")
	}
}

// A NESTED CHILD MUST NOT RENDER AS A GO MAP. `quoteValue` falls through to `%v`, which on a nested
// mapping prints `map[a:1 b:2]` — the exact failure the section form exists to prevent, arriving one
// level down. `automation:` never had a nested child; `renamedSections` is a general mechanism and
// the next section put in it may.
func TestANestedChildIsNamedRatherThanDumpedAsAGoMap(t *testing.T) {
	msg, ok := renameSectionWarning("automation", map[string]any{
		"staleness_days": 7,
		"nested":         map[string]any{"a": 1, "b": 2},
		"a_list":         []any{1, 2},
	})
	if !ok {
		t.Fatalf("no warning for a section with a nested child")
	}
	if strings.Contains(msg, "map[") {
		t.Errorf("a nested child was dumped as Go map syntax:\n%s", msg)
	}
	for _, want := range []string{"nested: (a nested section)", "a_list: (a list)", "staleness_days"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q:\n%s", want, msg)
		}
	}
}

package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// RENAMED KEYS, AND WHY THE TYPO GUARD IS NOT ENOUGH ON ITS OWN (quince#401).
//
// `unknownKeys` reports a key the schema does not know as *"unknown config key … (ignored)"*, which
// is correct and, for a key that was RENAMED, unhelpful in a specific way: it does not say that a
// key by that name used to exist, or what replaced it. So a setting the user deliberately chose is
// quietly not in force, and the only signal names neither the successor nor the loss.
//
// FOUND IN THE WILD rather than reasoned about, on the live stand while writing its `storage:` entry
// (quince#378). On that instance there was no behaviour difference at all — the old value was `auto`
// and `seed`'s default is `auto`, so it matched by luck. **That is what makes it easy to miss.**
// `mirror: copy`, a reasonable thing to set where reflink misbehaves, reads today as `seed: auto`.
//
// ECHOING THE VALUE IS THE LOAD-BEARING HALF, and the issue is explicit that it is: it turns *"a key
// you do not recognise"* into *"the thing you set is not happening"*, which is a different sentence
// to act on. A warning naming only the successor still leaves the reader to work out whether they
// lost anything.
//
// DELIBERATELY NOT AUTO-MIGRATING. Silently rewriting a config to mean what quince guesses is a
// bigger promise than this is worth, and `CLAUDE.md`'s `config.yml` rule is that the file contains
// only what the user set. A loud warning is the proportionate answer.
//
// THE TABLE'S VALUE IS THE NEXT RENAME, not this one. It starts at one row on purpose; what it buys
// is somewhere for the next rename to be recorded, rather than being discovered by whoever inherits
// the config. Two known candidates are deliberately NOT here — see below.
var renamedKeys = map[string]renamedKey{
	// qn.5b renamed this when the reflink moved commit→seed; `contracts.md` records the rename and
	// nothing in the RUNNING system did.
	"storage.zfs.mirror": {successor: "storage.zfs.seed", since: "qn.5b"},
}

type renamedKey struct {
	successor string
	since     string // the rung that renamed it, so a reader can date their config against it
}

// WHAT IS DELIBERATELY ABSENT, because an entry here is a claim that the key reaches this code path:
//
//   - `backup.transport` → `preferred_transport` (quince#654). The old key sat under a `backup:`
//     section that no longer exists, so what `unknownKeys` reports is the unknown PARENT — `backup`
//     — and a row keyed on the leaf would never match. **THE SHAPE THIS ASKED FOR NOW EXISTS** —
//     `renamedSections` below, built by qn.12 for `automation:` → `notifications:`. This row is
//     still absent, and now for a smaller reason: nobody has checked whether `backup:` is reported
//     as an unknown parent in a real config, and an entry here is a claim that it reaches this code
//     path. The thinking is done; the measurement is not.
//   - `storage.zfs.hook_cmd` and `storage.zfs.mode` (quince#818). Both are RETIRED rather than
//     renamed, and both are deliberately still declared in the schema so `Validate` refuses them
//     against their exact path — see their field comments. They never reach the typo guard at all,
//     which is the whole point of keeping the fields.
//
// Each is a real candidate and neither is a row. Named so the absence reads as a decision.

// keyIndex strips list indices from a reported path, so a table keyed on the SCHEMA path matches a
// warning about one entry: `unknownKeys` reports `storage[0].zfs.mirror`, and the rename is a fact
// about `storage.zfs.mirror` regardless of which entry carried it.
//
// THIS IS THE PART THAT WOULD HAVE MADE THE TABLE SILENTLY DEAD. Every rename worth recording so far
// is inside `storage:`, which is the one section reported with an index — so a table keyed on the
// obvious spelling would match nothing, forever, with every test that did not use a real config
// still passing. There is a test for exactly this.
var keyIndexRe = regexp.MustCompile(`\[[0-9]+\]`)

func keyIndex(path string) string { return keyIndexRe.ReplaceAllString(path, "") }

// renameWarning returns the message for a key that was renamed, and ok=false for one that was not.
//
// `value` is what the file actually carried, rendered with `%v` — it is echoed back so the warning
// says what is not in force rather than merely which key is unknown. A nil or absent value still
// produces a useful sentence; the successor and the loss are the claim, and the value sharpens it.
func renameWarning(path string, value any) (string, bool) {
	r, ok := renamedKeys[keyIndex(path)]
	if !ok {
		return renameSectionWarning(path, value)
	}
	// The successor is spelled at the SCHEMA path, and the reported path may be indexed. Naming the
	// indexed successor would be more precise and is not worth the string surgery: an operator
	// reading `storage[0].zfs.mirror` and told to move it to `storage.zfs.seed` is not confused
	// about which entry they are editing, because they are looking at it.
	return fmt.Sprintf("`%s` was renamed to `%s` in %s and is IGNORED — your value %v is NOT in "+
		"force; move it to `%s`.", path, r.successor, r.since, quoteValue(value), r.successor), true
}

// RENAMED SECTIONS — the shape the table above named as unsolved, built by qn.12 (quince#1124).
//
// A renamed SECTION is not a renamed key with a longer path, and reusing `renamedKeys` for one
// produces a warning nobody can act on. `unknownKeys` never recurses into a key it does not
// recognise, so for a section the only path it ever offers is the PARENT — and the `value` it hands
// over is the whole `map[string]any` beneath it. Rendering that with `%v` yields Go map syntax in
// nondeterministic order, which is the opposite of the thing the echo exists to do: tell the
// operator, in their own words, which of their settings stopped happening.
//
// So the section form echoes the CHILD KEYS, sorted, each with its value.
var renamedSections = map[string]renamedSection{
	// qn.12. Both keys existed only to answer *notify or not*, so the name stopped being accurate
	// when the Shortcut opportunity signal left the rung's scope (quince#1124).
	"automation": {successor: "notifications", since: "qn.12"},
}

type renamedSection struct {
	successor string
	since     string
}

// renameSectionWarning is renameWarning's counterpart for a whole section.
//
// DELIBERATELY NOT AUTO-MIGRATING, for the same reason stated at the top of this file: silently
// rewriting a config to mean what quince guesses is a bigger promise than this is worth, and
// `config.yml` holds only what the user set.
//
// IT DOES NOT CLAIM EVERY CHILD SURVIVED THE RENAME. It says the section moved and lists what the
// file carried; whether a particular child still exists under the successor is `unknownKeys`'
// business once the operator has moved the block, and telling them here would be a second claim
// this table has no way to check.
func renameSectionWarning(path string, value any) (string, bool) {
	r, ok := renamedSections[keyIndex(path)]
	if !ok {
		return "", false
	}
	children, _ := value.(map[string]any)
	if len(children) == 0 {
		// An empty or non-mapping section: there is nothing to echo, so the sentence is the move
		// alone rather than a dangling "your keys ".
		return fmt.Sprintf("`%s:` was renamed to `%s:` in %s and the whole section is IGNORED; "+
			"move it to `%s:`.", path, r.successor, r.since, r.successor), true
	}
	names := make([]string, 0, len(children))
	for k := range children {
		names = append(names, k)
	}
	// SORTED, because Go map iteration is randomised per run and a warning whose text changes between
	// two loads of the same file is one a reader cannot diff or grep for.
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s: %v", k, quoteValue(children[k])))
	}
	return fmt.Sprintf("`%s:` was renamed to `%s:` in %s and the whole section is IGNORED — your "+
		"settings %s are NOT in force; move them under `%s:`.",
		path, r.successor, r.since, strings.Join(parts, ", "), r.successor), true
}

// quoteValue renders a scalar the way it appeared, and says so plainly when there is nothing to
// echo. `%q` on an `any` holding a non-string prints Go syntax, which is not what the operator
// typed — `%v` inside quotes is closer to the file.
func quoteValue(value any) string {
	if value == nil {
		return "(empty)"
	}
	if s, ok := value.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q", fmt.Sprintf("%v", value))
}

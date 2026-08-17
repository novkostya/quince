package config

import (
	"fmt"
	"regexp"
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
//     — and a row keyed on the leaf would never match. Whoever adds it has to decide what a renamed
//     SECTION should say, which is a different shape from a renamed leaf and wants its own thinking.
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
		return "", false
	}
	// The successor is spelled at the SCHEMA path, and the reported path may be indexed. Naming the
	// indexed successor would be more precise and is not worth the string surgery: an operator
	// reading `storage[0].zfs.mirror` and told to move it to `storage.zfs.seed` is not confused
	// about which entry they are editing, because they are looking at it.
	return fmt.Sprintf("`%s` was renamed to `%s` in %s and is IGNORED — your value %v is NOT in "+
		"force; move it to `%s`.", path, r.successor, r.since, quoteValue(value), r.successor), true
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

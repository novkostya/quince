package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// THE TWO SETS ARE HELD IN AGREEMENT BY A TEST, BECAUSE NOTHING ELSE HOLDS THEM (quince#1015).
//
// `contracts.md` states `tls_unusable_code`'s values as a closed set — *"`unreadable | malformed |
// mismatched | not_yet_valid | expired | unknown`. A CLASSIFICATION and never a path or loader
// text."* That is a totality claim about an UNAUTHENTICATED field. What backs it is
// `tlsUnusableCode` returning `tlsx.Inspect`'s outcome straight through, so a seventh outcome added
// to `tlsx` — by someone working on certificate handling and not thinking about onboarding — is
// disclosed pre-auth and falsifies canon with no test failing.
//
// THE PASS-THROUGH IS RIGHT AND STAYS. One classification in the product beats two that drift; the
// gap is that the choice had an unguarded consequence, not that the choice is wrong.
//
// READ OUT OF THE SOURCE, NEVER RESTATED. A list of outcomes written here would be a third copy of
// the same set, stale the moment `tlsx` grows one — the defect one layer along, which is exactly
// what quince#1012 refused to do for the helper's arms a day earlier. The floors below are its
// anti-vacuity half: a parse that silently matches nothing would otherwise pass loudest of all.
//
// `wrong_host` is the existence proof that this is not hypothetical: it is a real `tlsx` outcome
// today, absent from the contracts list, kept out by a `""` at one call site rather than by
// anything structural.

// constsWithPrefix reads `<prefix>*` string constants out of one file and returns name → value.
// Only untyped string literals are collected — anything computed is not an enum member and is not
// this test's business.
func constsWithPrefix(t *testing.T, file, prefix string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	out := map[string]string{}
	ast.Inspect(parsed, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, prefix) || i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			out[name.Name] = v
		}
		return true
	})
	return out
}

// cannotReachTheWire names every `tlsx` outcome this endpoint cannot emit, WITH THE REASON.
//
// An exclusion is a decision, so it is written down where the decision is checked rather than
// inferred from a handler branch. Both entries are asserted to still be real outcomes below — an
// exclusion for a value that no longer exists is a guard that has quietly stopped guarding.
var cannotReachTheWire = map[string]string{
	"usable": "a pair that inspects clean is not a failure; tlsUnusableCode returns `unknown` for it " +
		"and the field is omitted entirely when TLS is off or serving",
	"wrong_host": "the call site passes \"\" for the hostname, and inspect.go gates this outcome on " +
		"hostname != \"\" — the certificate STEP asks that question, with the name the user typed",
}

func TestEveryTLSOutcomeIsDecidedBeforeItCanGoPreAuth(t *testing.T) {
	outcomes := constsWithPrefix(t, "../tlsx/inspect.go", "Outcome")
	wire := constsWithPrefix(t, "../wire/objects.go", "TLSUnusable")

	// ANTI-VACUITY, both sides. A rename, a move to another file, or a prefix change would leave
	// this test passing over an empty set and asserting nothing — the shape quince#1012 named.
	// The floors are BELOW today's counts on purpose: this guards against a parse going quiet, not
	// against the sets changing, which is the whole point of reading them rather than listing them.
	if len(outcomes) < 5 {
		t.Fatalf("parsed only %d tlsx Outcome* constants (%v) — the parse went quiet, so every "+
			"assertion below is vacuous", len(outcomes), outcomes)
	}
	if len(wire) < 5 {
		t.Fatalf("parsed only %d wire.TLSUnusable* constants (%v) — the parse went quiet", len(wire), wire)
	}

	declared := map[string]string{} // value → the wire constant declaring it
	for name, v := range wire {
		declared[v] = name
	}

	for name, v := range outcomes {
		if _, ok := declared[v]; ok {
			continue
		}
		if _, ok := cannotReachTheWire[v]; ok {
			continue
		}
		t.Errorf("tlsx.%s = %q is neither a declared wire.TLSUnusable* value nor listed as one this "+
			"endpoint cannot produce.\n"+
			"GET /api/onboarding/https returns tlsx.Inspect's outcome UNAUTHENTICATED, so adding an "+
			"outcome disclosed it pre-auth and made docs/contracts.md's value list wrong.\n"+
			"Decide which it is: add it to wire (and to contracts.md), or add it to "+
			"cannotReachTheWire with the reason it cannot arrive.", name, v)
	}

	// THE EXCLUSIONS MUST STILL NAME REAL OUTCOMES. Otherwise `wrong_host` gets renamed in `tlsx`,
	// the new name falls through to the error above — correctly — and this stale entry sits here
	// looking like a live decision about a value that no longer exists.
	byValue := map[string]bool{}
	for _, v := range outcomes {
		byValue[v] = true
	}
	for v := range cannotReachTheWire {
		if !byValue[v] {
			t.Errorf("cannotReachTheWire excludes %q, which is no longer a tlsx outcome — the "+
				"exclusion is stale and guards nothing", v)
		}
	}
}

// THE OTHER DIRECTION, which catches a RENAME where the first test catches an ADDITION.
//
// The three existing handler tests pin `unreadable`, `expired` and `mismatched` as string literals,
// so they already fail on a rename of those. The other three are declared, exported and asserted by
// nothing — `not_yet_valid` most of all, since reaching it needs a certificate from the future.
//
// `unknown` is the one wire value with no `tlsx` outcome behind it, and that is by ruling rather
// than by omission: it is what the handler SYNTHESISES when a pair inspects clean and still is not
// loaded. Named here so the exemption is a decision on the record instead of a hole in the loop.
func TestEveryDocumentedTLSCodeIsAThingTLSXCanSay(t *testing.T) {
	outcomes := constsWithPrefix(t, "../tlsx/inspect.go", "Outcome")
	wire := constsWithPrefix(t, "../wire/objects.go", "TLSUnusable")
	if len(outcomes) < 5 || len(wire) < 5 {
		t.Fatalf("parse went quiet: %d outcomes, %d wire constants", len(outcomes), len(wire))
	}

	produced := map[string]bool{}
	for _, v := range outcomes {
		produced[v] = true
	}
	for name, v := range wire {
		if v == "unknown" || produced[v] {
			continue
		}
		t.Errorf("wire.%s = %q is documented as a value of tls_unusable_code, and no tlsx outcome "+
			"produces it — so either tlsx renamed one out from under the wire, or this constant is "+
			"documentation for something that can never appear", name, v)
	}
}

package config

import (
	"reflect"
	"strings"
	"testing"
)

// The declared set and the marshaller that reads it (qn.6j, quince#728). These drive MarshalDeclared
// DIRECTLY, which is narrower than the write path it serves: `replaceLocked` calls it with a declared
// set it has already unioned and filtered, and that assembly is asserted next door in
// writedeclared_test.go. What is covered here is the marshaller's own contract, given a set.

func parseDeclared(t *testing.T, raw string) (Config, Declared) {
	t.Helper()
	cfg, d, _, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg, d
}

// The declared set is what the FILE said, never what Resolved() filled in. This is the whole
// distinction the rung turns on: `- path: /backups` declares one key, and the entry it produces
// carries six.
func TestDeclaredRecordsOnlyWhatTheFileSaid(t *testing.T) {
	_, d := parseDeclared(t, "storage:\n  - path: /backups\n")

	for _, want := range []string{"storage", "storage[/backups].path"} {
		if !d.Has(want) {
			t.Errorf("declared is missing %q; it has %v", want, keysOf(d))
		}
	}
	// Every one of these is present in the RESOLVED entry and absent from the file.
	for _, unwanted := range []string{
		"storage[/backups].name", "storage[/backups].backend", "storage[/backups].default",
		"storage[/backups].zfs", "storage[/backups].retention", "backup", "tls",
	} {
		if d.Has(unwanted) {
			t.Errorf("declared claims %q, which the file never wrote", unwanted)
		}
	}
}

// An entry is keyed by NAME when it has one, so a declared path survives the entry moving in the
// list. Keyed by index it would silently re-point on any insertion.
func TestDeclaredKeysAnEntryByItsName(t *testing.T) {
	_, d := parseDeclared(t,
		"storage:\n  - name: nas\n    path: /a\n    backend: reflink\n    default: true\n"+
			"  - name: usb\n    path: /b\n    backend: hardlink\n")

	for _, want := range []string{"storage[nas].backend", "storage[usb].backend", "storage[nas].default"} {
		if !d.Has(want) {
			t.Errorf("declared is missing %q; it has %v", want, keysOf(d))
		}
	}
	if d.Has("storage[0].backend") || d.Has("storage[usb].default") {
		t.Errorf("declared is keyed wrongly: %v", keysOf(d))
	}
}

// THE FIXED POINT, and it is the property the whole design rests on: what MarshalDeclared writes,
// re-parsed, declares the same set and resolves to the same config. That is what makes the record
// survive a restart without anything being persisted — the file IS the record.
func TestAMinimalFileIsAFixedPoint(t *testing.T) {
	const raw = "storage:\n    - path: /backups\n"
	cfg, d := parseDeclared(t, raw)

	out, err := MarshalDeclared(cfg, d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != raw {
		t.Fatalf("not a fixed point.\n got: %q\nwant: %q", out, raw)
	}

	// And again, from what was just written — twice in a row, because a rule that converges on the
	// second pass would still re-inflate the file once per save.
	cfg2, d2 := parseDeclared(t, string(out))
	out2, err := MarshalDeclared(cfg2, d2)
	if err != nil {
		t.Fatalf("marshal 2: %v", err)
	}
	if string(out2) != raw {
		t.Fatalf("second pass diverged.\n got: %q\nwant: %q", out2, raw)
	}
}

// An explicitly-written default SURVIVES, which is the clause that makes the rule "only what was
// set" rather than "only what differs from the default" — spec D5. Deleting it would change the
// user's file's meaning the day a default changes.
//
// THE INPUT HERE IS IN CANONICAL KEY ORDER AND THAT IS NOT INCIDENTAL. A fixed point is only a
// fixed point for a file already in struct-field order: write `ui:` above `storage:` by hand and
// the first save reorders it, because struct field order IS the key order (D12's deterministic
// regeneration). Measured while writing this test, which had it the other way round and failed
// on the reorder rather than on the value.
func TestAnExplicitlySetDefaultValueIsKept(t *testing.T) {
	const raw = "storage:\n    - path: /backups\nui:\n    theme: system\n"
	cfg, d := parseDeclared(t, raw)

	out, err := MarshalDeclared(cfg, d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != raw {
		t.Fatalf("an explicitly-written default was not preserved.\n got: %q\nwant: %q", out, raw)
	}
}

// THE GENERAL ROUND-TRIP INVARIANT (spec G4): for every document, what comes back from what was
// written is deeply equal to what was written. This is the gate that catches an omission the
// case-by-case tests missed, which is worth more than any one of them.
func TestWhatIsWrittenParsesBackToTheSameConfig(t *testing.T) {
	for _, raw := range []string{
		"storage:\n  - path: /backups\n",
		"storage:\n  - path: /backups\n    backend: hardlink\n",
		"backup:\n  require_encryption: false\nstorage:\n  - path: /backups\n",
		"storage:\n  - name: nas\n    path: /a\n    default: true\n  - name: usb\n    path: /b\n",
		"sessions:\n  allow_insecure_transport: true\nstorage:\n  - path: /backups\n",
		"storage:\n  - path: /backups\n    retention:\n      keep_recent: 0\n",
		"storage:\n  - path: /backups\n    zfs:\n      parent_dataset: tank/q\n      mode: hook\n",
	} {
		cfg, d := parseDeclared(t, raw)
		out, err := MarshalDeclared(cfg, d)
		if err != nil {
			t.Fatalf("marshal %q: %v", raw, err)
		}
		back, _, _, err := Parse(out)
		if err != nil {
			t.Fatalf("re-parse of %q: %v\nwritten: %q", raw, err, out)
		}
		if !reflect.DeepEqual(cfg, back) {
			t.Errorf("round trip changed the config for %q\nwritten: %q\n got: %+v\nwant: %+v",
				raw, out, back, cfg)
		}
	}
}

// A storage entry ALWAYS keeps its path, whatever the declared set says — an entry without one is
// not a storage, it is a mapping the next parse would refuse. The "could not be re-parsed without
// it" clause of the write rule, not an exception to it.
func TestAnEntryKeepsItsPathEvenWhenNothingDeclaresIt(t *testing.T) {
	cfg, _ := parseDeclared(t, "storage:\n  - path: /backups\n    backend: hardlink\n")

	out, err := MarshalDeclared(cfg, Declared{"storage": true}) // deliberately declares no leaf
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, _, _, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v\nwritten: %q", err, out)
	}
	if back.Storage == nil || len(*back.Storage) != 1 || (*back.Storage)[0].Path != "/backups" {
		t.Fatalf("the entry lost its path and the document no longer describes a storage: %q", out)
	}
}

func keysOf(d Declared) []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	return out
}

// CANONICAL ORDER SURVIVES A KEPT-BUT-UNDECLARED KEY, which is the property the whole node-prune
// design was chosen for and the one the first version of pruneSequence broke.
//
// `path` is kept whatever the declared set says. When the entry ALSO keeps a key that sorts before
// it — `name` — the output must read `name, path`, the struct's own field order. The original
// implementation removed `path` and re-inserted it at the front, producing `path, name`: wrong order,
// in the function whose job is to preserve it, unreachable in production only because Validate
// refuses a nameless entry. Found at review of quince#758.
func TestAKeptKeyDoesNotJumpToTheFrontOfItsEntry(t *testing.T) {
	cfg, _ := parseDeclared(t, "storage:\n  - name: nas\n    path: /backups\n    backend: hardlink\n")

	// `name` declared, `path` NOT — so path survives only as a kept key, which is the ordering case.
	out, err := MarshalDeclared(cfg, Declared{"storage": true, "storage[nas].name": true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	name, path := strings.Index(string(out), "name:"), strings.Index(string(out), "path:")
	if name < 0 || path < 0 {
		t.Fatalf("expected both keys in the output, got: %q", out)
	}
	if name > path {
		t.Errorf("`path` sorted before `name`; canonical order is name, path, default, backend, "+
			"zfs, retention.\ngot: %q", out)
	}
	if _, _, _, err := Parse(out); err != nil {
		t.Errorf("re-parse: %v\nwritten: %q", err, out)
	}
}

// THE TWO ENTRY KEYS MUST AGREE, and until now only the round-trip tests would have noticed if they
// stopped (architect note, quince#758).
//
// `entryKey` runs at PARSE, over the raw document, where `name` may be absent. `nodeEntryKey` runs at
// MARSHAL, over a node tree that `Resolved()` has already filled. If they ever disagree, the declared
// set records paths under one identity and the pruner looks them up under another — every key reads
// as undeclared, and the file silently loses everything the user wrote. Asserted directly, because
// the indirect version does not survive a refactor.
func TestTheParseAndMarshalEntryKeysAgree(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"storage:\n  - path: /backups\n", "/backups"},                 // no name: both fall back to path
		{"storage:\n  - name: nas\n    path: /backups\n", "nas"},       // a name: both take it
		{"storage:\n  - name: \"\"\n    path: /backups\n", "/backups"}, // an EMPTY name is not a name
	} {
		cfg, d := parseDeclared(t, tc.raw)
		if !d.Has("storage[" + tc.want + "].path") {
			t.Errorf("parse side keyed %q as something else: %v", tc.want, keysOf(d))
		}
		// The marshal side agrees iff the document round-trips carrying its path — which it can only
		// do if the pruner found the same identity the declared set used.
		out, err := MarshalDeclared(cfg, d)
		if err != nil {
			t.Fatalf("marshal %q: %v", tc.raw, err)
		}
		back, _, _, err := Parse(out)
		if err != nil {
			t.Fatalf("re-parse %q: %v", tc.raw, err)
		}
		if !SameConfig(back, cfg) {
			t.Errorf("the two entry keys disagree for %q — the pruner looked the declared paths up "+
				"under a different identity\nwritten: %q", tc.raw, out)
		}
	}
}

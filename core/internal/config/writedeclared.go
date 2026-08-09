package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SameConfig is THE comparator for "did this document survive a round trip?", and it has exactly two
// callers on purpose (architect condition, quince#753): the runtime guard in `replaceLocked` and the
// round-trip gate in the tests.
//
// A gate and a runtime guard that can disagree about the same invariant is worse than either alone —
// the gate goes green, the guard fires in production, and the two get debugged as separate problems.
// If a second implementation of "deeply equal" ever appears beside this, that is the defect.
//
// `Retention` is a pointer and DeepEqual compares what it points AT, which is what this wants:
// two configs that mean the same thing are the same config, whoever allocated them.
func SameConfig(a, b Config) bool { return reflect.DeepEqual(a, b) }

// changedKeys reports every key path whose value THIS WRITE changes.
//
// IT IS CLAUSE 2 OF THE WRITE RULE AND IT IS NOT OPTIONAL (spec D2). `PUT /api/config` arrives as a
// COMPLETE document — the request cannot distinguish a key the user touched from one the UI merely
// echoed back — so "what was set" on that path is recoverable only as a diff against the
// configuration that was live. Without it, a user who changes their theme gets a file that still
// says nothing about the theme.
//
// BOTH SIDES ARE RESOLVED, which is quince#754's fix doing its work here: `replaceLocked` resolves
// before anything looks at the document, so an unfilled key on one side and a defaulted key on the
// other cannot read as a change. Before that fix this function would have called `name`, `backend`,
// `zfs.mode` and `zfs.seed` changed on every partial PUT and written them all back — the rung's own
// defect, reached from the other side.
func changedKeys(old, next Config) (Declared, error) {
	oldMap, err := toMap(old)
	if err != nil {
		return nil, err
	}
	nextMap, err := toMap(next)
	if err != nil {
		return nil, err
	}
	out := Declared{}
	diffMaps(oldMap, nextMap, "", out)
	return out, nil
}

// toMap renders a Config as the same generic tree `Parse` walks, so a path computed here means
// exactly what a path in `Declared` means. Going through the marshaller rather than reflecting over
// the struct is deliberate: the yaml key names and the nesting are the marshaller's to decide, and
// two opinions about them is how a declared path stops matching the key it names.
func toMap(c Config) (map[string]any, error) {
	data, err := Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// diffMaps records every leaf path present in next whose value differs from old's.
func diffMaps(old, next map[string]any, prefix string, into Declared) {
	for k, nv := range next {
		path := prefix + k
		ov, had := old[k]
		switch typed := nv.(type) {
		case map[string]any:
			oldSub, _ := ov.(map[string]any)
			if oldSub == nil {
				oldSub = map[string]any{}
			}
			diffMaps(oldSub, typed, path+".", into)
		case []any:
			diffSeq(ov, typed, path, into)
		default:
			if !had || !sameScalar(ov, nv) {
				into[path] = true
			}
		}
	}
}

// diffSeq diffs a sequence of mappings ENTRY BY ENTRY, matched on the entry's own name rather than
// its index — the same identity `Declared` uses. Matching on index would report every key of every
// entry as changed the moment a storage was inserted ahead of another, which would re-inflate the
// file for a list edit that touched nothing.
func diffSeq(oldVal any, next []any, prefix string, into Declared) {
	oldItems, _ := oldVal.([]any)
	byName := map[string]map[string]any{}
	for _, it := range oldItems {
		if m, ok := it.(map[string]any); ok {
			byName[entryKey(m)] = m
		}
	}
	for _, it := range next {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		key := entryKey(m)
		prev, had := byName[key]
		if !had {
			// A NEW ENTRY IS DIFFED AGAINST WHAT IT WOULD DEFAULT TO, not against nothing.
			//
			// Against an empty map every key of the entry reads as *set by this write*, so the add
			// door wrote eleven keys for the two a caller supplied — 292 bytes for a first-run file,
			// measured on quince#759. That is this rung's own defect surviving at the one door most
			// users meet first.
			//
			// The baseline is `StorageEntry{Path: …}.Resolved()`: what quince would have filled in
			// had the user written the path alone. A value matching it was not chosen; a value
			// differing from it was. This is the test D5 REJECTS for the document at large — there
			// it would delete a key the user explicitly wrote — and it is right here for the
			// opposite reason: a new entry has no prior file it could have been written in, so
			// "differs from what it would default to" is the only signal that exists.
			prev = newEntryBaseline(m, len(next) == 1)
		}
		diffMaps(prev, m, prefix+"["+key+"].", into)
	}
}

// newEntryBaseline renders what a storage entry declared as nothing but its path would resolve to.
//
// `lone` carries the list-level rule: `ResolveStorages` implies `default: true` on a single entry, so
// in a one-entry list a `true` was implied rather than chosen. With a second entry present the
// implication stops applying and a `true` becomes a real choice — which is why the same value is
// undeclared in one case and declared in the other.
func newEntryBaseline(entry map[string]any, lone bool) map[string]any {
	path, _ := entry["path"].(string)
	base := StorageEntry{Path: path}.Resolved()
	base.Default = lone
	m, err := toMapOf(base)
	if err != nil {
		return map[string]any{} // fall back to "everything was set": over-writes rather than loses
	}
	return m
}

// toMapOf renders any value through the marshaller into the generic tree the diff walks.
func toMapOf(v any) (map[string]any, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// sameScalar compares two decoded YAML scalars. They come back as typed values (bool, int, string),
// so a formatted comparison is enough and avoids caring which numeric type the decoder chose.
func sameScalar(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) }

// union returns d plus every path in extra. Neither input is mutated: `s.declared` is shared under
// `mu` and a write that mutated it in place would publish a half-built set to a concurrent reader.
func (d Declared) union(extra Declared) Declared {
	out := make(Declared, len(d)+len(extra))
	for k := range d {
		out[k] = true
	}
	for k := range extra {
		out[k] = true
	}
	return out
}

// sorted renders a Declared for a message, so a warning naming lost keys is stable to read and to
// test against.
func (d Declared) sorted() []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// String makes a Declared readable in a test failure without every caller building the list.
func (d Declared) String() string { return strings.Join(d.sorted(), " ") }

// lostPaths names what a failed round trip disagreed about, for the warning and the log line.
// A guard that fires without saying WHAT it caught is a guard nobody can act on.
func lostPaths(want, got Config, perr error) string {
	if perr != nil {
		return "the written document did not parse: " + perr.Error()
	}
	diff, err := changedKeys(got, want)
	if err != nil || len(diff) == 0 {
		return "(the difference could not be named)"
	}
	return diff.String()
}

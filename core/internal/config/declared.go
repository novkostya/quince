package config

import (
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Declared is the set of key paths PRESENT in config.yml as it was read — what the user actually
// wrote, before `Resolved()` filled anything in (qn.6j, quince#728; Operator ruling 2026-08-08,
// carried in `docs/quince.stack.md` D12).
//
// NOTHING IS PERSISTED, AND THAT IS THE DESIGN. This is derived from the file at every `Load`, so
// it survives a restart by construction — the file IS the record of what was set. There is no
// sidecar, no app-DB column, and nothing that can disagree with a hand-edit. What `Service` holds
// is a cache of a fact the file already carries.
//
// PATH FORM. Dotted for nested structs (`tls.cert_file`), and storage entries are keyed by their
// NAME rather than their index — `storage[/backups].backend`. Entries are added, forgotten and
// reordered, `DELETE /api/config/storage/{name}` already treats the name as the identity, and an
// index would silently re-point every declared key the moment a list changed. At parse time an
// entry's name is `name` if present, else `path`, which is the rule `Resolved()` applies one step
// later.
type Declared map[string]bool

// Has reports whether a path was written. A nil Declared has nothing, which is the correct answer
// for a config that was never read from a file.
func (d Declared) Has(path string) bool { return d[path] }

// declaredKeys walks a decoded YAML mapping against the struct's yaml tags and records every path
// the document actually contains.
//
// It is `unknownKeys` with a different accumulator, deliberately: that function already knows how to
// recurse into nested structs and into slices of structs, and a second traversal that could disagree
// with it about what a key path IS would be worth more trouble than it saves. The one difference is
// the storage index — `unknownKeys` reports `storage[0].` because an INDEX is what a user needs to
// find their typo, and this needs the NAME because a name is what survives a list edit.
func declaredKeys(raw map[string]any, t reflect.Type, prefix string, into Declared) {
	known := map[string]reflect.StructField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		known[name] = f
	}
	for k, v := range raw {
		f, ok := known[k]
		if !ok {
			continue // an unknown key is a warning's business, not this one's
		}
		path := prefix + k
		into[path] = true
		switch ft := deref(f.Type); ft.Kind() {
		case reflect.Struct:
			if sub, ok := v.(map[string]any); ok {
				declaredKeys(sub, ft, path+".", into)
			}
		case reflect.Slice:
			elem := deref(ft.Elem())
			if elem.Kind() != reflect.Struct {
				continue
			}
			items, ok := v.([]any)
			if !ok {
				continue
			}
			for _, item := range items {
				sub, ok := item.(map[string]any)
				if !ok {
					continue
				}
				declaredKeys(sub, elem, path+"["+entryKey(sub)+"].", into)
			}
		}
	}
}

// entryKey is a storage entry's identity as written: `name` if the user gave one, else `path`.
// Mirrors `Resolved()`'s `if e.Name == "" { e.Name = e.Path }` one step earlier, so a declared path
// and the resolved entry it describes agree on what the entry is called.
func entryKey(raw map[string]any) string {
	if s, ok := raw["name"].(string); ok && s != "" {
		return s
	}
	if s, ok := raw["path"].(string); ok {
		return s
	}
	return ""
}

// MarshalDeclared serializes config carrying ONLY what the user set, plus what the file could not be
// re-parsed without. NOTHING CALLS IT YET — `replaceLocked` still writes the full document, so this
// PR changes no behaviour; the switch is its own change with its own proof (spec PR 4).
//
// IT PRUNES A MARSHALLED NODE TREE RATHER THAN EMITTING FROM A REFLECT WALK. Marshalling first is
// what preserves canonical key order for free — struct field order IS the key order — and a prune
// cannot reorder what it only removes. Emitting from a walk would re-derive the yaml names and
// nesting `yaml.Marshal` already knows, which is a second encoder that can disagree with the first.
func MarshalDeclared(c Config, d Declared) ([]byte, error) {
	full, err := Marshal(c)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(full, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return full, nil // an empty or non-mapping document has nothing to prune
	}
	pruneMapping(doc.Content[0], d, "", nil)
	return yaml.Marshal(&doc)
}

// pruneMapping removes every key the user did not write, bottom-up so a section whose last child
// goes does not survive as `backup: {}`.
//
// `keep` names leaf keys this mapping must retain whatever the declared set says — the "could not be
// re-parsed without it" clause of the write rule. It is a KEEP rather than a re-insert, and that is
// the whole reason ordering is safe here: a key that is never removed cannot come back in the wrong
// place. See pruneSequence.
func pruneMapping(n *yaml.Node, d Declared, prefix string, keep map[string]bool) {
	if n.Kind != yaml.MappingNode {
		return
	}
	kept := make([]*yaml.Node, 0, len(n.Content))
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		path := prefix + key.Value
		switch val.Kind {
		case yaml.MappingNode:
			pruneMapping(val, d, path+".", nil)
			if len(val.Content) == 0 {
				continue // every child pruned: the section itself goes
			}
		case yaml.SequenceNode:
			pruneSequence(val, d, path)
			// THE LENGTH TEST IS LOAD-BEARING AND IS NOT A BELT-AND-BRACES DUPLICATE OF `Has`.
			// `storage:` survives an empty declared set because the SEQUENCE is non-empty, not
			// because anything declared it — which is what makes a fresh install write
			// `storage:\n  - path: …` rather than an empty document (spec story 10). Drop it and
			// keep only `!d.Has(path)`, which reads like a simplification, and the first save on a
			// new install deletes the storage list.
			if len(val.Content) == 0 && !d.Has(path) {
				continue
			}
		default:
			if !d.Has(path) && !keep[key.Value] {
				continue
			}
		}
		kept = append(kept, key, val)
	}
	n.Content = kept
}

// pruneSequence prunes each element of a sequence of mappings, keyed by the element's own name.
//
// AN ENTRY ALWAYS KEEPS ITS `path`, whatever the declared set says. An entry without one is not a
// storage — it is a mapping the next parse would refuse — so this is the "could not be re-parsed
// without it" clause of the write rule rather than an exception to it.
//
// KEEPING IT IS NOT THE SAME AS PUTTING IT BACK, AND PR 5 IS WHERE THE DIFFERENCE BITES. This
// removed `path` and re-inserted it at the FRONT until the review of quince#758, which is wrong
// order — canonical for a `StorageEntry` is `name, path, default, backend, zfs, retention`
// (`schema.go`), so an entry that kept `name` and lost `path` marshalled as `path, name, …`. That
// broke the exact property the node-prune design was chosen to preserve, inside the function that
// implements it, and it was unreachable only because `Validate` refuses a nameless entry.
//
// A key that is never removed cannot come back in the wrong place, so the fix is to not remove it.
// **D3's `default: true` materialisation (PR 5) cannot use this trick**: it inserts a key that may
// not be in the document at all, so it needs a real canonical-position insert. Do not copy the
// shape that used to be here.
func pruneSequence(n *yaml.Node, d Declared, prefix string) {
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		pruneMapping(item, d, prefix+"["+nodeEntryKey(item)+"].", map[string]bool{"path": true})
	}
}

// nodeEntryKey is entryKey against a yaml node: the RESOLVED name, because by the time anything
// marshals, `Resolved()` has already filled it from the path.
func nodeEntryKey(item *yaml.Node) string {
	for i := 0; i+1 < len(item.Content); i += 2 {
		if item.Content[i].Value == "name" && item.Content[i+1].Value != "" {
			return item.Content[i+1].Value
		}
	}
	for i := 0; i+1 < len(item.Content); i += 2 {
		if item.Content[i].Value == "path" {
			return item.Content[i+1].Value
		}
	}
	return ""
}

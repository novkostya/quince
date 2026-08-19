package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// THE TS `Config` TYPE MUST COVER EVERY JSON TAG ON THE GO ONE — quince#493, Operator ruling
// 2026-08-19. `PUT /api/config` keeps full-document replace semantics (contracts §1); this is the
// gate that was ruled instead of changing them.
//
// WHAT GOES WRONG WITHOUT IT. The handler decodes into a ZERO-VALUED `config.Config`, so a key the
// client omits arrives as the Go zero value rather than its default. `manage_muxer` missing from the
// TS type meant a client built from that type would tell quince to stop managing its muxers; for
// `tls` the zero value is two empty strings, which is TLS OFF. The severity is per-key and the
// mechanism is uniform, which is what makes it a gate rather than a review habit.
//
// AND IT HAS ALREADY HAPPENED TWICE. `types.ts` says so about the first, in its own words: the type
// *"said `{ storages, backend, zfs, retention }` UNTIL 2026-08-02, one shape behind the daemon, and
// the UI crashed on `storage.backend` of a null. `make gates-ui` was green throughout: the type was
// internally consistent and NOTHING CROSS-CHECKS IT AGAINST THE GO SCHEMA."* The second is
// `manage_muxer` (quince#1240).
//
// REFLECT OVER THE TYPES, RECURSIVELY — NEVER OVER A MARSHALLED ZERO VALUE, which is the constraint
// the ruling singles out. quince#1240 audited by marshalling `Config{}` and reported 19 paths. That
// count is right and it is the whole visible surface of a zero config: a nil `storage` marshals to
// `null`, so every path under `StorageEntry` is structurally invisible to that method. A gate
// inheriting the blind spot would report green over exactly the region where the UI has already been
// one shape behind the daemon.
//
// `json:"-"` IS EXCLUDED, and that is load-bearing rather than prospective: `Config.Devices` carries
// it today (quince#1219), deliberately — a section retired with a refusal, never served. Demanding a
// TS counterpart for a key that must never reach the wire would make this gate wrong on the day it
// landed.
//
// THE TS SIDE IS PARSED, not generated-and-diffed. The ruling accepts either and asks which and why.
// Parsing is fragile and a generated artifact is sturdier — but a checked-in generated file needs a
// build step nothing here runs, and it can itself go stale between generations, which is the failure
// this gate exists to catch wearing a different hat. The parser refuses rather than guessing when it
// meets a shape it does not understand, so its fragility surfaces as a failing test rather than as a
// silent pass. That is the trade, stated so the next reader can retake it.
func TestTheTSConfigTypeCoversEveryGoKey(t *testing.T) {
	goPaths := goJSONPaths(reflect.TypeOf(Config{}), "")

	src, err := os.ReadFile(filepath.Join("..", "..", "..", "ui", "src", "lib", "types.ts"))
	if err != nil {
		t.Fatalf("cannot read the TS types: %v", err)
	}
	ifaces := parseTSInterfaces(string(src))
	if _, ok := ifaces["Config"]; !ok {
		t.Fatal("no `export interface Config` found in ui/src/lib/types.ts — the parser is broken, " +
			"not the type. Fix the parser rather than deleting this test.")
	}
	tsPaths := map[string]bool{}
	tsFlatten(t, ifaces, "Config", "", tsPaths, 0)

	var missing []string
	for _, p := range goPaths {
		if !tsPaths[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these keys exist on the Go config.Config and NOT on the TS Config type:\n  %s\n\n"+
			"PUT /api/config decodes into a zero-valued struct, so a client built from the TS type "+
			"omits each of these and quince sets it to its Go zero value on the next save. Add them to "+
			"ui/src/lib/types.ts. If a key must never reach the wire, tag it `json:\"-\"` — that is "+
			"what Config.Devices does and this gate skips it.",
			strings.Join(missing, "\n  "))
	}
}

// goJSONPaths walks the STRUCT TYPE, following nested structs, pointers and slice element types.
// Leaves are anything that is not a struct after those are peeled — so `map[string]string` is a leaf
// and its keys are data rather than contract, which is correct: the TS side declares the map, not
// its contents.
func goJSONPaths(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		if prefix == "" {
			return nil
		}
		return []string{prefix}
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported: not on the wire
		}
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		p := name
		if prefix != "" {
			p = prefix + "." + name
		}
		out = append(out, goJSONPaths(f.Type, p)...)
	}
	return out
}

var (
	tsIfaceRe   = regexp.MustCompile(`(?m)^export interface ([A-Za-z0-9_]+) \{`)
	tsCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// parseTSInterfaces returns each `export interface X { … }` body, comments stripped and braces
// balanced. Balanced rather than regexed to the next `}` because the bodies contain inline objects.
func parseTSInterfaces(src string) map[string]string {
	src = tsCommentRe.ReplaceAllString(src, "")
	var lines []string
	for _, l := range strings.Split(src, "\n") {
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		lines = append(lines, l)
	}
	src = strings.Join(lines, "\n")

	out := map[string]string{}
	for _, m := range tsIfaceRe.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[2]:m[3]]
		depth, start := 0, -1
		for i := m[1] - 1; i < len(src); i++ {
			switch src[i] {
			case '{':
				if depth == 0 {
					start = i + 1
				}
				depth++
			case '}':
				depth--
				if depth == 0 {
					out[name] = src[start:i]
					i = len(src)
				}
			}
		}
	}
	return out
}

// tsFlatten resolves one interface into leaf paths, recursing into inline objects and named
// interfaces. It REFUSES on anything it cannot classify rather than skipping it, so a shape this
// parser does not understand fails loudly instead of shrinking the set it compares against — which
// would be this gate reporting green over the keys it could not see.
func tsFlatten(t *testing.T, ifaces map[string]string, iface, prefix string, out map[string]bool, depth int) {
	t.Helper()
	if depth > 8 {
		t.Fatalf("TS type recursion too deep at %q — cyclic types?", prefix)
	}
	body, ok := ifaces[iface]
	if !ok {
		t.Fatalf("TS interface %q is referenced but not declared in types.ts", iface)
	}
	tsFlattenBody(t, ifaces, body, prefix, out, depth)
}

func tsFlattenBody(t *testing.T, ifaces map[string]string, body, prefix string, out map[string]bool, depth int) {
	t.Helper()
	for _, e := range tsEntries(body) {
		p := e.key
		if prefix != "" {
			p = prefix + "." + e.key
		}
		val := strings.TrimSpace(e.val)
		// `| null` / `| undefined` say nothing about the SHAPE, and every optional key here carries
		// one. Stripped before classifying so `X[] | null` reads as `X[]`.
		for _, suffix := range []string{"| null", "| undefined"} {
			val = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(val), suffix))
		}
		val = strings.TrimSuffix(strings.TrimSpace(val), "[]")
		val = strings.TrimSpace(val)
		switch {
		case strings.HasPrefix(val, "{"):
			inner := strings.TrimSuffix(strings.TrimPrefix(val, "{"), "}")
			tsFlattenBody(t, ifaces, inner, p, out, depth+1)
		case regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`).MatchString(val):
			if _, known := ifaces[val]; known {
				tsFlatten(t, ifaces, val, p, out, depth+1)
			} else {
				out[p] = true // a named type alias (JobState, Backend…) is a leaf
			}
		default:
			out[p] = true
		}
	}
}

type tsEntry struct{ key, val string }

// tsEntries splits one interface body into `key: value` pairs at brace depth 0.
func tsEntries(body string) []tsEntry {
	var out []tsEntry
	depth, cur := 0, strings.Builder{}
	flush := func() {
		s := strings.TrimSpace(cur.String())
		cur.Reset()
		if s == "" {
			return
		}
		k, v, ok := strings.Cut(s, ":")
		if !ok {
			return
		}
		key := strings.TrimSpace(k)
		key = strings.TrimSuffix(key, "?")
		if key == "" || strings.ContainsAny(key, " \t") {
			return
		}
		out = append(out, tsEntry{key: key, val: strings.TrimSpace(v)})
	}
	for _, r := range body {
		switch r {
		case '{':
			depth++
			cur.WriteRune(r)
		case '}':
			depth--
			cur.WriteRune(r)
		case ';', '\n':
			if depth == 0 {
				flush()
				continue
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

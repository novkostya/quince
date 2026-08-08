package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/config"
)

// THE WIRE STAYS COMPLETE: GET /api/config carries every field of config.Config (qn.6j story 6).
//
// THIS IS A GUARD ON A CHANGE THAT HAS NOT LANDED YET, and it is deliberately here first.
// qn.6j (quince#728) makes config.yml carry only what the user set. The obvious implementation is
// `omitempty` on the struct tags, and `schema.go` pairs both encodings on one line:
//
//	CertFile string    `yaml:"cert_file" json:"cert_file"`
//	ZFS      ZFSConfig `yaml:"zfs" json:"zfs"`
//
// They are the same tag STRING and two independent KEYS — `yaml:"cert_file,omitempty"
// json:"cert_file"` is perfectly expressible — so nothing structural stops a developer editing one
// half of a line they are already in and taking the other half with them. The risk is discipline,
// which is what a test is for.
//
// WHAT IT COSTS IF NOBODY GUARDS IT (quince#493): a sparse wire representation makes GET drop keys →
// `ConfigEditor.tsx` spreads the document it fetched, so it spreads a partial one → PUT sends it →
// `decodeJSON` into a zero-valued Config sets every absent key to Go's zero →
// `devices.manage_muxer` becomes FALSE and quince stops supervising its muxers. That is a backup
// that silently cannot run, produced by a change whose entire purpose was to tidy a file.
//
// quince#493 is open and this does not close it: it is latent today only because ConfigEditor
// happens to send back a complete document. This test pins the property that keeps it latent.
func TestGetConfigCarriesEveryGoField(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	// Seeded, because `storage:` is a list and an absent one gives the walk nothing to descend
	// into — the entry's own keys are exactly where a partial encoding would bite hardest.
	seedStorages(t, srv, c, oneStorage)

	var doc map[string]any
	if err := json.Unmarshal(fetchConfigDoc(t, c, srv.URL), &doc); err != nil {
		t.Fatalf("unmarshal config doc: %v", err)
	}

	missing := missingJSONKeys(t, reflect.TypeOf(config.Config{}), doc, "")
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("GET /api/config is missing %d key(s) the Go type declares:\n  %s\n\n"+
			"If this is an `omitempty` or `omitzero` added to a `json:` tag, remove it — the "+
			"WIRE must stay complete whatever the written file does. A sparse response makes "+
			"the UI spread a partial document, and PUT then zeroes every key it did not carry "+
			"(quince#493).", len(missing), strings.Join(missing, "\n  "))
	}
}

// missingJSONKeys walks a struct type against a decoded JSON object and reports every json-tagged
// field with no key present. It recurses into nested structs and into the FIRST element of a slice
// of structs — one element is enough, because the encoding is a property of the type.
//
// Pointer fields are followed by type, not by value: a `null` in the document is a MISSING key for
// this test's purposes only if the key itself is absent. `retention: null` is a value the client can
// see and reason about; a dropped `retention` is not, and that is the distinction quince#493 turns
// on.
//
// AN ANONYMOUS FIELD IS A HARD FAILURE, NOT A SKIP. Go inlines an embedded struct's fields into the
// parent object, which this walk does not model — so skipping one would silently stop covering part
// of the type, inside the guard whose entire job is to notice a key nobody is checking. There are no
// embedded fields in `config.Config` today; making it loud turns the day somebody adds one into a
// failing test rather than a quiet hole. `jsonOmittingFields` handles inlining by recursing, so the
// two walks would otherwise disagree about the same field.
func missingJSONKeys(t *testing.T, typ reflect.Type, doc map[string]any, prefix string) []string {
	t.Helper()
	typ = derefType(typ)
	var missing []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous {
			t.Fatalf("%s%s is an EMBEDDED field and this walk does not model json inlining — its "+
				"keys appear in the parent object and nothing here checks them. Teach the walk, or "+
				"give the field an explicit json name.", prefix, f.Name)
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name // encoding/json falls back to the Go field name; so does this walk
		}
		path := prefix + name
		v, ok := doc[name]
		if !ok {
			missing = append(missing, path)
			continue
		}
		ft := derefType(f.Type)
		switch ft.Kind() {
		case reflect.Struct:
			if sub, ok := v.(map[string]any); ok {
				missing = append(missing, missingJSONKeys(t, ft, sub, path+".")...)
			}
		case reflect.Slice:
			elem := derefType(ft.Elem())
			if elem.Kind() != reflect.Struct {
				continue
			}
			items, ok := v.([]any)
			if !ok || len(items) == 0 {
				continue
			}
			if first, ok := items[0].(map[string]any); ok {
				missing = append(missing, missingJSONKeys(t, elem, first, fmt.Sprintf("%s[0].", path))...)
			}
		}
	}
	return missing
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// THE RESPONSE WALK ABOVE IS FIXTURE-DEPENDENT, AND ON ITS OWN IT WOULD MISS THE FIELD CANON WARNS
// ABOUT. This is the total check, and the two are complementary rather than redundant.
//
// `omitempty` drops a field only when its value is EMPTY, so whether the walk catches one depends on
// what the seeded config happens to hold. Measured: with `omitempty` added to three `json:` tags at
// once, the walk reported ONE — `tls.cert_file`, whose value is `""`. It missed
// `devices.manage_muxer`, because the default is `true`, and it missed `storage[].retention`,
// because the fixture seeds one. **`devices.manage_muxer` is precisely the field quince#493's
// failure is named after**, and a guard that misses its own headline example is worth less than it
// reads.
//
// TWO TAGS, TWO MECHANISMS, AND BOTH ARE REFUSED ON THE `json:` SIDE. This is the whole reason the
// check greps the type rather than the file, and the second tag is the one that actually bites.
//
//   - `omitempty` omits false, 0, "", a nil pointer/interface, and an empty slice/map/string.
//     **NOT structs** — a struct is never "empty" — so `json:"zfs,omitempty"` changes nothing.
//   - `omitzero` (Go 1.24; `core/go.mod` is at 1.25.0) omits the **zero value of the type, structs
//     included**, and honours an `IsZero() bool` method if the type has one.
//
// **So `omitzero` is strictly more dangerous here, and it is also the tag a careful person reaches
// for.** Asked to make a document carry *only what was set*, `omitzero` is what actually means
// "unset"; `omitempty` is the older approximation that `qn.6j`'s D4 spends a paragraph rejecting. A
// guard that matched only `omitempty` would be aimed at the option a reader would discard and blind
// to the one they would choose.
//
// Measured, both of them, at this head:
//
//	json:"zfs,omitempty"  → the block STAYS on the wire (no-op on a struct)
//	json:"tls,omitzero"   → the WHOLE `tls` object disappears from GET /api/config
//
// The response walk caught that `tls` case only because TLS happens to be zero in the fixture. On a
// deployment with a certificate configured it would not have, which is `manage_muxer`'s escape in a
// second guise — and the argument for having both checks.
//
// **`gopkg.in/yaml.v3` has no `omitzero`**, so refusing both costs the yaml half nothing.
//
// THIS DOES NOT REPLACE THE SPEC'S G6, and an earlier version of this comment said it did on a false
// premise — that `qn.6j` wants `omitempty` on the yaml side, so a grep would have to be suppressed.
// **D4 forbids the tag on BOTH sides**: *"No struct tag in `schema.go` gains `omitempty` in this
// rung."* So G6's grep is correct for `qn.6j` and never needs suppressing. What this test adds is
// that it stays correct for a rung that DOES want a yaml-side tag, where a grep could not tell the
// two halves apart — and that a grep for `omitempty` would never have seen `omitzero` at all.
func TestNoJSONTagInConfigOmitsAnything(t *testing.T) {
	offenders := jsonOmittingFields(reflect.TypeOf(config.Config{}), "", map[reflect.Type]bool{})
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("%d `json:` tag(s) can drop a key:\n  %s\n\n"+
			"The WIRE must stay complete. A sparse GET /api/config makes the UI spread a partial "+
			"document and PUT then zeroes every absent key (quince#493) — `devices.manage_muxer` "+
			"going false stops quince supervising its muxers. Neither `omitempty` nor `omitzero` "+
			"belongs on a `json:` tag here; qn.6j's tidy file is a property of the WRITTEN DOCUMENT "+
			"(the declared set, spec D2), not of either encoding tag.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// jsonOmittingFields reports every json-tagged field carrying an option that can drop the key —
// `omitempty` or `omitzero` — recursing through nested structs and slice/pointer element types.
// `seen` guards against a recursive type; there is none today and a test that hangs is a worse
// failure than one that reports.
func jsonOmittingFields(t reflect.Type, prefix string, seen map[reflect.Type]bool) []string {
	t = derefType(t)
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		path := prefix + name
		for _, o := range strings.Split(opts, ",") {
			if o == "omitempty" || o == "omitzero" {
				out = append(out, path)
			}
		}
		ft := derefType(f.Type)
		if ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array {
			ft = derefType(ft.Elem())
		}
		out = append(out, jsonOmittingFields(ft, path+".", seen)...)
	}
	return out
}

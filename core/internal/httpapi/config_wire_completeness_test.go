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

	missing := missingJSONKeys(reflect.TypeOf(config.Config{}), doc, "")
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("GET /api/config is missing %d key(s) the Go type declares:\n  %s\n\n"+
			"If this is an `omitempty` added to a `json:` tag, remove it — the yaml half is what "+
			"qn.6j needs and the two are independent keys on one line. A sparse wire response makes "+
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
func missingJSONKeys(t reflect.Type, doc map[string]any, prefix string) []string {
	t = derefType(t)
	var missing []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
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
				missing = append(missing, missingJSONKeys(ft, sub, path+".")...)
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
				missing = append(missing, missingJSONKeys(elem, first, fmt.Sprintf("%s[0].", path))...)
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
// It also established something worth knowing before anyone reaches for the tag: **`omitempty` on a
// STRUCT field is a no-op in encoding/json** — a struct is never "empty" — so `json:"zfs,omitempty"`
// changes nothing at all. The droppable kinds are bool, string, numeric, pointer, slice and map. So
// the hazard is real and NARROWER than "any key could vanish": the `zfs:` block cannot leave the
// wire this way, and `manage_muxer` can.
//
// This walks the TYPE and never a value, so it cannot be fooled by a fixture. It replaces the
// `grep -c omitempty schema.go` the qn.6j spec proposed as G6, which cannot tell a `yaml:` tag from
// a `json:` one — and qn.6j WANTS `omitempty` on the yaml side, so a grep would have to be
// suppressed on the very PR it exists to guard.
func TestNoJSONTagInConfigCarriesOmitempty(t *testing.T) {
	offenders := jsonOmitemptyFields(reflect.TypeOf(config.Config{}), "", map[reflect.Type]bool{})
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("%d `json:` tag(s) carry `omitempty`:\n  %s\n\n"+
			"The WIRE must stay complete. A sparse GET /api/config makes the UI spread a partial "+
			"document and PUT then zeroes every absent key (quince#493) — `devices.manage_muxer` "+
			"going false stops quince supervising its muxers. qn.6j's tidy file is the `yaml:` "+
			"half of the same tag line and is unaffected by this rule.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// jsonOmitemptyFields reports every json-tagged field carrying `omitempty`, recursing through nested
// structs and slice/pointer element types. `seen` guards against a recursive type; there is none
// today and a test that hangs is a worse failure than one that reports.
func jsonOmitemptyFields(t reflect.Type, prefix string, seen map[reflect.Type]bool) []string {
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
			if o == "omitempty" {
				out = append(out, path)
			}
		}
		ft := derefType(f.Type)
		if ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array {
			ft = derefType(ft.Elem())
		}
		out = append(out, jsonOmitemptyFields(ft, path+".", seen)...)
	}
	return out
}

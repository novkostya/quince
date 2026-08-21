package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// THE TOTALITY GATE. Every declared `Event*` constant must be classified by `EventDevice`.
//
// This is `qn.12`'s routing-table shape (its G1) applied to the socket, and the reason is the same:
// a thirteenth event added later must fail the BUILD rather than quietly reaching — or quietly not
// reaching — a principal nobody considered it for. The classification is a security decision now,
// so leaving it to a `default` would be the permissive-by-omission pattern qn.13 has been removing.
//
// It parses the source rather than listing the constants here, because a hand-kept list in the test
// is a second place to forget.
func TestEveryEventConstantIsClassified(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "envelope.go", nil, 0)
	if err != nil {
		t.Fatalf("parse envelope.go: %v", err)
	}

	var declared []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, name := range vs.Names {
			if strings.HasPrefix(name.Name, "Event") {
				declared = append(declared, name.Name)
			}
		}
		return true
	})
	if len(declared) < 10 {
		// The control: if the parse stopped finding constants — a rename, a moved file — this test
		// would pass by checking nothing.
		t.Fatalf("found only %d Event constants; the parse is not seeing them", len(declared))
	}

	src, err := parser.ParseFile(fset, "eventscope.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse eventscope.go: %v", err)
	}
	var body strings.Builder
	ast.Inspect(src, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			body.WriteString(id.Name)
			body.WriteString(" ")
		}
		return true
	})
	classified := body.String()

	for _, name := range declared {
		if !strings.Contains(classified, name+" ") {
			t.Errorf("event constant %s is not classified in EventDevice — it would reach only the "+
				"admin, which is the safe direction but not a decision anybody took", name)
		}
	}
	t.Logf("%d event constants, all classified", len(declared))
}

func TestGlobalEventsAreNotDeviceScoped(t *testing.T) {
	for _, typ := range []string{EventHello, EventSessionLocked, EventConfigUpdated} {
		udid, scoped := EventDevice(Envelope{Type: typ})
		if scoped {
			t.Errorf("%s reported as device-scoped; a scoped holder would stop receiving it", typ)
		}
		if udid != "" {
			t.Errorf("%s produced a udid %q", typ, udid)
		}
	}
}

func TestDeviceEventsCarryTheirDevice(t *testing.T) {
	// KEYED BY THE CONSTANT'S VALUE, and checked for completeness against the parsed set below.
	//
	// The first version of this test was six literal cases against eight device-bearing constants —
	// `device.detached` and `version.deleted` were missing (quince#1380 review). Neither had a live
	// defect, but the property this test exists to hold was unheld for them: if either later carried
	// a lighter payload, `udidOf` would return "", a scoped holder would silently stop receiving
	// their own device's events, and this test would still pass. **A guard against hand-kept lists
	// that is itself a hand-kept list**, one layer in.
	cases := map[string]any{
		EventDeviceAttached: DeviceEvent{Device: Device{UDID: "DEV-A"}},
		EventDeviceDetached: DeviceEvent{Device: Device{UDID: "DEV-A"}},
		EventDeviceUpdated:  Device{UDID: "DEV-A"},
		EventJobUpdated:     Job{UDID: "DEV-A"},
		EventJobLog:         JobLogChunk{JobID: "j1", UDID: "DEV-A"},
		EventOpUpdated:      Op{UDID: "DEV-A"},
		EventVersionCreated: Version{UDID: "DEV-A"},
		EventVersionDeleted: Version{UDID: "DEV-A"},
	}

	// COMPLETENESS, DERIVED. Every constant the classifier calls device-bearing must have a case, so
	// a ninth one cannot be added without either a payload here or a failure naming it.
	for _, value := range declaredEventValues(t) {
		if _, scoped := EventDevice(Envelope{Type: value}); !scoped {
			continue
		}
		if _, ok := cases[value]; !ok {
			t.Errorf("%q is device-bearing and has no case here — the property that it carries its "+
				"device is unheld for it", value)
		}
	}

	for typ, data := range cases {
		udid, scoped := EventDevice(Envelope{Type: typ, Data: data})
		if !scoped {
			t.Errorf("%s reported as global — a scoped holder would receive every device's", typ)
		}
		if udid != "DEV-A" {
			t.Errorf("%s: got udid %q want DEV-A", typ, udid)
		}
	}
}

// declaredEventValues returns the string VALUE of every Event* constant, parsed from source.
//
// Values rather than names, because the classifier switches on the value. The parse is shared with
// the totality gate so both tests see the same set.
func declaredEventValues(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "envelope.go", nil, 0)
	if err != nil {
		t.Fatalf("parse envelope.go: %v", err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) == 0 || len(vs.Values) == 0 {
			return true
		}
		if !strings.HasPrefix(vs.Names[0].Name, "Event") {
			return true
		}
		lit, ok := vs.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		out = append(out, strings.Trim(lit.Value, `"`))
		return true
	})
	if len(out) < 10 {
		t.Fatalf("parsed only %d event values; the parse is not seeing them", len(out))
	}
	return out
}

// AN UNKNOWN TYPE FAILS CLOSED. It is unreachable while the totality gate passes; this asserts the
// direction it fails in for the window between somebody adding a constant and the gate saying so.
func TestAnUnknownEventIsScopedToNoDevice(t *testing.T) {
	udid, scoped := EventDevice(Envelope{Type: "something.new"})
	if !scoped || udid != "" {
		t.Fatalf("unknown event: udid=%q scoped=%v — want scoped to no device, so it reaches only "+
			"the admin rather than everyone", udid, scoped)
	}
}

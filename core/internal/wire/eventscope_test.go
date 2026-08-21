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
	cases := []struct {
		typ  string
		data any
	}{
		{EventDeviceAttached, DeviceEvent{Device: Device{UDID: "DEV-A"}}},
		{EventDeviceUpdated, Device{UDID: "DEV-A"}},
		{EventJobUpdated, Job{UDID: "DEV-A"}},
		{EventJobLog, JobLogChunk{JobID: "j1", UDID: "DEV-A"}},
		{EventOpUpdated, Op{UDID: "DEV-A"}},
		{EventVersionCreated, Version{UDID: "DEV-A"}},
	}
	for _, c := range cases {
		udid, scoped := EventDevice(Envelope{Type: c.typ, Data: c.data})
		if !scoped {
			t.Errorf("%s reported as global — a scoped holder would receive every device's", c.typ)
		}
		if udid != "DEV-A" {
			t.Errorf("%s: got udid %q want DEV-A", c.typ, udid)
		}
	}
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

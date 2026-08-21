package ws

import (
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

func env(typ string, data any) wire.Envelope { return wire.Envelope{Type: typ, Data: data} }

// THE ADMIN HEARS EVERYTHING — today's behaviour for every connection, and it must not change until
// something mints a scoped credential. This is the control for the whole file: without it, a filter
// that dropped every frame would pass the confinement assertions below.
func TestAdminReceivesEverything(t *testing.T) {
	p := AdminPrincipal()
	for _, e := range []wire.Envelope{
		env(wire.EventHello, wire.Hello{}),
		env(wire.EventDeviceUpdated, wire.Device{UDID: "DEV-A"}),
		env(wire.EventDeviceUpdated, wire.Device{UDID: "DEV-B"}),
		env(wire.EventJobLog, wire.JobLogChunk{UDID: "DEV-B"}),
		env("something.new", nil),
	} {
		if !p.MayReceive(e) {
			t.Errorf("the admin was denied %s", e.Type)
		}
	}
}

// THE CONFINEMENT. A scoped holder hears its own device and no other.
func TestScopedPrincipalHearsOnlyItsDevice(t *testing.T) {
	p := DevicePrincipal("DEV-A")

	mine := []wire.Envelope{
		env(wire.EventDeviceUpdated, wire.Device{UDID: "DEV-A"}),
		env(wire.EventJobUpdated, wire.Job{UDID: "DEV-A"}),
		env(wire.EventJobLog, wire.JobLogChunk{JobID: "j1", UDID: "DEV-A"}),
		env(wire.EventVersionCreated, wire.Version{UDID: "DEV-A"}),
		env(wire.EventDeviceAttached, wire.DeviceEvent{Device: wire.Device{UDID: "DEV-A"}}),
	}
	for _, e := range mine {
		if !p.MayReceive(e) {
			t.Errorf("a scoped holder was denied its OWN device's %s", e.Type)
		}
	}

	// The leak this whole change exists to close: another device's name, model, jobs and versions.
	theirs := []wire.Envelope{
		env(wire.EventDeviceUpdated, wire.Device{UDID: "DEV-B", Name: "somebody else's iPhone"}),
		env(wire.EventJobUpdated, wire.Job{UDID: "DEV-B"}),
		env(wire.EventJobLog, wire.JobLogChunk{JobID: "j2", UDID: "DEV-B"}),
		env(wire.EventVersionCreated, wire.Version{UDID: "DEV-B"}),
		env(wire.EventDeviceAttached, wire.DeviceEvent{Device: wire.Device{UDID: "DEV-B"}}),
	}
	for _, e := range theirs {
		if p.MayReceive(e) {
			t.Errorf("a scoped holder received ANOTHER device's %s — this is the leak", e.Type)
		}
	}
}

// GLOBAL EVENTS STILL REACH A SCOPED HOLDER, and withholding them would break the socket rather than
// confine it: `hello` is the first frame, and `session.locked` is how a client learns its own
// session ended.
func TestScopedPrincipalStillHearsGlobalEvents(t *testing.T) {
	p := DevicePrincipal("DEV-A")
	for _, e := range []wire.Envelope{
		env(wire.EventHello, wire.Hello{}),
		env(wire.EventSessionLocked, nil),
		env(wire.EventConfigUpdated, nil),
	} {
		if !p.MayReceive(e) {
			t.Errorf("a scoped holder was denied the global event %s", e.Type)
		}
	}
}

// AN UNKNOWN EVENT REACHES ONLY THE ADMIN. Unreachable while the totality gate passes; this asserts
// which way it fails in the window between a constant being added and the gate saying so.
func TestUnknownEventDoesNotReachAScopedPrincipal(t *testing.T) {
	if DevicePrincipal("DEV-A").MayReceive(env("something.new", nil)) {
		t.Fatal("an unclassified event reached a scoped holder — the default must fail closed")
	}
}

// A DEVICE-BEARING EVENT WITH NO DEVICE reaches nobody but the admin. It should not occur; if a
// producer ever omits the udid, the failure must be silence for scoped holders rather than a
// broadcast to all of them.
func TestADeviceEventWithNoUDIDReachesNoScopedPrincipal(t *testing.T) {
	if DevicePrincipal("DEV-A").MayReceive(env(wire.EventJobUpdated, wire.Job{})) {
		t.Fatal("an event with an empty udid matched a scoped principal")
	}
}

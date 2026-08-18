package device

import (
	"testing"

	"github.com/novkostya/quince/core/internal/muxd"
)

// The routing tests (quince#1219 item D). The registry has always keyed presence by
// (source, udid, transport); what was missing is a way to ASK it which muxer reported a device,
// so every consumer guessed an endpoint from the transport name instead.

// TestSourceForNamesTheMuxerThatReportedTheDevice is the whole claim in one assertion: two muxers,
// each reporting a DIFFERENT device over USB. Routing by transport sends both ops to whichever
// endpoint was configured as "the USB one", and one of them cannot see its device.
func TestSourceForNamesTheMuxerThatReportedTheDevice(t *testing.T) {
	reg, _ := newTestRegistry(t)
	const srcOther = "/run/mux/second-usbmuxd"
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	reg.Sink(srcOther).Apply(attach(udidB, muxd.TransportUSB))

	for _, tc := range []struct{ udid, want string }{{udidA, srcUSB}, {udidB, srcOther}} {
		got, ok := reg.SourceFor(tc.udid, muxd.TransportUSB)
		if !ok || got != tc.want {
			t.Fatalf("SourceFor(%s, usb) = %q ok=%v; want %q — an op would be sent to a muxer that cannot see this device",
				tc.udid, got, ok, tc.want)
		}
	}
}

// TestSourceForIsPerTransport: one device on both transports through two different muxers. This is
// the shape the old code got right by accident and for the wrong reason — it happened to match
// because the deployment had one daemon per transport.
func TestSourceForIsPerTransport(t *testing.T) {
	reg, _ := newTestRegistry(t)
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	reg.Sink(srcWiFi).Apply(attach(udidA, muxd.TransportWiFi))

	if got, _ := reg.SourceFor(udidA, muxd.TransportUSB); got != srcUSB {
		t.Fatalf("SourceFor(usb) = %q; want %q", got, srcUSB)
	}
	if got, _ := reg.SourceFor(udidA, muxd.TransportWiFi); got != srcWiFi {
		t.Fatalf("SourceFor(wifi) = %q; want %q", got, srcWiFi)
	}
}

// TestSourceForFalseWhenNothingReportsTheDevice: the answer that must NOT be a default. A device
// no muxer holds on that transport has no endpoint, and inventing one is how a CLI ends up talking
// to a daemon the operator never named.
func TestSourceForFalseWhenNothingReportsTheDevice(t *testing.T) {
	reg, _ := newTestRegistry(t)
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))

	if got, ok := reg.SourceFor(udidA, muxd.TransportWiFi); ok {
		t.Fatalf("SourceFor(%s, wifi) = %q ok=true; want no answer — that device is USB-only here", udidA, got)
	}
	if got, ok := reg.SourceFor(udidB, muxd.TransportUSB); ok {
		t.Fatalf("SourceFor(unknown udid) = %q ok=true; want no answer", got)
	}
}

// TestSourceForAgreesWithTheMergedTable pins routing to the rule the UI displays: when two muxers
// both hold the same edge, the merged table shows the NEWEST last_seen and the op must go to that
// same muxer.
//
// THE TIMESTAMPS ARE WRITTEN, NOT APPLIED, AND THAT IS THE POINT OF THIS TEST EXISTING (quince#1232
// review). It read *"the second attach is the newer edge"* and drove both edges through `Apply`,
// which stamps `wire.Now()` — `time.RFC3339`, SECOND resolution, no fractional part. Two calls
// microseconds apart therefore produce byte-identical strings, so `won != *dev.Transports.USB`
// compared a string to itself whichever source `SourceFor` happened to pick, and the assertion
// passed for an implementation that returned EITHER holder. The rule the comment named was never
// exercised: a green that could not have failed (quince-devlog#260).
//
// Writing `reg.sources` under the lock is the same move the assertion already makes to read it, and
// it needs no clock injection for a rule that is about ordering rather than about time.
func TestSourceForAgreesWithTheMergedTable(t *testing.T) {
	reg, _ := newTestRegistry(t)
	const srcOther = "/run/mux/second-usbmuxd"
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	reg.Sink(srcOther).Apply(attach(udidA, muxd.TransportUSB))

	// srcOther is made strictly newer. It is also the lexicographically LARGER sourceID, so the
	// tie-break below would pick the other one — which is what makes this test able to fail: an
	// implementation that ignored the timestamp and fell straight to `source < best` returns srcUSB
	// and is caught here.
	const older, newer = "2026-08-18T09:00:00Z", "2026-08-18T09:00:01Z"
	reg.mu.Lock()
	reg.sources[srcUSB][udidA][muxd.TransportUSB] = older
	reg.sources[srcOther][udidA][muxd.TransportUSB] = newer
	reg.mu.Unlock()

	source, ok := reg.SourceFor(udidA, muxd.TransportUSB)
	if !ok {
		t.Fatal("SourceFor = no answer; both muxers report this device")
	}
	if source != srcOther {
		t.Fatalf("SourceFor = %q; want %q, which holds the newer edge (%s vs %s). Routing must send the op to the muxer whose reading the merged table shows",
			source, srcOther, newer, older)
	}
	dev, ok := reg.Device(udidA)
	if !ok || dev.Transports.USB == nil {
		t.Fatalf("Device(%s) = %+v ok=%v; want a USB edge", udidA, dev, ok)
	}
	if *dev.Transports.USB != newer {
		t.Fatalf("the merged table shows %s; want %s — mergedLocked and SourceFor disagree about which edge is newest",
			*dev.Transports.USB, newer)
	}
	// And the two agree by construction rather than by coincidence: the winner's own timestamp IS
	// what the table displays.
	reg.mu.RLock()
	won := reg.sources[source][udidA][muxd.TransportUSB]
	reg.mu.RUnlock()
	if won != *dev.Transports.USB {
		t.Fatalf("routing picked source %q at %s; the merged table shows %s — routing and the UI disagree",
			source, won, *dev.Transports.USB)
	}
}

// TestSourceForBreaksTiesStably is the case that ACTUALLY ROUTES, and it had no test at all
// (quince#1232 review). `wire.Now()` is second-resolution, so two muxers replaying their attached
// sets at startup — the topology this whole change exists for — land in the same second essentially
// always. The tie-break is therefore the common path rather than the rare one, and `source < best`
// is the production decision.
//
// WHAT IT PINS IS DETERMINISM, NOT A PREFERENCE. Go randomises map iteration order deliberately, so
// without the comparison this returns a different muxer per call for one unchanged registry — ops
// for one device sprayed across daemons, and a bug that reproduces one run in N. Repeated because a
// single call passes against a map-order-dependent implementation whenever it happens to guess
// right; the failure this guards is intermittent by construction, so the test has to be too.
func TestSourceForBreaksTiesStably(t *testing.T) {
	reg, _ := newTestRegistry(t)
	const srcOther = "/run/mux/second-usbmuxd"
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	reg.Sink(srcOther).Apply(attach(udidA, muxd.TransportUSB))

	const same = "2026-08-18T09:00:00Z"
	reg.mu.Lock()
	reg.sources[srcUSB][udidA][muxd.TransportUSB] = same
	reg.sources[srcOther][udidA][muxd.TransportUSB] = same
	reg.mu.Unlock()

	// srcOther sorts BEFORE srcUSB ("/run/mux/second-usbmuxd" < "/run/mux/usbmuxd"), so the expected
	// winner is the one the timestamp cannot choose — asserting the tie-break rather than an
	// accident of insertion order.
	want := srcOther
	if srcUSB < want {
		want = srcUSB
	}
	for i := 0; i < 64; i++ {
		got, ok := reg.SourceFor(udidA, muxd.TransportUSB)
		if !ok {
			t.Fatalf("call %d: SourceFor = no answer; both muxers report this device", i)
		}
		if got != want {
			t.Fatalf("call %d: SourceFor = %q; want %q. Equal timestamps must resolve to the lexicographically smaller sourceID — a map-order answer sends one device's ops to a different daemon per call",
				i, got, want)
		}
	}
}

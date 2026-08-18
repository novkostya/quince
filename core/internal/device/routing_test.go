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
// both hold the same edge, the merged table shows the NEWEST last_seen, and the op must go to that
// same muxer. Two sources, one device; the second attach is the newer edge.
func TestSourceForAgreesWithTheMergedTable(t *testing.T) {
	reg, _ := newTestRegistry(t)
	const srcOther = "/run/mux/second-usbmuxd"
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	reg.Sink(srcOther).Apply(attach(udidA, muxd.TransportUSB))

	source, ok := reg.SourceFor(udidA, muxd.TransportUSB)
	if !ok {
		t.Fatal("SourceFor = no answer; both muxers report this device")
	}
	dev, ok := reg.Device(udidA)
	if !ok || dev.Transports.USB == nil {
		t.Fatalf("Device(%s) = %+v ok=%v; want a USB edge", udidA, dev, ok)
	}
	// Whichever source won, the timestamp it holds is the one the merged table shows.
	reg.mu.RLock()
	won := reg.sources[source][udidA][muxd.TransportUSB]
	reg.mu.RUnlock()
	if won != *dev.Transports.USB {
		t.Fatalf("routing picked source %q at %s; the merged table shows %s — routing and the UI disagree",
			source, won, *dev.Transports.USB)
	}
}

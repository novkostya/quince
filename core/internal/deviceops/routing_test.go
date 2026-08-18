package deviceops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/muxaddr"
)

// The routing tests (quince#1219 item D): the muxer a CLI is pointed at comes from the DEVICE,
// resolved against the registry, not from the transport label.

// byUDID is a MuxerFor that answers per device — what the live registry does, in the smallest
// shape a test can assert against.
func byUDID(m map[string]string) MuxerFor {
	return func(udid, _ string) (muxaddr.Endpoint, error) {
		s, ok := m[udid]
		if !ok {
			return muxaddr.Endpoint{}, errors.New("no muxer reports this device")
		}
		return mustEP(s), nil
	}
}

// TestSocketAddrRoutesByDeviceNotTransport: two devices, both on USB, reported by two different
// muxers. Before this, both got whichever address was configured as the USB one and one of them
// was dispatched at a daemon that cannot see it.
func TestSocketAddrRoutesByDeviceNotTransport(t *testing.T) {
	tl := NewTools(byUDID(map[string]string{
		fakeUDID:  "/var/run/usbmuxd",
		"OTHER-1": "/run/mux/second-usbmuxd",
	}), discard())

	for _, tc := range []struct{ udid, want string }{
		{fakeUDID, "UNIX:/var/run/usbmuxd"},
		{"OTHER-1", "UNIX:/run/mux/second-usbmuxd"},
	} {
		got, err := tl.socketAddr(tc.udid, TransportUSB)
		if err != nil {
			t.Fatalf("socketAddr(%s): %v", tc.udid, err)
		}
		if got != tc.want {
			t.Fatalf("socketAddr(%s, usb) = %q; want %q — both devices are `usb`, and they are not on the same muxer",
				tc.udid, got, tc.want)
		}
	}
}

// TestChildEnvRefusesWhenNothingReportsTheDevice: the refusal that replaces a guess. The old code
// could not express this — it always had an address to hand — so an op on an absent device ran
// against whatever the transport's configured daemon happened to be.
func TestChildEnvRefusesWhenNothingReportsTheDevice(t *testing.T) {
	tl := NewTools(byUDID(nil), discard())
	if env, err := tl.childEnv(fakeUDID, TransportUSB); err == nil {
		t.Fatalf("childEnv = %v, nil error; want a refusal naming the device", env)
	}
}

// TestChildEnvRefusesAZeroEndpoint closes quince#897 item 4's latent path BY CONSTRUCTION.
// libusbmuxd only NULL-checks USBMUXD_SOCKET_ADDRESS —
//
//	char *usbmuxd_socket_addr = getenv("USBMUXD_SOCKET_ADDRESS");
//	if (usbmuxd_socket_addr) { ... }
//
// — so an empty value is USED rather than falling back to the compiled-in default. It was
// unreachable only because an unconfigured transport never carried a device; a resolver that can
// answer "not configured" makes refusing it the code's job rather than the topology's.
func TestChildEnvRefusesAZeroEndpoint(t *testing.T) {
	tl := NewTools(func(string, string) (muxaddr.Endpoint, error) { return muxaddr.Endpoint{}, nil }, discard())
	_, err := tl.childEnv(fakeUDID, TransportWiFi)
	if !errors.Is(err, muxaddr.ErrEmpty) {
		t.Fatalf("childEnv with an unconfigured endpoint = %v; want muxaddr.ErrEmpty, never USBMUXD_SOCKET_ADDRESS=\"\"", err)
	}
}

// TestOpRefusesBeforeSpawningWhenTheMuxerIsUnknown: the refusal reaches a caller as an error from
// the op itself, and no CLI is started. Ideviceinfo/Idevicepair are left at their production names
// here on purpose — if the refusal ever stopped preceding the spawn, this test would try to run the
// real binaries, which are not on the box.
func TestOpRefusesBeforeSpawningWhenTheMuxerIsUnknown(t *testing.T) {
	tl := NewTools(byUDID(nil), discard())
	_, err := tl.Validate(context.Background(), fakeUDID, TransportUSB)
	if err == nil {
		t.Fatal("Validate = nil error; want the resolver's refusal")
	}
	if strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("Validate spawned a CLI before resolving the muxer: %v", err)
	}
}

// TestNoResolverIsARefusalNotADefault: a Tools built without a resolver (a wiring mistake) must
// fail loudly rather than inherit the ambient USBMUXD_SOCKET_ADDRESS or libusbmuxd's default.
func TestNoResolverIsARefusalNotADefault(t *testing.T) {
	if _, err := (&Tools{}).socketAddr(fakeUDID, TransportUSB); err == nil {
		t.Fatal("socketAddr with no resolver = nil error; want a refusal")
	}
}

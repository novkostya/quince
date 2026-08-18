package backup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/muxaddr"
)

// testEndpoint is the one muxer the fake harness is pointed at. Every test that only needs A
// muxer uses it; the routing tests below build their own.
func testEndpoint(t *testing.T) muxaddr.Endpoint {
	t.Helper()
	ep, err := muxaddr.Parse("/var/run/usbmuxd")
	if err != nil {
		t.Fatalf("muxaddr.Parse: %v", err)
	}
	return ep
}

func envValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- { // last wins, as exec does
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// TestBackupChildGoesToTheMuxerThatReportedTheDevice (quince#1219 item D). A backup is the most
// expensive thing to misroute: hours of transfer, and the old code chose the endpoint from the
// job's transport label, which names a kind of connection rather than a daemon.
func TestBackupChildGoesToTheMuxerThatReportedTheDevice(t *testing.T) {
	const udid = "SYNTHETIC-UDID-AAAA-0001"
	second, err := muxaddr.Parse("/run/mux/second-usbmuxd")
	if err != nil {
		t.Fatalf("muxaddr.Parse: %v", err)
	}
	tl := &tool{bin: "idevicebackup2", muxerFor: StaticMuxer(second)}

	cmd, err := tl.command(context.Background(), TransportUSB, udid, filepath.Join(t.TempDir(), "working"), "")
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	got, ok := envValue(cmd.Env, "USBMUXD_SOCKET_ADDRESS")
	if !ok {
		t.Fatal("no USBMUXD_SOCKET_ADDRESS in the child env")
	}
	if want := second.Env(); got != want {
		t.Fatalf("USBMUXD_SOCKET_ADDRESS = %q; want %q — the child must reach the muxer that reported the device", got, want)
	}
}

// TestBackupRefusesWhenNoMuxerReportsTheDevice: no endpoint means no child. The alternative is
// USBMUXD_SOCKET_ADDRESS="" , which libusbmuxd treats as SET (it only NULL-checks the variable),
// so the transfer would fail against nothing with the reason nowhere.
func TestBackupRefusesWhenNoMuxerReportsTheDevice(t *testing.T) {
	refuse := errors.New("no muxer reports this device on usb")
	tl := &tool{bin: "idevicebackup2", muxerFor: func(string, string) (muxaddr.Endpoint, error) { return muxaddr.Endpoint{}, refuse }}

	cmd, err := tl.command(context.Background(), TransportUSB, "SYNTHETIC-UDID-AAAA-0001", t.TempDir(), "")
	if !errors.Is(err, refuse) {
		t.Fatalf("command = %v (cmd %v); want the resolver's refusal", err, cmd)
	}
	if cmd != nil {
		t.Fatal("command returned a process to start alongside its error")
	}
}

// TestBackupRefusesAZeroEndpoint: the same refusal for a transport with no muxer configured at
// all — quince#897 item 4's set-but-empty path, now unreachable by construction.
func TestBackupRefusesAZeroEndpoint(t *testing.T) {
	tl := &tool{bin: "idevicebackup2", muxerFor: StaticMuxer(muxaddr.Endpoint{})}
	if _, err := tl.command(context.Background(), TransportWiFi, "SYNTHETIC-UDID-AAAA-0001", t.TempDir(), ""); !errors.Is(err, muxaddr.ErrEmpty) {
		t.Fatalf("command with an unconfigured endpoint = %v; want muxaddr.ErrEmpty", err)
	}
}

package config

import (
	"strings"
	"testing"
)

// Validate is what answers a PUT (contracts §1: 422 {errors:[{path,message}]}), so it must accept
// every form muxaddr does and refuse the rest BY PATH. The serve-path parse in buildLiveStack is
// the other half — see validateDevices for why one check is not enough.
func TestValidateAcceptsEveryMuxerAddressForm(t *testing.T) {
	for _, form := range []string{
		"/run/mux/usbmuxd",      // a unix socket path
		"UNIX:/run/mux/usbmuxd", // libusbmuxd's own spelling — REGRESSION (quince#897 item 1)
		"127.0.0.1:27015",       // TCP
		"",                      // no muxer for this transport
	} {
		c := Default()
		c.Devices.UsbmuxdSocket, c.Devices.NetmuxdAddr = form, form
		for _, e := range Validate(c) {
			if e.Path == "devices.usbmuxd_socket" || e.Path == "devices.netmuxd_addr" {
				t.Errorf("Validate refused %q at %s: %s", form, e.Path, e.Message)
			}
		}
	}
}

// A malformed address is reported against the KEY the operator typed it into. Both keys are
// checked, because netmuxd_addr was the one that accepted a narrower grammar than it documented.
func TestValidateRefusesAMalformedMuxerAddressByPath(t *testing.T) {
	for _, tc := range []struct{ path, bad string }{
		{"devices.usbmuxd_socket", "usbmuxd"},
		{"devices.netmuxd_addr", "not-an-address"},
	} {
		c := Default()
		switch tc.path {
		case "devices.usbmuxd_socket":
			c.Devices.UsbmuxdSocket = tc.bad
		default:
			c.Devices.NetmuxdAddr = tc.bad
		}
		found := false
		for _, e := range Validate(c) {
			if e.Path == tc.path {
				found = true
			}
		}
		if !found {
			t.Errorf("Validate accepted %q at %s; want a 422 error on that path", tc.bad, tc.path)
		}
	}
}

// qn.6p D2. `manage_muxer: true` asks for a profile v0.1 does not ship, and it is REFUSED rather
// than ignored — because yaml.Unmarshal drops unknown keys silently, so deleting the key would
// have turned an existing all-in-one install into a muxerless one with no muxer configured and
// nothing said about it (storages_validate_test.go:162 is this repo's record of that class).
func TestValidateRefusesManageMuxerTrue(t *testing.T) {
	c := Default()
	c.Devices.ManageMuxer = true

	var msg string
	for _, e := range Validate(c) {
		if e.Path == "devices.manage_muxer" {
			msg = e.Message
		}
	}
	if msg == "" {
		t.Fatal("Validate accepted manage_muxer: true; want a refusal on devices.manage_muxer")
	}
	// The refusal must tell an upgrader what to do instead. A bare "not supported" would leave
	// them to find the replacement, and the replacement is the whole point of the change.
	for _, want := range []string{"usbmuxd_socket", "quince#897"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not name %q", msg, want)
		}
	}
}

// Default() must not carry a value Validate refuses: Load() DISCARDS an invalid config and falls
// back to Default(), so a Default that failed its own validation would make that fallback
// invalid too — a loop with no honest answer.
func TestDefaultPassesItsOwnValidation(t *testing.T) {
	if errs := Validate(Default()); len(errs) > 0 {
		t.Fatalf("Default() fails Validate: %+v", errs)
	}
}

// The dead `127.0.0.1:27015` default is gone (quince#897 item 3): it made quince dial a port
// nothing listened on forever, and made "no Wi-Fi muxer" inexpressible except by knowing that an
// explicit "" differs from an absent key.
func TestDefaultHasNoPhantomWifiMuxer(t *testing.T) {
	if got := Default().Devices.NetmuxdAddr; got != "" {
		t.Errorf("default netmuxd_addr = %q; want empty — a default here is a dead port dialed forever", got)
	}
	// The USB default stays, and stays usbmuxd's OWN default path, so a host already running
	// usbmuxd works with no config.yml at all — the case quince#897 was filed about.
	if got := Default().Devices.UsbmuxdSocket; got != "/var/run/usbmuxd" {
		t.Errorf("default usbmuxd_socket = %q; want /var/run/usbmuxd (usbmuxd's own default)", got)
	}
}

package config

import "testing"

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

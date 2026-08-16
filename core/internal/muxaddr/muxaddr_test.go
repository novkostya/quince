package muxaddr

import (
	"strings"
	"testing"
)

// The table is the rung's G1. The two rows marked REGRESSION are the values quince#897 measured
// failing: each worked for one consumer and broke the other, which is why the grammar is parsed
// once rather than three times.
func TestParse(t *testing.T) {
	// allForms is what a GRAMMAR refusal owes the reader: every accepted spelling, because the
	// failure it replaces named none of them. A refusal about ONE malformed form owes something
	// narrower — the fix for that form — so those rows name it themselves.
	allForms := []string{"/run/mux/usbmuxd", "UNIX:", "host:port"}

	for _, tc := range []struct {
		name    string
		in      string
		network string
		address string
		env     string
		wantErr bool
		// errNames is what the refusal must contain. Empty means allForms.
		errNames []string
	}{
		{name: "bare unix path", in: "/run/mux/usbmuxd",
			network: "unix", address: "/run/mux/usbmuxd", env: "UNIX:/run/mux/usbmuxd"},

		// REGRESSION (quince#897): this dialed TCP and produced
		// `dial tcp: lookup tcp//run/mux/usbmuxd: unknown port`.
		{name: "UNIX-prefixed path dials unix", in: "UNIX:/run/mux/usbmuxd",
			network: "unix", address: "/run/mux/usbmuxd", env: "UNIX:/run/mux/usbmuxd"},

		// REGRESSION (quince#897): the bare path dialed correctly and then reached the CLIs as a
		// bare path, which libusbmuxd reads as host:port.
		{name: "bare unix path reaches a subprocess as UNIX:", in: "/var/run/usbmuxd",
			network: "unix", address: "/var/run/usbmuxd", env: "UNIX:/var/run/usbmuxd"},

		{name: "lowercase unix: is accepted", in: "unix:/run/mux/usbmuxd",
			network: "unix", address: "/run/mux/usbmuxd", env: "UNIX:/run/mux/usbmuxd"},
		{name: "host:port", in: "127.0.0.1:27015",
			network: "tcp", address: "127.0.0.1:27015", env: "127.0.0.1:27015"},
		{name: "hostname:port", in: "muxer:27015",
			network: "tcp", address: "muxer:27015", env: "muxer:27015"},
		{name: "ipv6 literal", in: "[::1]:27015",
			network: "tcp", address: "[::1]:27015", env: "[::1]:27015"},
		{name: "surrounding space is trimmed", in: "  /run/mux/usbmuxd  ",
			network: "unix", address: "/run/mux/usbmuxd", env: "UNIX:/run/mux/usbmuxd"},

		{name: "empty is not configured, not an error", in: ""},
		{name: "whitespace only is not configured", in: "   "},

		// Not a grammar error: the spelling was recognised and the path was missing, so the
		// refusal names the fix for THAT form rather than reciting all three.
		{name: "UNIX: with no path", in: "UNIX:", wantErr: true,
			errNames: []string{"UNIX:/path/to/socket"}},
		{name: "a bare word is not an address", in: "usbmuxd", wantErr: true},
		{name: "host with no port", in: "127.0.0.1:", wantErr: true},
		{name: "port with no host", in: ":27015", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want an error", tc.in, got)
				}
				// The refusal must name what IS accepted: the failure it replaces named none of
				// the accepted forms and left the grammar to be inferred from a dial error.
				want := tc.errNames
				if len(want) == 0 {
					want = allForms
				}
				for _, w := range want {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("Parse(%q) error %q does not name %q", tc.in, err, w)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.in, err)
			}
			if tc.network == "" {
				if !got.IsZero() {
					t.Fatalf("Parse(%q) = %+v, want the zero Endpoint", tc.in, got)
				}
				if got.Env() != "" {
					t.Errorf("zero Endpoint Env() = %q, want empty so the CLI falls back", got.Env())
				}
				return
			}
			if got.IsZero() {
				t.Fatalf("Parse(%q) is zero, want configured", tc.in)
			}
			gotNet, gotAddr := got.DialArgs()
			if gotNet != tc.network || gotAddr != tc.address {
				t.Errorf("Parse(%q) dials (%q, %q), want (%q, %q)",
					tc.in, gotNet, gotAddr, tc.network, tc.address)
			}
			if got.Env() != tc.env {
				t.Errorf("Parse(%q) env = %q, want %q", tc.in, got.Env(), tc.env)
			}
		})
	}
}

// Every accepted form round-trips through its own canonical spelling, so a value written into a
// log or a health detail can be pasted back into config.yml.
func TestStringRoundTrips(t *testing.T) {
	for _, in := range []string{"/run/mux/usbmuxd", "UNIX:/run/mux/usbmuxd", "127.0.0.1:27015", "[::1]:27015"} {
		first, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		second, err := Parse(first.String())
		if err != nil {
			t.Fatalf("Parse(%q) round trip: %v", first.String(), err)
		}
		if first != second {
			t.Errorf("%q did not round-trip: %+v then %+v", in, first, second)
		}
	}
}

// qn.6p D4 rests on this: "both keys name the same muxer" must be one comparison, so that
// pointing usbmuxd_socket and netmuxd_addr at one daemon opens ONE connection. quince#897's
// two-clients-on-one-socket is what happens without it.
func TestEndpointIsComparable(t *testing.T) {
	bare, _ := Parse("/run/mux/usbmuxd")
	prefixed, _ := Parse("UNIX:/run/mux/usbmuxd")
	if bare != prefixed {
		t.Errorf("the same socket written two ways compares unequal: %+v vs %+v", bare, prefixed)
	}
	other, _ := Parse("127.0.0.1:27015")
	if bare == other {
		t.Errorf("different muxers compare equal: %+v", bare)
	}
}

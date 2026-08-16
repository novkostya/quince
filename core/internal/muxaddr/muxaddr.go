// Package muxaddr parses the one thing `devices.usbmuxd_socket` and `devices.netmuxd_addr` both
// are: an address at which a muxer answers. It exists because quince had THREE places that turned
// one of those strings into something concrete and they did not agree, so no single value worked
// everywhere (qn.6p D3, quince#897 item 1):
//
//   - muxd.Client.dial      — picked "unix" for a leading "/", "tcp" otherwise
//   - deviceops.socketAddr  — prefixed "UNIX:" for USB, returned the Wi-Fi address verbatim
//   - backup.socketAddr     — its own copy of the same logic, for the idevicebackup2 child
//
// Measured consequence: `UNIX:/run/mux/usbmuxd` in `netmuxd_addr` dialed TCP and failed with
// `dial tcp: lookup tcp//run/mux/usbmuxd: unknown port`, while the bare path `/run/mux/usbmuxd`
// dialed correctly and then handed the CLIs an env value libusbmuxd reads as a host:port. One
// value could satisfy the dialer or the subprocess, never both.
//
// So the grammar is parsed ONCE and each consumer asks for the form it needs. Three forms are
// accepted for both keys, because a muxer is a muxer and which daemon serves which transport is
// the operator's business, not the key name's:
//
//	/run/mux/usbmuxd         a unix socket path
//	UNIX:/run/mux/usbmuxd    the same, in libusbmuxd's own spelling
//	127.0.0.1:27015          TCP
//
// The names `usbmuxd_socket` and `netmuxd_addr` therefore under-describe what they accept. They
// are deliberately NOT renamed: they carry daemon identity, which is what a reintroduced
// `manage_muxer` needs, and all-in-one is descoped rather than abandoned (qn.6p, Operator
// 2026-08-16).
package muxaddr

import (
	"errors"
	"net"
	"strings"
)

// unixPrefix is libusbmuxd's own spelling of "this is a socket path", and the only value of
// USBMUXD_SOCKET_ADDRESS that reaches a subprocess for a unix socket.
const unixPrefix = "UNIX:"

// Endpoint is a parsed muxer address. It is a comparable value on purpose: qn.6p D4 needs
// "these two keys name the same muxer" to be one `==`, so that pointing both at one daemon opens
// one connection rather than two (quince#897's two-clients-on-one-socket).
//
// The zero Endpoint means NOT CONFIGURED, which is a real answer rather than an error — an
// operator with no Wi-Fi muxer says so by leaving the key empty.
type Endpoint struct {
	network string // "unix" | "tcp" — empty when not configured
	address string // socket path | host:port
}

// ErrEmpty is never returned by Parse. It is here so a caller that needs "configured" to be
// mandatory can say why in one shared sentence rather than inventing its own.
var ErrEmpty = errors.New("muxaddr: no muxer address configured")

// Parse reads one of the three accepted forms. An empty string is not an error: it is the
// documented way to say a transport has no muxer, and Endpoint.IsZero reports it.
//
// The refusal names every accepted form, because the failure this replaces named none of them
// and left an operator to infer the grammar from a dial error about a port.
func Parse(s string) (Endpoint, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Endpoint{}, nil
	}

	// libusbmuxd spells it "UNIX:". Matched case-insensitively because an operator who writes
	// `unix:` has said exactly what they meant, and refusing that would be pedantry with a
	// dial error attached.
	if len(s) >= len(unixPrefix) && strings.EqualFold(s[:len(unixPrefix)], unixPrefix) {
		path := s[len(unixPrefix):]
		if path == "" {
			return Endpoint{}, errors.New("muxaddr: " + unixPrefix + " with no path — expected " +
				unixPrefix + "/path/to/socket")
		}
		return Endpoint{network: "unix", address: path}, nil
	}

	if strings.HasPrefix(s, "/") {
		return Endpoint{network: "unix", address: s}, nil
	}

	// Anything else must be host:port. SplitHostPort is the authority rather than a colon
	// count, so an IPv6 literal is handled by the standard library instead of by us.
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" || port == "" {
		return Endpoint{}, errors.New("muxaddr: " + s + " is not a muxer address — expected a " +
			"socket path (/run/mux/usbmuxd), " + unixPrefix + "/run/mux/usbmuxd, or host:port " +
			"(127.0.0.1:27015)")
	}
	return Endpoint{network: "tcp", address: s}, nil
}

// IsZero reports that no muxer was configured for this transport.
func (e Endpoint) IsZero() bool { return e.network == "" }

// DialArgs returns the (network, address) pair for net.Dialer. Calling it on a zero Endpoint is
// a caller bug — check IsZero first — and it returns empties rather than panicking, because a
// muxer client that dials "" fails visibly in a log line either way.
func (e Endpoint) DialArgs() (network, address string) { return e.network, e.address }

// Env is the USBMUXD_SOCKET_ADDRESS value for a subprocess: libusbmuxd wants UNIX:<path> for a
// socket and host:port for TCP. A zero Endpoint yields "", which is what an unset variable means
// and lets the CLI fall back to its own default.
func (e Endpoint) Env() string {
	if e.network == "unix" {
		return unixPrefix + e.address
	}
	return e.address
}

// String is the canonical spelling, for logs and health detail. It round-trips through Parse.
func (e Endpoint) String() string {
	if e.IsZero() {
		return ""
	}
	return e.Env()
}

package config

import "errors"

// CheckMuxerProfile refuses a config asking for a profile v0.1 does not ship: quince runs no muxer
// daemon, so `devices.manage_muxer: true` cannot be honoured (qn.6p D1/D2).
//
// A FATAL SERVE-PATH CHECK, NOT A VALIDATION ERROR, and the distinction is this project's own
// ruling rather than a preference. `contracts.md` records it for `tls:`:
//
//	WHETHER THESE FILES EXIST, PARSE, OR MATCH EACH OTHER IS NOT A VALIDATION ERROR. An invalid
//	config is DISCARDED in favour of last-good/defaults […] It is a FATAL check on the serve path
//	instead.
//
// Substitute this key and the sentence survives: Default() has `manage_muxer: false` AND no
// storage, so raising this in Validate would drop the operator's whole file and start quince in
// first-run onboarding — telling somebody with a working install and real backups that they have
// no storage, while the true reason sits in `GET /api/config` warnings, which quince#849 measured
// as rendered by no surface a user can reach. A diagnostic that collapses distinguishable causes
// is a defect even when every word of it is true (Operator, quince#940).
//
// THE `PUT` HOLE IS ACCEPTED, not overlooked (architect ruling, quince#1059): a write can still set
// this key, and the next start then refuses with the message below — recoverable and explained,
// where onboarding-with-no-storage is neither. Closing it in the PUT handler is an optional
// separate slice.
func CheckMuxerProfile(d DevicesConfig) error {
	if !d.ManageMuxer {
		return nil
	}
	// The idiom is StorageRequirement's and TLSRequirement's, which is preflight's: name what was
	// OBSERVED, say what follows from it, and print the exact thing to do. An error message is a
	// claim, and this one is read by somebody whose working install just stopped starting.
	return errors.New("devices.manage_muxer: true is not supported — quince ships no muxer daemon.\n" +
		"  Run one yourself (a host usbmuxd, a sidecar container, or another tool's) and point\n" +
		"  quince at it:\n" +
		"      devices:\n" +
		"        manage_muxer: false\n" +
		"        usbmuxd_socket: /var/run/usbmuxd   # or UNIX:<path>, or host:port\n" +
		"        netmuxd_addr: \"\"                   # a Wi-Fi muxer's address, or empty for none\n" +
		"  Point both at one address when a single muxer serves both transports.\n" +
		"  The in-container profile is DESCOPED, not abandoned — see quince#897.\n" +
		"  Nothing else in your config.yml has been changed or discarded")
}

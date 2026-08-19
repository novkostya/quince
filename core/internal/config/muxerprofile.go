package config

import (
	"errors"
	"fmt"
	"strings"
)

// CheckMuxers refuses a config asking for something v0.1 does not ship, and a config still written
// in the shape v0.1 no longer reads (quince#1219 A/B/C, ruled 2026-08-18).
//
// A FATAL SERVE-PATH CHECK, NOT A VALIDATION ERROR, and the distinction is this project's own
// ruling rather than a preference. `contracts.md` records it for `tls:`:
//
//	WHETHER THESE FILES EXIST, PARSE, OR MATCH EACH OTHER IS NOT A VALIDATION ERROR. An invalid
//	config is DISCARDED in favour of last-good/defaults […] It is a FATAL check on the serve path
//	instead.
//
// Substitute either check below and the sentence survives: a validation error drops the operator's
// whole file for Default(), which has ONE muxer and NO storage — so an operator with a working
// install and real backups would be shown first-run onboarding, while the true reason sat in
// `GET /api/config` warnings, which quince#849 measured as rendered by no surface a user can reach.
// A diagnostic that collapses distinguishable causes is a defect even when every word of it is true
// (Operator, quince#940).
//
// BOTH MESSAGES NAME WHAT TO WRITE, not merely what is wrong. That is quince#940's requirement and
// it is why the legacy check reads the operator's own addresses back to them: a refusal that quotes
// the entry you must add is actionable, and *"unknown section `devices:`"* is not.
func CheckMuxers(c Config) error {
	if err := checkRetiredDevices(c.Devices); err != nil {
		return err
	}
	return checkMuxerTypes(c.ResolvedMuxers())
}

// checkRetiredDevices refuses a config that still carries the retired `devices:` section.
//
// WITHOUT THIS THE SECTION WOULD BE DROPPED IN SILENCE. Config load is a plain `yaml.Unmarshal`,
// which discards unknown keys without a word — the incident is on record at
// `storages_validate_test.go:162`. So an operator whose file says `usbmuxd_socket:
// /run/mux/usbmuxd` would be started on the DEFAULT `/var/run/usbmuxd`, and would watch every
// device vanish with nothing to read. That is the *no silent caps or fallbacks* rule applied to a
// config key.
//
// NO MIGRATION IS OWED — Operator, 2026-08-18: quince is pre-release with one user, so `devices:`
// retires with no compatibility path and no alias. A refusal is not a migration: it is the
// diagnostic that stops the retirement being silent.
func checkRetiredDevices(d *LegacyDevices) error {
	if d == nil {
		return nil
	}
	var b strings.Builder
	b.WriteString("devices: is no longer read — muxers are configured by the top-level `muxers:` list.\n")
	b.WriteString("  Replace that section with:\n")
	b.WriteString("      muxers:\n")
	for _, addr := range d.addresses() {
		fmt.Fprintf(&b, "        - address: %s\n", addr)
	}
	if len(d.addresses()) == 0 {
		b.WriteString("        - address: /var/run/usbmuxd   # or UNIX:<path>, or host:port\n")
	}
	b.WriteString("  One entry per muxer. A muxer serving BOTH transports is ONE entry — that is the\n")
	b.WriteString("  case the old two-key shape made you write twice.\n")
	if d.ManageMuxer != nil && *d.ManageMuxer {
		b.WriteString("  `manage_muxer: true` has no replacement in v0.1: quince ships no muxer daemon, so\n")
		b.WriteString("  run one yourself and list its address above. The in-container profile is DESCOPED,\n")
		b.WriteString("  not abandoned — see quince#897.\n")
	}
	b.WriteString("  Nothing else in your config.yml has been changed or discarded")
	return errors.New(b.String())
}

// addresses returns the muxer addresses the retired section actually declared, deduplicated and in
// key order — usbmuxd first, as the old section listed them.
//
// DEDUPLICATED BECAUSE POINTING BOTH KEYS AT ONE DAEMON WAS THE DOCUMENTED WAY to say "this muxer
// serves both transports" (qn.6p D4). Emitting that as two identical entries would hand the
// operator a config `validateMuxers` then refuses as a duplicate — a remedy that does not work is
// worse than none.
func (d *LegacyDevices) addresses() []string {
	var out []string
	for _, p := range []*string{d.UsbmuxdSocket, d.NetmuxdAddr} {
		if p == nil || strings.TrimSpace(*p) == "" {
			continue
		}
		if len(out) == 1 && out[0] == *p {
			continue
		}
		out = append(out, *p)
	}
	return out
}

// checkMuxerTypes refuses `type: managed`, which the schema knows by name and v0.1 does not ship.
//
// KNOWN-AND-UNSHIPPED IS NOT MALFORMED, which is why this is here and not in `validateMuxers`. An
// unknown type is a typo and belongs to validation; `managed` is a value the schema documents as
// planned, and telling somebody who wrote it that it is invalid would be false.
func checkMuxerTypes(muxers []MuxerConfig) error {
	for i, m := range muxers {
		if m.Type != MuxerManaged {
			continue
		}
		return fmt.Errorf("muxers[%d].type: %s is not supported — quince ships no muxer daemon.\n"+
			"  Run one yourself (a host usbmuxd, a sidecar container, or another tool's) and let\n"+
			"  quince dial it:\n"+
			"      muxers:\n"+
			"        - address: %s\n"+
			"  `type:` may be omitted entirely; %s is the default and the only value v0.1 accepts.\n"+
			"  The in-container profile is DESCOPED, not abandoned — see quince#897.\n"+
			"  Nothing else in your config.yml has been changed or discarded",
			i, MuxerManaged, m.Address, MuxerExternal)
	}
	return nil
}

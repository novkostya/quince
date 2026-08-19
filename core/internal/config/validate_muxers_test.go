package config

import (
	"strings"
	"testing"
)

// Validate is what answers a PUT (contracts §1: 422 {errors:[{path,message}]}), so it must accept
// every form muxaddr does and refuse the rest BY PATH. The serve-path parse in buildLiveStack is
// the other half — see validateMuxers for why one check is not enough.
func TestValidateAcceptsEveryMuxerAddressForm(t *testing.T) {
	for _, form := range []string{
		"/run/mux/usbmuxd",      // a unix socket path
		"UNIX:/run/mux/usbmuxd", // libusbmuxd's own spelling — REGRESSION (quince#897 item 1)
		"127.0.0.1:27015",       // TCP
	} {
		c := Default()
		c.Muxers = &[]MuxerConfig{{Address: form}}
		for _, e := range Validate(c) {
			if strings.HasPrefix(e.Path, "muxers[") {
				t.Errorf("Validate refused %q at %s: %s", form, e.Path, e.Message)
			}
		}
	}
}

// The path carries the INDEX the operator can find in their own file. `storage[i]` set that
// convention and a muxer list has the same problem: "one of your muxers is wrong" is not a
// diagnostic.
func TestValidateRefusesAMalformedAddressByIndex(t *testing.T) {
	c := Default()
	c.Muxers = &[]MuxerConfig{{Address: "/run/mux/usbmuxd"}, {Address: "not-an-address"}}

	var got []string
	for _, e := range Validate(c) {
		got = append(got, e.Path)
	}
	if len(got) != 1 || got[0] != "muxers[1].address" {
		t.Fatalf("Validate paths = %v; want exactly muxers[1].address — the entry that is wrong", got)
	}
}

// An entry with no address configures nothing, and silently ignoring it would leave an operator
// believing they had declared a muxer.
func TestValidateRefusesAnEntryWithNoAddress(t *testing.T) {
	c := Default()
	c.Muxers = &[]MuxerConfig{{Type: MuxerExternal}}
	if !hasPath(Validate(c), "muxers[0].address") {
		t.Fatalf("Validate accepted an entry with no address; want a 422 on muxers[0].address")
	}
}

// AN UNKNOWN TYPE IS MALFORMED AND BELONGS HERE; `managed` IS KNOWN-AND-UNSHIPPED AND DOES NOT.
// That split is the whole of quince#1219 B's refusal design: telling somebody who wrote a
// documented-but-unbuilt value that it is invalid would be false, and a validation error would
// discard their file besides.
func TestValidateRefusesAnUnknownTypeButNotManaged(t *testing.T) {
	c := Default()
	c.Muxers = &[]MuxerConfig{{Address: "/run/mux/usbmuxd", Type: "banana"}}
	if !hasPath(Validate(c), "muxers[0].type") {
		t.Fatalf("Validate accepted type: banana; want a 422 on muxers[0].type")
	}

	c.Muxers = &[]MuxerConfig{{Address: "/run/mux/usbmuxd", Type: MuxerManaged}}
	if hasPath(Validate(c), "muxers[0].type") {
		t.Fatalf("Validate refused type: %s — that is CheckMuxers' refusal, on the serve path, "+
			"because a validation error would discard the whole config", MuxerManaged)
	}
	c.Muxers = &[]MuxerConfig{{Address: "/run/mux/usbmuxd", Type: MuxerExternal}}
	if hasPath(Validate(c), "muxers[0].type") {
		t.Fatalf("Validate refused the only type v0.1 accepts")
	}
}

// One connection per muxer, so a repeated address is a mistake rather than a topology — and it is
// the mistake the old two-key shape MADE you write, which is why it is worth refusing by name.
func TestValidateRefusesADuplicateAddress(t *testing.T) {
	c := Default()
	c.Muxers = &[]MuxerConfig{{Address: "/run/mux/usbmuxd"}, {Address: "/run/mux/usbmuxd"}}
	if !hasPath(Validate(c), "muxers[1].address") {
		t.Fatalf("Validate accepted the same address twice; want a 422 naming the second entry")
	}
}

// `muxers: []` IS A LEGAL THING TO WRITE and must not be a validation error. Default() has one
// entry, so refusing an empty list here would DISCARD the operator's file and start quince on a
// muxer they deliberately removed — the silent downgrade every other check on this path avoids.
func TestValidateAcceptsAnEmptyMuxerList(t *testing.T) {
	c := Default()
	c.Muxers = &[]MuxerConfig{}
	for _, e := range Validate(c) {
		if strings.HasPrefix(e.Path, "muxers") {
			t.Fatalf("Validate refused an empty list at %s: %s", e.Path, e.Message)
		}
	}
}

// The default is the whole point of the reshape: a compose file is enough, and nobody hand-edits
// config.yml before first launch. `/var/run/usbmuxd` is libusbmuxd's OWN default, so it serves both
// "I ran your sidecar" and "I already had a muxer".
func TestDefaultResolvesToOneExternalMuxerAtLibusbmuxdsOwnPath(t *testing.T) {
	got := Default().ResolvedMuxers()
	if len(got) != 1 || got[0].Address != DefaultMuxerAddress {
		t.Fatalf("Default().ResolvedMuxers() = %+v; want exactly one entry at %s", got, DefaultMuxerAddress)
	}
	// Left EMPTY rather than spelled `external`: empty means external, and writing the default in
	// would make the resolved value disagree with the file D12 promises — the one carrying only
	// what the user set.
	if got[0].Type != "" {
		t.Fatalf("default muxer type = %q; want it unset, since external is the default", got[0].Type)
	}
	if errs := Validate(Default()); len(errs) != 0 {
		t.Fatalf("Default() does not validate: %+v — the fallback an invalid config lands on must itself be valid", errs)
	}
}

// AND Default() ITSELF MUST NOT CARRY IT, which is the property that keeps it out of every written
// config.yml. `MarshalDeclared` keeps a non-empty sequence even when nothing declared it — so a
// default entry here would be written as a key nobody set, and the measured symptom was quince
// warning that it had had to write the LONG form of the file.
func TestDefaultDeclaresNoMuxerSoNoneIsEverWritten(t *testing.T) {
	if Default().Muxers != nil {
		t.Fatalf("Default().Muxers = %+v; want nil — a default entry here lands in every config.yml", *Default().Muxers)
	}
}

// ABSENT AND EMPTY ARE DIFFERENT, and that is why this key is a pointer. `muxers: []` is an
// operator saying "none"; substituting the default there would hand back a muxer they deliberately
// removed.
func TestAnEmptyMuxerListMeansNoneNotTheDefault(t *testing.T) {
	c := Default()
	c.Muxers = &[]MuxerConfig{}
	if got := c.ResolvedMuxers(); len(got) != 0 {
		t.Fatalf("ResolvedMuxers() on an explicit empty list = %+v; want none", got)
	}
}

package main

import (
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/muxsup"
)

// qn.4c story 1: ONE config flag governs every configured muxer (ruled (bz)-2). These assert the
// resolution table directly — the supervisors themselves spawn processes, so the decision is kept
// in a pure function.

func names(specs []muxsup.Spec) string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return strings.Join(out, ",")
}

// externalAddresses joins the plan's dial-only entries. It yielded daemon NAMES until
// quince#1219 item E, which retired them: a muxer's identity is its address, because the name was
// only ever a literal chosen by which config key the address came out of.
func externalAddresses(ext []externalMuxer) string {
	out := make([]string, 0, len(ext))
	for _, e := range ext {
		out = append(out, e.address)
	}
	return strings.Join(out, ",")
}

func TestPlannedMuxers(t *testing.T) {
	cases := []struct {
		name     string
		muxers   []config.MuxerConfig
		external string
	}{
		{
			name:     "the defaults are two external muxers, in order",
			muxers:   config.Default().ResolvedMuxers(),
			external: "UNIX:/var/run/usbmuxd,UNIX:/var/run/mux/usbmuxd",
		},
		{
			name:     "two muxers are two entries, in list order",
			muxers:   []config.MuxerConfig{{Address: "/var/run/usbmuxd"}, {Address: "127.0.0.1:27015"}},
			external: "UNIX:/var/run/usbmuxd,127.0.0.1:27015",
		},
		{
			// The shape the old two-key section made you write twice. ONE entry now, and the
			// dedupe that existed only to undo the schema is gone with it.
			name:     "one muxer serving both transports is one entry",
			muxers:   []config.MuxerConfig{{Address: "/run/mux/usbmuxd"}},
			external: "UNIX:/run/mux/usbmuxd",
		},
		{
			// `UNIX:/x` and `/x` are one daemon written two ways. The plan carries the CANONICAL
			// spelling — muxaddr.Endpoint.String(), which is libusbmuxd's own `UNIX:` form and
			// round-trips through Parse. That is also what an operator sees in /api/health.
			// spelling, because health reports it as the muxer's identity and item E looks
			// transports up by it against the registry, which keys sources the same way.
			name:     "an address is canonicalised, so both spellings name one muxer",
			muxers:   []config.MuxerConfig{{Address: "UNIX:/run/mux/usbmuxd"}},
			external: "UNIX:/run/mux/usbmuxd",
		},
		{
			name:     "a duplicate address is dialled once",
			muxers:   []config.MuxerConfig{{Address: "/run/mux/usbmuxd"}, {Address: "UNIX:/run/mux/usbmuxd"}},
			external: "UNIX:/run/mux/usbmuxd",
		},
		{
			// `muxers: []` is a legal thing to write. It plans nothing, and buildLiveStack says so
			// out loud rather than letting an empty Devices screen be the only symptom.
			name:   "an empty list plans nothing",
			muxers: []config.MuxerConfig{},
		},
		{
			name:   "an entry with no address plans nothing",
			muxers: []config.MuxerConfig{{Type: config.MuxerExternal}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := plannedMuxers(tc.muxers)
			if externalAddresses(got.external) != tc.external {
				t.Errorf("external = %q; want %q", externalAddresses(got.external), tc.external)
			}
			// NOTHING IS EVER SUPERVISED IN v0.1. `type: managed` is refused on the serve path, and
			// the config→spec mapping is gone because the ruled schema has no way to NAME a daemon.
			if len(got.supervise) != 0 {
				t.Errorf("supervise = %q; want nothing — quince ships no muxer daemon", names(got.supervise))
			}
			if len(got.problems) != 0 {
				t.Errorf("unexpected problems: %q", got.problems)
			}
		})
	}
}

// TestPlannedMuxersDefaultConfig: the shipped defaults SUPERVISE NOTHING (qn.6p D1). quince runs
// no muxer daemon in v0.1 — the operator runs one and quince dials it.
//
// This asserted the opposite until qn.6p: `manage_muxer` defaulted true and the default plan
// supervised both daemons. That was qn.2b/(by)'s answer to "nothing starts netmuxd, so Wi-Fi is
// silently dead after a restart", and it was right for the profile it shipped in. The profile
// changed and the assertion follows it, rather than the test being deleted.
func TestPlannedMuxersDefaultConfig(t *testing.T) {
	plan := plannedMuxers(config.Default().ResolvedMuxers())
	if got := names(plan.supervise); got != "" {
		t.Fatalf("default config supervises %q; want nothing — quince ships no muxer daemon", got)
	}
	if len(plan.problems) != 0 {
		t.Fatalf("default config has problems: %q", plan.problems)
	}
	// The USB muxer is still REACHED, just not owned. An absent external entry would leave health
	// with nothing to report, and design §10 says an absent entry reads as "no muxer".
	// BOTH defaults are dialled, not just the standard one (quince#1256). The second is where the
	// shipped compose stack puts its socket; whichever answers is used, and the other is reported
	// `absent` rather than as a fault, which is what lets a default name more than one candidate.
	if len(plan.external) != 2 {
		t.Fatalf("default externals = %+v; want both default muxer addresses dialled", plan.external)
	}
	if plan.external[0].address != "UNIX:/var/run/usbmuxd" ||
		plan.external[1].address != "UNIX:/var/run/mux/usbmuxd" {
		t.Fatalf("default externals = %+v; want the standard path first, then the sidecar path", plan.external)
	}
}

// TestDialerLookupReturnsAnUntypedNil pins the typed-nil trap (architect, quince#1060).
//
// `var c *muxd.Client; return c` compiles, reads correctly, and is WRONG: an interface holding a
// nil pointer is not nil, so muxsup's `dialer == nil` check would pass and it would call Health()
// on nothing. That turns the wiring bug status() deliberately reports — "configured but nothing is
// dialing it" — into a panic at the moment somebody opens /api/health to find out what is wrong.
//
// Comments in two files said so and nothing enforced it. This does.
func TestDialerLookupReturnsAnUntypedNil(t *testing.T) {
	lookup := dialerLookup(map[string]*muxd.Client{
		"/run/mux/usbmuxd": nil, // configured, canonical, and NO client was built for it
	})

	if got := lookup("/run/mux/usbmuxd"); got != nil {
		t.Errorf("a configured address with no client returned %#v; want an untyped nil, or "+
			"muxsup calls Health() on nothing instead of reporting the wiring bug", got)
	}
	if got := lookup("never-configured"); got != nil {
		t.Errorf("an unconfigured address returned %#v; want an untyped nil", got)
	}
}

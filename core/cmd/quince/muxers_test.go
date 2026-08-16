package main

import (
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/muxaddr"
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

func externalNames(ext []externalMuxer) string {
	out := make([]string, 0, len(ext))
	for _, e := range ext {
		out = append(out, e.name)
	}
	return strings.Join(out, ",")
}

func TestPlannedMuxers(t *testing.T) {
	cases := []struct {
		name             string
		cfg              config.DevicesConfig
		supervise        string
		external         string
		wantProblem      bool
		problemSubstring string
	}{
		{
			name:      "managed with both addresses supervises both daemons",
			cfg:       config.DevicesConfig{ManageMuxer: true, UsbmuxdSocket: "/var/run/usbmuxd", NetmuxdAddr: "127.0.0.1:27015"},
			supervise: "usbmuxd,netmuxd",
		},
		{
			name:      "managed without netmuxd_addr supervises usbmuxd only",
			cfg:       config.DevicesConfig{ManageMuxer: true, UsbmuxdSocket: "/var/run/usbmuxd"},
			supervise: "usbmuxd",
		},
		{
			name:      "managed without usbmuxd_socket supervises netmuxd only",
			cfg:       config.DevicesConfig{ManageMuxer: true, NetmuxdAddr: "127.0.0.1:27015"},
			supervise: "netmuxd",
		},
		{
			name:     "unmanaged supervises nothing but still reports both as external",
			cfg:      config.DevicesConfig{UsbmuxdSocket: "/var/run/usbmuxd", NetmuxdAddr: "127.0.0.1:27015"},
			external: "usbmuxd,netmuxd",
		},
		{
			name: "nothing configured plans nothing",
			cfg:  config.DevicesConfig{ManageMuxer: true},
		},
		{
			// The spike finding: netmuxd deletes and rebinds whatever --socket-path names, so a
			// path equal to the usbmuxd socket is a silent USB blackout. Refuse loudly instead.
			name:             "netmuxd socket colliding with the usbmuxd socket is refused loudly",
			cfg:              config.DevicesConfig{ManageMuxer: true, UsbmuxdSocket: "/var/run/netmuxd", NetmuxdAddr: "127.0.0.1:27015"},
			supervise:        "usbmuxd",
			external:         "netmuxd",
			wantProblem:      true,
			problemSubstring: "delete and rebind",
		},
		{
			name:             "a netmuxd_addr that is not host:port is refused loudly",
			cfg:              config.DevicesConfig{ManageMuxer: true, UsbmuxdSocket: "/var/run/usbmuxd", NetmuxdAddr: "not-an-address"},
			supervise:        "usbmuxd",
			external:         "netmuxd",
			wantProblem:      true,
			problemSubstring: "host:port",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := plannedMuxers(tc.cfg)
			if names(got.supervise) != tc.supervise {
				t.Errorf("supervise = %q; want %q", names(got.supervise), tc.supervise)
			}
			if externalNames(got.external) != tc.external {
				t.Errorf("external = %q; want %q", externalNames(got.external), tc.external)
			}
			if tc.wantProblem {
				if len(got.problems) == 0 {
					t.Fatal("want a loud problem, got none")
				}
				if !strings.Contains(strings.Join(got.problems, " "), tc.problemSubstring) {
					t.Errorf("problems = %q; want one mentioning %q", got.problems, tc.problemSubstring)
				}
			} else if len(got.problems) != 0 {
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
	plan := plannedMuxers(config.Default().Devices)
	if got := names(plan.supervise); got != "" {
		t.Fatalf("default config supervises %q; want nothing — quince ships no muxer daemon", got)
	}
	if len(plan.problems) != 0 {
		t.Fatalf("default config has problems: %q", plan.problems)
	}
	// The USB muxer is still REACHED, just not owned. An absent external entry would leave health
	// with nothing to report, and design §10 says an absent entry reads as "no muxer".
	if len(plan.external) != 1 || plan.external[0].name != "usbmuxd" {
		t.Fatalf("default externals = %+v; want exactly usbmuxd dialed", plan.external)
	}
}

// TestPlannedMuxersManagedProfileIsParked keeps the all-in-one plan proven while nothing ships it.
//
// `devices.manage_muxer: true` is refused by config validation (qn.6p D2), so this configuration
// cannot reach production — but the profile is DESCOPED, NOT ABANDONED, and reintroducing it is
// deleting one validation branch. If that branch returns and this behaviour has rotted meanwhile,
// the rung that brings it back has to re-earn proof that already exists. So the managed plan keeps
// its test, driven from an explicit config rather than from Default().
func TestPlannedMuxersManagedProfileIsParked(t *testing.T) {
	dcfg := config.Default().Devices
	dcfg.ManageMuxer = true
	dcfg.NetmuxdAddr = "127.0.0.1:27015" // no longer a default; named here because this plan needs one

	plan := plannedMuxers(dcfg)
	if names(plan.supervise) != "usbmuxd,netmuxd" {
		t.Fatalf("managed profile supervises %q; want usbmuxd,netmuxd", names(plan.supervise))
	}
	if len(plan.problems) != 0 {
		t.Fatalf("managed profile has problems: %q", plan.problems)
	}
	// The netmuxd child must never be pointed at usbmuxd's socket: netmuxd DELETES and rebinds
	// whatever --socket-path names, which is a silent USB blackout (stack D2, measured twice).
	for _, spec := range plan.supervise {
		if spec.Name != "netmuxd" {
			continue
		}
		for _, a := range spec.Args {
			if a == dcfg.UsbmuxdSocket {
				t.Fatal("netmuxd argv points at the usbmuxd socket — it would delete and rebind it")
			}
		}
	}
}

// TestDistinctEndpointsCollapsesOneMuxerServingBoth is qn.6p D4. Pointing both `devices:` keys at
// one daemon is how an operator says "this muxer serves both transports" — the hardened shape,
// since netmuxd serves USB and mDNS Wi-Fi over a single socket. It used to open TWO muxd clients
// on that socket, so the registry saw two sources and every replay arrived twice.
func TestDistinctEndpointsCollapsesOneMuxerServingBoth(t *testing.T) {
	for _, tc := range []struct {
		name       string
		usb, wifi  string
		wantClient int
		wantShared bool
	}{
		{name: "two separate muxers", usb: "/var/run/usbmuxd", wifi: "127.0.0.1:27015", wantClient: 2},
		{name: "one muxer, both keys", usb: "/run/mux/usbmuxd", wifi: "/run/mux/usbmuxd", wantClient: 1, wantShared: true},
		// The SAME socket written two ways. This is the row that needs Endpoint comparability
		// rather than string equality, and the one an operator produces by copying the value
		// out of a health detail into the other key.
		{name: "one muxer, two spellings", usb: "/run/mux/usbmuxd", wifi: "UNIX:/run/mux/usbmuxd", wantClient: 1, wantShared: true},
		{name: "usb only", usb: "/var/run/usbmuxd", wifi: "", wantClient: 1},
		{name: "wifi only", usb: "", wifi: "127.0.0.1:27015", wantClient: 1},
		{name: "no muxer at all", usb: "", wifi: "", wantClient: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usbEP, err := muxaddr.Parse(tc.usb)
			if err != nil {
				t.Fatalf("parse usb %q: %v", tc.usb, err)
			}
			wifiEP, err := muxaddr.Parse(tc.wifi)
			if err != nil {
				t.Fatalf("parse wifi %q: %v", tc.wifi, err)
			}

			unique, byConfigured := distinctEndpoints([]muxerBinding{{tc.usb, usbEP}, {tc.wifi, wifiEP}})
			if len(unique) != tc.wantClient {
				t.Fatalf("distinct endpoints = %d (%v); want %d", len(unique), unique, tc.wantClient)
			}

			// Every configured key must still resolve, or health loses its entry for that
			// transport — an absent entry reads as "no muxer" (design §10).
			for _, configured := range []string{tc.usb, tc.wifi} {
				if configured == "" {
					continue
				}
				if _, ok := byConfigured[configured]; !ok {
					t.Errorf("configured address %q resolves to nothing; health would lose its entry", configured)
				}
			}

			if tc.wantShared && byConfigured[tc.usb] != byConfigured[tc.wifi] {
				t.Errorf("both keys name one muxer but resolved differently: %v vs %v",
					byConfigured[tc.usb], byConfigured[tc.wifi])
			}
		})
	}
}

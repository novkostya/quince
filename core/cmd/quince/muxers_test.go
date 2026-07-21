package main

import (
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/config"
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

// TestPlannedMuxersDefaultConfig: the shipped defaults (manage_muxer true, both addresses set)
// supervise both daemons — i.e. `compose up` alone brings Wi-Fi up, which is the whole point of
// this rung ((by): without it nothing starts netmuxd and Wi-Fi is silently dead after a restart).
func TestPlannedMuxersDefaultConfig(t *testing.T) {
	plan := plannedMuxers(config.Default().Devices)
	if names(plan.supervise) != "usbmuxd,netmuxd" {
		t.Fatalf("default config supervises %q; want usbmuxd,netmuxd", names(plan.supervise))
	}
	if len(plan.problems) != 0 {
		t.Fatalf("default config has problems: %q", plan.problems)
	}
	// And the netmuxd child must not be pointed at usbmuxd's socket by default.
	for _, spec := range plan.supervise {
		if spec.Name != "netmuxd" {
			continue
		}
		for _, a := range spec.Args {
			if a == config.Default().Devices.UsbmuxdSocket {
				t.Fatal("default netmuxd argv points at the usbmuxd socket")
			}
		}
	}
}

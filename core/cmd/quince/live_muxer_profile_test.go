package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
)

// These pin the CALL, not the check (architect, quince#1059).
//
// config.CheckMuxers has its own tests, and its predecessor passed while nothing invoked it:
// deleting the call from buildLiveStack failed no test in either package. That gap is worse than
// untested wiring, and the difference is the failure mode. A missing muxer client fails LOUD — no
// device ever appears. A missing refusal fails SILENT: quince starts happily on a config it does
// not honour and looks exactly like a working install while the operator gets no clue.
//
// They enter at buildLiveStack for the same reason the ordering test does: the seam is the gate.

// A retired `devices:` section must stop the process, because the alternative is silence. Config
// load is a plain yaml.Unmarshal, which drops unknown keys without a word — so this operator's
// `/run/mux/usbmuxd` would be discarded, quince would start on the DEFAULT /var/run/usbmuxd, and
// every device would vanish with nothing to read.
func TestBuildLiveStackRefusesTheRetiredDevicesSection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	yaml := "" +
		"devices:\n" +
		"    manage_muxer: false\n" +
		"    usbmuxd_socket: /run/mux/usbmuxd\n" +
		"    netmuxd_addr: /run/mux/usbmuxd\n"
	_, err := buildLiveStack(ctx, config.Bootstrap{Data: t.TempDir()},
		configServiceFromYAML(t, t.TempDir(), yaml), testStore(t), bus.New(), quietLog(), scanDeferred)
	if err == nil {
		t.Fatal("buildLiveStack accepted a retired devices: section — the operator's muxer address " +
			"would be dropped in silence and every device would disappear")
	}
	// THE REFUSAL MUST BE ACTIONABLE, not merely loud (quince#940): it names the retired key, the
	// list that replaces it, and the operator's OWN address, so the fix can be copied out of it.
	for _, want := range []string{"devices:", "muxers:", "/run/mux/usbmuxd", "has been changed or discarded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q:\n%v", want, err)
		}
	}
	// ONE ENTRY, NOT TWO. Pointing both old keys at one daemon was the documented way to say "this
	// muxer serves both transports"; emitting it twice would hand the operator a config that
	// Validate then refuses as a duplicate — a remedy that does not work is worse than none.
	if n := strings.Count(err.Error(), "- address:"); n != 1 {
		t.Errorf("refusal offers %d entries for one muxer written twice; want 1:\n%v", n, err)
	}
}

// `type: managed` asks for a profile v0.1 does not ship. It is refused by NAME rather than reported
// as a bad value, because descoped must not reach the operator as malformed.
func TestBuildLiveStackRefusesTheManagedType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	yaml := "" +
		"muxers:\n" +
		"    - address: /run/mux/usbmuxd\n" +
		"      type: managed\n"
	_, err := buildLiveStack(ctx, config.Bootstrap{Data: t.TempDir()},
		configServiceFromYAML(t, t.TempDir(), yaml), testStore(t), bus.New(), quietLog(), scanDeferred)
	if err == nil {
		t.Fatal("buildLiveStack accepted type: managed — quince would supervise nothing and look " +
			"exactly like a working hardened install")
	}
	for _, want := range []string{"muxers[0].type", "managed", "external", "has been changed or discarded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q:\n%v", want, err)
		}
	}
}

// configServiceFromYAML writes a config that is otherwise valid — a real storage, so a refusal
// under test cannot be confused with a storage complaint — plus the raw muxer text under test.
// RAW YAML rather than a marshalled struct, because the retired section is exactly what a struct
// can no longer express, and a hand-written file is how it actually arrives.
func configServiceFromYAML(t *testing.T, root, muxerYAML string) *config.Service {
	t.Helper()
	storage := "" +
		"storage:\n" +
		"    - name: disk\n" +
		"      path: " + root + "\n" +
		"      default: true\n" +
		"      backend: copy\n"
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(storage+muxerYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return config.NewService(path, quietLog())
}

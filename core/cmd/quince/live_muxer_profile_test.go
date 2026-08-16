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

// TestBuildLiveStackRefusesTheManagedProfile pins the CALL, not the check (architect, quince#1059).
//
// config.CheckMuxerProfile has its own test, and it passed while nothing invoked it: deleting the
// call from buildLiveStack failed no test in either package. That gap is worse than the untested
// wiring PRs 2 and 3 declared, and the difference is the failure mode. Theirs fails LOUD — a muxer
// client that is never built means no device ever appears. This one fails SILENT: quince starts
// happily on a config asking for the in-container profile, supervises nothing, and looks exactly
// like a working hardened install while the operator gets no refusal and no clue.
//
// It enters at buildLiveStack for the same reason the ordering test does: the seam is the gate.
func TestBuildLiveStackRefusesTheManagedProfile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := buildLiveStack(ctx, config.Bootstrap{Data: t.TempDir()},
		managedMuxerConfigService(t, t.TempDir()), testStore(t), bus.New(), quietLog(), scanDeferred)
	if err == nil {
		t.Fatal("buildLiveStack accepted manage_muxer: true — quince would start supervising " +
			"nothing and look exactly like a working hardened install")
	}
	// The refusal an operator reads must survive the trip through this seam intact, so the
	// assertion is on the words rather than merely on non-nil.
	for _, want := range []string{"manage_muxer", "usbmuxd_socket", "has been changed or discarded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q:\n%v", want, err)
		}
	}
}

// managedMuxerConfigService writes a config that is otherwise valid — a real storage, so the
// refusal under test cannot be confused with a storage complaint — and asks for the descoped
// in-container profile.
func managedMuxerConfigService(t *testing.T, root string) *config.Service {
	t.Helper()
	cfg := config.Default()
	entries := []config.StorageEntry{
		config.StorageEntry{Name: "disk", Path: root, Default: true, Backend: "copy"}.Resolved(),
	}
	cfg.Storage = &entries
	cfg.Devices.ManageMuxer = true

	data, err := config.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return config.NewService(path, quietLog())
}

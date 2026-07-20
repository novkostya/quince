package storage

import (
	"context"
	"testing"
)

// Story 1: auto-selection probe + explicit honoring.

func TestSelectExplicitBackends(t *testing.T) {
	for _, name := range []string{BackendReflink, BackendHardlink, BackendCopy} {
		be, chosen, reason := Select(context.Background(), Options{
			Backend: name, Backups: t.TempDir(), AppVersion: "test",
		}, testLogger())
		if chosen != name || be.Name() != name {
			t.Fatalf("explicit %q → chosen %q backend %q (reason %q)", name, chosen, be.Name(), reason)
		}
	}
}

func TestSelectZFSIntent(t *testing.T) {
	// Explicit storage.backend: zfs.
	_, chosen, _ := Select(context.Background(), Options{Backend: BackendZFS, Backups: t.TempDir()}, testLogger())
	if chosen != BackendZFS {
		t.Fatalf("backend zfs → %q", chosen)
	}
	// auto + a parent dataset configured → zfs.
	_, chosen, _ = Select(context.Background(), Options{
		Backend: "auto", Backups: t.TempDir(), ZFSParent: "tank/x",
	}, testLogger())
	if chosen != BackendZFS {
		t.Fatalf("auto+parent → %q, want zfs", chosen)
	}
}

func TestSelectAutoNamespaceProbes(t *testing.T) {
	_, chosen, reason := Select(context.Background(), Options{Backend: "auto", Backups: t.TempDir()}, testLogger())
	switch chosen {
	case BackendReflink, BackendHardlink, BackendCopy:
		t.Logf("auto probe selected %q on the test filesystem (%s)", chosen, reason)
	default:
		t.Fatalf("auto probe returned a non-namespace backend %q", chosen)
	}
}

func TestHardlinkProbe(t *testing.T) {
	// Temp dirs support hardlinks on every CI filesystem quince runs on.
	if !hardlinkProbe(t.TempDir()) {
		t.Skip("test filesystem does not support hardlinks — hardlink backend proof runs on the lab host")
	}
}

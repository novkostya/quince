//go:build lab

// Lab gate 12 harness (spec docs/specs/qn.5, gate 12) — NOT compiled or run in CI (build tag
// `lab`). qn.5 ships no cmd/CLI (that is qn.4), so the deployed `quince serve` reconciles and
// serves reads/deletes but never COMMITS — this harness is the spec's "test harness" that drives
// the REAL zfs backend against a real rpool to prove the storage goal end-to-end on hardware.
//
// It hardcodes NO infrastructure (privacy rule) — everything comes from env. Produce an encrypted
// backup into the device's working/ first (idevicebackup2 backup, phone unlocked + passcode), set
// up the constrained hook per deploy/storage.md, then run in the toolchain container with the
// dataset parent bound at /backups and the hook key reachable, e.g.:
//
//	nerdctl run --rm -v /root/quince:/src -w /src/core \
//	  -v <dataset-parent-mount>:/backups -v <hook-key-dir>:/data/keys \
//	  -v quince-go-build:/root/.cache/go-build -v quince-go-mod:/go/pkg/mod -e CGO_ENABLED=1 \
//	  -e QUINCE_LAB_UDID=<udid> \
//	  -e QUINCE_LAB_ZFS_PARENT=<pool/parent> \
//	  -e QUINCE_LAB_ZFS_HOOK="ssh -i /data/keys/zfs -o BatchMode=yes -o UserKnownHostsFile=/data/keys/known_hosts -o StrictHostKeyChecking=accept-new <user>@<host>" \
//	  # the two host-key options are load-bearing: BatchMode disables the accept-key prompt, so a
//	  # container with an empty known_hosts REFUSES every hook call. See deploy/storage.md.
//	  -e QUINCE_LAB_ZFS_SEED=auto \
//	  quince-toolchain-go:local go test -tags lab ./internal/storage/ -run TestLabGate12 -v
//
// The harness commits the working/<udid> tree via the qn.5b atomic exchange, asserts the snapshot +
// encrypted-Verify + that latest/ holds the committed version + marker, exercises Reset, and prints
// the paths + the qn.5b observer procedures for the manual legs (the G-snapshot / G-rclone /
// G-exchange-live legs run DURING a real backup; iMazing opens browse_root; syncoid replicates the
// dataset). The destructive hardlink-safety matrix (gate 12c) is deferred past the freeze.
package storage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
)

// TestLabReflinkProbe runs ONLY quince's clonetree reflink (the seed clone's operative op) between
// two env-given paths — for the confirmation-ladder: run it INSIDE an OCI container with an
// rpool-backed path bound, and measure pool-level bcloneused/ALLOC from the host to prove the
// OCI + bind chain preserves block sharing. QUINCE_LAB_RF_SNAP (optional) checks EXDEV-from-
// snapshot at this layer.
func TestLabReflinkProbe(t *testing.T) {
	src := labEnv(t, "QUINCE_LAB_RF_SRC")
	dst := labEnv(t, "QUINCE_LAB_RF_DST")
	if err := clonetree.Clone(dst, src, clonetree.Reflink); err != nil {
		t.Fatalf("in-layer reflink %s -> %s FAILED: %v", src, dst, err)
	}
	t.Logf("in-layer reflink OK: %s -> %s", src, dst)
	if snap := os.Getenv("QUINCE_LAB_RF_SNAP"); snap != "" {
		err := clonetree.Clone(dst+".fromsnap", snap, clonetree.Reflink)
		t.Logf("reflink-from-snapshot at this layer: err=%v (EXDEV expected → working/ fallback justified)", err)
	}
}

func labEnv(t *testing.T, key string) string {
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("lab gate 12 requires %s (set the real-zfs env — see this file's header)", key)
	}
	return v
}

func TestLabGate12(t *testing.T) {
	backups := os.Getenv("QUINCE_LAB_BACKUPS")
	if backups == "" {
		backups = "/backups"
	}
	udid := labEnv(t, "QUINCE_LAB_UDID")
	parent := labEnv(t, "QUINCE_LAB_ZFS_PARENT")

	log := testLogger()
	backend, name, reason := Select(context.Background(), Options{
		Backend: BackendZFS, Backups: backups, AppVersion: "lab",
		// STILL SPLIT ON WHITESPACE HERE, AND ONLY HERE (quince#818). The product composes its argv
		// from structured fields now, but this gate takes a whole transport from ONE env var so a
		// lab operator can point it at any shim. `strings.Fields` is right for that and wrong for
		// config, which is the distinction the rung drew.
		ZFSParent: parent, ZFSHookArgv: strings.Fields(os.Getenv("QUINCE_LAB_ZFS_HOOK")),
		ZFSSeed: os.Getenv("QUINCE_LAB_ZFS_SEED"),
	}, log)
	if name != BackendZFS {
		t.Fatalf("expected zfs backend, got %q (%s)", name, reason)
	}
	t.Logf("backend=%s reason=%s", name, reason)

	st, err := store.Open(t.TempDir() + "/lab.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	// RETENTION IS PER-SLOT, not per-Manager (quince#473): `storage:` flattened into a list and a
	// list has nowhere to put a global, so the parameter this call used to pass went away with it.
	// The values are unchanged — this is where they live now.
	m := NewManager([]Slot{{
		Name: "lab", Root: backups, Backend: backend, BackendName: name, Reachable: true,
		Retention: RetentionPolicy{KeepRecent: 10, KeepDaily: 30, KeepWeekly: 12},
	}}, st, st, bus.New(), id.New, log)

	// (a) Provision + commit an encrypted tree in working/. If QUINCE_LAB_TREE points at a
	// pre-produced idevicebackup2 tree, seed working/ from it (reflink → copy fallback) — this
	// also exercises clonetree reflink on real data; otherwise working/ must already hold one.
	if _, err := m.Seed(udid, "labgate"); err != nil {
		t.Fatalf("provision/seed: %v", err)
	}
	working := workingTree(backups, udid)
	if tree := os.Getenv("QUINCE_LAB_TREE"); tree != "" && isEmptyDir(working) {
		if err := clonetree.Clone(working, tree, clonetree.Reflink); err != nil {
			t.Logf("reflink seed failed (%v) — falling back to copy", err)
			if err := clonetree.Clone(working, tree, clonetree.Copy); err != nil {
				t.Fatalf("seed working from %s: %v", tree, err)
			}
			t.Logf("seeded working/<udid> from %s via COPY", tree)
		} else {
			t.Logf("seeded working/<udid> from %s via REFLINK", tree)
		}
	}
	if isEmptyDir(working) {
		t.Fatalf("working/<udid> is empty — set QUINCE_LAB_TREE or produce a backup into %s first", working)
	}
	v, err := m.CommitJob(udid, "labgate")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if v.Backend != BackendZFS || v.ZFSSnapshot == nil {
		t.Fatalf("committed version not a zfs snapshot: %+v", v)
	}
	if !v.Encrypted {
		t.Fatalf("expected the encrypted Verify path on the real device tree (A1), got encrypted=false")
	}
	if v.StructureVerifiedAt == nil {
		t.Fatal("structure_verified_at not set")
	}
	t.Logf("committed version %s snapshot=%s kind=%s encrypted=%v", v.ID, *v.ZFSSnapshot, v.Kind, v.Encrypted)
	t.Logf("browse_root (point iMazing here): %s", v.BrowseRoot)

	// latest/ IS the committed version (the exchange moved the verified tree in) + carries the marker.
	lm, err := ReadMarker(latestDir(backups, udid))
	if err != nil || lm.VersionID != v.ID {
		t.Fatalf("latest/ does not hold the committed version: marker=%q err=%v", lm.VersionID, err)
	}
	t.Logf("latest/ holds the committed version at %s", latestDir(backups, udid))

	// (a') Reset discards any dirty working; idempotent (no-op when already clean). The next backup
	// re-seeds from latest/.
	if err := m.RepairWorkingCopy(udid); err != nil {
		t.Fatalf("reset-working: %v", err)
	}
	t.Log("reset-working (discard dirty working) OK")

	// (b/c) Manual legs — print what the operator drives by hand.
	// qn.5b gate — the two observers, run DURING a real backup (idevicebackup2 into working/<udid>):
	t.Logf("MANUAL (qn.5b G-snapshot): while a backup runs AND at the commit instant, loop "+
		"`zfs snapshot %s/%s@probe-$(date +%%s%%N)`; EVERY probe snapshot must contain a COMPLETE "+
		"latest/ (never none) — the two-rename swap failed exactly here.", parent, udid)
	t.Logf("MANUAL (qn.5b G-rclone): run a continuous `rclone sync` of the parent (filter below) " +
		"across many commits; the remote latest/ is NEVER deleted and NEVER torn (diff vs a known-good latest/).")
	t.Logf("MANUAL (qn.5b G-exchange-live): before trusting the in-container exchange, run `exch` " +
		"(util-linux-extra) on two non-empty dirs INSIDE the deployed container on this dataset — settle it in the layer it runs in.")
	t.Logf("MANUAL (b) syncoid: replicate %s/%s mid-write; every @quince-* + latest/ must arrive intact", parent, udid)
	t.Logf("MANUAL offsite (D5a): rclone sync the parent with the anchored filter:")
	for _, r := range AnchoredFilterRules(lastPathSegment(parent)) {
		t.Logf("    --filter %q", r)
	}
	t.Log("MANUAL (c) destructive hardlink-safety matrix (gate 12c): PASSED on hardware 2026-08-10 " +
		"(quince#518) for full->incremental on two devices, with clonetree's class list disabled — " +
		"the committed tree came back byte-identical and the seed downgrade is retired. STILL " +
		"UNCOVERED and worth running: big-file, delete, rename, INTERRUPTED+NEXT, iOS upgrade, " +
		"encryption change. Re-run the whole matrix whenever LIBIMOBILEDEVICE_REF moves — the " +
		"safety property belongs to the writer, not to quince.")
}

func lastPathSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

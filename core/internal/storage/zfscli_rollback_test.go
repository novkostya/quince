package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// qn.6h gate G4 — the ABANDON verb, asserted on argv rather than on a call count.
//
// A count passes on a rollback with the wrong target, and the wrong target here is a committed
// version. What must hold is narrower than "it called rollback": exactly one argument, the
// dataset@snapshot, and NEVER -r or -R — those are what destroy newer snapshots. The forced-command
// helper discards flags independently (measured 2026-08-08 on a real pool: `rollback -r <snap>`
// reached zfs as a plain rollback and the newer snapshot survived), so this pins quince's half of a
// guard that exists on both sides.

func fakeRollbackCLI(out string, err error) (*zfsCLI, *[][]string) {
	calls := &[][]string{}
	c := newZFSCLI("rpool/quince-labtest", []string{"ssh", "-i", "/k", "host"})
	c.run = func(_ context.Context, argv []string) (string, error) {
		*calls = append(*calls, argv)
		return out, err
	}
	return c, calls
}

func TestZFSRollbackSendsTargetOnlyAndNeverForces(t *testing.T) {
	c, calls := fakeRollbackCLI("", nil)

	if err := c.Rollback(context.Background(), "AAAABBBBCCCC", "quince-2026-08-08-0000"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("issued %d command(s), want exactly 1 — a retry against the same mount buys nothing", len(*calls))
	}

	argv := (*calls)[0]
	want := "rpool/quince-labtest/AAAABBBBCCCC@quince-2026-08-08-0000"
	if argv[len(argv)-1] != want {
		t.Errorf("target = %q, want %q", argv[len(argv)-1], want)
	}
	for _, a := range argv {
		if a == "-r" || a == "-R" || a == "-f" {
			t.Fatalf("argv carries %q — -r/-R destroy NEWER snapshots, i.e. committed versions: %v", a, argv)
		}
	}
	// The verb reaches the helper with the target and nothing else: hook argv is
	// <hookCmd...> rollback <target>, so exactly one argument follows the verb.
	verb := -1
	for i, a := range argv {
		if a == "rollback" {
			verb = i
			break
		}
	}
	if verb < 0 {
		t.Fatalf("no rollback verb in argv: %v", argv)
	}
	if got := len(argv) - verb - 1; got != 1 {
		t.Errorf("%d argument(s) after the verb, want exactly 1 — every extra is a flag the helper would discard anyway: %v", got, argv)
	}
}

// The answer-C refusal is the EXPECTED failure on any host running a snapshotter, and its text is
// what the caller has to show the operator — so it must survive verbatim rather than be flattened.
func TestZFSRollbackSurfacesTheNewerSnapshotRefusalVerbatim(t *testing.T) {
	const refusal = "cannot rollback to 'rpool/quince-labtest/AAAABBBBCCCC@quince-2026-08-08-0000': " +
		"more recent snapshots or bookmarks exist\nuse '-r' to force deletion of the following snapshots and bookmarks:\n" +
		"rpool/quince-labtest/AAAABBBBCCCC@quince-2026-08-08-0001"

	c, _ := fakeRollbackCLI(refusal, errors.New("exit status 1"))

	err := c.Rollback(context.Background(), "AAAABBBBCCCC", "quince-2026-08-08-0000")
	if err == nil {
		t.Fatal("rollback returned nil on a refusal — a reset that reports success having changed nothing is the state-honesty failure this rung is most likely to ship")
	}
	if !strings.Contains(err.Error(), "more recent snapshots or bookmarks exist") {
		t.Errorf("refusal text lost; got %q", err)
	}
}

func TestZFSRollbackRejectsMalformedNames(t *testing.T) {
	c, calls := fakeRollbackCLI("", nil)

	// A snapshot short name outside the quince pattern must never reach the host: the helper would
	// refuse it, and quince should not be issuing it in the first place.
	if err := c.Rollback(context.Background(), "AAAABBBBCCCC", "zfs-auto-snap_frequent-2026-08-08-0345"); err == nil {
		t.Error("accepted a foreign snapshot name — those are not quince's to roll back to")
	}
	if err := c.Rollback(context.Background(), "../escape", "quince-2026-08-08-0000"); err == nil {
		t.Error("accepted a dataset name outside the parent")
	}
	if len(*calls) != 0 {
		t.Errorf("issued %d command(s) for rejected input, want 0 — validation must refuse before the host is touched", len(*calls))
	}
}

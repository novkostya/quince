package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// quince#585 — capacity on zfs is read from zfs, not from `statfs`.
//
// The defect these pin came from the staging stand: `statfs` on the parent dataset reported
// `used = 256 K` against SEVENTEEN backups, because the backups live in per-device CHILD datasets
// the parent's statfs cannot see. The card rendered "431.4 GB free of 431.4 GB" for a storage that
// was far from empty. zfs `used` on a parent already includes descendants, so one call measures the
// quantity gap A had already ruled the field to mean.

func fakeCapacityCLI(out string, err error) (*zfsCLI, *[][]string) {
	calls := &[][]string{}
	c := newZFSCLI("rpool/quince-labtest", "hook", "ssh -i /k host", "")
	c.run = func(_ context.Context, argv []string) (string, error) {
		*calls = append(*calls, argv)
		return out, err
	}
	return c, calls
}

func TestZFSCapacityReadsUsedPlusAvailable(t *testing.T) {
	// The staging shape in bytes: children holding ~3.7G, with ~399.4G available.
	c, calls := fakeCapacityCLI("3972103372\t428871450624\n", nil)

	free, total, err := c.Capacity(context.Background())
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if free != 428871450624 {
		t.Errorf("free = %d, want `available` verbatim", free)
	}
	if want := uint64(3972103372 + 428871450624); total != want {
		t.Errorf("total = %d, want used+available = %d — the sum is exactly what statfs could not see",
			total, want)
	}

	// IN HOOK MODE THE ARGV IS THE BARE VERB, and no caller argument reaches `zfs` at all
	// (Operator ruling 2026-08-03, quince#600). The helper uses its OWN configured $PARENT. The
	// flags this used to send — `-H -p -o used,available <parent>` — belong to the helper's arm
	// now, and sending them WAS the defect: a forced command discards them.
	argv := (*calls)[0]
	if got := strings.Join(argv, " "); got != "ssh -i /k host capacity" {
		t.Errorf("hook argv = %q, want the bare `capacity` verb with no arguments", got)
	}
	// It runs through the configured HOOK like every other zfs op, never a direct `zfs` binary.
	if argv[0] != "ssh" {
		t.Errorf("capacity must go through the hook, got argv[0]=%q", argv[0])
	}
}

// EXEC MODE KEEPS THE DIRECT CALL, because no forced command is in the way — and `-p` is
// load-bearing there: without it zfs prints human units ("399G") and this would be parsing prose.
func TestZFSCapacityExecModeCallsZFSDirectly(t *testing.T) {
	calls := &[][]string{}
	c := newZFSCLI("rpool/quince-labtest", "exec", "", "")
	c.run = func(_ context.Context, argv []string) (string, error) {
		*calls = append(*calls, argv)
		return "3972103372\t428871450624\n", nil
	}
	if _, _, err := c.Capacity(context.Background()); err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if argv := strings.Join((*calls)[0], " "); argv != "zfs list -H -p -o used,available rpool/quince-labtest" {
		t.Errorf("exec argv = %q, want the direct flagged list", argv)
	}
}

// THE FIXTURE THE RULING ASKED FOR: a stub that behaves like the FORCED COMMAND rather than like a
// pass-through to `zfs`. This is the durable half of quince#600.
//
// Every assertion in the original tests was correct and every one passed — argv contains `-p`,
// contains `used,available`, goes through the hook — because the stub returned whatever the test
// chose regardless of what was ASKED. It was A MIRROR, NOT A PEER, so it could not express "the
// real helper ignores these flags and answers something else", and no gate could have caught the
// defect that shipped.
//
// This models the deployed helper's actual contract, measured on the staging stand: it dispatches
// on the VERB, discards every argument after it, and refuses a verb it does not know. Point it at
// the same arms `deploy/storage.md` documents, so the fixture and the helper cannot drift without
// one of them failing.
func forcedCommandHook(t *testing.T, arms map[string]string) *zfsCLI {
	t.Helper()
	c := newZFSCLI("rpool/quince-labtest", "hook", "ssh -i /k host", "")
	c.run = func(_ context.Context, argv []string) (string, error) {
		// argv is `ssh -i /k host <verb> [args…]`; the helper sees what follows the ssh target and
		// dispatches on the FIRST TOKEN ONLY.
		verb := ""
		if len(argv) > 4 {
			verb = argv[4]
		}
		out, ok := arms[verb]
		if !ok {
			return "quince-zfs-helper: refused: " + verb, errors.New("exit 1")
		}
		return out, nil
	}
	return c
}

// The regression proper: a helper WITHOUT a `capacity` arm must fail, and fail as a refusal rather
// than by silently answering something else. Before the fix this call went to `list` and came back
// exit 0 carrying a snapshot listing.
func TestZFSCapacityAgainstAHelperThatLacksTheVerb(t *testing.T) {
	c := forcedCommandHook(t, map[string]string{
		// The pre-quince#600 helper: `list` exists and answers with snapshots whatever flags it is
		// handed. This is what the staging stand actually returned, at exit 0.
		"list": "rpool/quince-labtest/UDID@quince-2026-07-22T17-46-01KY5EZAMTG0QBXEV75DG2JNPK\n" +
			"rpool/quince-labtest/UDID@quince-2026-07-23T21-09-01KY8D0DZH1C8XF91F9XVGY53X\n",
	})
	free, total, err := c.Capacity(context.Background())
	if err == nil {
		t.Fatalf("a helper with no `capacity` arm must ERROR, got free=%d total=%d", free, total)
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("the error should carry the helper's own refusal, got %v", err)
	}
	// NEVER ZEROES. gap A ruled capacity `null` rather than `0`, because a zero renders a full disk.
	if free != 0 || total != 0 {
		t.Errorf("a failed capacity returns zero VALUES beside a non-nil error, got %d/%d", free, total)
	}
}

// And the fix, through the same fixture: a helper that HAS the arm answers the two fields — and
// `list` is present so this cannot pass by accidentally reaching it.
func TestZFSCapacityAgainstAHelperWithTheVerb(t *testing.T) {
	c := forcedCommandHook(t, map[string]string{
		"capacity": "3972103372\t428871450624\n",
		"list":     "…snapshots…",
	})
	free, total, err := c.Capacity(context.Background())
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if free != 428871450624 || total != 3972103372+428871450624 {
		t.Errorf("free/total = %d/%d, want available and used+available", free, total)
	}
}

// A hook failure must ERROR rather than return zeroes. quince#585's ruling points the failure at
// gap A's already-ruled answer — capacity `null`, never `0` — and a silent zero renders a FULL DISK.
func TestZFSCapacityErrorsRatherThanReturningZero(t *testing.T) {
	c, _ := fakeCapacityCLI("ssh: connect to host port 22: Connection refused", errors.New("exit 255"))

	free, total, err := c.Capacity(context.Background())
	if err == nil {
		t.Fatal("a failed hook must error — a silent zero renders as a full disk")
	}
	if free != 0 || total != 0 {
		t.Errorf("on error both values must be ignorable, got free=%d total=%d", free, total)
	}
	if !strings.Contains(err.Error(), "rpool/quince-labtest") {
		t.Errorf("the error must name the dataset it asked about: %v", err)
	}
}

// Malformed output is an error, not a guess.
func TestZFSCapacityRejectsMalformedOutput(t *testing.T) {
	for _, tc := range []struct{ name, out string }{
		{"one field", "3972103372\n"},
		{"three fields", "1 2 3\n"},
		{"empty", "\n"},
		{"human units — the -p regression", "3.70G\t399G\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := fakeCapacityCLI(tc.out, nil)
			if _, _, err := c.Capacity(context.Background()); err == nil {
				t.Errorf("want an error for %q, got none", tc.out)
			}
		})
	}
}

// The dataset name is validated before it reaches an argv, like every other op here (design §6).
func TestZFSCapacityRejectsAnInvalidParent(t *testing.T) {
	c, calls := fakeCapacityCLI("1\t2\n", nil)
	c.parent = "rpool/quince; rm -rf /"
	if _, _, err := c.Capacity(context.Background()); err == nil {
		t.Fatal("an invalid dataset name must be refused before it reaches an argv")
	}
	if len(*calls) != 0 {
		t.Errorf("the hook must not run for an invalid dataset; it ran %d time(s)", len(*calls))
	}
}

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

	// `-p` is LOAD-BEARING: without it zfs prints human units ("399G") and this would be parsing
	// prose. Asserted on the argv, because dropping it still parses right up until it does not.
	argv := strings.Join((*calls)[0], " ")
	for _, want := range []string{"-p", "used,available", "rpool/quince-labtest"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv missing %q: %s", want, argv)
		}
	}
	// It runs through the configured HOOK like every other zfs op, never a direct `zfs` binary.
	if (*calls)[0][0] != "ssh" {
		t.Errorf("capacity must go through the hook, got argv[0]=%q", (*calls)[0][0])
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

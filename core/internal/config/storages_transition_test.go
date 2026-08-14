package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE STORAGE REQUIREMENT IS A TRANSITION ON THE WRITE PATH — Operator ruling 2026-08-14, relayed on
// quince#908, filed as a gap by quince#935.
//
// `replaceLocked` refuses a write that REDUCES the declared count to zero, and permits one that
// leaves an already-zero document at zero. The four cases are asserted together because the property
// is about the PAIR of documents, and a test that names only the new one is how the guard came to
// enforce something narrower than its comment claimed for a rung and a half.
func TestTheStorageRequirementRefusesATransitionNotAState(t *testing.T) {
	one := []StorageEntry{{Name: "local", Path: "/backups", Default: true}}
	two := []StorageEntry{
		{Name: "local", Path: "/backups", Default: true},
		{Name: "spare", Path: "/spare"},
	}

	for _, tc := range []struct {
		name    string
		seed    *[]StorageEntry // nil → never write a seed, so the service stays at its zero value
		write   Config
		refused bool
	}{
		{
			// THE CASE THE GUARD WAS WRITTEN FOR, and it must not soften. The UI is the editing
			// surface (D12), so removing the last storage is two clicks, and a 200 would disable
			// backups in silence and leave a file the daemon refuses to start on.
			name: "1 → 0 is refused", seed: &one, write: withStorages(), refused: true,
		},
		{
			// SAME MOVE, EXPRESSED AS AN ABSENT KEY RATHER THAN AN EMPTY LIST. `Missing` and `Empty`
			// are different sentences to a user and the same transition to this check, so both
			// directions of the removal have to be covered or half the guard can rot.
			name: "1 → absent is refused", seed: &one, write: Default(), refused: true,
		},
		{
			// THE CASE THE RULING CHANGES. It records a state quince is already running in — qn.6e
			// ruled a zero-storage start IS the onboarding state — so it creates no unstartable file.
			name: "0 → 0 succeeds", seed: nil, write: withStorages(), refused: false,
		},
		{
			name: "absent → absent succeeds", seed: nil, write: Default(), refused: false,
		},
		{
			name: "0 → 1 is unaffected", seed: nil, write: withStorages(one...), refused: false,
		},
		{
			name: "1 → 1 is unaffected", seed: &one, write: withStorages(one...), refused: false,
		},
		{
			// 2 → 1 IS NOT A REDUCTION TO ZERO, so it is not this check's business. Worth asserting:
			// "refuse a write that reduces the count" without the "to zero" is a plausible
			// misreading, and it would make removing a spare disk impossible.
			name: "2 → 1 is unaffected", seed: &two, write: withStorages(one...), refused: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))

			if tc.seed != nil {
				if errs, _, err := svc.Replace(withStorages(*tc.seed...), "test"); err != nil || len(errs) > 0 {
					t.Fatalf("seed: errs=%+v err=%v", errs, err)
				}
			}

			errs, _, err := svc.Replace(tc.write, "test")
			if err != nil {
				t.Fatalf("Replace: %v", err)
			}

			switch {
			case tc.refused && len(errs) == 0:
				t.Fatalf("the write was accepted; removing the last storage must still be a 422")
			case tc.refused:
				if errs[0].Path != "storage" {
					t.Errorf("refusal is at %q, want `storage`", errs[0].Path)
				}
			case len(errs) > 0:
				t.Fatalf("the write was refused: %+v", errs)
			}
		})
	}
}

// A REFUSED WRITE STILL WRITES NOTHING. The transition change moved a condition, not the guarantee
// underneath it, and this is the half that would be silently expensive to lose: a partial write on
// the path that refuses is worse than the acceptance the refusal exists to prevent.
func TestARefusedTransitionLeavesTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if errs, _, err := svc.Replace(withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true}), "test"); err != nil || len(errs) > 0 {
		t.Fatalf("seed: errs=%+v err=%v", errs, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded file: %v", err)
	}

	if errs, _, err := svc.Replace(withStorages(), "test"); err != nil || len(errs) == 0 {
		t.Fatalf("precondition: 1 → 0 must be refused; errs=%+v err=%v", errs, err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after refusal: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("a refused write changed the file on disk\n before %q\n after  %q", before, after)
	}
	// AND THE LIVE DOCUMENT IS UNCHANGED TOO. A refusal that wrote nothing but swapped `s.cfg` would
	// leave the running process disagreeing with its own file until a restart — quince#754's defect,
	// which was found exactly this way.
	if declaredStorage(svc.Current()) != 1 {
		t.Errorf("the refused write reached the live snapshot: %d storages, want 1",
			declaredStorage(svc.Current()))
	}
}

// THE `Missing` VERSUS `Empty` DISTINCTION SURVIVES IN THE MESSAGE. They reach the user as different
// sentences today, and the ruling says so explicitly — an absent `storage:` key and an empty list are
// the same transition and different mistakes, and the one extra clause is what tells somebody they
// wrote `storage: []` rather than forgetting the block.
func TestTheRefusalStillDistinguishesAbsentFromEmpty(t *testing.T) {
	one := StorageEntry{Name: "local", Path: "/backups", Default: true}

	for _, tc := range []struct {
		name       string
		write      Config
		wantPrefix bool // "the storage list is empty — "
	}{
		{"an empty list says so", withStorages(), true},
		{"an absent key does not", Default(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(filepath.Join(t.TempDir(), "config.yml"), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if errs, _, err := svc.Replace(withStorages(one), "test"); err != nil || len(errs) > 0 {
				t.Fatalf("seed: errs=%+v err=%v", errs, err)
			}
			errs, _, err := svc.Replace(tc.write, "test")
			if err != nil || len(errs) != 1 {
				t.Fatalf("want one refusal, got errs=%+v err=%v", errs, err)
			}
			got := strings.HasPrefix(errs[0].Message, "the storage list is empty")
			if got != tc.wantPrefix {
				t.Errorf("message = %q; empty-prefix = %v, want %v", errs[0].Message, got, tc.wantPrefix)
			}
		})
	}
}

// THE STARTUP REFUSAL IS UNCHANGED, AND THIS TEST EXISTS SO A LATER TIDY-UP CANNOT COLLAPSE THE TWO
// CALLERS (part of the ruling, not an extra). `CheckStorages` answers *may this daemon SERVE?* and
// has no previous document to compare against; folding the transition into the predicate would
// silently delete the refusal qn.6e and quince#508 rest on, and every test above would still pass.
func TestCheckStoragesStaysAStaticPredicate(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"an absent storage key", Default()},
		{"an empty storage list", withStorages()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if req := CheckStorages(tc.cfg, nil, nil); req.OK() {
				t.Errorf("CheckStorages permitted a storageless document — the daemon would boot on "+
					"defaults with no storage and no error, which is the gap 3 refusal (%+v)", req)
			}
		})
	}
	if req := CheckStorages(withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true}), nil, nil); !req.OK() {
		t.Errorf("CheckStorages refused a document that declares a storage: %+v", req)
	}
}

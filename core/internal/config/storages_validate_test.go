package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

func errPaths(errs []wire.ConfigError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Path)
	}
	return out
}

func TestValidateAcceptsAWellFormedStorageList(t *testing.T) {
	c := withStorages(
		StorageEntry{Name: "pool", Path: "/backups", Default: true},
		StorageEntry{Name: "usb", Path: "/mnt/usb"},
	)
	if errs := Validate(c); len(errs) != 0 {
		t.Errorf("well-formed list must validate, got %+v", errs)
	}
}

// Absent and empty are NOT validation errors — they are the refusal, which lives on the serve
// path. Routing them through Validate would make Load() discard the config and fall back to
// defaults with OK:false, i.e. a daemon running with no storage and no error. That is the silent
// zero-storage start the ruling forbids, so this test pins the boundary between the two checks.
func TestValidateDoesNotReportAbsentOrEmptyStorages(t *testing.T) {
	if errs := Validate(Default()); len(errs) != 0 {
		t.Errorf("absent storages is the REFUSAL's business, not Validate's; got %+v", errs)
	}
	if errs := Validate(withStorages()); len(errs) != 0 {
		t.Errorf("empty storages is the REFUSAL's business, not Validate's; got %+v", errs)
	}
}

// The other half of the pair above, and the reason both exist: Replace must REFUSE what Validate
// PERMITS. The two paths differ in the property the exclusion rests on — Load discards a config
// that fails Validate and runs on defaults, so an error there is dangerous; Replace returns the
// errors and writes nothing, so an error there is exactly right. Conflating them once already
// shipped a PUT that answered 200 to a config that could not start (quince#394 review).
func TestReplaceRefusesWhatValidatePermits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// SEEDED, BECAUSE THE REFUSAL IS NOW A TRANSITION (Operator ruling 2026-08-14, quince#908, filed
	// as a gap by quince#935). This test's claim — *Replace refuses what Validate permits* — is
	// unchanged and still true. What changed is that the refusal is a REMOVAL, so it needs a previous
	// document holding a storage. Unseeded, these two cases are 0 → 0, which the ruling permits, and
	// the test would assert the behaviour that was corrected rather than the one that survives.
	if errs, _, err := svc.Replace(withStorages(StorageEntry{Name: "seed", Path: "/backups", Default: true})); err != nil || len(errs) > 0 {
		t.Fatalf("seed: errs=%+v err=%v", errs, err)
	}
	seeded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the seed must have written a file: %v", err)
	}

	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"absent", Default()},
		{"empty", withStorages()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := Validate(tc.cfg); len(errs) != 0 {
				t.Fatalf("precondition: Validate must still permit this, got %+v", errs)
			}
			errs, _, err := svc.Replace(tc.cfg)
			if err != nil {
				t.Fatalf("Replace: %v", err)
			}
			if len(errs) != 1 || errs[0].Path != "storage" {
				t.Fatalf("removing the last storage must be a 422 at storage:, got %+v", errs)
			}
			// "A REFUSED SAVE WRITES NOTHING" asserted as UNCHANGED rather than as ABSENT. It read
			// `os.Stat(path) == nil → fail`, which only worked while nothing had ever been saved;
			// with a seed on disk the same guarantee is that the bytes did not move, which is also
			// the stronger statement — an absent file cannot catch a partial overwrite.
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("a refused save must leave the file readable: %v", readErr)
			}
			if string(after) != string(seeded) {
				t.Errorf("a refused save changed config.yml\n before %q\n after  %q", seeded, after)
			}
		})
	}
}

func TestReplaceAcceptsAConfigWithAStorage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))

	errs, _, err := svc.Replace(withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true}))
	if err != nil || len(errs) != 0 {
		t.Fatalf("a well-formed config must save: errs=%+v err=%v", errs, err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("an accepted save must write the file: %v", statErr)
	}
}

func TestValidateRequiresExactlyOneDefault(t *testing.T) {
	none := Validate(withStorages(
		StorageEntry{Name: "a", Path: "/a"},
		StorageEntry{Name: "b", Path: "/b"},
	))
	if len(none) != 1 || none[0].Path != "storage" {
		t.Errorf("a list with no default must be rejected, got %+v", none)
	}

	two := Validate(withStorages(
		StorageEntry{Name: "a", Path: "/a", Default: true},
		StorageEntry{Name: "b", Path: "/b", Default: true},
	))
	if len(two) != 1 || two[0].Path != "storage" {
		t.Errorf("a list with two defaults must be rejected, got %+v", two)
	}
}

// An EMPTY NAME IS NO LONGER AN ERROR — it defaults to the path (quince#504, ruled 2026-08-01).
// This test asserted the opposite until the flattening, which is the ruling arriving: entry 0
// below has no name and is accepted, and only the genuinely malformed paths are reported.
//
// The one case where an empty name still surfaces is an entry whose PATH is also empty, and there
// the path's own error is the report. Naming both would be two errors for one mistake.
func TestValidateRejectsEmptyAndRelativeFields(t *testing.T) {
	errs := Validate(withStorages(
		StorageEntry{Name: "", Path: "/a", Default: true},
		StorageEntry{Name: "b", Path: ""},
		StorageEntry{Name: "c", Path: "relative/path"},
	))
	got := strings.Join(errPaths(errs), " ")
	for _, want := range []string{
		"storage[1].path",
		"storage[2].path",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want an error at %s; got %v", want, got)
		}
	}
	if strings.Contains(got, "storage[0]") {
		t.Errorf("an unnamed storage with a good path must be accepted; got %v", got)
	}
}

// Duplicate names collide because the name keys a storage's DB row, which is what tells a new
// storage from an absent medium (design §5). Duplicate paths collide because two storages at one
// path would each claim the other's identity marker.
func TestValidateRejectsDuplicateNamesAndPaths(t *testing.T) {
	dupName := Validate(withStorages(
		StorageEntry{Name: "same", Path: "/a", Default: true},
		StorageEntry{Name: "same", Path: "/b"},
	))
	if len(dupName) != 1 || dupName[0].Path != "storage[1].name" {
		t.Errorf("duplicate names must be rejected, got %+v", dupName)
	}

	// Cleaned before comparison, so /b and /b/ are the same place.
	dupPath := Validate(withStorages(
		StorageEntry{Name: "a", Path: "/b", Default: true},
		StorageEntry{Name: "b", Path: "/b/"},
	))
	if len(dupPath) != 1 || dupPath[0].Path != "storage[1].path" {
		t.Errorf("duplicate paths must be rejected after cleaning, got %+v", dupPath)
	}
}

// The typo guard must reach INSIDE a storages entry. Before qn.6c, unknownKeys recursed only into
// struct fields, so a slice of structs was walked as far as the slice and no further — meaning
// `pathh:` inside an entry was dropped by yaml.Unmarshal and reported by nothing. A mistyped path
// would then read as an omitted one, and the storage would land somewhere the user never named.
func TestUnknownKeysReachesInsideStorageEntries(t *testing.T) {
	raw := []byte(`
storage:
  - name: pool
    pathh: /backups
    default: true
`)
	_, _, warns, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var found bool
	for _, w := range warns {
		if w.Path == "storage[0].pathh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a typo inside a storages entry must warn with its index; got %+v", warns)
	}
}

func TestParseDistinguishesAbsentStoragesFromEmpty(t *testing.T) {
	absent, _, _, err := Parse([]byte("backup:\n  transport: auto\n"))
	if err != nil {
		t.Fatalf("parse absent: %v", err)
	}
	if absent.Storage != nil {
		t.Errorf("an absent storages key must stay nil, got %+v", absent.Storage)
	}

	empty, _, _, err := Parse([]byte("storage: []\n"))
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if empty.Storage == nil || len(*empty.Storage) != 0 {
		t.Errorf("an explicit empty list must be non-nil and empty, got %+v", empty.Storage)
	}
}

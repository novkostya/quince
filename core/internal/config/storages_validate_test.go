package config

import (
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

func TestValidateRequiresExactlyOneDefault(t *testing.T) {
	none := Validate(withStorages(
		StorageEntry{Name: "a", Path: "/a"},
		StorageEntry{Name: "b", Path: "/b"},
	))
	if len(none) != 1 || none[0].Path != "storage.storages" {
		t.Errorf("a list with no default must be rejected, got %+v", none)
	}

	two := Validate(withStorages(
		StorageEntry{Name: "a", Path: "/a", Default: true},
		StorageEntry{Name: "b", Path: "/b", Default: true},
	))
	if len(two) != 1 || two[0].Path != "storage.storages" {
		t.Errorf("a list with two defaults must be rejected, got %+v", two)
	}
}

func TestValidateRejectsEmptyAndRelativeFields(t *testing.T) {
	errs := Validate(withStorages(
		StorageEntry{Name: "", Path: "/a", Default: true},
		StorageEntry{Name: "b", Path: ""},
		StorageEntry{Name: "c", Path: "relative/path"},
	))
	got := strings.Join(errPaths(errs), " ")
	for _, want := range []string{
		"storage.storages[0].name",
		"storage.storages[1].path",
		"storage.storages[2].path",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want an error at %s; got %v", want, got)
		}
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
	if len(dupName) != 1 || dupName[0].Path != "storage.storages[1].name" {
		t.Errorf("duplicate names must be rejected, got %+v", dupName)
	}

	// Cleaned before comparison, so /b and /b/ are the same place.
	dupPath := Validate(withStorages(
		StorageEntry{Name: "a", Path: "/b", Default: true},
		StorageEntry{Name: "b", Path: "/b/"},
	))
	if len(dupPath) != 1 || dupPath[0].Path != "storage.storages[1].path" {
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
  storages:
    - name: pool
      pathh: /backups
      default: true
`)
	_, warns, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var found bool
	for _, w := range warns {
		if w.Path == "storage.storages[0].pathh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a typo inside a storages entry must warn with its index; got %+v", warns)
	}
}

func TestParseDistinguishesAbsentStoragesFromEmpty(t *testing.T) {
	absent, _, err := Parse([]byte("storage:\n  backend: auto\n"))
	if err != nil {
		t.Fatalf("parse absent: %v", err)
	}
	if absent.Storage.Storages != nil {
		t.Errorf("an absent storages key must stay nil, got %+v", absent.Storage.Storages)
	}

	empty, _, err := Parse([]byte("storage:\n  storages: []\n"))
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if empty.Storage.Storages == nil || len(*empty.Storage.Storages) != 0 {
		t.Errorf("an explicit empty list must be non-nil and empty, got %+v", empty.Storage.Storages)
	}
}

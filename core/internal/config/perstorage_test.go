package config

import "testing"

// qn.6c quince#458 — per-storage backend and zfs, so a second storage on different media is
// expressible at all.

func entries(e ...StorageEntry) *[]StorageEntry { return &e }

// The global values are the INHERITED DEFAULT, so a single-storage config is unchanged. That is
// what makes this fixable without touching every deployment's config — of which there is one, but
// the property is what matters.
func TestAnEntryInheritsTheGlobalBackendAndZFS(t *testing.T) {
	c := StorageConfig{
		Backend:  "zfs",
		ZFS:      ZFSConfig{ParentDataset: "rpool/quince", Mode: "hook"},
		Storages: entries(StorageEntry{Name: "local", Path: "/backups", Default: true}),
	}
	e := (*c.Storages)[0]
	if got := c.BackendFor(e); got != "zfs" {
		t.Errorf("an entry with no backend must inherit the global, got %q", got)
	}
	if got := c.ZFSFor(e); got.ParentDataset != "rpool/quince" || got.Mode != "hook" {
		t.Errorf("an entry with no zfs block must inherit the global, got %+v", got)
	}
}

// The case quince#458 was filed for: a USB disk alongside a zfs default. Before this, the global
// `backend: zfs` applied to it and its datasets would have been created under another pool.
func TestAnEntryOverridesTheGlobalBackend(t *testing.T) {
	c := StorageConfig{
		Backend: "zfs",
		ZFS:     ZFSConfig{ParentDataset: "rpool/quince"},
		Storages: entries(
			StorageEntry{Name: "local", Path: "/backups", Default: true},
			StorageEntry{Name: "shuttle", Path: "/backups-usb", Backend: "auto", ZFS: &ZFSConfig{}},
		),
	}
	usb := (*c.Storages)[1]
	if got := c.BackendFor(usb); got != "auto" {
		t.Errorf("the entry's own backend must win, got %q", got)
	}
	// An EXPLICIT EMPTY zfs block is how a storage says "I am not zfs" on a stand whose global
	// block is set. Without the pointer distinction it could never opt out.
	if got := c.ZFSFor(usb); got.ParentDataset != "" || got.HookCmd != "" {
		t.Errorf("`zfs: {}` must mean NOT zfs, got %+v", got)
	}
	// And the default is untouched.
	if got := c.ZFSFor((*c.Storages)[0]); got.ParentDataset != "rpool/quince" {
		t.Errorf("overriding one entry must not disturb another, got %+v", got)
	}
}

// TWO STORAGES THAT ARE ONE STORAGE. A zfs backend creates <parent>/<udid>, so two storages sharing
// a parent dataset would create the same dataset for a device and each believe it owned it — every
// per-storage guarantee in this rung is void beneath that.
func TestTwoZFSStoragesOnOneParentDatasetAreRefused(t *testing.T) {
	c := StorageConfig{
		Backend: "zfs",
		ZFS:     ZFSConfig{ParentDataset: "rpool/quince"},
		Storages: entries(
			StorageEntry{Name: "a", Path: "/backups-a", Default: true},
			StorageEntry{Name: "b", Path: "/backups-b"},
		),
	}
	bad := CheckStorageBackends(c)
	if len(bad) != 1 {
		t.Fatalf("two zfs storages on one parent dataset must be refused, got %v", bad)
	}
	for _, want := range []string{"a", "b", "rpool/quince", "parent_dataset"} {
		if !contains(bad[0], want) {
			t.Errorf("the refusal must name %q so the user can fix it: %q", want, bad[0])
		}
	}
}

// Distinct parents are fine — that is the supported two-zfs-storage shape.
func TestTwoZFSStoragesOnDIFFERENTParentsArePermitted(t *testing.T) {
	c := StorageConfig{
		Backend: "zfs",
		Storages: entries(
			StorageEntry{Name: "a", Path: "/a", Default: true, ZFS: &ZFSConfig{ParentDataset: "rpool/a"}},
			StorageEntry{Name: "b", Path: "/b", ZFS: &ZFSConfig{ParentDataset: "tank/b"}},
		),
	}
	if bad := CheckStorageBackends(c); len(bad) != 0 {
		t.Errorf("distinct parent datasets must be permitted, got %v", bad)
	}
}

// A namespace storage alongside a zfs one needs no guard: each is rooted at its own path, which
// CheckStorages already requires to be distinct.
func TestAZFSStorageBesideANamespaceStorageIsPermitted(t *testing.T) {
	c := StorageConfig{
		Backend: "zfs",
		ZFS:     ZFSConfig{ParentDataset: "rpool/quince"},
		Storages: entries(
			StorageEntry{Name: "local", Path: "/backups", Default: true},
			StorageEntry{Name: "shuttle", Path: "/backups-usb", Backend: "hardlink", ZFS: &ZFSConfig{}},
		),
	}
	if bad := CheckStorageBackends(c); len(bad) != 0 {
		t.Errorf("a USB disk beside a zfs default is the whole point of quince#458, got %v", bad)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// A ZFS BACKEND WITH NO PARENT DATASET is incoherent, and it is what my own first guidance on
// quince#458 produced: `zfs: {}` to opt out, while the global `backend: zfs` still applied. Select
// would build a zfs backend with nothing to create datasets under.
func TestZFSWithNoParentDatasetIsRefused(t *testing.T) {
	c := StorageConfig{
		Backend: "zfs",
		ZFS:     ZFSConfig{ParentDataset: "rpool/quince"},
		Storages: entries(
			StorageEntry{Name: "local", Path: "/backups", Default: true},
			StorageEntry{Name: "shuttle", Path: "/usb", ZFS: &ZFSConfig{}},
		),
	}
	bad := CheckStorageBackends(c)
	if len(bad) != 1 {
		t.Fatalf("`zfs: {}` without a backend override must be refused, got %v", bad)
	}
	// The refusal must name the REMEDY, because the config looks deliberate — the user wrote
	// `zfs: {}` on purpose and needs to know it is not sufficient on its own.
	for _, want := range []string{"shuttle", "backend: auto", "opt OUT"} {
		if !contains(bad[0], want) {
			t.Errorf("the refusal must contain %q: %q", want, bad[0])
		}
	}
}

// And the CORRECT opt-out is accepted: both keys, which is what the docs must show.
func TestTheCorrectOptOutIsAccepted(t *testing.T) {
	c := StorageConfig{
		Backend: "zfs",
		ZFS:     ZFSConfig{ParentDataset: "rpool/quince"},
		Storages: entries(
			StorageEntry{Name: "local", Path: "/backups", Default: true},
			StorageEntry{Name: "shuttle", Path: "/usb", Backend: "auto", ZFS: &ZFSConfig{}},
		),
	}
	if bad := CheckStorageBackends(c); len(bad) != 0 {
		t.Errorf("`backend: auto` + `zfs: {}` is the supported opt-out, got %v", bad)
	}
}

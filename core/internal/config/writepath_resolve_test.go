package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The write path resolves before it validates (quince#754). These are the measurements that opened
// the issue, inverted: each one asserts the behaviour that replaced the defect rather than
// describing it.
//
// They go through Service.Replace rather than through the handler on purpose — Replace is what
// handleConfigPut calls two lines after decoding, and the handler adds no normalization of its own.
// The HTTP half is proven once, by hand, and recorded in the PR; this is the part that must keep
// being true.

// newServiceOn writes raw to a temp config.yml and loads a Service from it.
func newServiceOn(t *testing.T, raw string) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return NewService(path, slog.New(slog.NewTextHandler(os.Stderr, nil))), path
}

// wireDoc is a PUT body: the defaults a client would send, plus whatever storage it declares. It is
// deliberately NOT run through ResolveStorages — that is the thing under test.
func wireDoc(storages ...StorageEntry) Config {
	c := Default()
	list := append([]StorageEntry(nil), storages...)
	c.Storage = &list
	return c
}

// THE DEFECT'S FIRST HALF: the wire refused what the file accepts. `- path: /backups` is the
// declaration quince's own startup refusal teaches, and it earned three 422s.
func TestAPutMayOmitTheKeysTheFileMayOmit(t *testing.T) {
	svc, _ := newServiceOn(t, "storage:\n  - path: /backups\n    backend: hardlink\n")

	errs, _, err := svc.Replace(wireDoc(StorageEntry{Path: "/backups", Backend: "hardlink"}))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("a PUT omitting zfs.mode, zfs.seed and default was refused, but the same storage is "+
			"legal in config.yml: %+v", errs)
	}
}

// The same for a storage as minimal as the file allows: a path and nothing else.
func TestAPutMayCarryTheShortFormTheStartupRefusalTeaches(t *testing.T) {
	svc, _ := newServiceOn(t, "storage:\n  - path: /backups\n")

	errs, _, err := svc.Replace(wireDoc(StorageEntry{Path: "/backups"}))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("`- path: /backups` was refused over the wire: %+v", errs)
	}
}

// THE DEFECT'S SECOND HALF, and the one nothing caught: `name` and `retention` are unchecked, so
// they reached the disk AND the live snapshot. The file self-healed at the next Load; the running
// process did not, and `name` is the identity DELETE /api/config/storage/{name} addresses by.
func TestAPutLeavesNoEmptyNameInTheLiveSnapshot(t *testing.T) {
	svc, path := newServiceOn(t, "storage:\n  - path: /backups\n    backend: hardlink\n")

	errs, _, err := svc.Replace(wireDoc(StorageEntry{
		Path: "/backups", Backend: "hardlink", Default: true,
		ZFS: ZFSConfig{Mode: "exec", Seed: "auto"},
	}))
	if err != nil || len(errs) > 0 {
		t.Fatalf("replace: err=%v errs=%+v", err, errs)
	}

	live := (*svc.Current().Storage)[0]
	if live.Name != "/backups" {
		t.Errorf("live snapshot name = %q, want it resolved to the path — an empty name is the "+
			"identity DELETE /api/config/storage/{name} addresses by", live.Name)
	}
	if live.Retention == nil {
		t.Error("live snapshot retention is nil, want the code default filled at the write path")
	}

	// And the same must be true of what landed on disk, read back rather than assumed.
	reloaded := Load(path)
	if !reloaded.OK {
		t.Fatalf("the written file does not load: %+v", reloaded.Errors)
	}
	if got := (*reloaded.Config.Storage)[0].Name; got != "/backups" {
		t.Errorf("reloaded name = %q, want /backups", got)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`name: ""`, "retention: null"} {
		if strings.Contains(string(written), forbidden) {
			t.Errorf("the written file still carries %s:\n%s", forbidden, written)
		}
	}
}

// THE ADD PATH STAYS STRICT (ruled, quince#754). validateAddition refuses an empty backend on
// purpose and runs BEFORE ResolveStorages, so the resolve added here is an idempotent second pass
// behind its gate rather than a softening of it.
func TestTheAddPathStillRefusesAnEmptyBackend(t *testing.T) {
	svc, _ := newServiceOn(t, "storage:\n  - path: /backups\n    backend: hardlink\n")

	_, errs, _, err := svc.AddStorage(StorageEntry{Name: "second", Path: "/backups2"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(errs) == 0 {
		t.Fatal("an add with no backend was accepted; it must stay a 422 — a narrow add whose caller " +
			"has just watched quince probe a path is a place an omission really is a client bug")
	}
	var sawBackend bool
	for _, e := range errs {
		if e.Path == "backend" {
			sawBackend = true
		}
	}
	if !sawBackend {
		t.Errorf("the refusal does not name `backend`: %+v", errs)
	}
}

// THE IMPLICATION MUST STAY NARROW ON THE DOOR THIS CHANGE OPENED IT ON.
//
// ResolveStorages implies `default: true` on a LONE entry only, so two storages with none marked
// must still be refused — order is not intent, and a silent pick would be a guess.
//
// The narrowness is quince#504's, ruled 2026-08-01: `- path: /backups` is the legal short form, so
// `default` is optional WHEN THERE IS ONE STORAGE and implied only there. NOT quince#473, which is
// the flattening of `storage:` to fully-specified entries — the right family, the wrong ruling. It
// said #473 until the review of quince#755 asked me to check a number it could not verify.
// The permissive tests above all use one storage, so without this the guard holds by `len(out) == 1`
// and by nothing else, and the plausible future edit ("pick the first when none is marked") would
// soften PUT silently with every other test in this file still green.
//
// TestValidateRequiresExactlyOneDefault covers Validate directly; this covers the write path, which
// is what the change claims to have made honest — validateStorages' own comment is about exactly this
// case: "defaults == 0 here can only mean several storages and none chosen".
func TestAPutWithTwoStoragesAndNoDefaultIsStillRefused(t *testing.T) {
	svc, _ := newServiceOn(t, "storage:\n  - path: /backups\n    backend: hardlink\n")

	errs, _, err := svc.Replace(wireDoc(
		StorageEntry{Path: "/backups", Backend: "hardlink"},
		StorageEntry{Path: "/backups2", Backend: "hardlink"},
	))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(errs) == 0 {
		t.Fatal("two storages with neither marked `default: true` were accepted; the implication " +
			"covers a LONE entry only and a silent pick is what it exists to prevent")
	}
	var sawDefault bool
	for _, e := range errs {
		if e.Path == "storage" {
			sawDefault = true
		}
	}
	if !sawDefault {
		t.Errorf("the refusal does not name `storage`: %+v", errs)
	}
}

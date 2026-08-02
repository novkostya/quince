package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultRoundTripIsStableAndAtomicWrites(t *testing.T) {
	m1, err := Marshal(Default())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	cfg, warns, err := Parse(m1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("defaults produced warnings: %+v", warns)
	}
	m2, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(m1, m2) {
		t.Fatalf("marshal is not canonical/stable:\n---1---\n%s\n---2---\n%s", m1, m2)
	}

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := AtomicWrite(path, m1); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, m1) {
		t.Fatalf("written file != marshaled bytes")
	}
}

func TestParseKeepsDefaultsForMissingKeys(t *testing.T) {
	cfg, warns, err := Parse([]byte("backup:\n  transport: usb\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %+v", warns)
	}
	if cfg.Backup.Transport != "usb" {
		t.Errorf("transport = %q, want usb", cfg.Backup.Transport)
	}
	if cfg.Backup.RequireEncryption != true {
		t.Errorf("require_encryption default lost")
	}
	// storage: has NO defaults to lose — it is a list, and per-entry defaults are applied by
	// ResolveStorages at parse rather than pre-filled into Default() (quince#473). What this
	// asserts instead is that an absent key stays NIL, which is what G7's refusal reads.
	if cfg.Storage != nil {
		t.Errorf("storage should be nil when the key is absent, got %+v", cfg.Storage)
	}
	if cfg.Sessions.TTLMinutes != 30 {
		t.Errorf("sessions.ttl_minutes default lost = %d", cfg.Sessions.TTLMinutes)
	}
}

func TestUnknownKeysWarn(t *testing.T) {
	// `storage:` is a LIST now, so a typo lives inside an ENTRY and the warning is indexed
	// (quince#473). unknownKeys already recursed into slices of structs, which is why this needed
	// no new machinery — measured before the flatten was written rather than discovered after.
	raw := "nonsense: 1\nstorage:\n  - path: /backups\n    bogus: 2\n    zfs:\n      typo: 3\n"
	_, warns, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	gotPaths := map[string]bool{}
	for _, w := range warns {
		gotPaths[w.Path] = true
	}
	for _, want := range []string{"nonsense", "storage[0].bogus", "storage[0].zfs.typo"} {
		if !gotPaths[want] {
			t.Errorf("missing unknown-key warning for %q; got %+v", want, warns)
		}
	}
}

func TestValidateCatchesBadEnums(t *testing.T) {
	c := Default()
	// The backend enum moved onto the ENTRY with the flattening, so this reaches it through the
	// list rather than through a global.
	c.Storage = &[]StorageEntry{{Name: "local", Path: "/backups", Default: true, Backend: "banana",
		ZFS: ZFSConfig{Mode: "exec", Seed: "auto"}}}
	c.UI.Theme = "neon"
	c.Sessions.TTLMinutes = 0
	errs := Validate(c)
	gotPaths := map[string]bool{}
	for _, e := range errs {
		gotPaths[e.Path] = true
	}
	for _, want := range []string{"storage[0].backend", "ui.theme", "sessions.ttl_minutes"} {
		if !gotPaths[want] {
			t.Errorf("missing validation error for %q; got %+v", want, errs)
		}
	}
}

func TestLoadHandEditVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("sessions:\n  ttl_minutes: 45\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if !l.OK {
		t.Fatalf("expected OK load, got errors %+v", l.Errors)
	}
	if l.Config.Sessions.TTLMinutes != 45 {
		t.Errorf("ttl_minutes = %d, want 45", l.Config.Sessions.TTLMinutes)
	}
	if l.Source.Mtime == "" {
		t.Errorf("source mtime not set for an existing file")
	}
}

func TestLoadGarbageKeepsLastGoodAndNamesBadKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("ui:\n  theme: neon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if l.OK {
		t.Fatalf("expected invalid load")
	}
	if l.Config.UI.Theme != Default().UI.Theme {
		t.Errorf("did not fall back to last-good; theme = %q", l.Config.UI.Theme)
	}
	named := false
	for _, e := range l.Errors {
		if e.Path == "ui.theme" {
			named = true
		}
	}
	if !named {
		t.Errorf("bad key not named in errors: %+v", l.Errors)
	}
}

func TestLoadSyntaxErrorIsNotOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("backup: : : not yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if l := Load(path); l.OK {
		t.Fatalf("expected syntax error to be !OK")
	}
}

func TestLoadMissingFileIsDefaultsOK(t *testing.T) {
	l := Load(filepath.Join(t.TempDir(), "does-not-exist.yml"))
	if !l.OK {
		t.Fatalf("missing file should load defaults OK")
	}
	// reflect.DeepEqual rather than !=: Config stopped being comparable when
	// server.trusted_proxies added the first slice-valued field (quince#464). A list is the right
	// shape for CIDRs, so the test moves rather than the schema.
	if !reflect.DeepEqual(l.Config, Default()) {
		t.Errorf("missing file should yield defaults")
	}
}

func TestLoadBootstrapWarnsOnUnknownVar(t *testing.T) {
	b, warns := LoadBootstrap([]string{
		"QUINCE_DATA=/d", "QUINCE_LISTEN=:9000", "QUINCE_TYPOO=x", "PATH=/bin",
	})
	if b.Data != "/d" || b.Listen != ":9000" {
		t.Errorf("bootstrap parse wrong: %+v", b)
	}
	if b.Cache != "/cache" {
		t.Errorf("bootstrap defaults wrong: %+v", b)
	}
	if len(warns) != 1 || warns[0].Path != "QUINCE_TYPOO" {
		t.Errorf("want one warning for QUINCE_TYPOO, got %+v", warns)
	}
}

// The default listen port is :8968 (qn.6f gap B, Operator ruling 2026-08-02). Pinned as its own
// test because a default is only a default if nothing overrides it and everything follows it —
// the deployment files, the e2e harness and the demo all hardcode the same number, and this is
// the single place that decides it. The message names the OLD value too, so a revert reads as a
// deliberate act rather than a plausible-looking edit.
func TestBootstrapDefaultListenPort(t *testing.T) {
	b, warns := LoadBootstrap(nil)
	if len(warns) != 0 {
		t.Fatalf("an empty environment produced warnings: %+v", warns)
	}
	if b.Listen != ":8968" {
		t.Errorf("default listen = %q, want \":8968\" (it was \":8080\" until qn.6f; 8080 is "+
			"IANA-assigned http-alt and heavily squatted, and under network_mode: host — which "+
			"Wi-Fi requires — a collision means quince does not start at all)", b.Listen)
	}
}

// TestLoadBootstrapWarnsOnRetiredBackupsVar is G7b's first half: QUINCE_BACKUPS is GONE, not
// merely unused. A retirement that leaves the variable silently accepted is indistinguishable
// from one that never happened, so the guard is that it now takes the unknown-variable path.
func TestLoadBootstrapWarnsOnRetiredBackupsVar(t *testing.T) {
	_, warns := LoadBootstrap([]string{"QUINCE_DATA=/d", "QUINCE_BACKUPS=/backups"})
	if len(warns) != 1 || warns[0].Path != "QUINCE_BACKUPS" {
		t.Fatalf("a retired QUINCE_BACKUPS must warn like any unknown var, got %+v", warns)
	}
}

func TestValidateDirsFlagsNonWritable(t *testing.T) {
	good := t.TempDir()
	b := Bootstrap{Data: good, Cache: filepath.Join(good, "nope")}
	warns := ValidateDirs(b)
	if len(warns) != 1 || warns[0].Path != "QUINCE_CACHE" {
		t.Errorf("want one warning for missing cache dir, got %+v", warns)
	}
}

// TestValidateDirsDoesNotProbeStorage guards the other direction: ValidateDirs must not acquire a
// backups probe again. Each declared storage is probed on the storage path, where an unreachable
// one is a per-storage state rather than one global warning.
func TestValidateDirsDoesNotProbeStorage(t *testing.T) {
	good := t.TempDir()
	if warns := ValidateDirs(Bootstrap{Data: good, Cache: good}); len(warns) != 0 {
		t.Errorf("bootstrap dirs are data+cache only, got %+v", warns)
	}
}

// A VALID config that is deliberately weaker than the baseline must keep saying so — that is
// `no silent caps or fallbacks`, and a warning is the channel the UI renders in Settings.
// The off case is asserted too: a warning that is always present is noise, and noise is how
// a real one stops being read.
func TestAllowInsecureTransportIsSurfacedAsAWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	const storage = "storage:\n  - path: /backups\n"

	if err := os.WriteFile(path, []byte(storage), 0o600); err != nil {
		t.Fatal(err)
	}
	if l := Load(path); !l.OK || len(l.Warnings) != 0 {
		t.Fatalf("the default config warned: ok=%v warnings=%+v", l.OK, l.Warnings)
	}

	if err := os.WriteFile(path, []byte(storage+"sessions:\n  allow_insecure_transport: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if !l.OK {
		t.Fatalf("the opt-in must be VALID, not an error: %+v", l.Errors)
	}
	if len(l.Warnings) != 1 || l.Warnings[0].Path != "sessions.allow_insecure_transport" {
		t.Fatalf("want one warning naming the setting, got %+v", l.Warnings)
	}
	if !strings.Contains(l.Warnings[0].Message, "in clear") {
		t.Errorf("the warning does not say what is unprotected: %q", l.Warnings[0].Message)
	}
}

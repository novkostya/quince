package config

import (
	"bytes"
	"os"
	"path/filepath"
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
	if cfg.Storage.Backend != "auto" {
		t.Errorf("storage.backend default lost = %q", cfg.Storage.Backend)
	}
	if cfg.Sessions.TTLMinutes != 30 {
		t.Errorf("sessions.ttl_minutes default lost = %d", cfg.Sessions.TTLMinutes)
	}
}

func TestUnknownKeysWarn(t *testing.T) {
	raw := "nonsense: 1\nstorage:\n  bogus: 2\n  zfs:\n    typo: 3\n"
	_, warns, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	gotPaths := map[string]bool{}
	for _, w := range warns {
		gotPaths[w.Path] = true
	}
	for _, want := range []string{"nonsense", "storage.bogus", "storage.zfs.typo"} {
		if !gotPaths[want] {
			t.Errorf("missing unknown-key warning for %q; got %+v", want, warns)
		}
	}
}

func TestValidateCatchesBadEnums(t *testing.T) {
	c := Default()
	c.Storage.Backend = "banana"
	c.UI.Theme = "neon"
	c.Sessions.TTLMinutes = 0
	errs := Validate(c)
	gotPaths := map[string]bool{}
	for _, e := range errs {
		gotPaths[e.Path] = true
	}
	for _, want := range []string{"storage.backend", "ui.theme", "sessions.ttl_minutes"} {
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
	if l.Config != Default() {
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
	if b.Cache != "/cache" || b.Backups != "/backups" {
		t.Errorf("bootstrap defaults wrong: %+v", b)
	}
	if len(warns) != 1 || warns[0].Path != "QUINCE_TYPOO" {
		t.Errorf("want one warning for QUINCE_TYPOO, got %+v", warns)
	}
}

func TestValidateDirsFlagsNonWritable(t *testing.T) {
	good := t.TempDir()
	b := Bootstrap{Data: good, Cache: good, Backups: filepath.Join(good, "nope")}
	warns := ValidateDirs(b)
	if len(warns) != 1 || warns[0].Path != "QUINCE_BACKUPS" {
		t.Errorf("want one warning for missing backups dir, got %+v", warns)
	}
}

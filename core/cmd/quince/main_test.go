package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidateExitCodes(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "good.yml")
	if err := os.WriteFile(valid, []byte("sessions:\n  ttl_minutes: 45\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configCmd([]string{"validate", valid}); err != nil {
		t.Errorf("valid config should pass, got %v", err)
	}

	bad := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(bad, []byte("ui:\n  theme: neon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configCmd([]string{"validate", bad}); err == nil {
		t.Error("invalid config should return a nonzero error")
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	if err := run([]string{"frobnicate"}); err == nil {
		t.Error("unknown subcommand should error")
	}
}

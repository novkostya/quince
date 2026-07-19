package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Bootstrap is the deployment-topology environment (contracts §6): everything a container
// needs before the app can run. Nothing else lives in env — the rest is config.yml.
type Bootstrap struct {
	Data    string // QUINCE_DATA    — app state (DB, config.yml, logs)
	Cache   string // QUINCE_CACHE   — derived caches + session scratch
	Backups string // QUINCE_BACKUPS — the backup dataset
	Listen  string // QUINCE_LISTEN  — HTTP listen address
}

// knownBootstrapVars is the exact set of env vars quince understands. Anything else with a
// QUINCE_ prefix is a typo-guard warning (contracts §6).
var knownBootstrapVars = map[string]struct{}{
	"QUINCE_DATA":    {},
	"QUINCE_CACHE":   {},
	"QUINCE_BACKUPS": {},
	"QUINCE_LISTEN":  {},
}

// LoadBootstrap parses the bootstrap env from an os.Environ()-style slice ("KEY=VALUE").
// Unknown QUINCE_* vars become warnings. Taking environ as a parameter keeps it testable.
func LoadBootstrap(environ []string) (Bootstrap, []Warning) {
	vals := map[string]string{}
	var warnings []Warning
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, "QUINCE_") {
			continue
		}
		if _, known := knownBootstrapVars[k]; !known {
			warnings = append(warnings, Warning{
				Path:    k,
				Message: fmt.Sprintf("unknown QUINCE_ environment variable %q (ignored)", k),
			})
			continue
		}
		vals[k] = v
	}

	b := Bootstrap{
		Data:    orDefault(vals["QUINCE_DATA"], "/data"),
		Cache:   orDefault(vals["QUINCE_CACHE"], "/cache"),
		Backups: orDefault(vals["QUINCE_BACKUPS"], "/backups"),
		Listen:  orDefault(vals["QUINCE_LISTEN"], ":8080"),
	}
	return b, warnings
}

// ConfigPath is where config.yml lives, under the data dir.
func (b Bootstrap) ConfigPath() string { return filepath.Join(b.Data, "config.yml") }

// DBPath is where the app SQLite DB lives, under the data dir.
func (b Bootstrap) DBPath() string { return filepath.Join(b.Data, "quince.db") }

// ValidateDirs probes that each bootstrap directory exists and is writable, returning a
// warning per problem. The data dir is load-bearing (the DB lives there) — the caller
// treats a failure there as fatal via store.Open; cache/backups issues are surfaced as
// degraded modes, never silently swallowed (hard rule: no silent caps).
func ValidateDirs(b Bootstrap) []Warning {
	var warnings []Warning
	for _, d := range []struct{ name, path string }{
		{"QUINCE_DATA", b.Data},
		{"QUINCE_CACHE", b.Cache},
		{"QUINCE_BACKUPS", b.Backups},
	} {
		if err := probeWritable(d.path); err != nil {
			warnings = append(warnings, Warning{
				Path:    d.name,
				Message: fmt.Sprintf("directory %q is not writable: %v", d.path, err),
			})
		}
	}
	return warnings
}

// probeWritable confirms path is a directory we can create a file in, then cleans up.
func probeWritable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	f, err := os.CreateTemp(path, ".quince-writecheck-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

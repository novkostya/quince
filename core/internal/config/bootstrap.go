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
	Data   string // QUINCE_DATA   — app state (DB, config.yml, logs)
	Cache  string // QUINCE_CACHE  — derived caches + session scratch
	Listen string // QUINCE_LISTEN — HTTP listen address
}

// knownBootstrapVars is the exact set of env vars quince understands. Anything else with a
// QUINCE_ prefix is a typo-guard warning (contracts §6).
//
// qn.6c RETIRED QUINCE_BACKUPS (gap 3, Operator ruling 2026-07-31: every storage is declared in
// config.yml — no env var, no implicit storage, no fallback). Its ABSENCE here is load-bearing
// rather than an omission: a box that still sets it now gets the unknown-variable warning, which
// is how a retired variable says so instead of being quietly honoured. That matters because the
// var carried a built-in "/backups" default, so every deployment had a working storage while
// declaring nothing — the implicit path the ruling removed.
var knownBootstrapVars = map[string]struct{}{
	"QUINCE_DATA":   {},
	"QUINCE_CACHE":  {},
	"QUINCE_LISTEN": {},
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

	// :8968 is IANA-unassigned, mid-block in the 8955–8979 run, below the 32768 ephemeral
	// floor and off Chromium's restricted list — Operator ruling 2026-08-02 (qn.6f gap B,
	// quince#446), measured against the live registry rather than chosen for looks.
	//
	// IT REPLACED :8080, AND THE REASON IS HOST NETWORKING. Under bridged networking a
	// collision costs a remap and nothing more. Wi-Fi backup needs `network_mode: host` for
	// mDNS, and there nothing can be remapped: anything already holding the port means
	// quince does not start. `8080` is assigned (`http-alt`) and squatted by Synology's own
	// stack, Tomcat, qBittorrent and UniFi among others, so it was close to the worst
	// available choice for the deployment this project's primary transport requires.
	//
	// A bind failure stays a loud named error and never falls back to another port — no
	// silent caps or fallbacks. The real mitigation for "8968 is not memorable" is that the
	// listen address is a first-class setting, not that the number is a good one.
	b := Bootstrap{
		Data:   orDefault(vals["QUINCE_DATA"], "/data"),
		Cache:  orDefault(vals["QUINCE_CACHE"], "/cache"),
		Listen: orDefault(vals["QUINCE_LISTEN"], ":8968"),
	}
	return b, warnings
}

// ConfigPath is where config.yml lives, under the data dir.
func (b Bootstrap) ConfigPath() string { return filepath.Join(b.Data, "config.yml") }

// DBPath is where the app SQLite DB lives, under the data dir.
func (b Bootstrap) DBPath() string { return filepath.Join(b.Data, "quince.db") }

// ValidateDirs probes that each bootstrap directory exists and is writable, returning a
// warning per problem. The data dir is load-bearing (the DB lives there) — the caller
// treats a failure there as fatal via store.Open; cache issues are surfaced as degraded
// modes, never silently swallowed (hard rule: no silent caps).
//
// qn.6c: the backups dir is no longer here, because there is no longer A backups dir. Each
// declared storage is probed on the storage path, where an unreachable one is a per-storage
// state (`reachable: false`) rather than one global warning — and where an absent medium must be
// told apart from a new storage (design §5), which a writability probe cannot do.
func ValidateDirs(b Bootstrap) []Warning {
	var warnings []Warning
	for _, d := range []struct{ name, path string }{
		{"QUINCE_DATA", b.Data},
		{"QUINCE_CACHE", b.Cache},
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

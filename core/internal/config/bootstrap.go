package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Bootstrap is the deployment-topology environment (contracts §6): everything a container
// needs before the app can run. Nothing else lives in env — the rest is config.yml.
type Bootstrap struct {
	Data   string // QUINCE_DATA   — app state (DB, config.yml, logs)
	Cache  string // QUINCE_CACHE  — derived caches + session scratch
	Listen string // QUINCE_LISTEN — HTTP listen address
	// TrustedProxies are the IPs/CIDRs whose X-Forwarded-* headers quince believes
	// (QUINCE_TRUSTED_PROXIES, comma-separated). EMPTY is the shipping default and means
	// trust none — byte-for-byte pre-quince#464 behaviour.
	//
	// ENV rather than config.yml, Operator ruling 2026-08-02 (quince#549). It was a
	// config key for one afternoon and never shipped. Two reasons it cannot be one:
	// --public-demo DELETES its config at startup, so the deployment that most needs a
	// trust list could never carry one; and in that mode every visitor can PUT /api/config,
	// so a file-based trust list is editable by the population it protects against.
	TrustedProxies []string
	// DemoResetMinutes is how often the DEPLOYMENT restarts a --public-demo instance, in whole
	// minutes (QUINCE_DEMO_RESET_MINUTES). quince runs no timer and performs no reset: design
	// D4 puts the restart outside the process, so this is a fact the deployment TELLS quince so
	// the login screen can warn a visitor before their edits are wiped (spec story 6).
	//
	// A REPORTED DEPLOYMENT FACT, not a setting — Operator ruling 2026-08-02 (quince#470). D12
	// governs settings; the test that ruling gives is *does any code branch on this value*, and
	// nothing does. Env rather than config.yml for the same two reasons as TrustedProxies above.
	//
	// ZERO means the deployment did not say, and that is a supported state rather than a broken
	// one: the UI then states that the demo resets WITHOUT naming a schedule. Naming one quince
	// cannot keep would be worse than saying less.
	DemoResetMinutes int
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
	"QUINCE_DATA":               {},
	"QUINCE_CACHE":              {},
	"QUINCE_LISTEN":             {},
	"QUINCE_TRUSTED_PROXIES":    {},
	"QUINCE_DEMO_RESET_MINUTES": {},
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

	resetMinutes, mwarn := parseMinutes("QUINCE_DEMO_RESET_MINUTES", vals["QUINCE_DEMO_RESET_MINUTES"])
	warnings = append(warnings, mwarn...)

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
		Data:             orDefault(vals["QUINCE_DATA"], "/data"),
		Cache:            orDefault(vals["QUINCE_CACHE"], "/cache"),
		Listen:           orDefault(vals["QUINCE_LISTEN"], ":8968"),
		TrustedProxies:   splitList(vals["QUINCE_TRUSTED_PROXIES"]),
		DemoResetMinutes: resetMinutes,
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

// parseMinutes reads a whole-minutes env value, returning 0 plus a warning for anything it cannot
// use. An UNSET var is 0 and silent — that is the shipping default and says nothing is claimed.
//
// A value that is present and unusable WARNS rather than being dropped, including an explicit "0".
// The consumer renders "resets periodically" for 0, which is indistinguishable from a correctly
// unset deployment — so without the warning an operator who typed `30 minutes` or `0` would see a
// plausible page and never learn their interval never arrived. That is exactly the silent fallback
// the hard rule forbids.
//
// It does NOT refuse to start. The value is cosmetic — nothing branches on it (quince#470) — and
// refusing to serve a demo over a typo in a notice would be a worse failure than the notice.
func parseMinutes(name, v string) (int, []Warning) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, []Warning{{
			Path: name,
			Message: fmt.Sprintf("%q is not a positive whole number of minutes (ignored; the demo "+
				"reset notice will not name an interval)", v),
		}}
	}
	return n, nil
}

// splitList parses a comma-separated env value into trimmed, non-empty entries. An unset or empty
// var yields nil, which every consumer must read as "trust nothing" rather than "trust anything".
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ScratchRoot is where vault sessions decrypt — contracts §5's `/cache/scratch/<session_id>/`,
// under the cache dir because that is what the cache dir is for: "derived caches + session
// scratch", and because wiping it whole is always safe.
//
// A METHOD RATHER THAN A CONSTANT AT THE CALL SITE, so the one place that knows the layout is
// the one place that already knows every other path.
func (b Bootstrap) ScratchRoot() string { return filepath.Join(b.Cache, "scratch") }

package config

import (
	"bytes"
	"encoding/json"
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
	cfg, _, warns, err := Parse(m1)
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
	// `preferred_transport: wifi` rather than the retired `transport: usb` (quince#654) — a
	// NON-DEFAULT value on purpose, so this test still proves that setting one key keeps the others'
	// defaults. With `usb` (now the default) the assertion below would pass whether the key was read
	// or ignored, which is the shape of check this repository keeps filing bugs about.
	cfg, _, warns, err := Parse([]byte("backup:\n  preferred_transport: wifi\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %+v", warns)
	}
	if cfg.Backup.PreferredTransport != "wifi" {
		t.Errorf("preferred_transport = %q, want wifi", cfg.Backup.PreferredTransport)
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
	_, _, warns, err := Parse([]byte(raw))
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
		ZFS: ZFSConfig{Mode: "hook", Seed: "auto"}}}
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
	// reflect.DeepEqual rather than !=: Config carries a pointer-to-slice (storage), so == is not
	// defined on it. Introduced by quince#547, which briefly added a second slice field to the
	// schema; quince#549 removed that field again and this stays, because the pointer remains.
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

// TestBootstrapDemoResetMinutes covers the carrier for public-demo story 6. The value is cosmetic
// — the login screen states it and nothing branches on it (quince#470) — so the whole risk lives in
// the parse: an interval the operator MEANT to set and that never arrived renders as "resets
// periodically", which is exactly what a correctly-unset deployment renders. The two are
// indistinguishable on screen, so they must be distinguishable in the log.
func TestBootstrapDemoResetMinutes(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		want      int
		warn      bool
		because   string
	}{
		{"unset", "", 0, false,
			"the shipping default claims nothing and must not warn about it"},
		{"a plain interval", "QUINCE_DEMO_RESET_MINUTES=30", 30, false, ""},
		{"whitespace is trimmed", "QUINCE_DEMO_RESET_MINUTES=  45  ", 45, false,
			"an env var copied out of a compose file carries whatever spacing it had"},
		{"units the operator added", "QUINCE_DEMO_RESET_MINUTES=30 minutes", 0, true,
			"the var NAMES its unit, so a value repeating it is a mistake rather than a dialect"},
		{"a duration string", "QUINCE_DEMO_RESET_MINUTES=30m", 0, true,
			"Go duration syntax is the obvious guess and this var does not take it"},
		{"explicit zero", "QUINCE_DEMO_RESET_MINUTES=0", 0, true,
			"zero minutes is not an interval, and unset already means `did not say`"},
		{"negative", "QUINCE_DEMO_RESET_MINUTES=-5", 0, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var environ []string
			if tc.env != "" {
				environ = []string{tc.env}
			}
			b, warns := LoadBootstrap(environ)
			if b.DemoResetMinutes != tc.want {
				t.Errorf("DemoResetMinutes = %d, want %d — %s", b.DemoResetMinutes, tc.want, tc.because)
			}
			gotWarn := len(warns) == 1 && warns[0].Path == "QUINCE_DEMO_RESET_MINUTES"
			if gotWarn != tc.warn {
				t.Errorf("warned = %v, want %v (warnings %+v) — an unusable value dropped in "+
					"silence is the fallback the hard rule forbids. %s", gotWarn, tc.warn, warns, tc.because)
			}
		})
	}
}

// TestDemoResetMinutesIsAKnownVar is the half a reader would assume rather than check: a var absent
// from knownBootstrapVars is rejected by the typo guard BEFORE it is ever parsed, so the parse above
// could be perfect and the variable still never arrive.
func TestDemoResetMinutesIsAKnownVar(t *testing.T) {
	_, warns := LoadBootstrap([]string{"QUINCE_DEMO_RESET_MINUTES=30"})
	if len(warns) != 0 {
		t.Fatalf("a valid QUINCE_DEMO_RESET_MINUTES was treated as an unknown variable: %+v", warns)
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

// TestBootstrapParsesTrustedProxies is quince#549: the trust list moved from `config.yml` to the
// bootstrap env, because --public-demo deletes its config at startup and the deployment that most
// needs a trust list could never carry one.
func TestBootstrapParsesTrustedProxies(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		want      []string
	}{
		{"unset means trust NOTHING", "", nil},
		{"empty means trust NOTHING", "QUINCE_TRUSTED_PROXIES=", nil},
		{"whitespace only", "QUINCE_TRUSTED_PROXIES=   ", nil},
		{"one entry", "QUINCE_TRUSTED_PROXIES=203.0.113.5", []string{"203.0.113.5"}},
		{"several, spaced", "QUINCE_TRUSTED_PROXIES=203.0.113.5, 198.51.100.0/24 ", []string{"203.0.113.5", "198.51.100.0/24"}},
		{"empty entries dropped", "QUINCE_TRUSTED_PROXIES=203.0.113.5,,", []string{"203.0.113.5"}},
	} {
		env := []string{"QUINCE_DATA=/d"}
		if tc.env != "" {
			env = append(env, tc.env)
		}
		b, warns := LoadBootstrap(env)
		if len(warns) != 0 {
			t.Errorf("%s: unexpected warnings %+v", tc.name, warns)
		}
		if !reflect.DeepEqual(b.TrustedProxies, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, b.TrustedProxies, tc.want)
		}
	}
}

// TestConfigHasNoServerSection is the deletion half of quince#549. `server.trusted_proxies` existed
// for one afternoon and never shipped, so there is no migration and no shim — but a key that is
// gone from the struct and still accepted by the parser would be silently ignored, which is the
// failure the retirement of QUINCE_BACKUPS was designed to make loud.
//
// Asserted through the PARSER rather than by grepping the struct: what matters is that a user who
// writes the old key is TOLD, not that a field is absent.
func TestConfigHasNoServerSection(t *testing.T) {
	_, _, warns, err := Parse([]byte("server:\n  trusted_proxies: [203.0.113.5]\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var named bool
	for _, w := range warns {
		if strings.HasPrefix(w.Path, "server") {
			named = true
		}
	}
	if !named {
		t.Fatalf("the retired server: section parsed without a warning — a user moving to "+
			"QUINCE_TRUSTED_PROXIES would be silently ignored; got %+v", warns)
	}
}

// quince#654 — THE RENAME ROUND-TRIPS THROUGH THE FILE AND THROUGH JSON, which is the assertion the
// ruling asked for rather than "it compiles".
//
// quince#493: `PUT /api/config` decodes into a ZERO-VALUED Config, so any key the client omits is
// reset to the Go zero value. A rename that changed the Go field and left the TS type saying
// `transport` would therefore be that defect fired deliberately — every Settings save sending a
// document with no `preferred_transport`, zeroing it, and `Validate` then rejecting the result. The
// two renames are one commit; this is what proves the Go half of it and pins the wire name.
func TestPreferredTransportRoundTripsUnderItsNewName(t *testing.T) {
	// YAML: written under the new key, and read back.
	c := Default()
	c.Backup.PreferredTransport = "wifi"
	data, err := Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "preferred_transport: wifi") {
		t.Errorf("marshalled config does not carry `preferred_transport: wifi`:\n%s", data)
	}
	if strings.Contains(string(data), "\n  transport:") {
		t.Errorf("marshalled config still carries the OLD `transport:` key:\n%s", data)
	}
	back, _, _, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if back.Backup.PreferredTransport != "wifi" {
		t.Errorf("round-tripped preferred_transport = %q, want wifi", back.Backup.PreferredTransport)
	}

	// JSON: the name the UI's Config type must use. A mismatch here is exactly the quince#493 hazard.
	j, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	if !strings.Contains(string(j), `"preferred_transport":"wifi"`) {
		t.Errorf("JSON does not carry preferred_transport:\n%s", j)
	}
}

// The default is `usb`, and it is behaviour-preserving rather than a speed claim (quince#654).
func TestPreferredTransportDefaultsToUSB(t *testing.T) {
	if got := Default().Backup.PreferredTransport; got != "usb" {
		t.Errorf("default preferred_transport = %q, want usb — resolveTransport returned USB for a "+
			"both-present device before this key existed, and the default preserves that", got)
	}
}

// NO `auto` IN THE PREFERENCE ENUM. As a preference it would mean "prefer whatever is already
// preferred" — quince#653's defect migrating out of the UI and into config.yml. `auto` stays legal
// as a REQUEST transport, which this package never validates because it arrives on POST /api/jobs.
func TestPreferredTransportRefusesAuto(t *testing.T) {
	c := Default()
	c.Backup.PreferredTransport = "auto"
	errs := Validate(c)
	var found bool
	for _, e := range errs {
		if e.Path == "backup.preferred_transport" {
			found = true
		}
	}
	if !found {
		t.Errorf("`auto` was accepted as a preference; errors = %+v", errs)
	}
}

// `storage.zfs.mode: exec` IS REFUSED, BY PATH — Operator ruling 2026-08-10 (quince#697,
// quince#793). It ran `zfs` in the container, and the shipped image has no `zfs` binary.
//
// THE REFUSAL IS THE POINT, and it is why the key survived losing its second value. Deleting the
// field would make `exec` an unknown key, which warns *"(ignored)"* — so an operator carrying the
// mode that cannot work would be told it was being ignored rather than that it is gone. Kept as a
// one-value enum, the error names the exact path and the one legal value. See ZFSConfig.Mode.
func TestZFSModeExecIsRefusedRatherThanIgnored(t *testing.T) {
	cfg, _, warns, err := Parse([]byte("storage:\n  - path: /backups\n    backend: zfs\n    zfs:\n      mode: exec\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, w := range warns {
		if strings.Contains(w.Path, "zfs.mode") {
			t.Errorf("`mode: exec` produced an unknown-key warning (%q) — it must be a validation "+
				"ERROR, not something reported as ignored", w.Message)
		}
	}
	var msg string
	for _, e := range Validate(cfg) {
		if e.Path == "storage[0].zfs.mode" {
			msg = e.Message
		}
	}
	if msg == "" {
		t.Fatalf("`mode: exec` was accepted; errors = %+v", Validate(cfg))
	}
	if !strings.Contains(msg, "hook") {
		t.Errorf("the refusal must name the one legal value so the operator knows what to write; got %q", msg)
	}
}

// A config that never mentions `mode` gets `hook`, which is the mode that works in the shipped
// image. It got `exec` — the one that cannot — until quince#697.
func TestZFSModeDefaultsToHook(t *testing.T) {
	cfg, _, _, err := Parse([]byte("storage:\n  - path: /backups\n    backend: zfs\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := (*cfg.Storage)[0].ZFS.Mode; got != "hook" {
		t.Errorf("default zfs mode = %q, want hook", got)
	}
	if errs := Validate(cfg); len(errs) > 0 {
		t.Errorf("a storage declaring no zfs mode must validate; errs = %+v", errs)
	}
}

// The OLD key is now an unknown key. quince#401 means the warning does not name its successor — that
// is a known, filed defect and not this change's to fix. What matters here is that the value does not
// silently take effect under a name that no longer means anything.
func TestTheOldTransportKeyIsNowUnknown(t *testing.T) {
	_, _, warns, err := Parse([]byte("backup:\n  transport: wifi\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var named bool
	for _, w := range warns {
		if strings.Contains(w.Message, "transport") || strings.Contains(w.Path, "transport") {
			named = true
		}
	}
	if !named {
		t.Errorf("setting the retired `backup.transport` produced no warning at all; warnings = %+v", warns)
	}
}

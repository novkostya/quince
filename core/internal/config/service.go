package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/novkostya/quince/core/internal/wire"
	"gopkg.in/yaml.v3"
)

// Source describes where the live config was read from (contracts §1: GET returns
// source:{path, mtime}). Mtime is RFC3339 UTC, empty when the file does not exist yet.
type Source struct {
	Path  string `json:"path"`
	Mtime string `json:"mtime"`
}

// Loaded is the full result of reading config.yml from disk. OK is false when parsing or
// validation failed — the caller keeps last-good and surfaces Warnings/Errors.
type Loaded struct {
	Config   Config
	Warnings []Warning
	Errors   []wire.ConfigError
	Source   Source
	OK       bool
}

// Parse decodes YAML over the defaults (missing keys keep their default) and collects
// unknown-key warnings (typo guard, contracts §6 — a key the app doesn't know is a
// warning, never an error). A YAML syntax error is returned as err.
func Parse(raw []byte) (Config, []Warning, error) {
	cfg := Default()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Default(), nil, err
	}
	// PER-ENTRY DEFAULTS ARE APPLIED HERE, once, rather than at each read (quince#473,
	// quince#504). `storage:` entries do not exist until the file is decoded, so unlike every
	// other section their defaults cannot be pre-filled into Default(). Doing it in one place is
	// what lets `- path: /backups` mean what the 2026-08-01 ruling says it means, without any
	// consumer learning that a name might be empty.
	cfg.Storage = ResolveStorages(cfg.Storage)
	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil || rawMap == nil {
		return cfg, nil, nil // empty doc or non-mapping root: no unknown keys to report
	}
	warnings := unknownKeys(rawMap, reflect.TypeOf(Config{}), "")
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Path < warnings[j].Path })
	return cfg, warnings, nil
}

// unknownKeys walks a decoded YAML mapping against the struct's yaml tags, reporting any
// key with no matching field. It recurses into nested struct fields.
func unknownKeys(raw map[string]any, t reflect.Type, prefix string) []Warning {
	known := map[string]reflect.StructField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		known[name] = f
	}
	var warnings []Warning
	for k, v := range raw {
		f, ok := known[k]
		if !ok {
			path := prefix + k
			warnings = append(warnings, Warning{Path: path, Message: fmt.Sprintf("unknown config key %q (ignored)", path)})
			continue
		}
		switch ft := deref(f.Type); ft.Kind() {
		case reflect.Struct:
			if sub, ok := v.(map[string]any); ok {
				warnings = append(warnings, unknownKeys(sub, ft, prefix+k+".")...)
			}
		case reflect.Slice:
			// qn.6c: recurse into slices of structs. Without this a typo INSIDE a storages
			// entry (`pathh:`) is silently dropped by yaml.Unmarshal and never reported —
			// the guard would cover `storage:` itself and nothing under it, so a
			// mistyped key reads as an omitted one and the storage lands somewhere the user
			// did not name. Indexed, because "which entry" is the first thing you need.
			elem := deref(ft.Elem())
			if elem.Kind() != reflect.Struct {
				continue
			}
			items, ok := v.([]any)
			if !ok {
				continue
			}
			for i, item := range items {
				sub, ok := item.(map[string]any)
				if !ok {
					continue
				}
				warnings = append(warnings, unknownKeys(sub, elem, fmt.Sprintf("%s%s[%d].", prefix, k, i))...)
			}
		}
	}
	return warnings
}

// deref unwraps pointer types so a *[]T field is walked as []T. Config.Storage is a
// pointer purely to distinguish an absent key from an empty list; that encoding must not also
// decide whether the typo guard looks inside it.
func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// Marshal serializes config canonically (struct field order = key order). qn.6 replaces
// this with a yaml.Node encoder emitting generated doc-comments.
func Marshal(c Config) ([]byte, error) { return yaml.Marshal(c) }

// AtomicWrite writes data to a temp file in the same dir, fsyncs, and renames over path —
// so a reader never sees a half-written config, and a crash mid-write leaves the old file.
func AtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil { // no secrets in config — diffable/shareable (D12)
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Load reads config.yml, applying last-good-on-invalid semantics.
func Load(path string) Loaded {
	src := Source{Path: path}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return Loaded{Config: Default(), Source: src, OK: true} // no file yet: defaults, written on first save
	}
	src.Mtime = info.ModTime().UTC().Format(time.RFC3339)

	data, err := os.ReadFile(path)
	if err != nil {
		return Loaded{
			Config: Default(), Source: src, OK: false,
			Warnings: []Warning{{Path: path, Message: "cannot read config: " + err.Error()}},
		}
	}
	cfg, warnings, perr := Parse(data)
	if perr != nil {
		return Loaded{
			Config: Default(), Source: src, OK: false,
			Warnings: append(warnings, Warning{Path: "", Message: "invalid YAML: " + perr.Error()}),
		}
	}
	if errs := Validate(cfg); len(errs) > 0 {
		for _, e := range errs {
			warnings = append(warnings, Warning{Path: e.Path, Message: "invalid value: " + e.Message})
		}
		return Loaded{Config: Default(), Warnings: warnings, Errors: errs, Source: src, OK: false}
	}
	return Loaded{Config: cfg, Warnings: append(warnings, degradedModeWarnings(cfg)...), Source: src, OK: true}
}

// degradedModeWarnings surfaces settings that are VALID and deliberately weaker than the
// security baseline. They are not errors — the user asked for them — but `no silent caps or
// fallbacks` means a running quince keeps saying so, and a warning is the channel the UI
// already renders in Settings.
//
// Only on the OK path, and that is the right place rather than an oversight: an invalid
// config is discarded in favour of Default(), which has no degraded mode to report, so
// warning there would name a setting that is not actually in force.
func degradedModeWarnings(c Config) []Warning {
	var out []Warning
	if c.Sessions.AllowInsecureTransport {
		out = append(out, Warning{
			Path: "sessions.allow_insecure_transport",
			Message: "ON — session and CSRF cookies are served without the Secure flag to " +
				"plain-http clients, so they cross the network in clear and anyone who can read " +
				"the path can sign in as you. Deliberate for a network you trust; turn it off if not.",
		})
	}
	return out
}

// Service owns the live config and serves GET/PUT /api/config. It is safe for concurrent use.
//
// SUBSYSTEMS CAN NOW BE TOLD (qn.6g, quince#577). A valid write updates the file and the in-memory
// snapshot and then runs the registered Appliers, which is what turns a saved setting into an
// applied one. Whether a given KEY is applied live is a property of its consumer, not of this
// mechanism — the per-setting answer lives in contracts §6.
type Service struct {
	// writeMu serialises the WHOLE write path — validate, write, swap, notify — so that the last
	// Applier call always carries the configuration that is on disk and in the snapshot.
	//
	// WITHOUT IT THE SEAM HAS NO ORDERING (quince#665 review). `Replace` touches three things that
	// were independently ordered: which AtomicWrite lands last, which snapshot swap lands last, and
	// which notify reaches the appliers last. Two concurrent writes could therefore leave a
	// subsystem on a config that neither the file nor the snapshot holds — and leave it there, since
	// nothing re-notifies. `old` was incoherent for the same reason: read at swap time, so it could
	// be the OTHER writer's document rather than what was live when this call began.
	//
	// That race predates this rung and was harmless while nothing consumed the config. It stops
	// being harmless here, which is why it is closed rather than declared: PRs 3–7 are built against
	// this contract, and a concurrency property baked in unstated is far more expensive once four
	// consumers exist.
	//
	// IT IS NOT `mu`, and the two must not be merged. Appliers take `mu` — via Current() — while
	// writeMu is held, which is exactly why notify can run outside `mu` without deadlocking.
	//
	// THE ONE RULE IT CREATES: an Applier must not call Replace or ForgetStorage. See Applier.
	writeMu sync.Mutex

	mu       sync.RWMutex
	path     string
	log      *slog.Logger
	cfg      Config
	warnings []Warning
	source   Source

	// appliers is written ONLY at wiring time and read under mu on every write.
	//
	// Registration is deliberately not a runtime operation (spec decision 3): a fixed list cannot
	// be mutated concurrently, so this never becomes the unsynchronised-func-value race that
	// `storage.Manager.SetRefresher` is today (a benign startup-only write that stops being benign
	// the moment anything re-registers).
	appliers []applier
}

// applier is one registered subsystem, named so a failure can say which one.
type applier struct {
	name  string
	apply Applier
}

// Applier is a subsystem's chance to take a new configuration. It returns warnings describing
// anything it could NOT apply.
//
// IT CANNOT REFUSE, AND THAT IS THE LOAD-BEARING DECISION (spec decision 2). By the time an Applier
// runs the file is already written, and the file is the source of truth — so an Applier that could
// fail the request would leave the file and the process disagreeing, with a 500 to explain it.
// Anything that may refuse runs BEFORE the write, in Validate / CheckStorages / CheckTLS, which is
// the shape this package already has rather than a new one.
//
// An Applier that cannot complete its work says so in a Warning and leaves its subsystem on its
// last-good state — which is D12's own rule for a bad edit, one layer down.
//
// AN APPLIER MUST NOT CALL Replace OR ForgetStorage. Both hold `writeMu` for the whole write, so a
// re-entrant call deadlocks. It may freely call Current()/Snapshot(), which take `mu` only — and
// `next` is already the configuration Current() would return, so there is no reason to.
//
// `old` is the configuration that was live until this write; `next` is what has just been written.
// Both are passed because most consumers only care about their own section, and comparing is how
// they decide whether they have anything to do at all.
type Applier func(old, next Config) []Warning

// Subscribe registers an Applier. WIRING TIME ONLY — see the `appliers` field for why that is a
// constraint rather than a convention. Order of registration is order of application; nothing today
// needs one applier to observe another's effect, and if that ever changes it becomes a dependency
// graph rather than a slice.
func (s *Service) Subscribe(name string, apply Applier) {
	if apply == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appliers = append(s.appliers, applier{name: name, apply: apply})
}

// notify runs every Applier against a completed write and collects their warnings.
//
// HOLDS NO LOCK WHILE APPLYING. An Applier reaches into another subsystem — taking its mutex,
// doing filesystem work — and holding `mu` across that would deadlock the moment one of them called
// back into Current(), which is exactly what a storage applier re-resolving the declared list does.
// So the list is copied under the read lock and released before anything runs.
//
// A PANICKING APPLIER MUST NOT TAKE THE WRITE WITH IT. The file is already on disk and the snapshot
// already swapped; unwinding through the HTTP handler would report a failed save that succeeded.
// It is recovered, logged, and reported as a warning naming the applier.
func (s *Service) notify(old, next Config) []Warning {
	s.mu.RLock()
	list := append([]applier(nil), s.appliers...)
	s.mu.RUnlock()

	var out []Warning
	for _, a := range list {
		out = append(out, s.runApplier(a, old, next)...)
	}
	return out
}

// runApplier is one applier call, isolated so a panic in it cannot escape the loop.
func (s *Service) runApplier(a applier, old, next Config) (warns []Warning) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("config: applier panicked — the write STANDS and the subsystem may be stale",
				"applier", a.name, "panic", r)
			warns = []Warning{{
				Path: a.name,
				Message: "the configuration was saved, but the " + a.name + " subsystem failed while " +
					"applying it and may still be running the previous settings — restart quince to be sure",
			}}
		}
	}()
	return a.apply(old, next)
}

// NewService loads config.yml at startup, logging any warnings/invalidity (never fatal).
func NewService(path string, log *slog.Logger) *Service {
	l := Load(path)
	if !l.OK {
		log.Warn("config invalid at startup — running on last-good defaults", "path", path, "errors", len(l.Errors))
	}
	for _, w := range l.Warnings {
		log.Warn("config warning", "path", w.Path, "message", w.Message)
	}
	return &Service{path: path, log: log, cfg: l.Config, warnings: l.Warnings, source: l.Source}
}

// Snapshot returns the live config, its warnings, and its source (for GET /api/config).
func (s *Service) Snapshot() (Config, []Warning, Source) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, append([]Warning(nil), s.warnings...), s.source
}

// Current returns just the live config for internal consumers.
func (s *Service) Current() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Replace validates a full-document config, writes it canonically, updates the live snapshot, and
// then tells every registered Applier (qn.6g).
//
// Returns validation errors (→ 422), the Appliers' warnings, and a write error. The warnings are
// per-response and are never stored — see the notify call at the end for why.
func (s *Service) Replace(c Config) ([]wire.ConfigError, []Warning, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.replaceLocked(c)
}

// replaceLocked is Replace's body. CALLER MUST HOLD writeMu.
//
// It exists so ForgetStorage can do its READ-MODIFY-WRITE under the same lock. That is a second
// race the split closes and the review did not name: Forget reads the storage list, splices one
// entry out, and writes the result — so two concurrent forgets both read the same list and the
// second write silently restores the entry the first removed.
func (s *Service) replaceLocked(c Config) ([]wire.ConfigError, []Warning, error) {
	if errs := Validate(c); len(errs) > 0 {
		return errs, nil, nil
	}
	// qn.6c: a SAVE must also satisfy the storage requirement, and this check lives here rather
	// than in Validate for a reason that is easy to get backwards (quince#394 review).
	//
	// Validate deliberately does not report an absent or empty list, because Load() DISCARDS a
	// config that fails Validate and falls back to Default() with OK:false — so a validation
	// error there would produce a running daemon on defaults with no storage and no error, the
	// silent zero-storage start gap 3's ruling forbids.
	//
	// Replace has the OPPOSITE property: it returns the errors and writes NOTHING. The hazard
	// that justifies the exclusion is absent from this path, so excluding it here bought nothing
	// and cost the rule. Without this, the UI could remove the last storage, get a 200, and the
	// user would discover backups were disabled at the next restart — an acceptance that is
	// silent, which is what `no silent caps or fallbacks` forbids and what D12 makes reachable by
	// making the UI the editing surface.
	if req := CheckStorages(c, nil, nil); !req.OK() {
		msg := "at least one storage must be declared — saving this would leave quince unable to back anything up, and it would refuse to start"
		if req.Empty {
			msg = "the storage list is empty — " + msg
		}
		return []wire.ConfigError{{Path: "storage", Message: msg}}, nil, nil
	}
	// THE SAME ARGUMENT, ONE CHECK LATER (quince#683, ruled 2026-08-07).
	//
	// CheckStorageBackends refuses two storages that are one storage — a shared zfs parent_dataset,
	// where each device's dataset would be created twice and each storage would believe it owned it
	// (quince#458). It was enforced ONLY at startup, so PUT /api/config could write a document the
	// daemon then refuses to boot on, while the running process kept serving happily until something
	// restarted it. A save that produces an unstartable file is the silent acceptance
	// `no silent caps or fallbacks` forbids, reached through the surface D12 makes primary.
	//
	// It goes HERE and not in Validate, for the reason the paragraph above already gives for its
	// sibling: Load() discards a config that fails Validate and falls back to Default(), which would
	// trade a refusal naming the dataset and both storages for a daemon running on defaults —
	// quince#508's defect in a new guise. Replace returns the errors and writes NOTHING.
	//
	// Both doors are now one door: qn.6e's add endpoint writes through Replace too, so it inherits
	// this rather than carrying its own copy. Two call sites for one invariant is how they diverge.
	if errs := CheckStorageBackendErrors(c.Storage); len(errs) > 0 {
		return errs, nil, nil
	}
	data, err := Marshal(c)
	if err != nil {
		return nil, nil, err
	}
	if err := AtomicWrite(s.path, data); err != nil {
		return nil, nil, err
	}
	mtime := ""
	if info, err := os.Stat(s.path); err == nil {
		mtime = info.ModTime().UTC().Format(time.RFC3339)
	}
	s.mu.Lock()
	old := s.cfg
	s.cfg = c
	s.warnings = nil // a valid structured replace clears prior file warnings
	s.source = Source{Path: s.path, Mtime: mtime}
	s.mu.Unlock()

	// THE SUBSYSTEMS ARE TOLD, AFTER the write and after the snapshot swap (qn.6g).
	//
	// After, because an Applier that observed the old snapshot while the new file was on disk would
	// see a state that never existed. And outside the lock — see notify.
	//
	// THE WARNINGS ARE RETURNED, NEVER STORED, which settles the spec's open question 2. `warnings`
	// above is cleared on every valid write because it describes THE FILE AS PARSED; an apply
	// warning describes the gap between the file and the running process, which is a different fact
	// with a different lifetime. Storing it there would have it wiped by the next unrelated save
	// while its cause persisted. `ForgetRestartWarning` already made exactly this split for exactly
	// this reason — "a property of the response, not of the stored state" — and this follows it
	// rather than inventing a second rule.
	return nil, s.notify(old, c), nil
}

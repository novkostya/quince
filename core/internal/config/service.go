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
	Config Config
	// Declared is what the file actually carried (qn.6j). Empty when there is no file, which is
	// why a fresh install writes a minimal one rather than a full dump.
	Declared Declared
	Warnings []Warning
	Errors   []wire.ConfigError
	Source   Source
	OK       bool
}

// Parse decodes YAML over the defaults (missing keys keep their default) and collects
// unknown-key warnings (typo guard, contracts §6 — a key the app doesn't know is a
// warning, never an error). A YAML syntax error is returned as err.
// The DECLARED SET rides out with it (qn.6j) — see Declared. It is computed from the same
// decoded mapping the typo guard already walks, so a key path means the same thing to both.
func Parse(raw []byte) (Config, Declared, []Warning, error) {
	cfg := Default()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Default(), nil, nil, err
	}
	// PER-ENTRY DEFAULTS ARE APPLIED HERE, once, rather than at each read (quince#473,
	// quince#504). `storage:` entries do not exist until the file is decoded, so unlike every
	// other section their defaults cannot be pre-filled into Default(). Doing it in one place is
	// what lets `- path: /backups` mean what the 2026-08-01 ruling says it means, without any
	// consumer learning that a name might be empty.
	cfg.Storage = ResolveStorages(cfg.Storage)
	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil || rawMap == nil {
		return cfg, Declared{}, nil, nil // empty doc or non-mapping root: nothing declared, nothing unknown
	}
	warnings := unknownKeys(rawMap, reflect.TypeOf(Config{}), "")
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Path < warnings[j].Path })
	declared := Declared{}
	declaredKeys(rawMap, reflect.TypeOf(Config{}), "", declared)
	return cfg, declared, warnings, nil
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
	cfg, declared, warnings, perr := Parse(data)
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
	return Loaded{Config: cfg, Declared: declared, Warnings: append(warnings, DegradedModeWarnings(cfg)...), Source: src, OK: true}
}

// DegradedModeWarnings surfaces settings that are VALID and deliberately weaker than the
// security baseline. They are not errors — the user asked for them — but `no silent caps or
// fallbacks` means a running quince keeps saying so, and a warning is the channel the UI
// already renders in Settings.
//
// Only on the OK path, and that is the right place rather than an oversight: an invalid
// config is discarded in favour of Default(), which has no degraded mode to report, so
// warning there would name a setting that is not actually in force.
//
// EXPORTED FOR THE APPLIER THAT MAKES THE OPT-IN LIVE (quince#900). This runs on LOAD, and a
// `PUT` is not a load — so a write switching a degraded mode ON returned nothing about it,
// which was tolerable only while such a write did not take effect until a restart. The
// applier calls this rather than restating the prose, so there stays exactly one copy of the
// sentence a user reads when they relax the baseline.
func DegradedModeWarnings(c Config) []Warning {
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

	mu   sync.RWMutex
	path string
	log  *slog.Logger
	cfg  Config
	// declared is the file's own record of what the user set (qn.6j).
	//
	// SET ONCE, AT CONSTRUCTION, AND NOTHING MAINTAINS IT YET. `NewService` takes it from `Load`
	// and no assignment to it exists anywhere in this package. Stated exactly because the next
	// author is the one who has to change that:
	//
	//   - THE WRITE HALF IS PR 4. `replaceLocked` must update this when it writes, or the second
	//     save re-inflates the file the first one tidied.
	//   - THERE IS NO RELOAD PATH AT ALL. `Load` runs at construction and nothing re-reads the
	//     file, so a hand-edit is invisible to a running quince — quince#727, post-`v0.1`.
	//
	// Read under `mu`, like `cfg`. Nothing outside this package needs it.
	declared Declared
	warnings []Warning
	source   Source

	// loadErrs is WHY the file on disk was discarded, and until quince#852 it was recorded nowhere.
	// `Load` returns `OK: false` with these errors and `Config: Default()`; `NewService` logged them
	// and kept neither — so nothing downstream could tell *the operator declared nothing* from *the
	// operator's declaration could not be parsed*.
	//
	// THAT DISTINCTION IS WHAT MAKES A SPLICE SAFE OR DESTRUCTIVE. `AddStorage` reads `Current()`
	// and writes the result; on this path `Current()` is `Default()` with no storage, so the write
	// replaces the operator's file with defaults plus one new entry. Measured 2026-08-12: a zfs
	// storage carrying a `parent_dataset` and a `hook_cmd` was reduced to three lines, `warnings`
	// came back `[]`, and every surface then reported health.
	//
	// EMPTY IS THE ORDINARY CASE, AND IT INCLUDES A FILE THAT PARSED WITH WARNINGS — which is a
	// different state and must not be confused with this one. An unknown-key warning leaves
	// `cfg.Storage` intact, so a splice over it loses nothing. Refusing on any warning would decline
	// writes that are perfectly safe, on a config quince is perfectly happy to run.
	//
	// Cleared by `replaceLocked` on a successful write, beside `warnings`, for the same reason: the
	// document now on disk is one this process built and validated.
	//
	// IT IS THE DETAIL, NOT THE CONDITION — and reading it as the condition was quince#852's own
	// fix reintroducing the defect it fixed. **`Load` has THREE discard paths and only ONE fills
	// `Errors`**: an unreadable file and invalid YAML both return `OK: false` with `Errors` empty
	// (see `Load`, this file). A guard keyed on `len(loadErrs) > 0` therefore covers one of the
	// three states it is named for. Measured 2026-08-12 on a real container — a `config.yml` that
	// stats and cannot be read serves with `errors=0`, and the add went THROUGH the guard to the
	// write. `discarded` is the condition; this is only what the refusal says.
	loadErrs []wire.ConfigError

	// discarded is THE CONDITION: `Load` refused the file on disk, so this process is running on
	// `Default()` and anything the file declared is not in effect. It is `!Loaded.OK` and nothing
	// else, so it cannot drift from the thing it names.
	//
	// **THE CAUSE IS ALWAYS IN `warnings`; THE FATALITY IS ONLY HERE.** That split is deliberate on
	// every path — the validation branch copies each error into `Warnings` in an explicit loop, and
	// the other two write their own sentence there — which is why the refusal can key on this and
	// still have something to say.
	//
	// One of the three is not reachable through a serving daemon and is covered anyway: invalid
	// YAML makes `CheckStorages` report `Malformed` and quince refuses to start (quince#508), so no
	// request arrives. Covered rather than excluded, because "the daemon exits" is a property of
	// today's startup path and this is a property of the write.
	discarded bool

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
	// `!l.OK` IS THE CONDITION AND `l.Errors` IS ONLY THE DETAIL — see the two field comments. The
	// errors are carried only on the discard path, because `Load` may report on a document it still
	// accepted and this must mean "the file was DISCARDED".
	var loadErrs []wire.ConfigError
	if !l.OK {
		loadErrs = l.Errors
	}
	return &Service{
		path: path, log: log, cfg: l.Config, declared: l.Declared, warnings: l.Warnings,
		source: l.Source, loadErrs: loadErrs, discarded: !l.OK,
	}
}

// Snapshot returns the live config, its warnings, and its source (for GET /api/config).
func (s *Service) Snapshot() (Config, []Warning, Source) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, append([]Warning(nil), s.warnings...), s.source
}

// Discarded reports whether the file on disk was REFUSED at load, so this process is running on
// `Default()` and nothing the file declares is in effect (contracts §1, Operator ruling 2026-08-12
// on quince#852/quince#849).
//
// IT EXISTS BECAUSE NO CLIENT CAN INFER IT. `warnings` is served and carries the cause, but it is
// non-empty in two states that want opposite answers: a discarded config, where the declared storage
// is not running, and a config that parsed with an ignored unknown key, where it is fine.
// `config.storage: null` does not separate them either — a fresh install with a typo has that too.
//
// `errors` WAS THE OTHER CANDIDATE AND IS WRONG, on evidence rather than taste: only ONE of `Load`'s
// three discard paths fills `Errors`. A client keying off it would tell somebody whose config cannot
// be read that their storage is fine — worse than saying nothing.
//
// **THE CAUSE IS ALWAYS IN `warnings`; THE FATALITY IS ONLY HERE.** That split is what lets a client
// render the detail it already has and branch only its headline on this.
func (s *Service) Discarded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.discarded
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
	// RESOLVE FIRST, BEFORE ANYTHING LOOKS AT THE DOCUMENT (quince#754).
	//
	// Parse resolves (`cfg.Storage = ResolveStorages(...)`) and AddStorage resolves. This door did
	// not, and it is the door `PUT /api/config` comes through: handleConfigPut decodes the body into
	// a zero-valued Config and calls Replace two lines later, with nothing in between. So one of
	// three write paths handed Validate and Marshal an UNRESOLVED document, and both misbehaved.
	//
	// VALIDATE REFUSED DOCUMENTS THE FILE ACCEPTS. `- path: /backups` is the minimal storage
	// declaration quince's own startup refusal teaches, it is legal in config.yml, and over the wire
	// it earned three 422s: zfs.mode "", zfs.seed "", and `exactly one storage must be marked
	// default: true`. validateStorages states the opposite in its own comment — "A LONE STORAGE IS
	// ALREADY MARKED DEFAULT by ResolveStorages, so defaults == 0 here can only mean several
	// storages and none chosen" — which was true of the load path and false of this one. The editing
	// surface disagreeing with the thing it edits contradicts D12, which makes the file the truth.
	//
	// AND WHAT VALIDATE DID NOT CATCH REACHED THE DISK. `name` and `retention` are unchecked, so a
	// successful PUT wrote `name: ""` and `retention: null`. The file self-heals at the next Load;
	// THE RUNNING PROCESS DOES NOT. `s.cfg` held an empty name until a restart, and GET /api/config
	// reported it — on the field DELETE /api/config/storage/{name} addresses by.
	//
	// `old` WAS NOT RELIABLY RESOLVED EITHER, which is the same defect from the other end: it is
	// s.cfg, which is post-Parse after a load but whatever Replace was handed after a PUT. So "the
	// live snapshot is resolved" held only because Validate happened to refuse most unresolved
	// documents — an invariant resting on a coincidence. It now rests on this line.
	//
	// THIS DOES NOT MAKE THE ADD PATH SOFTER, AND IT MUST NOT (ruled, quince#754). AddStorage's
	// validateAddition refuses an empty `backend` deliberately — see its comment — and runs BEFORE
	// ResolveStorages, so its strict gate still fires first and this is an idempotent second pass
	// behind it. A full-document replace must mean what the FILE means; a narrow add whose caller
	// has just watched quince probe a path and name a concrete backend is a place where an omission
	// really is a client bug. The asymmetry is preserved by ORDERING rather than by a flag, so
	// nothing has to remember which door a document came through. contracts §6 says why.
	c.Storage = ResolveStorages(c.Storage)
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
	// THE FILE NOW CARRIES ONLY WHAT WAS SET (qn.6j, quince#728; Operator ruling 2026-08-08, canon
	// in `docs/quince.stack.md` D12). Read under `mu` here rather than at the swap below: the whole
	// write is serialised by `writeMu`, so this is the same `old` the swap would have seen, and the
	// marshaller needs it BEFORE the write rather than after.
	s.mu.RLock()
	old, wasDeclared := s.cfg, s.declared
	s.mu.RUnlock()

	// Clause 1 ∪ clause 2 of the write rule: what the file already said, plus what this write
	// changes. See changedKeys for why the second half is not optional.
	changed, err := changedKeys(old, c)
	if err != nil {
		return nil, nil, err
	}
	// …and nothing belonging to a storage this document no longer has. See forStorages.
	declared := wasDeclared.union(changed).forStorages(c.Storage)

	data, err := MarshalDeclared(c, declared)
	if err != nil {
		return nil, nil, err
	}

	// THE WRITE VERIFIES ITSELF, AND DEGRADES LOUDLY (spec rung-ruled decision 3, confirmed at
	// review of quince#753). Re-parse the bytes about to be written and compare them to the
	// document in hand; on a mismatch, write the FULL document instead and say so.
	//
	// IT IS A FALLBACK, SO IT IS ADMISSIBLE ONLY BECAUSE IT IS SURFACED — which is exactly the test
	// `no silent caps or fallbacks` sets. What it buys is that a future defect in the pruning
	// degrades into a fat file plus a visible warning, rather than into a config the daemon will not
	// start on (quince#683's class, which this project has already paid for once).
	//
	// IT COULD NOT HAVE SHIPPED BEFORE quince#754. The comparison is against a RESOLVED re-parse, and
	// until `replaceLocked` resolved its input the held document was not resolved on the `PUT` path —
	// so every partial PUT would have mismatched and the fallback would have fired on the happy path
	// of an ordinary client. A guard that cries during normal use is worse than no guard, because it
	// teaches the reader to ignore it.
	//
	// THE DEGRADATION IS STICKY ACROSS A RESTART, AND THEN IT IS SILENT. Within this process it is
	// well-behaved: `s.declared` keeps the PRUNED set, so the next save re-attempts the short form
	// and warns again if it still fails. A restart ends that. The fallback wrote the full document,
	// `Load` derives `declared` FROM THE FILE, so now every key is declared — the tidy write and the
	// full write become the same bytes, the guard passes, and no warning is ever emitted again.
	// Measured at this head: 639 bytes written by the fallback, 639 after the restart, guard silent.
	//
	// **The file then stays long forever and nothing says so**, which is the one place this mechanism
	// fails `no silent caps or fallbacks` — after a restart the degraded mode is in neither the UI
	// nor the logs. It is not a correctness bug: the document is self-consistent, and by the
	// derivation rule those keys really are set now. **Recovery is a hand-trim** — delete the keys
	// you did not write and save again. If you are reading this because a `config.yml` is
	// mysteriously long, that is what happened, and nothing else would have told you.
	//
	// One in-memory parse per save, on a path already doing `fsync` + `rename`.
	var guardWarnings []Warning
	if back, _, _, perr := Parse(data); perr != nil || !SameConfig(back, c) {
		full, ferr := Marshal(c)
		if ferr != nil {
			return nil, nil, ferr
		}
		lost := lostPaths(c, back, perr)
		s.log.Error("config: the tidy write did not round-trip — writing the full document instead",
			"path", s.path, "parse_error", perr, "differing", lost)
		data = full
		guardWarnings = []Warning{{
			Path: "",
			Message: "your configuration was saved correctly, but quince could not write it in the " +
				"short form and wrote every key instead — the file is larger than it needs to be and " +
				"nothing else is wrong. It will STAY long, including after a restart, until you trim " +
				"the keys you did not write and save again. Differing: " + lost,
		}}
	}

	if err := AtomicWrite(s.path, data); err != nil {
		return nil, nil, err
	}
	mtime := ""
	if info, err := os.Stat(s.path); err == nil {
		mtime = info.ModTime().UTC().Format(time.RFC3339)
	}
	s.mu.Lock()
	s.cfg = c
	// THE DECLARED SET MOVES WITH THE FILE, and this is the assignment the field comment promised
	// when it said the write half was PR 4's. Without it the next save re-inflates the file this one
	// tidied: `s.declared` would still describe the document as it was READ, and every key this
	// write added would look undeclared again.
	s.declared = declared
	s.warnings = nil // a valid structured replace clears prior file warnings
	// AND SO DOES THE DISCARD RECORD (quince#852). It says the file ON DISK could not be read; the
	// bytes just written were built and validated by this process, so the statement has stopped
	// being true. Without this, a config repaired through `PUT /api/config` would leave `AddStorage`
	// refusing for the lifetime of the process — a guard that cannot be cleared by fixing the thing
	// it guards against.
	//
	// BOTH FIELDS, and the condition matters more than the detail: clearing only `loadErrs` would
	// leave `discarded` true forever on the two paths that never set errors in the first place.
	s.loadErrs = nil
	s.discarded = false
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
	// The guard's warning rides out with the appliers' — both describe the gap between what was
	// asked for and what the running system has, which is a property of THIS response rather than of
	// the stored state. Same reason the applier warnings are never stored.
	return nil, append(guardWarnings, s.notify(old, c)...), nil
}

// FileText returns the bytes currently on disk, for the Settings panel's "Current configuration"
// preview (contracts §6; Operator ruling 2026-08-09 on quince#728).
//
// IT READS AT REQUEST TIME AND IS NEVER CACHED, which is the ruling's own condition rather than an
// implementation choice. There is no reload path — `Load` runs at construction and nothing re-reads
// the file (quince#727, post-`v0.1`) — so a cached copy of what quince last WROTE goes stale the
// moment somebody hand-edits `config.yml`. The panel sits under the subtitle *"You can edit the file
// by hand instead"*, so showing a document that is not the file would contradict the sentence next
// to it.
//
// IT CAN THEREFORE SHOW A FILE QUINCE HAS NOT ADOPTED, and that is the point rather than a wart.
// After a bad hand-edit the running configuration is still last-good (`OK: false`) while the file on
// disk is the broken one — which is exactly when a user needs to see what is on disk. The response
// already carries `warnings` and `source` to say so.
//
// An unreadable or absent file is "" and no error: a fresh install has no file yet, and the panel's
// job is to show what is there rather than to diagnose. `source.mtime` is empty in that case, which
// is how a client already distinguishes "not written yet".
func (s *Service) FileText() string {
	s.mu.RLock()
	path := s.path
	s.mu.RUnlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

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
		if f.Type.Kind() == reflect.Struct {
			if sub, ok := v.(map[string]any); ok {
				warnings = append(warnings, unknownKeys(sub, f.Type, prefix+k+".")...)
			}
		}
	}
	return warnings
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
	return Loaded{Config: cfg, Warnings: warnings, Source: src, OK: true}
}

// Service owns the live config and serves GET/PUT /api/config. It is safe for concurrent
// use. Restart-required semantics this rung: a valid PUT updates the in-memory snapshot
// (so GET reflects it) and the file, but no subsystem consumes config live until qn.2+.
type Service struct {
	mu       sync.RWMutex
	path     string
	log      *slog.Logger
	cfg      Config
	warnings []Warning
	source   Source
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

// Replace validates a full-document config, writes it canonically, and updates the live
// snapshot. Returns validation errors (→ 422) or a write error.
func (s *Service) Replace(c Config) ([]wire.ConfigError, error) {
	if errs := Validate(c); len(errs) > 0 {
		return errs, nil
	}
	data, err := Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := AtomicWrite(s.path, data); err != nil {
		return nil, err
	}
	mtime := ""
	if info, err := os.Stat(s.path); err == nil {
		mtime = info.ModTime().UTC().Format(time.RFC3339)
	}
	s.mu.Lock()
	s.cfg = c
	s.warnings = nil // a valid structured replace clears prior file warnings
	s.source = Source{Path: s.path, Mtime: mtime}
	s.mu.Unlock()
	return nil, nil
}

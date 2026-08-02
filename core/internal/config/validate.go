package config

import (
	"fmt"
	"path/filepath"

	"github.com/novkostya/quince/core/internal/wire"
)

// Validate checks enums and ranges, returning one wire.ConfigError per problem (contracts
// §1: PUT returns 422 {errors: [{path, message}]}). An empty slice means valid.
func Validate(c Config) []wire.ConfigError {
	var errs []wire.ConfigError
	add := func(path, msg string) { errs = append(errs, wire.ConfigError{Path: path, Message: msg}) }

	if !oneOf(c.Backup.Transport, "auto", "usb", "wifi") {
		add("backup.transport", enumMsg(c.Backup.Transport, "auto", "usb", "wifi"))
	}
	validateStorages(c.Storage, add)
	if c.Sessions.TTLMinutes <= 0 {
		add("sessions.ttl_minutes", "must be > 0")
	}
	if c.Automation.StalenessDays < 0 {
		add("automation.staleness_days", "must be >= 0")
	}
	if c.Automation.ReminderCooldownHours < 0 {
		add("automation.reminder_cooldown_hours", "must be >= 0")
	}
	if !oneOf(c.UI.Theme, "system", "light", "dark") {
		add("ui.theme", enumMsg(c.UI.Theme, "system", "light", "dark"))
	}
	return errs
}

// validateStorages checks the declared storage list (qn.6c story 1). `storage:` IS the list
// (quince#473), so every path below reads `storage[i]`.
//
// It deliberately does NOT report an absent or empty list. That is the refusal-to-start case, and
// it does not belong here: Load() DISCARDS a config that fails Validate and falls back to
// Default() with OK:false, so expressing "no storages" as a validation error would produce a
// daemon running on defaults with no storage and no error — the silent zero-storage start that
// looks healthy, which is the one outcome gap 3's ruling forbids. The refusal is a separate,
// fatal check on the serve path (StorageRequirement). Everything below is a well-formedness
// problem in a list the user DID declare, which is what 422 is for.
//
// It runs AFTER ResolveStorages, which is why name is never empty here and a lone storage is
// already default. Both were REQUIRED keys until quince#504: the 2026-08-01 ruling made `name`
// optional (defaulting to the path) and `default` implied on a list of one, and it had been ruled
// and never built — `- path: /backups` failed with two errors while canon documented both as
// required, so code and canon agreed with each other and both disagreed with the ruling.
func validateStorages(storages *[]StorageEntry, add func(path, msg string)) {
	if storages == nil {
		return
	}
	seenName := map[string]int{}
	seenPath := map[string]int{}
	defaults := 0
	for i, s := range *storages {
		at := fmt.Sprintf("storage[%d]", i)
		// Name is defaulted from Path, so an empty one here means the path was empty too — which
		// its own error already reports. Reporting both would name two problems for one mistake.
		if s.Name != "" {
			if prev, dup := seenName[s.Name]; dup {
				add(at+".name", fmt.Sprintf("duplicate name %q (also storage[%d]); names must be unique because they key a storage's DB row", s.Name, prev))
			} else {
				seenName[s.Name] = i
			}
		}
		switch {
		case s.Path == "":
			add(at+".path", "must not be empty")
		case !filepath.IsAbs(s.Path):
			add(at+".path", fmt.Sprintf("must be an absolute path, got %q", s.Path))
		default:
			clean := filepath.Clean(s.Path)
			if prev, dup := seenPath[clean]; dup {
				add(at+".path", fmt.Sprintf("duplicate path %q (also storage[%d]); two storages at one path would each claim the other's identity marker", s.Path, prev))
			} else {
				seenPath[clean] = i
			}
		}
		if !oneOf(s.Backend, "auto", "zfs", "reflink", "hardlink", "copy") {
			add(at+".backend", enumMsg(s.Backend, "auto", "zfs", "reflink", "hardlink", "copy"))
		}
		if !oneOf(s.ZFS.Mode, "exec", "hook") {
			add(at+".zfs.mode", enumMsg(s.ZFS.Mode, "exec", "hook"))
		}
		if !oneOf(s.ZFS.Seed, "auto", "reflink", "copy") {
			add(at+".zfs.seed", enumMsg(s.ZFS.Seed, "auto", "reflink", "copy"))
		}
		if r := s.Retention; r != nil {
			if r.KeepRecent < 0 {
				add(at+".retention.keep_recent", "must be >= 0")
			}
			if r.KeepDaily < 0 {
				add(at+".retention.keep_daily", "must be >= 0")
			}
			if r.KeepWeekly < 0 {
				add(at+".retention.keep_weekly", "must be >= 0")
			}
		}
		if s.Default {
			defaults++
		}
	}
	// A LONE STORAGE IS ALREADY MARKED DEFAULT by ResolveStorages, so `defaults == 0` here can
	// only mean several storages and none chosen. That stays an error rather than a pick: order is
	// not intent, and a silent pick is the class this rung exists to remove.
	if n := len(*storages); n > 0 {
		switch {
		case defaults == 0:
			add("storage", "exactly one storage must be marked `default: true` — a backup that names no storage resolves to it, and there is no sane guess. It is implied only when there is exactly one storage")
		case defaults > 1:
			add("storage", fmt.Sprintf("%d storages are marked `default: true`; exactly one may be", defaults))
		}
	}
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func enumMsg(got string, allowed ...string) string {
	return fmt.Sprintf("invalid value %q; must be one of %v", got, allowed)
}

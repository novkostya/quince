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
	if !oneOf(c.Storage.Backend, "auto", "zfs", "reflink", "hardlink", "copy") {
		add("storage.backend", enumMsg(c.Storage.Backend, "auto", "zfs", "reflink", "hardlink", "copy"))
	}
	if !oneOf(c.Storage.ZFS.Mode, "exec", "hook") {
		add("storage.zfs.mode", enumMsg(c.Storage.ZFS.Mode, "exec", "hook"))
	}
	if !oneOf(c.Storage.ZFS.Seed, "auto", "reflink", "copy") {
		add("storage.zfs.seed", enumMsg(c.Storage.ZFS.Seed, "auto", "reflink", "copy"))
	}
	if c.Storage.Retention.KeepRecent < 0 {
		add("storage.retention.keep_recent", "must be >= 0")
	}
	if c.Storage.Retention.KeepDaily < 0 {
		add("storage.retention.keep_daily", "must be >= 0")
	}
	if c.Storage.Retention.KeepWeekly < 0 {
		add("storage.retention.keep_weekly", "must be >= 0")
	}
	validateStorages(c.Storage.Storages, add)
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

// validateStorages checks the declared storage list (qn.6c story 1).
//
// It deliberately does NOT report an absent or empty list. That is the refusal-to-start case, and
// it does not belong here: Load() DISCARDS a config that fails Validate and falls back to
// Default() with OK:false, so expressing "no storages" as a validation error would produce a
// daemon running on defaults with no storage and no error — the silent zero-storage start that
// looks healthy, which is the one outcome gap 3's ruling forbids. The refusal is a separate,
// fatal check on the serve path (StorageRequirement). Everything below is a well-formedness
// problem in a list the user DID declare, which is what 422 is for.
func validateStorages(storages *[]StorageEntry, add func(path, msg string)) {
	if storages == nil {
		return
	}
	seenName := map[string]int{}
	seenPath := map[string]int{}
	defaults := 0
	for i, s := range *storages {
		at := fmt.Sprintf("storage.storages[%d]", i)
		if s.Name == "" {
			add(at+".name", "must not be empty — the name is the stable identity of a storage across replug, where the path is not")
		} else if prev, dup := seenName[s.Name]; dup {
			add(at+".name", fmt.Sprintf("duplicate name %q (also storage.storages[%d]); names must be unique because they key a storage's DB row", s.Name, prev))
		} else {
			seenName[s.Name] = i
		}
		switch {
		case s.Path == "":
			add(at+".path", "must not be empty")
		case !filepath.IsAbs(s.Path):
			add(at+".path", fmt.Sprintf("must be an absolute path, got %q", s.Path))
		default:
			clean := filepath.Clean(s.Path)
			if prev, dup := seenPath[clean]; dup {
				add(at+".path", fmt.Sprintf("duplicate path %q (also storage.storages[%d]); two storages at one path would each claim the other's identity marker", s.Path, prev))
			} else {
				seenPath[clean] = i
			}
		}
		if s.Default {
			defaults++
		}
	}
	if n := len(*storages); n > 0 {
		switch {
		case defaults == 0:
			add("storage.storages", "exactly one storage must be marked `default: true` — a backup that names no storage resolves to it, and there is no sane guess")
		case defaults > 1:
			add("storage.storages", fmt.Sprintf("%d storages are marked `default: true`; exactly one may be", defaults))
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

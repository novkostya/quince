package config

import (
	"fmt"

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

package config

import (
	"fmt"
	"path/filepath"

	"github.com/novkostya/quince/core/internal/muxaddr"
	"github.com/novkostya/quince/core/internal/wire"
)

// Validate checks enums and ranges, returning one wire.ConfigError per problem (contracts
// §1: PUT returns 422 {errors: [{path, message}]}). An empty slice means valid.
func Validate(c Config) []wire.ConfigError {
	var errs []wire.ConfigError
	add := func(path, msg string) { errs = append(errs, wire.ConfigError{Path: path, Message: msg}) }

	// NO `auto` HERE (quince#654). This is the PREFERENCE enum — which transport wins when a device
	// is present on both — and `auto` would mean "prefer whatever is already preferred". `auto`
	// stays legal as a REQUEST transport; it is not validated here because it arrives on
	// POST /api/jobs rather than in config.yml.
	if !oneOf(c.Backup.PreferredTransport, "usb", "wifi") {
		add("backup.preferred_transport", enumMsg(c.Backup.PreferredTransport, "usb", "wifi"))
	}
	validateStorages(c.Storage, add)
	validateTLS(c.TLS, add)
	validateDevices(c.Devices, add)
	// `>= 0`, NOT `> 0`, and the difference IS the feature: 0 is how the schedule is turned off
	// (qn.6i). A negative is meaningless rather than meaningful-and-off, so it is refused.
	if c.Reconcile.IntervalMinutes < 0 {
		add("reconcile.interval_minutes", "must be >= 0 (0 disables the scheduled pass)")
	}
	if c.Notifications.StalenessDays < 0 {
		add("notifications.staleness_days", "must be >= 0")
	}
	if c.Notifications.ReminderCooldownHours < 0 {
		add("notifications.reminder_cooldown_hours", "must be >= 0")
	}
	if c.Notifications.OverdueDays < 0 {
		add("notifications.overdue_days", "must be >= 0")
	}
	// OVERDUE MUST NOT PRECEDE STALE. The reminder track ranks by these two, so an inverted pair makes
	// every first reminder a `backup_overdue` — a device one day past its threshold greeted as a
	// reproach rather than invited. Refused rather than silently clamped: a clamp would honour
	// neither number and say nothing, which is the *no silent caps* failure inside a validator.
	//
	// Only checked when both are individually sane, so one mistake produces one error.
	if c.Notifications.OverdueDays >= 0 && c.Notifications.StalenessDays >= 0 &&
		c.Notifications.OverdueDays < c.Notifications.StalenessDays {
		add("notifications.overdue_days", fmt.Sprintf(
			"must be >= notifications.staleness_days (%d) — a device cannot be overdue before it is stale",
			c.Notifications.StalenessDays))
	}
	if !oneOf(c.UI.Theme, "system", "light", "dark") {
		add("ui.theme", enumMsg(c.UI.Theme, "system", "light", "dark"))
	}
	return errs
}

// validateDevices checks that each muxer address is WELL-FORMED and nothing else (qn.6p D3) —
// the same division validateTLS draws, for the same reason.
//
// WELL-FORMEDNESS ONLY, and the boundary is load-bearing. Whether anything ANSWERS at the address
// is not checked here: an external muxer may legitimately be down at the moment a config is
// written, and refusing the write would make quince unconfigurable exactly when an operator is
// trying to fix it. Reachability is reported by /api/health, which probes.
//
// THIS IS WHAT REJECTS A BAD `PUT`, and it is deliberately not the only guard. Load() DISCARDS a
// config that fails Validate and falls back to Default(), so on the FILE path an unparseable
// address would silently become the default muxer addresses. buildLiveStack therefore parses
// again and refuses to start. Two checks, because they catch two different failures: this one
// answers the user typing into the UI, that one stops a typo in config.yml being ignored.
func validateDevices(d DevicesConfig, add func(path, msg string)) {
	// `manage_muxer: true` IS NOT CHECKED HERE, and that is the same ruling `tls:` records two
	// screens up (architect, quince#1059). A validation error DISCARDS the config in favour of
	// Default(), and Default() has no storage — so refusing this key here would land an operator
	// with a working all-in-one install on *"Add your first storage"*, with the real reason in
	// `GET /api/config` warnings, which quince#849 measured as rendered by no surface a user can
	// reach. The cause is one line in their config.yml and quince knows it.
	//
	// It is a FATAL SERVE-PATH check instead: CheckMuxerProfile, called from buildLiveStack.
	for _, f := range []struct{ path, value string }{
		{"devices.usbmuxd_socket", d.UsbmuxdSocket},
		{"devices.netmuxd_addr", d.NetmuxdAddr},
	} {
		if _, err := muxaddr.Parse(f.value); err != nil {
			add(f.path, err.Error())
		}
	}
}

// validateTLS checks the `tls:` pair for well-formedness and nothing else (qn.6f story 3).
//
// Half a pair is the only thing wrong here that is knowable from the values alone: it can
// only be a mistake, since neither file does anything without the other, and left
// unreported it reads as "TLS is off" — the operator writes a cert_file, restarts, and
// gets plain HTTP with no complaint.
//
// EVERYTHING ELSE ABOUT THE CERTIFICATE IS DELIBERATELY NOT CHECKED HERE, for the reason
// validateStorages gives below and this rung's spec calls its load-bearing measurement:
// Load() discards a config that fails Validate and returns Default(), which has no TLS, so
// a certificate error raised here would produce a daemon serving plain HTTP to somebody who
// asked for HTTPS and saw only a warning banner. Unreadable, malformed and mismatched are a
// FATAL serve-path check (slice 4), in the shape StorageRequirement already established.
func validateTLS(t TLSConfig, add func(path, msg string)) {
	switch {
	case t.CertFile != "" && t.KeyFile == "":
		add("tls.key_file", "required when tls.cert_file is set — a certificate cannot be served without its key")
	case t.KeyFile != "" && t.CertFile == "":
		add("tls.cert_file", "required when tls.key_file is set — a key alone serves nothing")
	}
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
		// ONE legal value since `exec` was removed (quince#697) — see ZFSConfig.Mode for why the key
		// outlived its second value. A config carrying `mode: exec` is REFUSED here, by path, rather
		// than ignored.
		if !oneOf(s.ZFS.Mode, "hook") {
			add(at+".zfs.mode", enumMsg(s.ZFS.Mode, "hook"))
		}
		if !oneOf(s.ZFS.Seed, "auto", "reflink", "copy") {
			add(at+".zfs.seed", enumMsg(s.ZFS.Seed, "auto", "reflink", "copy"))
		}
		// `hook_cmd` IS REFUSED BY NAME, AND THE MESSAGE NAMES ITS SUCCESSORS. Operator ruling,
		// relayed at https://github.com/novkostya/quince/issues/818#issuecomment-5245496176 — cite
		// the URL rather than a date, which is the relay's own instruction and which this comment
		// got wrong once (it said 2026-08-12; the relay was posted 2026-08-10 under a 2026-08-11
		// headline, so no date is a citation here).
		//
		// Same shape as `mode: exec` above and for the same reason: the field is kept so this can be
		// a refusal against the exact path rather than an "unknown key (ignored)" on the one key
		// every existing zfs install has set.
		//
		// `qn.6g`: a remedy the user cannot follow is the same defect as a silent failure. So this
		// does not say "retired" — it says what to write instead.
		if s.ZFS.HookCmd != "" {
			add(at+".zfs.hook_cmd", "retired in favour of `ssh_user`, `ssh_host`, `ssh_port` and "+
				"`ssh_key` — quince composes the ssh command itself now. Replace the whole key: "+
				"`ssh_user: <the helper's user>` and `ssh_host: <the ZFS host>`, plus `ssh_port` "+
				"and `ssh_key` only if they are not 22 and the key quince derives for this "+
				"storage's parent dataset under "+DefaultZFSKeyDir)
		}
		// A MISSING TRANSPORT IS NOT VALIDATED HERE, and that is the same layering decision the
		// parent-dataset check already records in `storagereq.go`: `Load` DISCARDS a config that
		// fails `Validate` and falls back to `Default()`, so refusing here would trade a refusal
		// naming the storage for a daemon running on defaults with no storage at all — quince#508's
		// defect in a new guise. `CheckStorageBackendErrors` returns the errors and writes nothing,
		// which is where a *missing configuration* belongs.
		//
		// A MALFORMED PORT IS DIFFERENT and stays here: it is a bad VALUE, the same class as the
		// enums above, and there is no coherent document containing one.
		if p := s.ZFS.SSHPort; p < 0 || p > 65535 {
			add(at+".zfs.ssh_port", fmt.Sprintf("invalid port %d; must be 1-65535 (or unset for %d)", p, DefaultZFSSSHPort))
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

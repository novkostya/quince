// Package config owns the two layers of quince configuration (contracts §6, stack D12):
// the bootstrap environment (deployment topology only) and /data/config.yml (the single
// source of truth for everything else). This rung ships the load-bearing core — typed
// schema v0 + defaults, validation, atomic canonical writes, last-good-on-invalid, and
// GET/PUT /api/config — but NOT file-watch live reload or generated doc-comments, which
// are staged to qn.6. The file-first, no-secrets, no-UI-only-state contract binds now.
package config

// Config is schema v0 (contracts §6). Field declaration order IS the canonical key order
// used by Marshal — keep it aligned with the documented YAML. qn.6 swaps Marshal for a
// yaml.Node encoder that also emits generated doc-comments; the ordering hook is here now.
type Config struct {
	Backup     BackupConfig     `yaml:"backup" json:"backup"`
	Storage    StorageConfig    `yaml:"storage" json:"storage"`
	Devices    DevicesConfig    `yaml:"devices" json:"devices"`
	Sessions   SessionsConfig   `yaml:"sessions" json:"sessions"`
	Automation AutomationConfig `yaml:"automation" json:"automation"`
	UI         UIConfig         `yaml:"ui" json:"ui"`
}

// BackupConfig is the `backup:` section.
type BackupConfig struct {
	Transport         string `yaml:"transport" json:"transport"` // auto | usb | wifi
	RequireEncryption bool   `yaml:"require_encryption" json:"require_encryption"`
}

// StorageConfig is the `storage:` section.
type StorageConfig struct {
	Backend   string          `yaml:"backend" json:"backend"` // auto | zfs | reflink | hardlink | copy
	ZFS       ZFSConfig       `yaml:"zfs" json:"zfs"`
	Retention RetentionConfig `yaml:"retention" json:"retention"`
}

// ZFSConfig is `storage.zfs:`.
type ZFSConfig struct {
	ParentDataset string `yaml:"parent_dataset" json:"parent_dataset"`
	Mode          string `yaml:"mode" json:"mode"` // exec | hook
	HookCmd       string `yaml:"hook_cmd" json:"hook_cmd"`
	Mirror        string `yaml:"mirror" json:"mirror"` // auto | reflink | hardlink | copy
}

// RetentionConfig is `storage.retention:`.
type RetentionConfig struct {
	KeepRecent int `yaml:"keep_recent" json:"keep_recent"`
	KeepDaily  int `yaml:"keep_daily" json:"keep_daily"`
	KeepWeekly int `yaml:"keep_weekly" json:"keep_weekly"`
}

// DevicesConfig is the `devices:` section (muxer supervision + sockets, stack D2). Field
// order is the canonical YAML key order (contracts §6): manage_muxer first.
type DevicesConfig struct {
	// ManageMuxer true (SIMPLE profile) = quince owns the lifecycle of EVERY muxer daemon it is
	// configured to reach (qn.4c): usbmuxd when UsbmuxdSocket is set, netmuxd when NetmuxdAddr is
	// set — each a supervised subprocess, restart w/ backoff; each refuses loudly at startup if
	// its address is already served (no silent adoption). false (HARDENED/external) = quince only
	// dials both, and reports them as `external` in /api/health. ONE flag governs both daemons on
	// purpose (D12 config tidiness): the mixed topology still degrades honestly through
	// refuse-loudly. Applied at process start; live re-supervision on an edit is qn.7.
	ManageMuxer bool `yaml:"manage_muxer" json:"manage_muxer"`
	// UsbmuxdSocket is where the USB muxer listens — authoritative: a managed usbmuxd is started
	// with `-S <this>`, and POST /api/devices/rescan restarts THIS daemon (USB hotplug is what
	// rescan exists for).
	UsbmuxdSocket string `yaml:"usbmuxd_socket" json:"usbmuxd_socket"`
	// NetmuxdAddr is the Wi-Fi muxer's host:port — authoritative: a managed netmuxd is started
	// with `--host/--port` from it (plus a private --socket-path, since netmuxd would otherwise
	// delete and rebind the usbmuxd socket, and --disable-usb, since usbmuxd is the USB anchor
	// until qn.7's audition). Empty = no Wi-Fi muxer at all.
	NetmuxdAddr string `yaml:"netmuxd_addr" json:"netmuxd_addr"`
}

// SessionsConfig is the `sessions:` section (vault-unlock TTL — NOT the admin cookie TTL,
// which has no config key in schema v0; see auth defaults).
type SessionsConfig struct {
	TTLMinutes int `yaml:"ttl_minutes" json:"ttl_minutes"`
}

// AutomationConfig is the `automation:` section (assisted-backup policy, consumed in qn.12).
type AutomationConfig struct {
	StalenessDays         int `yaml:"staleness_days" json:"staleness_days"`
	ReminderCooldownHours int `yaml:"reminder_cooldown_hours" json:"reminder_cooldown_hours"`
}

// UIConfig is the `ui:` section.
type UIConfig struct {
	Theme string `yaml:"theme" json:"theme"` // system | light | dark
}

// Default returns schema v0 with every documented default filled (contracts §6). Missing
// keys in a loaded file fall back to these.
func Default() Config {
	return Config{
		Backup: BackupConfig{
			Transport:         "auto",
			RequireEncryption: true,
		},
		Storage: StorageConfig{
			Backend: "auto",
			ZFS: ZFSConfig{
				Mode:   "exec",
				Mirror: "auto",
			},
			Retention: RetentionConfig{
				KeepRecent: 10,
				KeepDaily:  30,
				KeepWeekly: 12,
			},
		},
		Devices: DevicesConfig{
			ManageMuxer:   true,
			UsbmuxdSocket: "/var/run/usbmuxd",
			NetmuxdAddr:   "127.0.0.1:27015",
		},
		Sessions: SessionsConfig{
			TTLMinutes: 30,
		},
		Automation: AutomationConfig{
			StalenessDays:         3,
			ReminderCooldownHours: 24,
		},
		UI: UIConfig{
			Theme: "system",
		},
	}
}

// Warning is a non-fatal configuration issue surfaced to the UI and logs (unknown env
// var, unknown config key, non-writable dir, or a validation failure rendered for the
// GET /api/config banner). Path is the env var or dotted config path; Message is
// human-readable.
type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

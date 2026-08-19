// Package config owns the two layers of quince configuration (contracts §6, stack D12):
// the bootstrap environment (deployment topology only) and /data/config.yml (the single
// source of truth for everything else). This rung ships the load-bearing core — typed
// schema v0 + defaults, validation, atomic canonical writes, last-good-on-invalid, and
// GET/PUT /api/config — but NOT file-watch live reload, which is post-v0.1 and tracked by
// quince#1094. The file-first, no-secrets, no-UI-only-state contract binds now.
//
// GENERATED DOC-COMMENTS ARE CANCELLED AND ARE NOT COMING (Operator, 2026-08-08 — quince#728;
// D12 records it as cancelled). This sentence named them beside file-watch as *"staged to qn.6"*
// until quince#1095: one clause true, one false, in the line an implementer picking that rung up
// reads first. `config.yml` carries NO generated annotation at all — a file listing every key at
// its default is noise a reader has to filter, and it makes a hand-edit harder to diff, not easier.
package config

// Config is schema v0 (contracts §6). Field declaration order IS the canonical key order
// used by Marshal — keep it aligned with the documented YAML.
//
// THE ORDER IS THE WHOLE OF IT, NOT A HOOK FOR SOMETHING LATER. This said *"qn.6 swaps Marshal for
// a yaml.Node encoder that also emits generated doc-comments; the ordering hook is here now"*, and
// that encoder was CANCELLED (quince#728, quince#1095) — so there is no later pass for this to be a
// hook FOR. Keeping the order aligned with the documented YAML is a live obligation rather than a
// staging step, and it is the only thing producing canonical key order.
type Config struct {
	Backup BackupConfig `yaml:"backup" json:"backup"`
	// Storage IS the list of declared storages (qn.6c, quince#473). A POINTER so an absent
	// `storage:` key stays distinguishable from `storage: []` — no key and declared none want the
	// same refusal for different reasons, and Parse unmarshals over Default(), which would
	// otherwise make absent and zero-value identical.
	Storage *[]StorageEntry `yaml:"storage" json:"storage"`
	// Muxers IS the list of muxers quince dials (qn.6p, quince#1219 A/B/C). It replaces the
	// `devices:` section, whose only remaining keys were two addresses and a flag that moved into
	// `type:` — a section named for something quince never configures, since devices come FROM the
	// muxer.
	//
	// A POINTER, for the reason `storage:` is one, and quince#1219 discovered the second half of it:
	// an absent key and `muxers: []` mean different things — the built-in default, and DELIBERATELY
	// NONE — and Parse unmarshals over Default(), which would otherwise make them identical.
	//
	// AND Default() MUST NOT CARRY THE ENTRY, which is the half `storage:` never had to face: its
	// default list is EMPTY, so the pruner never had to drop one. `MarshalDeclared` keeps a
	// non-empty sequence even when nothing declared it — deliberately, so a fresh install still
	// writes the storage it was given — so a default entry here would be written into every
	// config.yml as a key nobody set, which is exactly what D12 forbids. Absent means absent;
	// ResolvedMuxers() supplies the default at the point of use.
	Muxers *[]MuxerConfig `yaml:"muxers" json:"muxers"`
	// Devices is the RETIRED `devices:` section, present only so that finding one is a loud
	// refusal instead of a silent drop. See LegacyDevices. Never served (`json:"-"`), never
	// written back (`omitempty`, so a config that has none does not gain a null key).
	Devices       *LegacyDevices      `yaml:"devices,omitempty" json:"-"`
	TLS           TLSConfig           `yaml:"tls" json:"tls"`
	Sessions      SessionsConfig      `yaml:"sessions" json:"sessions"`
	Notifications NotificationsConfig `yaml:"notifications" json:"notifications"`
	// Reconcile is the qn.6i scheduled reconciliation pass. A TOP-LEVEL SECTION because there is
	// nowhere else for it: `storage:` is a LIST, not a section, so a daemon-wide key placed there
	// would have to become a property of every declared storage — a different setting with a
	// different meaning. `notifications:` — `automation:` when this was written — was the other
	// candidate and was qn.12's declared debt, so putting a LIVE key in it would have made that
	// row false. The debt is discharged and the section is live; the reasoning against sharing it
	// is unchanged, because a reconciliation interval is still not a notification policy.
	Reconcile ReconcileConfig `yaml:"reconcile" json:"reconcile"`
	UI        UIConfig        `yaml:"ui" json:"ui"`
}

// BackupConfig is the `backup:` section.
type BackupConfig struct {
	// PreferredTransport decides which transport a backup uses WHEN THE DEVICE IS PRESENT ON BOTH.
	// `usb` | `wifi`, default `usb`. It is IGNORED when only one transport is available.
	//
	// A PREFERENCE, NEVER A RESTRICTION. A device reachable on one transport is backed up over that
	// transport whatever this says. Part of the ruling rather than a caveat on it: the restriction
	// reading would make a Wi-Fi-only device silently unbackupable through a setting whose name does
	// not say so, and Wi-Fi is the PRIMARY transport under the assisted model.
	//
	// IT WAS `backup.transport`, AND IT DID NOTHING (quince#654, Operator ruling 2026-08-04).
	// Parsed, range-checked, rendered in Settings, read by nobody — so setting it to `usb` still
	// backed up over Wi-Fi. The rename is part of the ruling rather than tidying: `transport: usb`
	// reads as "use USB", the only safe meaning is "prefer USB", and the two would disagree
	// permanently. The evidence that they would is the report that opened the issue — "If I select
	// 'usb' in settings then I won't be able to sync over wifi at all or what?" — which IS that
	// misreading, made by the person with the most context in the project.
	//
	// The rename was free EXACTLY ONCE. quince#401: a renamed key warns `unknown config key
	// (ignored)` without naming its successor, so whoever set it silently loses it. While the key
	// did nothing, nobody lost anything; wiring it under the old name would have ended that.
	//
	// THERE IS NO `auto` HERE, AND THIS IS NOT THE REQUEST ENUM. As a *preference*, `auto` would
	// mean "prefer whatever the engine already prefers" — a third label for `usb`, which is
	// quince#653's defect migrating out of the UI and into config.yml. `auto` stays legal and
	// correct as a REQUEST transport: `Job.transport`, the wire contract, and the CLI's documented
	// `--transport auto`. Two enums sharing two of their values; said out loud because the next
	// reader will try to unify them.
	//
	// The default is `usb` because that is BEHAVIOUR-PRESERVING — `resolveTransport` returns USB
	// today when both are present — and for no other reason. NO CLAIM IS MADE ABOUT SPEED in either
	// direction: the Operator reports Wi-Fi outperforming USB on the soak stand, and nothing here
	// has measured it. If anyone ever does, this default is the thing to revisit.
	PreferredTransport string `yaml:"preferred_transport" json:"preferred_transport"` // usb | wifi
	RequireEncryption  bool   `yaml:"require_encryption" json:"require_encryption"`
}

// StorageEntry is one declared storage under `storage:` (qn.6c, quince#473).
//
// `storage:` IS THE LIST. There is no `storage.storages:`, no global `backend`, `zfs` or
// `retention`, and no inheritance — Operator direction 2026-08-02, five inline comments on
// quince#461, ruled onto quince#500.
//
// THE INHERITANCE IS WHAT WENT, AND IT TOOK A CLASS OF BUG WITH IT. A global block applied to
// every storage, so a second storage on a USB disk got a zfs backend whose parent dataset pointed
// at another pool (quince#458). With every entry fully specified, nothing can bleed from a global
// onto a storage it was never written for — the defect is not guarded against, it is unconstructible.
// `BackendFor`, `ZFSFor` and the `zfs: {}` opt-out idiom went with it, and quince#468 — choosing
// between remedies that named a global — cannot exist without something to inherit from.
//
// `backend: auto` IS STILL LEGAL, AND IT IS NOW LEGAL BY RULING RATHER THAN BY DEFERRAL. The
// 2026-08-02 direction that only a CONCRETE backend may land in the file was ruled ABSORBED, NOT
// REMOVED — Operator, 2026-08-07 on quince#502, built in qn.6e.
//
// So the loader is unchanged and `auto` stays the default. What changed is one layer up: the add
// flow (POST /api/config/storage) refuses an empty or `auto` backend with a 422 rather than
// defaulting it, so quince records the value its probe just showed.
//
// The original reason for keeping it stands, and is why removal was never the cheap option: `auto`
// is the only thing that checks a declaration against the medium. `storage.Select` returns an
// explicit namespace backend WITHOUT probing, so a hand-written guess would be accepted at startup,
// frozen into `quince-storage.json` where the marker is the authority, and fail at SEED TIME. And
// the startup refusal itself teaches `storage:` / `- path: /backups`, which IS `auto`. Do not
// remove it here.
type StorageEntry struct {
	// Name is the stable identity across replug, where a path is not; it keys the DB row.
	//
	// OPTIONAL — it defaults to Path at load (ruled 2026-08-01, quince#504). On a single-storage
	// install `name: backups, path: /backups` says the same thing twice, so the short form is just
	// `- path: /backups`. Multi-storage users still write `usb` and `nas`, which is what the field
	// is for. The defaulting happens HERE, at config load, so nothing downstream learns a new
	// shape and `wire.Storage.Name` stays non-optional.
	Name string `yaml:"name" json:"name"`

	// Path is absolute and unique across entries.
	Path string `yaml:"path" json:"path"`

	// Default marks the storage a backup goes to when none is named.
	//
	// OPTIONAL, and only when there is exactly ONE storage, where it is implied (ruled
	// 2026-08-01). With several, exactly one must carry it: declaring none of several stays an
	// error rather than defaulting to the first, because order is not intent and a silent pick is
	// the class this rung exists to remove.
	Default bool `yaml:"default" json:"default"`

	// Backend is this storage's backend. auto | zfs | reflink | hardlink | copy.
	//
	// Defaults to `auto` at load, which probes the path — see the type comment on why `auto` is
	// still here.
	Backend string `yaml:"backend" json:"backend"`

	// ZFS is this storage's zfs settings. A VALUE, not a pointer: the pointer existed so an entry
	// could tell "inherit the global" from "explicitly empty, opt out". With no global there is
	// nothing to inherit and nothing to opt out of.
	ZFS ZFSConfig `yaml:"zfs" json:"zfs"`

	// Retention is this storage's retention policy.
	//
	// A POINTER, and for a reason the others do not need: absent must differ from zero. `0` is a
	// legal explicit value for every Keep* field, so a value type could not tell "the user did not
	// write retention" from "the user asked to keep none". Absent falls back to CODE defaults,
	// which D12 permits — a setting with a sane default the file need not spell out.
	Retention *RetentionConfig `yaml:"retention" json:"retention"`
}

// DefaultRetention is the policy an entry gets when it declares none.
func DefaultRetention() RetentionConfig {
	return RetentionConfig{KeepRecent: 10, KeepDaily: 30, KeepWeekly: 12}
}

// Resolved returns the entry with every optional field filled: the name defaulted to the path, the
// backend to `auto`, the zfs mode/seed to their defaults, and retention to the code policy.
//
// It is applied at PARSE, not at each read, so exactly one place decides what an omitted key means
// and no consumer has to remember. That is why `Default()` no longer carries a storage block:
// entries do not exist until the file is read, so their defaults cannot be pre-filled into a
// struct the way every other section's are.
func (e StorageEntry) Resolved() StorageEntry {
	if e.Name == "" {
		e.Name = e.Path
	}
	if e.Backend == "" {
		e.Backend = "auto"
	}
	if e.ZFS.Mode == "" {
		e.ZFS.Mode = "hook"
	}
	if e.ZFS.Seed == "" {
		e.ZFS.Seed = "auto"
	}
	if e.Retention == nil {
		r := DefaultRetention()
		e.Retention = &r
	}
	return e
}

// ResolveStorages applies Resolved to every entry and implies `default: true` on a lone storage.
//
// The implication is deliberately narrow — ONE entry only. With several, an absent default is an
// error from Validate, never a pick.
func ResolveStorages(in *[]StorageEntry) *[]StorageEntry {
	if in == nil {
		return nil
	}
	out := make([]StorageEntry, 0, len(*in))
	for _, e := range *in {
		out = append(out, e.Resolved())
	}
	if len(out) == 1 {
		out[0].Default = true
	}
	return &out
}

// ZFSConfig is `storage[].zfs:` — PER ENTRY, not global.
//
// This comment said `storage.zfs:` until quince#818. The shape has been per-entry since `qn.6c`
// (`StorageEntry.ZFS` is a value on each entry) and a global `zfs:` block has not existed since;
// the stale form invited exactly the mistake the plural rung removed.
type ZFSConfig struct {
	ParentDataset string `yaml:"parent_dataset" json:"parent_dataset"`
	// Mode has ONE legal value, `hook`, since the Operator removed `exec` (quince#697, quince#793).
	//
	// THE KEY IS KEPT ON PURPOSE, and the reason is the refusal rather than the key. Deleting the
	// field would make `mode: exec` an UNKNOWN key, and `unknownKeys` says *"unknown config key
	// … (ignored)"* — so the change that removes an unrunnable mode would also downgrade its
	// diagnosis from a refusal to something explicitly ignored, which is the silent-fallback case
	// CLAUDE.md forbids. As a one-value enum, `Validate` answers `invalid value "exec"; must be
	// one of [hook]` against the exact path, which is what an operator needs.
	//
	// Removing the key stays available and gets easier, not harder: it wants a retired-key warning
	// that names its successor, which is quince#401 and its own reviewable claim.
	Mode string `yaml:"mode" json:"mode"` // hook (the only value)
	// HookCmd IS RETIRED AND THE FIELD IS KEPT TO REFUSE IT. Operator ruling, relayed at
	// https://github.com/novkostya/quince/issues/818#issuecomment-5245496176. It carried a free-text
	// argv — `ssh -i /data/keys/zfs … user@host` — which is replaced by the four `ssh_*` keys below.
	//
	// KEPT FOR THE SAME REASON `Mode` IS. Deleting the field makes `hook_cmd:` an UNKNOWN key, and
	// `unknownKeys` says *"unknown config key … (ignored)"* — so removing the transport would
	// downgrade its diagnosis from a refusal naming the successors to something explicitly ignored,
	// on the one key every existing zfs install has set. `Validate` answers against this exact path
	// instead, which is what an operator upgrading actually needs.
	//
	// **A config carrying it is DISCARDED**, not repaired — the migration the ruling chose as the
	// cheapest correct one. quince#852 and quince#849 are what make that safe: the operator meets a
	// screen naming this key and its successors, is told nothing is being backed up, and the add
	// button refuses rather than replacing their file.
	HookCmd string `yaml:"hook_cmd" json:"hook_cmd"`
	// SSHUser / SSHHost / SSHPort / SSHKey ARE THE TRANSPORT, and quince composes the argv from
	// them (quince#818). SSH is the only shape rather than one option among many, because the
	// guarantee hook mode rests on — `command="…quince-zfs-helper"` in `authorized_keys`, which
	// pins the helper regardless of what the client asks for — is an SSH mechanism. Under any other
	// transport the constraint would live only inside the script.
	//
	// THE ARGV IS BUILT, NOT SPLIT. `zfsCLI` used `strings.Fields(hook_cmd)`, a whitespace split of
	// an operator string, so a key path containing a space produced a silently wrong argv. `SSHArgv`
	// returns the array directly and that class is gone.
	SSHUser string `yaml:"ssh_user" json:"ssh_user"`
	SSHHost string `yaml:"ssh_host" json:"ssh_host"`
	// SSHPort defaults to 22, and SSHKey to the key quince derives for this storage's parent dataset
	// (quince#989), so the happy path writes NEITHER into `config.yml` (D12: every setting has a
	// sane default and the file carries only what was set).
	//
	// SSHKey stays settable rather than being hardcoded, and that is not hedging: the path is
	// already settable today — it lives inside `hook_cmd` as `-i /data/keys/zfs` — so removing the
	// ability would be a narrowing dressed as a simplification. An operator who already has a key
	// with a forced command deployed should not have to move it to adopt this form.
	SSHPort int    `yaml:"ssh_port" json:"ssh_port"`
	SSHKey  string `yaml:"ssh_key" json:"ssh_key"`
	// Seed is the in-container strategy for cloning latest/ → working/<udid> at job start (qn.5b;
	// renamed from `mirror` when the reflink moved from commit-time to seed-time). auto | reflink |
	// copy — the hardlink tier is never used for the seed (amendment A: it would alias the
	// committed latest/). In hook mode the host-side `seed` verb does the reflink and this is moot.
	Seed string `yaml:"seed" json:"seed"`
}

// RetentionConfig is `storage.retention:`.
type RetentionConfig struct {
	KeepRecent int `yaml:"keep_recent" json:"keep_recent"`
	KeepDaily  int `yaml:"keep_daily" json:"keep_daily"`
	KeepWeekly int `yaml:"keep_weekly" json:"keep_weekly"`
}

// MuxerConfig is one entry of the top-level `muxers:` list — where quince finds a muxer, and
// (later) who runs it. It REPLACES the `devices:` section entirely (quince#1219 A/B/C, ruled
// 2026-08-18).
//
// `address:` RATHER THAN `socket:`, because either endpoint may be a path or `host:port` —
// `usbmuxd -S` takes `ADDR:PORT | PATH`, measured. `socket:` would re-create the naming lie
// `usbmuxd_socket`/`netmuxd_addr` each carried a paragraph to explain. The grammar is
// `internal/muxaddr`'s and accepts all three forms:
//
//	/run/mux/usbmuxd         a unix socket path
//	UNIX:/run/mux/usbmuxd    the same, in libusbmuxd's own spelling
//	127.0.0.1:27015          TCP
//
// A LIST, BECAUSE THE OLD SHAPE MADE YOU WRITE A THING TWICE TO MEAN ONCE. netmuxd serves USB and
// Wi-Fi over ONE socket, and saying so meant pointing both keys at one address and having
// `distinctEndpoints` collapse them back — machinery that exists only to undo the schema. One
// entry says it once.
type MuxerConfig struct {
	// Address is where this muxer answers. Required: an entry without one configures nothing.
	Address string `yaml:"address" json:"address"`
	// Type is who runs this muxer. `external` (the default, and the ONLY value v0.1 accepts) means
	// somebody else runs it and quince only dials it; `managed` is reserved for the return of the
	// in-container profile and is refused BY NAME on the serve path, because descoped must not
	// reach the operator as malformed.
	//
	// OPTIONAL, DEFAULTING TO `external`, AND THE REASON IS DOCUMENTATION RATHER THAN
	// COMPATIBILITY (ruled, quince#1219 B). The compatibility argument offered for it — *"the
	// alternative is a breaking reshape later"* — was REJECTED as unsound: `type` omitted =
	// `external` is what makes adding `managed` non-breaking, and that rule is equally available
	// whenever the discriminator is introduced. What carries it is that a reader meeting `muxers:`
	// learns the shape HAS a discriminator and that `managed` is planned. Recorded this way
	// deliberately: a ruling taken on a wrong reason is hard to revisit, because the reason is
	// what a future session reads.
	Type string `yaml:"type" json:"type"`
}

// MuxerTypes. `external` is the only one v0.1 honours; `managed` is known BY NAME so that asking
// for it is refused with an explanation rather than reported as a malformed value.
const (
	MuxerExternal = "external"
	MuxerManaged  = "managed"
)

// DefaultMuxerAddress is where quince looks for a muxer when the operator has not said. It is
// libusbmuxd's OWN default — measured from the shipped binary, `-S … Default: /var/run/usbmuxd` —
// so one value serves both *"I ran your sidecar"* and *"I already had a muxer"*, and a compose file
// is enough: nobody hand-edits config.yml before first launch.
const DefaultMuxerAddress = "/var/run/usbmuxd"

// ResolvedMuxers is the muxer list as quince ACTUALLY USES IT: what the operator wrote, or the
// single built-in default when they wrote nothing.
//
// ABSENT AND EMPTY ARE DIFFERENT, and that is the whole reason `muxers:` is a pointer. No key means
// *"I have not thought about this"* → the default. `muxers: []` means *"none"* → none, and quince
// then sees no devices and says so at startup rather than quietly substituting a muxer the operator
// removed on purpose.
//
// THE DEFAULT LIVES HERE RATHER THAN IN Default() so that it is never WRITTEN. `MarshalDeclared`
// keeps a non-empty sequence even when nothing declared it, so a default entry in Default() would
// land in every config.yml as a key nobody set — the opposite of D12's promise that the file
// carries only what the user set.
//
// NOT A REVERSAL OF qn.6c, AND THE TEST IS THE FAILURE MODE rather than the taxonomy (ruled,
// quince#1219). `QUINCE_BACKUPS`' default gave every deployment a WORKING STORAGE while declaring
// nothing — data written somewhere nobody chose, in a deployment that looked healthy: silent,
// durable, expensive to discover late. A defaulted muxer address that is wrong writes NOTHING
// ANYWHERE: no devices appear, which is loud, immediate and self-diagnosing on the first screen a
// user opens. qn.6c objected to a default whose failure is invisible and whose consequence is
// durable; neither property is present here.
func (c Config) ResolvedMuxers() []MuxerConfig {
	if c.Muxers == nil {
		return []MuxerConfig{{Address: DefaultMuxerAddress}}
	}
	return *c.Muxers
}

// LegacyDevices EXISTS ONLY TO CATCH A RETIRED SECTION, and it is a field rather than a lookup
// because absence cannot be detected any other way: config load is a plain `yaml.Unmarshal`, which
// drops unknown keys SILENTLY. This repo has that incident on record at
// `storages_validate_test.go:162`, where a mistyped `pathh:` *"was dropped by yaml.Unmarshal and
// reported by nothing"*.
//
// WITHOUT THIS, RETIRING `devices:` WOULD TAKE AN OPERATOR'S MUXER ADDRESS WITH IT IN SILENCE:
// their `usbmuxd_socket: /run/mux/usbmuxd` would be dropped, quince would start on the default
// `/var/run/usbmuxd`, and every device would vanish with nothing to read. That is the
// no-silent-fallbacks rule applied to a config key, and it is the same argument `manage_muxer`'s
// own comment made for keeping a key that had outlived its profile.
//
// The values are POINTERS so the refusal can quote what was actually written — a message that
// names the address you had and the entry to replace it with is actionable; one that says
// "unknown section" is not (quince#940).
//
// `json:"-"` ON EVERY FIELD: a retired section must never reach the wire. It is read from the file,
// refused, and never served.
type LegacyDevices struct {
	ManageMuxer   *bool   `yaml:"manage_muxer" json:"-"`
	UsbmuxdSocket *string `yaml:"usbmuxd_socket" json:"-"`
	NetmuxdAddr   *string `yaml:"netmuxd_addr" json:"-"`
}

// TLSConfig is the `tls:` section (qn.6f): the certificate quince serves itself, for the
// tier where there is no reverse proxy in front of it.
//
// BOTH EMPTY IS THE DEFAULT AND MEANS TLS IS OFF. That is not a degraded mode needing a
// surfaced warning — it is the correct configuration for the reverse-proxy and
// `tailscale serve` tiers, which are the recommended ones, and for `--demo`. What must
// never happen is a config that ASKS for TLS and silently does not get it, which is why
// the certificate is checked on the serve path and not here (see below).
//
// VALIDATION HERE IS WELL-FORMEDNESS ONLY — one of the pair set is an error, because it
// can only be a mistake. Whether the files exist, parse, and match each other is NOT
// checked by Validate, for exactly the reason validateStorages spells out for `storage:`:
// Load() DISCARDS a config that fails Validate and falls back to Default(), and Default()
// has no TLS. So a certificate error expressed as a validation error would start the
// daemon on defaults and serve PLAIN HTTP to a user who asked for HTTPS — the silent
// downgrade, reached by putting the check in the obvious place. The fatal check lives on
// the serve path beside StorageRequirement, and is slice 4.
type TLSConfig struct {
	// CertFile is the PEM certificate chain quince serves. Empty (with KeyFile) = TLS off.
	CertFile string `yaml:"cert_file" json:"cert_file"`
	// KeyFile is the PEM private key. A PATH, never a key body: secrets never enter
	// config.yml (D12), and this file is world-readable by design.
	KeyFile string `yaml:"key_file" json:"key_file"`
}

// Enabled reports whether the operator asked quince to serve TLS itself. One accessor so
// no caller re-derives it and gets the half-set case wrong; Validate has already rejected
// that case by the time anything can ask.
func (t TLSConfig) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

// SessionsConfig is the `sessions:` section. Admin-session timeouts have no config key in schema
// v0 and are hardcoded; see auth defaults. `ttl_minutes` lived here, was read by nothing, and was
// removed rather than relabelled (quince#656) — a key earns its place by being read, so a vault
// unlock TTL gets minted when something reads it, with the right name and a consumer on the day.
type SessionsConfig struct {

	// AllowInsecureTransport lets a user on a network they trust knowingly serve the session
	// and CSRF cookies over plain http to a non-loopback host. Off by default. Operator
	// ruling 2026-08-02, option (b) (qn.6f, quince#446; design §6 carries the whole thing).
	//
	// It lives under `sessions:` rather than in `tls:` because it governs the SESSION and
	// CSRF cookies, and it applies precisely when there is no TLS to configure.
	//
	// IT RELAXES THE FALLBACK ONLY. `r.TLS != nil` and a BELIEVED `X-Forwarded-Proto: https`
	// keep returning Secure regardless; only the final !isLoopbackHost branch becomes
	// conditional on this flag.
	//
	// "Believed" is load-bearing since quince#555: the header now counts only from an address
	// in QUINCE_TRUSTED_PROXIES (an unset list believes anyone, which is the default and the
	// old behaviour). This comment said *the header can only ever upgrade* survives verbatim,
	// which was true of the COOKIE and false of the two consumers that invert the predicate —
	// the quince#497 refusal and the onboarding check both read it and both fail toward
	// "everything is fine" on an injected header.
	//
	// THE CASE THAT MADE THIS RIGHT RATHER THAN LAZY IS A VPN. Over WireGuard or Tailscale
	// the transport is already encrypted, so TLS inside the tunnel buys nothing and costs a
	// certificate to manage — and quince still broke, for a reason with nothing to do with
	// the threat model. Detection was REJECTED (option (c)): a baseline that switches itself
	// off when the network makes it inconvenient is the thing the baseline exists to prevent.
	//
	// It is a DEGRADED MODE, so it is surfaced and never merely permitted — a startup log
	// line, a config warning, and (owed, not in the PR that added this) a non-dismissible UI
	// banner. Applied at process start: there is no live config reload in schema v0.
	AllowInsecureTransport bool `yaml:"allow_insecure_transport" json:"allow_insecure_transport"`
}

// NotificationsConfig is the `notifications:` section — the assisted-backup notification policy.
//
// IT WAS `automation:`, AND THE RENAME IS PART OF THE RUNG RATHER THAN TIDYING (qn.12, quince#1124).
// Both original keys exist only to answer *notify or not*, so the old name stopped being accurate
// the moment the iOS Shortcut opportunity signal left this rung's scope. Renaming was cheap exactly
// once — while both keys were still inert, read by nobody — and that window closes here, because
// this is the change that gives them consumers.
//
// A hand-written `automation:` block therefore becomes an unknown SECTION. That is handled loudly
// rather than silently: see `renamedSections` in renames.go, which is the half of quince#401 this
// rename had to build.
type NotificationsConfig struct {
	// StalenessDays is how old the last good backup must be before a device is worth a reminder.
	StalenessDays int `yaml:"staleness_days" json:"staleness_days"`
	// ReminderCooldownHours bounds the reminder TRACK, not either kind on it — so escalating from
	// `backup_available` to `backup_overdue` does not reset it and cannot produce a second push for
	// one lapse. That is the structural half of the spec's D5.
	ReminderCooldownHours int `yaml:"reminder_cooldown_hours" json:"reminder_cooldown_hours"`
	// OverdueDays is when a reminder stops being an invitation and becomes a reproach.
	//
	// A DECLARED KEY RATHER THAN A MULTIPLE OF StalenessDays. *Overdue* is a claim about the user's
	// own tolerance, and a derived threshold is a policy nobody can see or change. The alternative
	// considered and rejected was `StalenessDays * 3` with no key (spec D5).
	OverdueDays int `yaml:"overdue_days" json:"overdue_days"`

	// The per-category switches. Flat and global by ruling — the Operator asked for *"at least some
	// simplified version … just global settings by category"* (2026-08-17). They are the DEFAULTS a
	// later per-principal layer puts exceptions on, so nothing here renames when that rung lands.
	BackupAvailable bool `yaml:"backup_available" json:"backup_available"`
	BackupOverdue   bool `yaml:"backup_overdue" json:"backup_overdue"`
	ActionRequired  bool `yaml:"action_required" json:"action_required"`
	// BackupFailed is the fifth kind, added to the frozen four by quince#1124 scope item 4. It
	// carries the failures the USER cannot fix from the phone — storage unreachable, verify failed,
	// commit failed — which `action_required` was telling people to go and unlock a device for.
	BackupFailed bool `yaml:"backup_failed" json:"backup_failed"`
	// BackupCompleted defaults OFF, and that is the one default that is not "on". A push per
	// successful nightly backup is the noise that teaches a person to swipe without reading, which
	// costs the four kinds that matter.
	BackupCompleted bool `yaml:"backup_completed" json:"backup_completed"`
}

// UIConfig is the `ui:` section.
type UIConfig struct {
	Theme string `yaml:"theme" json:"theme"` // system | light | dark
}

// Default returns schema v0 with every documented default filled (contracts §6). Missing
// keys in a loaded file fall back to these.
//
// THERE IS NO STORAGE BLOCK HERE, and that is structural rather than an omission (quince#473).
// Every other section is a fixed set of keys whose defaults can be pre-filled into a struct;
// `storage:` is a LIST whose entries do not exist until the file is read. Per-entry defaults are
// applied by ResolveStorages at parse instead, which is the one place that decides what an omitted
// key means.
func Default() Config {
	return Config{
		Backup: BackupConfig{
			// `usb` preserves today's behaviour: resolveTransport already returns USB when a device
			// is present on both. Not a speed claim — see the field's own comment.
			PreferredTransport: "usb",
			RequireEncryption:  true,
		},
		// NO `Muxers` ENTRY HERE ON PURPOSE — see ResolvedMuxers(). A default in Default() would be
		// WRITTEN into every config.yml, because MarshalDeclared keeps a non-empty sequence even when
		// nothing declared it. Absent means absent; the default is supplied at the point of use.
		Reconcile: ReconcileConfig{
			IntervalMinutes: 360,
		},
		Notifications: NotificationsConfig{
			StalenessDays:         3,
			ReminderCooldownHours: 24,
			OverdueDays:           14,
			BackupAvailable:       true,
			BackupOverdue:         true,
			ActionRequired:        true,
			BackupFailed:          true,
			BackupCompleted:       false,
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

// ReconcileConfig is the `reconcile:` section — the qn.6i scheduled reconciliation pass.
type ReconcileConfig struct {
	// IntervalMinutes is how often a reconciliation pass runs on its own. Default 360 (six hours).
	//
	// `0` DISABLES THE SCHEDULE AND NOTHING ELSE. Startup and storage-added still fire, because those
	// are CORRECTNESS triggers — a registry that has never been scanned, and a disk whose contents
	// nobody has looked at — where the schedule is hygiene: it catches a version deleted or restored
	// under quince's feet. Turning off hygiene is a reasonable thing to want; turning off correctness
	// is not, and one key should not do both.
	//
	// INTEGER MINUTES RATHER THAN A DURATION STRING, following the document rather than expressiveness:
	// `notifications.staleness_days`, `.reminder_cooldown_hours` and `.overdue_days` are
	// all bare numbers with the unit in the name. `6h` would be the first duration string in the file.
	//
	// IT IS LIVE (contracts §6): the runner reads it when it schedules the NEXT pass, so an edit takes
	// effect from the following tick rather than at a restart.
	IntervalMinutes int `yaml:"interval_minutes" json:"interval_minutes"`
}

// Wire types — the TypeScript mirror of docs/contracts.md §2 (and internal/wire in the
// core). snake_case matches the JSON on the wire. Nullable-explicit fields are `T | null`.

export interface Transports {
  usb?: string;
  wifi?: string;
}

export interface LastBackup {
  at: string;
  job_id: string | null; // null = derived from an adopted version (no job record) — contracts §2
  status: string;
}

// Pairing is a SYSTEM capability from the GET /api/devices envelope, not a per-device one
// (qn.6p D7): there is one lockdown directory behind every device.
//
// `reason` is DIAGNOSTIC, not screen copy. It carries a path and an errno from the server, so
// a surface renders its own sentence first and this second, as detail — a Go error string is
// not something a user should have to read to understand what is wrong.
export interface Pairing {
  writable: boolean;
  reason?: string;
}

export interface Device {
  udid: string;
  name: string;
  model: string;
  ios_version: string;
  transports: Transports;
  paired: "yes" | "no" | "unknown";
  backup_encryption: "on" | "off" | "unknown";
  wifi_sync: "on" | "off" | "unknown";
  last_seen: string;
  last_backup: LastBackup | null;
}

export type JobState =
  | "queued"
  | "waiting_for_device"
  | "preflight"
  | "seeding" // qn.6a: cloning latest/ → working/ before the tool starts (contracts §2)
  | "backing_up"
  | "verifying"
  | "committing"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "connection_lost";

// `""` is what a TERMINAL job carries: the other three are claims about a process that is
// running, and a finished job has none (quince#313, contracts §2). The union is widened rather
// than the empty value being smuggled in as a cast — a closed union that the server can violate
// is a type that lies, and this one was already being violated by every failed job.
export type Liveness = "" | "active" | "silent_but_connected" | "suspected_stall";

export interface JobProgress {
  phase: string;
  percent: number | null;
  bytes_done: number;
  bytes_total: number;
  files_received: number;
  liveness: Liveness;
}

export interface JobError {
  code: string;
  message: string;
}

export interface Job {
  id: string;
  udid: string;
  kind: string;
  transport: "usb" | "wifi";
  state: JobState;
  progress: JobProgress;
  started_at: string;
  finished_at: string | null;
  error: JobError | null;
  retry_of: string | null;
  intent_id: string;
  attempt: number;
  // storage_id = the RESOLVED concrete storage this backup was aimed at (qn.6c story 6b),
  // never the word "default". null for jobs that ran before qn.6c, meaning quince did not
  // record where this went — REQUIRED and nullable, not optional: the server has no
  // omitempty, so a `?` would model an omission that cannot happen.
  storage_id: string | null;
  version_id: string | null;
}

export type Backend = "zfs" | "reflink" | "hardlink" | "copy";

export interface Version {
  id: string;
  udid: string;
  backend: Backend;
  zfs_snapshot: string | null;
  browse_root: string;
  created_at: string;
  job_id: string | null;
  kind: "full" | "incremental" | "unknown";
  encrypted: boolean;
  is_latest: boolean;
  structure_verified_at: string | null;
  content_verified_at: string | null;
  // The apparent tree size, and the ONLY size a version carries. A `physical_bytes` shipped beside
  // it until quince#442, where it was measured to be the same walk under a second name — do not
  // reintroduce a second size figure, and do not derive one (contracts §2).
  logical_bytes: number;
  // missing = the artifact is GONE (reconciliation couldn't find it); the row survives so history
  // isn't silently shrunk. Rendered explicitly dead — no size, no Unlock, an "artifact gone — remove?"
  // action on DELETE (qn.6a (cr)).
  //
  // REQUIRED, not optional — same rule as storage_id below: `wire.Version.Missing` is `bool` with
  // no `omitempty`, so the server ALWAYS emits the key. The `?` here justified itself as version
  // skew, a new client against an old server; the UI is EMBEDDED IN THE DAEMON, so client and
  // server ship as one binary and that skew cannot occur in the direction described (quince#460).
  missing: boolean;
  // storage_id = which storage this version lives on (qn.6c). null means NOT YET ATTRIBUTED, and
  // that is TRANSITIONAL — unlike job_id, whose null (= adopted) is permanent and correct. Do not
  // render it as "no storage" and do not substitute a default: it means the server has not worked
  // out which storage this is yet, and it stops meaning that once the storage has an identity
  // marker.
  //
  // REQUIRED, not optional: the Go field carries no `omitempty`, so the key is ALWAYS emitted and
  // nil marshals to null. Writing `?` here would model an omission the server never performs and
  // give consumers THREE states (string | null | undefined) where the wire has two — one state
  // wearing two representations, which is the same confusion this field's null exists to avoid.
  storage_id: string | null;
}

export interface Op {
  id: string;
  udid: string;
  kind: "pair" | "encryption";
  state: "running" | "waiting_for_user" | "succeeded" | "failed";
  message: string;
  error: JobError | null;
}

export interface Session {
  id: string;
  version_id: string;
  expires_at: string;
}

export type AuthState = "needs_setup" | "needs_login" | "authenticated";

export interface AuthStatus {
  state: AuthState;
  csrf_token: string;
}

export interface WSEnvelope {
  type: string;
  ts: string;
  data: unknown;
}

// --- config (schema v0, contracts §6) ---

// StorageEntry is one declared storage under `storage:` — which IS the list (qn.6c, quince#473).
//
// EVERY FIELD IS FULLY SPECIFIED, because there are no globals to inherit from any more. The
// comment here used to read "there is no `backend` field by design: a storage's backend is
// discovered and frozen at its creation moment, never declared" — true of the pre-flatten schema
// and false since quince#506, which made `backend` a per-entry key with `auto` as its default.
export interface StorageEntry {
  name: string;
  path: string;
  default: boolean;
  backend: string; // auto | zfs | reflink | hardlink | copy
  zfs: {
    parent_dataset: string;
    mode: string;
    // `hook_cmd` IS RETIRED and refused by the schema (quince#818). It stays on this type because
    // the daemon still serves the key — the field is kept server-side so a config carrying it gets
    // a refusal naming its successors rather than an "unknown key (ignored)".
    hook_cmd: string;
    ssh_user: string;
    ssh_host: string;
    ssh_port: number;
    ssh_key: string;
    seed: string;
  };
  // Retention is a POINTER in Go, and null here for the same reason: `0` is a legal value for
  // every Keep*, so absent must stay distinguishable from zero. Absent means the code defaults.
  retention: { keep_recent: number; keep_daily: number; keep_weekly: number } | null;
}

export interface Config {
  // `preferred_transport` — usb | wifi. Which transport wins when a device is present on BOTH;
  // IGNORED when only one is available, so it is never a restriction (quince#654).
  //
  // RENAMED FROM `transport` IN THE SAME COMMIT AS THE GO FIELD, and that is load-bearing rather
  // than tidy. quince#493: `PUT /api/config` zeroes any key the client omits, so a TS type still
  // saying `transport` would make every Settings save send a document with no
  // `preferred_transport` — resetting it to the Go zero value on each save, which `Validate` then
  // rejects. The two renames are one atomic change.
  backup: { preferred_transport: string; require_encryption: boolean };
  // `storage` IS THE LIST (qn.6c, quince#473). No wrapper object, no global `backend`, `zfs` or
  // `retention` — every entry carries its own.
  //
  // REQUIRED AND NULLABLE, matching the Go shape exactly: the field carries no `omitempty`, so the
  // key is always emitted and a nil list marshals to null.
  //
  // null is reachable: `--demo` never runs the storage requirement, so a demo config genuinely
  // serves null. That is the state the type must let a client see — a document round-tripped
  // through the UI has to be able to represent a server that has none, rather than the client
  // inventing an empty list and PUTting it back as if declared.
  //
  // THIS TYPE SAID `{ storages, backend, zfs, retention }` UNTIL 2026-08-02, one shape behind the
  // daemon, and the UI crashed on `storage.backend` of a null. `make gates-ui` was green
  // throughout: the type was internally consistent and NOTHING CROSS-CHECKS IT AGAINST THE GO
  // SCHEMA — which is quince#493, filed before this happened and describing it exactly.
  storage: StorageEntry[] | null;
  devices: { usbmuxd_socket: string; netmuxd_addr: string };
  // `tls` MUST be here, and its absence would not have been the harmless kind. PUT /api/config
  // decodes into a zero-valued `config.Config`, so a key the client omits arrives as the Go zero
  // value rather than its default — and for `tls` the zero value is two empty strings, which is
  // TLS OFF. A UI that reconstructed a config document without this field would silently stop
  // quince serving HTTPS on the next save (qn.6f, interface fact 6).
  //
  // `devices.manage_muxer` is missing from this type and has been harmless purely by luck:
  // ConfigEditor spreads a document it FETCHED rather than building one, and nothing enforces
  // that it keeps doing so. Same gap, different blast radius — quince#493.
  tls: { cert_file: string; key_file: string };
  // allow_insecure_transport is a DEGRADED MODE the user opted into (qn.6f slice 8), not a
  // preference: with it on, session and CSRF cookies are served without `Secure` to plain-http
  // clients. It is here for the same reason `tls` directly above is — PUT is a full-document
  // replace decoded into a zero-valued Go struct, so a client that omits the key silently turns
  // the setting OFF on the next save. For this one that direction is safe-by-accident rather than
  // dangerous, but relying on which way an omission happens to fall is exactly quince#493.
  sessions: { allow_insecure_transport: boolean };
  // reconcile.interval_minutes (qn.6i): how often the reconciliation pass runs on its own; 0
  // disables the SCHEDULE only — startup, storage-added and job-end still fire. Here for the same
  // full-document-replace reason as everything else in this interface.
  reconcile: { interval_minutes: number };
  // `notifications:` — was `automation:` until qn.12, when these keys stopped being inert. Every
  // field is spelled out for the same full-document-replace reason as the rest of this interface:
  // a PUT sends the whole config back, so a field missing HERE is a field zeroed on the server.
  notifications: {
    staleness_days: number;
    reminder_cooldown_hours: number;
    overdue_days: number;
    backup_available: boolean;
    backup_overdue: boolean;
    action_required: boolean;
    backup_failed: boolean;
    backup_completed: boolean;
  };
  ui: { theme: string };
}

export interface ConfigWarning {
  path: string;
  message: string;
}

export interface ConfigSource {
  path: string;
  mtime: string;
}

export interface ConfigResponse {
  config: Config;
  warnings: ConfigWarning[];
  source: ConfigSource;
  /**
   * `config.yml` AS IT IS ON DISK — read at request time, never cached (contracts §6).
   *
   * NOT a rendering of `config`. Since qn.6j the two are different documents: `config` is the
   * RESOLVED configuration with every key filled, this is only what the user set. Empty when there
   * is no file yet.
   */
  file_text: string;
  /**
   * `discarded` — the file on disk was REFUSED, so the running configuration is not what the file
   * says (Operator ruling 2026-08-12, quince#849, AMENDED 2026-08-17, quince#1130).
   *
   * DO NOT RENDER THIS AS "running on its defaults" — that is true of only one of the two routes.
   * A refusal AT LOAD leaves quince on defaults; a hand-edit refused at RELOAD leaves it on
   * last-good, with storage running and backups continuing.
   *
   * READ `config.storage` TO TELL THEM APART — it is the RUNNING configuration: no storage means
   * defaults and nothing is backing up; storage present means last-good and backups continue. That
   * is why there is no second boolean, and `ConfigView` is the reference implementation.
   *
   * IT CARRIES THE FATALITY, NOT THE CAUSE, which is why it has to exist. The cause is in
   * `warnings` and always is — but `warnings` is non-empty in two states that want OPPOSITE
   * headlines: a discarded config, where the declared storage is not running and nothing is backing
   * up, and one that parsed with an ignored unknown key, where the storage is fine.
   * `config.storage: null` does not separate them either, since a fresh install with a typo has it
   * too.
   *
   * SO A CLIENT BRANCHES ITS HEADLINE ON THIS AND RENDERS `warnings` AS THE DETAIL EITHER WAY.
   * Reading it as "should I show the warnings at all" would put the same signal behind a second
   * gate, which is the defect quince#849 was filed about.
   */
  discarded: boolean;
}

export interface ConfigFieldError {
  path: string;
  message: string;
}

// Storage is one declared backup location (contracts §1 GET /api/storages, qn.6c).
//
// `unreachable_code` and `unreachable_reason` are REQUIRED and NULLABLE, never optional: the server
// has no `omitempty` on them, so a `?` here would model an omission that cannot happen and let a
// stale client read "absent" as "reachable". Same reasoning as `Version.storage_id`.
export interface Storage {
  id: string;
  name: string;
  path: string;
  backend: "zfs" | "reflink" | "hardlink" | "copy" | "unknown";
  default: boolean;
  reachable: boolean;
  // The code is what to branch on; the reason is what to show. The daemon's sentence carries what
  // the client cannot know — which path, which marker.
  //
  // `corrupt_marker` IS NOT A FLAVOUR OF `path_unreachable` (quince#569, ruled 2026-08-02). The disk
  // is PRESENT and READABLE; quince cannot confirm WHICH storage it is. The remedies diverge, which
  // is what makes the fourth value load-bearing rather than pedantic — `path_unreachable` means
  // *plug it in*, and `corrupt_marker` means something that is not obvious and is DANGEROUS to
  // guess at, because recreating a marker wrongly attaches the wrong storage. A card must not offer
  // a one-click remedy here.
  //
  // `unmapped` should never arrive. It means the daemon reached an internal state with no declared
  // wire code — a quince bug, deliberately obvious rather than a plausible neighbour, so it fails
  // visibly instead of rendering a confident wrong remedy.
  unreachable_code:
    | "path_unreachable"
    | "missing_medium"
    | "backend_mismatch"
    | "corrupt_marker"
    | "unmapped"
    | null;
  unreachable_reason: string | null;
  // Present only when the list was fetched with `?udid=`. null means "not asked", NOT "no".
  will_be_full: boolean | null;
  // `statfs` on this storage's path — of the FILESYSTEM, never of the storage (qn.6d gap A, ruled
  // 2026-08-03). Two storages that are two directories on one disk report IDENTICAL figures, and
  // nothing distinguishes them: `filesystem_id` and a `filesystem_shared` boolean were both
  // offered and both declined. THE CARD RENDERS NO CAVEAT and always says plain "1.2 TB free" —
  // a ruled acceptance, not a bug to fix.
  //
  // null when unreachable, never 0: a zero is a measurement and this is an absence.
  filesystem_free_bytes: number | null;
  filesystem_total_bytes: number | null;
  // Properties of the STORAGE, so present with or without `?udid=`. From the DB, so populated even
  // when unreachable — and CURRENT, not a last-known reading, so NO timestamp accompanies them
  // (quince#588). The asymmetry a client needs is these two being populated while capacity is null.
  backup_count: number;
  device_count: number;
}

// ServeMode is GET /api/health's `mode` — how this daemon is DEPLOYED, not who you are, which is
// why it lives on health rather than on /api/auth/status (ruled 2026-08-02; auth/status is a frozen
// contract and health explicitly is not). The login screen reads it to decide whether to print the
// demo password, so it must be readable BEFORE login — health is authExempt, which it needs to be.
export type ServeMode = "normal" | "demo" | "public_demo";

export interface Health {
  status: string;
  version: string;
  mode: ServeMode;
  // How often the DEPLOYMENT restarts a public-demo instance, in whole minutes. OPTIONAL because
  // the server omits it when the deployment did not say — quince runs no timer and performs no
  // reset, so this is a fact it is told rather than one it knows (public-demo spec story 6).
  demo_reset_minutes?: number;
  // reconciling: quince is re-reading its storages, so a version list or a backup count MAY BE
  // SHORT (qn.6i, contracts §1). A DECLARED PROVISIONAL STATE, NOT AN EMPTY RESULT — a client must
  // not conclude "this disk has no backups" while it holds. Optional so a UI newer than its server
  // reads `undefined` rather than a false `false`: absent means the server predates the field, and
  // a server that predates it also reconciled before it served anything.
  reconciling?: boolean;
  // insecure_origin: a session cookie earned on THIS connection would be marked Secure and then
  // discarded, so NO credential can be established over it — `POST /api/auth/setup` and
  // `/api/auth/login` both answer 426 before looking at what you sent (quince#908, contracts §1).
  //
  // NOT "this is http". It is FALSE on `http://localhost`, in `--demo`, and with
  // `sessions.allow_insecure_transport` on — all plain http, all of them fine to log in over,
  // which is exactly why a client must read this instead of `window.location.protocol`.
  //
  // Optional for the same reason as `reconciling`: a UI newer than its server reads `undefined`
  // rather than a false `false`. Every consumer must therefore act on `=== true` and nothing else.
  insecure_origin?: boolean;
  // insecure_transport_allowed: `sessions.allow_insecure_transport` is ON, so session and CSRF
  // cookies are served without the Secure flag and cross a plain http network in clear
  // (quince#908 slice 6, contracts §1). It is the fact a non-dismissible banner renders from
  // (quince#539).
  //
  // DO NOT REACH FOR `insecure_origin` ABOVE — they are INVERSES on the install that matters.
  // With this on, no cookie is discarded, so `insecure_origin` reads FALSE on exactly the
  // deployment whose login form must carry a warning.
  //
  // Optional for the same reason as the fields above, and the `=== true` rule matters more here
  // than anywhere else: `undefined` must not render a warning about a setting the server never
  // reported on.
  insecure_transport_allowed?: boolean;
}

// --- onboarding (qn.6f) ---

// OnboardingHTTPS is GET /api/onboarding/https. `detected` is the EVIDENCE and `complete` the
// VERDICT; both are sent although one is derivable, so a client deciding whether to show the
// setup options never keeps its own list of which reasons count — that list is exactly what
// goes stale when a fourth reason appears (contracts §1).
export interface OnboardingHTTPS {
  complete: boolean;
  detected: "tls" | "forwarded_proto" | "none";
  // WHICH of the four shapes of evidence produced `detected: "none"` (quince#940 §2 + quince#939 §7).
  // Present only then — there is nothing to explain when the origin IS encrypted.
  //
  // A SECOND FIELD RATHER THAN MORE `detected` VALUES, because `detected` is shared with the
  // CROSS-ORIGIN probe body and widening it would have widened that too, silently.
  //
  // `proxy_not_forwarding_scheme` IS A HINT AND MUST BE RENDERED AS ONE. It is inferred from
  // `X-Forwarded-For` being present, and nginx does not set that by default either — so a confident
  // sentence would tell some correctly-configured operators their proxy is broken.
  // WHY quince's OWN certificate is not being served, when `config.yml` asks for one
  // (quince#940 §1). A CLASSIFICATION AND NEVER A DETAIL: the path and the loader's own text are
  // authenticated and never reach a pre-auth caller.
  //
  // ORTHOGONAL TO `unencrypted_code` — both can be set at once, and they answer different
  // questions: one is about this CONNECTION, the other about quince's own certificate.
  tls_unusable_code?:
    | "unreadable"
    | "malformed"
    | "mismatched"
    | "not_yet_valid"
    | "expired"
    | "unknown";
  unencrypted_code?:
    | "no_proxy_seen"
    | "proxy_not_forwarding_scheme"
    | "proxy_untrusted"
    | "proxy_reports_plain";
}

// CertificateProbe is POST /api/onboarding/certificate — the OFFLINE half of the certificate step
// (quince#908 §5, contracts §1). No network: it answers what two files ARE.
//
// `outcome` is FROZEN, like `StorageProbe.outcome`: a client renders different prose and a different
// next action for each, so adding one is a contract change.
//
// `names` is populated even on `wrong_host` — ESPECIALLY then, because "does not cover
// quince.example" is a status and "covers quince.lan, not quince.example" is something a person can
// act on. `chain_length: 1` is a leaf with no intermediate: not an error, and often a problem.
export interface CertificateProbe {
  cert_file: string;
  key_file: string;
  hostname: string;
  outcome:
    | "usable"
    | "unreadable"
    | "malformed"
    | "mismatched"
    | "not_yet_valid"
    | "expired"
    | "wrong_host";
  reason: string;
  names: string[];
  not_before: string;
  not_after: string;
  chain_length: number;
  // `current_host` is the address this request arrived at, without its port, and
  // `current_host_covered` is whether the leaf covers it — the SECOND coverage question. `outcome`
  // answers *does it cover the name they typed*, which is unanswerable while that field is empty;
  // and empty means *keep using the address I am on*, so this is the answer for exactly that case.
  // It never changes `outcome`: an address the certificate does not cover is a browser warning to
  // accept, not a refusal to make.
  current_host: string;
  current_host_covered: boolean;
}

// CertificateApplied is POST /api/onboarding/certificate/apply (quince#908 §5, slice 5). The pair is
// being SERVED the moment this arrives — and `config.yml` has NOT been written. That only happens
// when a confirmation reaches the daemon over its own https half carrying `confirm_token`; until
// then, and if nobody does, quince goes back to what the file names and nothing was saved.
//
// `config_written` IS ALWAYS FALSE HERE, and it is sent rather than implied: a client that could not
// see the distinction would tell the user their certificate had been saved.
//
// `confirm_origin` is an ORIGIN and not a URL — the path belongs to whoever renders the page. It
// ALWAYS carries a port, including `:80`, because quince serves both protocols on ONE listener and a
// bare scheme swap would aim the browser at 443 where nothing is listening.
export interface CertificateApplied {
  confirm_origin: string;
  // Whether the certificate now being served covers the host inside `confirm_origin`. FALSE IS NOT A
  // FAILURE — the trial is running either way and the browser will offer an interstitial the user
  // can accept, which is how a self-signed pair or an IP-only install is meant to work. It is here
  // so the trial screen can stop claiming coverage it never checked.
  confirm_host_covered: boolean;
  confirm_token: string;
  expires_at: string;
  expires_seconds: number;
  config_written: boolean;
}

// ProbeNonce is GET /api/onboarding/probe/nonce — obtained SAME-ORIGIN, then presented to a name the
// user is considering. The mint is never CORS-readable, and that asymmetry is the gate (contracts §1).
export interface ProbeNonce {
  nonce: string;
}

// ProbeResult is GET /api/onboarding/probe?nonce= — the cross-origin half.
//
// `nonce` echoed proves the caller reached THIS quince rather than another one answering at that
// name. `detected` describes THAT request's own connection, which is what a proxy check needs and
// what no same-origin call can tell you.
//
// TWO FIELDS, AND THE LIMIT IS THE SAFETY ARGUMENT: the widening was permitted because the body gives
// up nothing a successful connection has not already revealed. Adding a field is a contracts change
// AND needs that ruling revisited (contracts §1).
export interface ProbeResult {
  nonce: string;
  detected: "tls" | "forwarded_proto" | "none";
}

// StorageProbe is POST /api/storages/probe (contracts §2, qn.6e) — what IS this path?
//
// NOT A `Storage`, deliberately: a Storage is declared, has an identity and is being served, where
// this describes a candidate that may never be declared at all. Sharing the type would mean `id`
// and `default` nullable on a resource where they are guarantees.
export interface StorageProbe {
  path: string;
  clean_path: string;
  // FROZEN. A client renders different prose AND a different next action for each, so adding a
  // value is a contract change.
  outcome:
    | "adopt"
    | "new"
    | "missing"
    | "not_a_directory"
    | "unwritable"
    | "corrupt_marker"
    | "unreadable"
    | "invalid_path";
  // The daemon's own sentence, always naming the path. Rendered rather than re-composed: quince
  // knows which path and which marker, and a client's copy of an enum cannot.
  reason: string;
  backend: string;
  backend_reason: string;
  marker: { storage_id: string; backend: string; created_at: string } | null;
  non_empty: boolean;
  // `none` MEANS NO SIGNAL. It must NEVER be rendered as "ZFS not supported" — in hook mode the
  // container holds no zfs userland and zfs works perfectly through the host helper, so a negative
  // reading is a guaranteed false negative for the supported containerised topology.
  zfs: "path" | "host" | "none";
  filesystem_free_bytes: number;
  filesystem_total_bytes: number;
}

export interface StorageProbeResponse {
  probe: StorageProbe;
}

// StorageAddition is POST /api/config/storage's request. `default` is deliberately absent: the
// first storage is default by implication and a later one must not steal it, so the server refuses
// an entry that claims it.
export interface StorageAddition {
  name?: string;
  path: string;
  // CONCRETE, never `auto` — the add flow records the backend quince probed and showed.
  backend: "zfs" | "reflink" | "hardlink" | "copy";
  // `mode` has ONE value: `exec` was removed on quince#793 (Operator ruling, quince#697) because
  // the shipped image has no `zfs` binary. The key is kept so the server can REFUSE an old `exec`
  // by path rather than ignore it as an unknown key.
  // `ssh_port` and `ssh_key` are OMITTED on the happy path, not sent empty: they default
  // server-side, and D12 says the file carries only what was set (quince#818).
  zfs?: {
    parent_dataset: string;
    mode: "hook";
    ssh_user: string;
    ssh_host: string;
    ssh_port?: number;
    ssh_key?: string;
    seed: string;
  };
  // zfs_key_fingerprint is the ssh key THIS SCREEN SHOWED, carried back so the save can prove it is
  // committing the key whose line the operator pasted (quince#1038).
  //
  // NOTHING REACHES `/data/keys/zfs-*` UNTIL THIS REQUEST. quince holds one `.pending` key while a
  // storage is being filled in, and moves it on Add — so a key under that name means a storage
  // quince committed to, rather than a record of everything anyone typed into the field. That one
  // pending key is shared by every open tab, so without this a second tab saving after the first
  // would silently get a different key and the line it pasted would authenticate nothing.
  zfs_key_fingerprint?: string;
}

// StorageHookCheck is POST /api/storages/probe/hook (contracts §2, qn.6e) — does the operator's
// constrained ZFS helper actually work, and does it agree about the parent dataset?
export interface StorageHookCheck {
  // FROZEN. Four outcomes rather than ok/failed, because the REMEDIES differ and a user cannot
  // guess between them: a missing helper, an un-migrated helper and a mistyped parent dataset all
  // present as "it did not work".
  outcome: "ok" | "not_migrated" | "parent_mismatch" | "unreachable";
  // quince's own sentence, safe to render anywhere.
  reason: string;
  // THE TRANSPORT'S OWN OUTPUT, and it MAY NAME THE OPERATOR'S HOST. Shown to the authenticated
  // admin in their own browser; never logged, never in a fixture, never pasted into a PR or issue.
  detail: string;
}

export interface StorageHookCheckResponse {
  check: StorageHookCheck;
}

// StorageZFSKey is POST /api/storages/zfs/key (contracts §1, quince#818 piece B).
//
// EVERYTHING HERE IS SAFE TO RENDER. The private half never leaves `/data/keys/` — it is not on this
// type, never logged, never in a fixture.
export interface StorageZFSKey {
  /** Where the private half lives, so the form can say what `ssh_key` would point at. */
  path: string;
  /** The `ssh-ed25519 AAAA… quince` line on its own. */
  public_key: string;
  /**
   * The COMPLETE line to paste on the ZFS host, forced command included.
   *
   * BOTH ARE SERVED BECAUSE THEY ARE DIFFERENT ACTS: the public key is what an operator recognises,
   * this is what they must actually paste. A key shown without its `command="…"` prefix invites
   * pasting a naked key — an unconstrained shell login on the storage host rather than a helper
   * pinned to one dataset.
   */
  authorized_keys: string;
  /**
   * true when this dataset has no committed key yet; false when quince already holds one for it.
   *
   * IT DESCRIBES THE STORAGE, NOT THE FILE (quince#1038). As *did this call write a file* it was
   * permanently false, because the debounced re-fetch meant the keystroke that finished the dataset
   * name found what an earlier keystroke had made — so the panel claimed to have *found a key it
   * made earlier* about one a second old.
   */
  created: boolean;
  /**
   * true while this is the single `.pending` key — shown for a storage nobody has added yet, and
   * moved into `/data/keys/zfs-*` on Add. A key under that name means a storage quince committed to.
   */
  pending: boolean;
  /** where a pending key will be moved on Add; empty when `pending` is false. */
  lands_at: string;
  /**
   * the `SHA256:…` of the public half — the string `ssh-keygen -lf` prints.
   *
   * SENT BACK ON SAVE, so the add commits the key this screen showed or refuses. One pending key is
   * shared by every open tab; without this a second tab would silently get a different one and the
   * line it pasted on the host would authenticate nothing.
   */
  fingerprint: string;
}

export interface StorageZFSKeyResponse {
  key: StorageZFSKey;
}

// StorageZFSHelperResponse is GET /api/storages/zfs/helper (contracts §1, quince#818 piece C) — the
// constrained helper, and since quince#985 the same bytes for every install.
export interface StorageZFSHelperResponse {
  // script is the WHOLE file, ready to save. Not an excerpt: the operator saves this and nothing
  // else, which is the difference between an instruction and a reference.
  script: string;
  // path is where it goes — the same constant the `authorized_keys` line pins as its forced command,
  // so a helper saved anywhere else is simply never reached.
  path: string;
  // source_path is where the same script is served as plain text, for the host that has to install
  // it. A PATH, not a URL: quince does not know its own address, so the client joins this to the
  // origin it is already talking to — the one address known to work from somewhere.
  source_path: string;
}

// The host-key ceremony (contracts §1, quince#912). quince composes StrictHostKeyChecking=yes, so
// an entry has to exist before the helper can be reached — and until this, nothing put one there.
export interface StorageZFSHostKey {
  host: string;
  port: number;
  key_type: string;
  // fingerprint is the SHA256 form ssh prints, so it compares character for character against
  // `ssh-keygen -lf /etc/ssh/ssh_host_<type>_key.pub` run on the host — the check that makes the
  // operator's confirmation mean something.
  fingerprint: string;
  // line is the complete known_hosts entry and goes BACK unchanged on the trust call. That round
  // trip is the security property: if trust re-scanned, a host answering differently between the
  // two calls would be recorded after the operator approved a different fingerprint.
  line: string;
}

export interface StorageZFSHostKeyResponse {
  found: boolean;
  host_key: StorageZFSHostKey | null;
  // reason is the daemon's own sentence when there is no key to show — unreachable, refused, no
  // answer. Every one of those is a 200: it is the ANSWER to the question, not a failure to answer.
  reason: string;
  // trust is what known_hosts ALREADY says about the key just scanned. Absent when there is no key.
  //
  // `changed` IS WHY THIS IS NOT A BOOLEAN: the host is offering a different key from the recorded
  // one, which is a rebuilt machine or an impersonation, and quince cannot tell which.
  trust?: "unknown" | "trusted" | "changed";
}

export interface StorageZFSHostKeyTrustResponse {
  trusted: boolean;
  path: string;
}

// GET /api/notifications (qn.12, contracts §1).
//
// THE ENDPOINT AND KEYS ARE ABSENT AND THAT IS THE POINT: they are what a sender needs, so anyone
// holding them can push to that device. A subscription is identified here by a label and an id.
export interface PushSubscriptionSummary {
  id: string;
  label: string;
  /** `live` | `expired` — an expired row is LISTED rather than hidden, so a device that stopped
   *  receiving can be named. Hiding it is what makes the failure invisible until a backup is missed. */
  state: string;
  created_at: string;
  expired_at?: string;
  last_sent_at?: string;
}

export interface NotificationsResponse {
  /** The `applicationServerKey` every subscription is created against. Public by construction. */
  vapid_public_key: string;
  subscriptions: PushSubscriptionSummary[];
}

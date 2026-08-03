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
  logical_bytes: number;
  physical_bytes: number;
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
  zfs: { parent_dataset: string; mode: string; hook_cmd: string; seed: string };
  // Retention is a POINTER in Go, and null here for the same reason: `0` is a legal value for
  // every Keep*, so absent must stay distinguishable from zero. Absent means the code defaults.
  retention: { keep_recent: number; keep_daily: number; keep_weekly: number } | null;
}

export interface Config {
  backup: { transport: string; require_encryption: boolean };
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
  sessions: { ttl_minutes: number; allow_insecure_transport: boolean };
  automation: { staleness_days: number; reminder_cooldown_hours: number };
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
  unreachable_code: "path_unreachable" | "missing_medium" | "backend_mismatch" | null;
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
  // when unreachable — `counts_as_of` is what says they were true at last contact rather than now,
  // and it is ALWAYS present so a client never infers staleness from `reachable`.
  backup_count: number;
  device_count: number;
  counts_as_of: string;
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
}

// --- onboarding (qn.6f) ---

// OnboardingHTTPS is GET /api/onboarding/https. `detected` is the EVIDENCE and `complete` the
// VERDICT; both are sent although one is derivable, so a client deciding whether to show the
// setup options never keeps its own list of which reasons count — that list is exactly what
// goes stale when a fourth reason appears (contracts §1).
export interface OnboardingHTTPS {
  complete: boolean;
  detected: "tls" | "forwarded_proto" | "none";
}

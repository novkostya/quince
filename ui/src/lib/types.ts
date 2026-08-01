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
  // action on DELETE (qn.6a (cr)). Older servers omit the key → undefined is treated as false.
  missing?: boolean;
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

// StorageEntry is one declared storage under `storage.storages` (qn.6c). There is no `backend`
// field by design: a storage's backend is discovered and frozen at its creation moment and
// recorded in quince-storage.json, never declared.
export interface StorageEntry {
  name: string;
  path: string;
  default: boolean;
}

export interface Config {
  backup: { transport: string; require_encryption: boolean };
  storage: {
    // qn.6c: required server-side — quince refuses to serve, and refuses a PUT, without at least
    // one. REQUIRED and NULLABLE here to match the Go shape exactly: the field carries no
    // `omitempty`, so the key is always emitted and a nil list marshals to null.
    //
    // null is reachable: `--demo` never runs the storage requirement, so a demo config genuinely
    // serves null. That is the state the type must let a client see — a document round-tripped
    // through the UI has to be able to represent a server that has none, rather than the client
    // inventing an empty list and PUTting it back as if declared.
    //
    // (Was `storages?:`. Same defect the review caught on `Version.storage_id` in this PR —
    // modelling an omission the server never performs — found by checking whether I had made it
    // twice. I had.)
    storages: StorageEntry[] | null;
    backend: string;
    zfs: { parent_dataset: string; mode: string; hook_cmd: string; seed: string };
    retention: { keep_recent: number; keep_daily: number; keep_weekly: number };
  };
  devices: { usbmuxd_socket: string; netmuxd_addr: string };
  sessions: { ttl_minutes: number };
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
}

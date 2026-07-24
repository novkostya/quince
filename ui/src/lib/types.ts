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

export type Liveness = "active" | "silent_but_connected" | "suspected_stall";

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

export interface Config {
  backup: { transport: string; require_encryption: boolean };
  storage: {
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

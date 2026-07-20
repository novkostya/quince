-- qn.5 versions registry. One row per committed backup version (contracts §2 Version).
-- The DISK (dataset dirs / zfs snapshots) is the source of truth; this table is quince's
-- record of what it committed. Reconciliation re-adopts on-disk versions with no row and
-- marks rows whose artifact vanished as `missing` (design §5) — it never silently drops.
-- No backup secrets are stored here (stack D8).
CREATE TABLE versions (
    id                    TEXT PRIMARY KEY,          -- ULID
    udid                  TEXT NOT NULL,
    backend               TEXT NOT NULL,             -- zfs | reflink | hardlink | copy
    zfs_snapshot          TEXT,                      -- zfs backend only; NULL elsewhere
    created_at            TEXT NOT NULL,             -- RFC3339 UTC
    job_id                TEXT,                      -- NULL = adopted (found on disk, no job)
    kind                  TEXT NOT NULL DEFAULT 'unknown',   -- full | incremental | unknown
    encrypted             INTEGER NOT NULL DEFAULT 0,        -- 0/1
    is_latest             INTEGER NOT NULL DEFAULT 0,        -- 0/1; at most one per udid
    structure_verified_at TEXT,                      -- set at commit
    content_verified_at   TEXT,                      -- set on a later unlock (qn.8)
    logical_bytes         INTEGER NOT NULL DEFAULT 0,
    physical_bytes        INTEGER NOT NULL DEFAULT 0,
    missing               INTEGER NOT NULL DEFAULT 0  -- 1 = row survives but its artifact is gone
);
CREATE INDEX idx_versions_udid ON versions (udid, created_at);

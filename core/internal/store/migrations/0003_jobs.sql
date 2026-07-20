-- qn.4a jobs registry. One row per backup attempt (contracts §2 Job, design §4). The row is
-- persisted BEFORE each state/progress event is emitted (design §2 job-engine: crash-safe), so
-- startup reconciliation can flip any non-terminal row left by a crash to connection_lost. No
-- backup secrets are ever stored here (stack D8 / secrets discipline). The live log tail is an
-- in-memory ring in the engine (the job.log WS stream is not replayable), not a column here.
CREATE TABLE jobs (
    id             TEXT PRIMARY KEY,          -- ULID (sortable by time)
    udid           TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT 'backup',
    transport      TEXT NOT NULL,             -- usb | wifi
    state          TEXT NOT NULL,             -- queued … succeeded/failed/cancelled/connection_lost
    phase          TEXT NOT NULL DEFAULT '',  -- progress.phase (incl. waiting_for_passcode)
    percent        REAL,                      -- NULL = indeterminate
    bytes_done     INTEGER NOT NULL DEFAULT 0,
    bytes_total    INTEGER NOT NULL DEFAULT 0,
    files_received INTEGER NOT NULL DEFAULT 0,
    liveness       TEXT NOT NULL DEFAULT 'active',   -- active | silent_but_connected | suspected_stall
    started_at     TEXT NOT NULL,             -- RFC3339 UTC
    finished_at    TEXT,                      -- NULL until terminal
    error_code     TEXT,                      -- NULL unless failed/connection_lost
    error_message  TEXT,
    retry_of       TEXT,                      -- NULL unless a manual retry
    intent_id      TEXT NOT NULL,             -- == id for a first attempt (retry-chain root)
    attempt        INTEGER NOT NULL DEFAULT 1,
    version_id     TEXT                       -- set on succeeded
);
CREATE INDEX idx_jobs_udid ON jobs (udid, id);
CREATE INDEX idx_jobs_state ON jobs (state);

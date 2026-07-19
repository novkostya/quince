-- qn.1 initial schema. Domain tables (devices, jobs, versions) land with their rungs.

-- Key/value app settings (e.g. the argon2id admin password hash). No backup secrets.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Admin cookie sessions. Times are RFC3339 UTC strings.
CREATE TABLE sessions_auth (
    id           TEXT PRIMARY KEY,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);

-- Security audit trail (login, unlock, download, version delete). Secrets never recorded.
CREATE TABLE audit (
    id     TEXT PRIMARY KEY,
    ts     TEXT NOT NULL,
    event  TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_audit_ts ON audit (ts);

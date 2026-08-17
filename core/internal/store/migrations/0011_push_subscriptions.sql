-- qn.12 — Web Push subscriptions and the VAPID signing key.
--
-- THE KEY LIVES HERE BY OPERATOR RULING (2026-08-17, quince#1128, design §6), and the reasoning is
-- what shapes this file: the state that must never occur is *subscriptions exist, signing key does
-- not*, because the public half of that key is baked into every subscription a phone ever created
-- and losing it makes every one of them silently unsignable. Putting both in ONE artifact makes that
-- state unrepresentable — they are lost together or not at all, and losing the app DB yields a clean
-- fresh install rather than a quietly broken one.
--
-- The key itself goes in `settings` under `push.vapid_private_key`, alongside the argon2id admin
-- hash: one secret-bearing artifact rather than two under two disciplines.

CREATE TABLE push_subscriptions (
    id           TEXT PRIMARY KEY,
    -- A SUBSCRIPTION IS CAPABILITY-GRADE. Anyone holding endpoint + keys can push to that device, so
    -- these three columns are never logged, never served to another session, and never enter a
    -- fixture (design §6, spec D8).
    endpoint     TEXT NOT NULL UNIQUE,
    p256dh       TEXT NOT NULL,
    auth         TEXT NOT NULL,
    -- A coarse, user-facing name so the Settings list is readable. NEVER a UDID and never a
    -- User-Agent verbatim; the client sends something like "iPhone · Safari".
    label        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    -- expired_at IS THE WHOLE POINT OF NOT DELETING THE ROW. A push service answering 410/404 means
    -- the phone will never receive again — and deleting it there is exactly what makes a device that
    -- quietly stopped receiving invisible, whose first symptom is a missed backup (spec D8). The row
    -- stays, marked, until that device re-subscribes or the user removes it. There is no time-based
    -- pruning and therefore no cap to surface.
    expired_at   TEXT,
    last_sent_at TEXT
);

-- Reads are "every live subscription", which is what a send does, so that is the index.
CREATE INDEX idx_push_subscriptions_live ON push_subscriptions (expired_at);

-- qn.12 — the reminder ledger, one row per device.
--
-- ONE TRACK PER DEVICE, WHICH IS WHY THIS IS KEYED ON UDID AND NOT ON (udid, kind). `backup_available`
-- and `backup_overdue` are two RANKS of one reminder, so the cooldown belongs to the track: a schema
-- with a row per kind would permit the double-notification the spec's D5 exists to make impossible.
CREATE TABLE push_reminders (
    udid         TEXT PRIMARY KEY,
    last_sent_at TEXT NOT NULL
);

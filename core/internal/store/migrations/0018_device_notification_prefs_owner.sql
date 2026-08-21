-- qn.13 slice 10b (spec D7): the per-device notifications switch gains an OWNER.
--
-- OPERATOR RULING, 2026-08-21: the mute moves to the send phase. *"Move it to second phase, there's
-- no other option."* The alternative — narrowing the ruling so the admin's mute suppresses everyone
-- — was offered and declined.
--
-- WHY AN OWNER COLUMN ALONE WOULD HAVE INVERTED THE RULING. The mute was read at the DECISION point
-- (`notify.ForTerminal`, `notify.Evaluate`), not at send: a muted device produced no `Decision` at
-- all, so slice 10a's send-path filter never ran and the scoped holder of that device received
-- nothing. Adding an owner here without moving the gate would have made the ADMIN's mute silence a
-- household member silently, with the status surface telling them nothing was wrong — the ruling
-- inverted while appearing to be implemented.
--
-- So this migration is one of three parts, and the other two are in the same PR: the decision point
-- stops consulting the switch, and the send loop consults it per subscription owner.
--
-- A TABLE REBUILD, BECAUSE THE PRIMARY KEY CHANGES. SQLite cannot add a column to a primary key, and
-- the key genuinely is now (device, owner) — the same device has one row per principal who has an
-- opinion about it. Copy, drop, rename: the standard shape, and the rows are few.
--
-- `owner_udid` IS '' FOR THE ADMIN, NOT NULL, and that is a departure from every other
-- NULL-means-admin column in this rung — deliberately. This one is in a PRIMARY KEY, and SQLite
-- treats NULLs as DISTINCT in a unique index: two admin rows for one device would both be accepted,
-- and the later write would stop being an upsert. An empty string collides properly.
--
-- EXISTING ROWS BACKFILL AS ADMIN-OWNED, NOT GLOBAL (spec D7). Every row that exists was written by
-- the admin, because there has never been another principal to write one. Reading them as global
-- would silently import the admin's preference into principals who never expressed it — and it
-- fails in the direction where a household member is muted and nothing on screen says why.
--
-- A SCOPED PRINCIPAL'S OWNER IS ITS OWN DEVICE, so its rows always have owner_udid = udid. That is
-- not enforced here: the constraint belongs where the principal is known, and a CHECK would make
-- the table refuse a legitimate future owner kind rather than the illegitimate write it is aimed at.
CREATE TABLE device_notification_prefs_new (
    udid       TEXT NOT NULL,
    owner_udid TEXT NOT NULL DEFAULT '',   -- '' = the admin
    -- A BOOLEAN, NOT wifi_sync's tri-state — 0013's reasoning, unchanged: this policy is quince's
    -- own, so there is no unknown to represent.
    enabled    INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (udid, owner_udid)
);

INSERT INTO device_notification_prefs_new (udid, owner_udid, enabled, updated_at)
    SELECT udid, '', enabled, updated_at FROM device_notification_prefs;

DROP TABLE device_notification_prefs;
ALTER TABLE device_notification_prefs_new RENAME TO device_notification_prefs;

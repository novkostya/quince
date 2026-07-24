-- qn.6a offline devices. A persisted copy of the lockdown identity the enrichment driver
-- already fetches (contracts §2 Device: name/model/ios/paired/backup_encryption), plus a
-- last_seen. Its ONLY job is to give a NAME + a real "last seen" to a device that is no longer
-- connected but still has backups — so the Devices page can list it (offline) instead of
-- forgetting it the moment it is unplugged. The registry's live table stays the source of truth
-- for PRESENCE; this table is read at startup and refreshed on each Enrich. No backup secrets.
CREATE TABLE device_identity (
    udid              TEXT PRIMARY KEY,
    name              TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    ios_version       TEXT NOT NULL DEFAULT '',
    paired            TEXT NOT NULL DEFAULT '',   -- yes | no | unknown | '' (not determined)
    backup_encryption TEXT NOT NULL DEFAULT '',   -- on | off | unknown | ''
    last_seen         TEXT NOT NULL DEFAULT '',   -- RFC3339 UTC; newest presence we recorded
    updated_at        TEXT NOT NULL DEFAULT ''
);

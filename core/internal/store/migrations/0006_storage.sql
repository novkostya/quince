-- qn.6c: storage becomes plural. A storages registry, and the storage a version lives on.
--
-- PURELY ADDITIVE BY DESIGN, AND THAT IS A REQUIREMENT RATHER THAN A CONVENIENCE. contracts.md's
-- "breaking is cheap here" clause applies to INTERFACE SHAPE, NOT TO PERSISTED STATE: its premise
-- is that the only consumer ships from the same commit, and a SQLite row behind a committed backup
-- has no commit to ship with. So this migration adds a table and adds a column, and REWRITES NO
-- ROW. Anything that changes what an existing row means is a migration against data that cannot be
-- regenerated, with `never mutate a committed version` on the other side of it.
--
-- storages: quince's record of the storages it has seen. The DISK is still the source of truth —
-- a storage's real identity is the UUID in its quince-storage.json (design §5), and this table
-- caches it so quince can tell "a storage I have created before, whose medium is absent" from "a
-- path I have never seen". That distinction is the whole of the unmounted-mountpoint guard, and
-- without a row to check, an unplugged disk's bare mountpoint reads as a brand-new storage.
--
-- Keyed on `name` — the config entry's stable label — NOT on `path`. A path moves when a disk is
-- remounted elsewhere; the name does not. When the medium IS present the marker is authoritative,
-- so a known storage_id found at a new path is a MOVE, recorded, not a new storage.
CREATE TABLE storages (
    name       TEXT PRIMARY KEY,          -- the config.yml entry's name; stable across replug
    storage_id TEXT,                      -- UUID from quince-storage.json; NULL until created
    backend    TEXT,                      -- frozen at the creation moment; NULL until then
    path       TEXT NOT NULL,             -- last known path; informational, never an identity
    created_at TEXT,                      -- RFC3339 UTC; NULL until the creation moment
    seen_at    TEXT NOT NULL              -- RFC3339 UTC; last time quince resolved this entry
);

-- versions.storage_id: which storage this version lives on.
--
-- NULLABLE, and `null` means NOT YET ATTRIBUTED — Operator ruling 2026-08-01 (quince#378).
--
-- It cannot be backfilled here and that is not a shortcut. A version's storage is identified by the
-- UUID in its storage's quince-storage.json, and that marker is written at the storage's creation
-- moment, which is a LATER rung PR; migrations cannot read config.yml either. Backfilling a
-- fabricated id would be inventing an identity for data that cannot be regenerated — the same
-- error, one level up, as treating an unmounted mountpoint as a new storage.
--
-- THIS NULL IS TRANSITIONAL, unlike versions.job_id whose NULL (= adopted) is permanent and
-- correct. Every pre-qn.6c version starts null here and is attributed once its storage has a
-- marker; a gate asserts that none remains null after that point, because a nullable-with-meaning
-- field whose meaning is "temporary" decays into a permanent unknown unless something says
-- otherwise.
ALTER TABLE versions ADD COLUMN storage_id TEXT;   -- NULL = not yet attributed (transitional)

CREATE INDEX idx_versions_storage ON versions (storage_id, created_at);

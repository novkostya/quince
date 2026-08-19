-- quince#1270 — the per-device notifications switch: "do not notify me about THIS device".

-- ITS OWN TABLE, NOT A COLUMN ON `device_identity` (Operator, 2026-08-20). `device_identity` is
-- *what the device is* — name, model, iOS, pairing, last seen — and nothing about the hardware
-- changes when this is flipped. This is a PREFERENCE, and today there is exactly one principal to
-- own it. When single-device-scoped passkeys land, a second principal exists and the row gains an
-- owner column; that is a column on a preference table, where the same change against
-- `device_identity` would be a migration plus a re-reading of what that table means.
--
-- DO NOT ADD THAT COLUMN NOW. There is one principal, and inventing a second before the rung that
-- defines it is building on an assumption nobody wrote down.
--
-- THE DB RATHER THAN `config.yml`, AND THAT IS A DEPARTURE FROM D12 TAKEN DELIBERATELY (Operator,
-- 2026-08-20). The device set is DISCOVERED, not authored, so these keys have no hand-written
-- origin, and quince#728 ruled `config.yml` holds only what the user set — a per-device block would
-- fill it with UDIDs nobody typed. The cost, stated rather than discovered later: **a hand-edit of
-- `config.yml` cannot reach this switch.**
CREATE TABLE device_notification_prefs (
    udid       TEXT PRIMARY KEY,
    -- A BOOLEAN, NOT wifi_sync's on|off|unknown TRI-STATE. `wifi_sync` *reflects* a value read from
    -- the device's lockdown record, so quince may genuinely not know it. This policy is quince's
    -- own, so there is no unknown to represent.
    --
    -- It stays a boolean under the later per-(device × category) matrix too, which is what makes
    -- deferring that matrix free: under AND precedence `1` already MEANS "defer to the global
    -- categories" and `0` means "silence this device entirely", so a device-level `inherit` would be
    -- indistinguishable from `1`. The tri-state belongs to the matrix's CELLS, not to this switch.
    enabled    INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL DEFAULT ''
);

-- qn.6k slice 2: the passkey credentials table, and it lands BEFORE anything can write to it.
--
-- ORDER IS THE POINT OF THIS SLICE. The Operator's ruling on quince#657 puts the console escape
-- hatch (`quince auth reset`) in the first slice, "before any credential can be issued" — because a
-- passkey that is the only way in, on a phone that is lost, locks the user out of their own backups.
-- So the table and the thing that empties it ship together, and the endpoints that fill it do not
-- ship until slice 3.
--
-- PURELY ADDITIVE, per 0006's reasoning: contracts.md's "breaking is cheap here" clause is about
-- INTERFACE SHAPE, not persisted state. This adds a table and rewrites no row.
--
-- ONE ADMIN, NO ACCOUNTS, which is what permits DISCOVERABLE credentials — no username field and no
-- account picker at login. There is therefore no user table to join to, and `user_handle` below is a
-- single constant shared by every row (see `passkey_user_handle` in settings).
CREATE TABLE passkeys (
    -- The raw WebAuthn credential id, base64url. PRIMARY KEY because the authenticator guarantees
    -- uniqueness and an assertion arrives carrying exactly this and nothing else to look up by.
    credential_id TEXT PRIMARY KEY,

    public_key    BLOB NOT NULL,          -- COSE_Key, as the authenticator returned it

    -- THE rpId THIS CREDENTIAL WAS REGISTERED AGAINST, and it is stored rather than derived at use
    -- time for one reason: without it the failure is SILENT. A passkey is bound to a domain, so
    -- moving between qn.6f access tiers — reverse proxy to Tailscale, or a domain change — breaks
    -- every credential while the phone still lists them, and nothing in the protocol says so. With
    -- it, quince can answer "this passkey was registered for <domain>; you are on <other>", which is
    -- the state-honesty rule applied to a credential (spec qn.6k D2).
    rp_id         TEXT NOT NULL,

    -- The authenticator's signature counter. Stored and CHECKED: a regression means a cloned
    -- credential, and per the no-silent-fallback rule it must surface rather than be swallowed.
    -- 0 is legitimate and means "this authenticator does not implement a counter".
    sign_count    INTEGER NOT NULL DEFAULT 0,

    aaguid        BLOB,                   -- authenticator model; informational, may be all-zero
    transports    TEXT,                   -- JSON array as reported at registration, e.g. ["internal"]

    -- What the user calls it. Several devices per admin — phone, tablet, laptop — and they are
    -- removable individually, so they need to be tellable apart by a human.
    name          TEXT NOT NULL,

    created_at    TEXT NOT NULL,          -- RFC3339 UTC
    last_used_at  TEXT                    -- RFC3339 UTC; NULL until first successful assertion
);

-- Assertion looks up by credential_id, which is already the primary key. This index serves the
-- OTHER direction — listing and clearing by rpId, and answering the mismatch question above without
-- a scan once an admin has credentials from more than one access tier.
CREATE INDEX idx_passkeys_rp_id ON passkeys (rp_id);

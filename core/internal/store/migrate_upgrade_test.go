package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A GENUINELY OLD DATABASE, MIGRATED FORWARD — not a synthetic row inserted into a current schema.
//
// WHY IT IS DIFFERENT FROM WHAT WE HAD. qn.13's two migrations each claim to be ADDITIVE: an
// existing session keeps working and reads as the admin (0014), and an existing credential keeps
// working and counts as an admin one (0015). Both claims were asserted by writing a pre-migration
// SHAPE into a database that had already been migrated — which tests the reader, not the migration.
// This builds the real thing: 0001..0013 applied, rows written the old way, then opened by the
// current code so 0014 and 0015 actually run against it.
//
// It is the upgrade every existing install performs, and nothing exercised it.
func TestUpgradeFromPre0014Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Apply 0001..0013 only, exactly as a pre-qn.13 install would hold them.
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	applied := 0
	for _, e := range entries {
		name := e.Name()
		if name >= "0014" {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name[:len(name)-len(".sql")]); err != nil {
			t.Fatal(err)
		}
		applied++
	}
	// A live session and a credential, written the pre-qn.13 way.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := raw.Exec(`INSERT INTO sessions_auth (id, created_at, last_seen_at, expires_at)
	                       VALUES ('old-session', ?, ?, ?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO passkeys (credential_id, public_key, rp_id, sign_count, name, created_at)
	                       VALUES ('old-cred-aaaa', X'00', 'quince.example', 0, 'phone', ?)`, now); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	t.Logf("seeded a pre-qn.13 database with %d migrations applied", applied)
	if applied != 13 {
		// The control: if this stopped seeding a PRE-0014 database — because the loop's cutoff
		// drifted, or a migration was renumbered — the test would pass by testing nothing.
		t.Fatalf("seeded %d migrations, want 13 — this is no longer a pre-qn.13 database", applied)
	}

	// NOW OPEN IT WITH THE CURRENT CODE, which runs 0014 and 0015.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("migrating a real pre-qn.13 database FAILED: %v", err)
	}
	defer func() { _ = st.Close() }()

	sess, ok, err := st.GetAuthSession("old-session")
	if err != nil || !ok {
		t.Fatalf("the pre-existing session did not survive the upgrade: ok=%v err=%v", ok, err)
	}
	if sess.CredentialID != nil {
		t.Fatalf("an upgraded session gained a credential: %q", *sess.CredentialID)
	}
	pk, ok, err := st.GetPasskey("old-cred-aaaa")
	if err != nil || !ok {
		t.Fatalf("the pre-existing credential did not survive: ok=%v err=%v", ok, err)
	}
	if pk.ScopeUDID != nil {
		t.Fatalf("an upgraded credential gained a scope: %q", *pk.ScopeUDID)
	}
	admin, err := st.ListAdminPasskeys()
	if err != nil || len(admin) != 1 {
		t.Fatalf("the upgraded credential does not count as admin: n=%d err=%v", len(admin), err)
	}
}

package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "quince.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMigrateCreatesTablesAndIsIdempotent(t *testing.T) {
	st := openTemp(t)
	for _, table := range []string{"settings", "sessions_auth", "audit", "schema_migrations"} {
		var name string
		err := st.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q missing: %v", table, err)
		}
	}
	// Re-running migrate must be a no-op (no duplicate-version errors).
	if err := st.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var applied int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations rows = %d, want 1", applied)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := openTemp(t)
	if _, ok, _ := st.GetSetting("k"); ok {
		t.Fatal("expected missing key")
	}
	if err := st.SetSetting("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := st.GetSetting("k"); !ok || v != "v1" {
		t.Fatalf("got %q ok=%v", v, ok)
	}
	if err := st.SetSetting("k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := st.GetSetting("k"); v != "v2" {
		t.Fatalf("upsert failed, got %q", v)
	}
}

func TestSetSettingIfAbsent(t *testing.T) {
	st := openTemp(t)
	ins, err := st.SetSettingIfAbsent("once", "a")
	if err != nil || !ins {
		t.Fatalf("first insert: ins=%v err=%v", ins, err)
	}
	ins, err = st.SetSettingIfAbsent("once", "b")
	if err != nil || ins {
		t.Fatalf("second insert should be skipped: ins=%v err=%v", ins, err)
	}
	if v, _, _ := st.GetSetting("once"); v != "a" {
		t.Fatalf("value changed to %q, want a", v)
	}
}

func TestAuthSessionCRUD(t *testing.T) {
	st := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)
	sess := AuthSession{ID: "s1", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := st.CreateAuthSession(sess); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetAuthSession("s1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if !got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Fatalf("expires round-trip: got %v want %v", got.ExpiresAt, sess.ExpiresAt)
	}
	later := now.Add(5 * time.Minute)
	if err := st.TouchAuthSession("s1", later); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.GetAuthSession("s1")
	if !got.LastSeenAt.Equal(later) {
		t.Fatalf("touch failed: %v", got.LastSeenAt)
	}
	if err := st.DeleteAuthSession("s1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.GetAuthSession("s1"); ok {
		t.Fatal("expected deleted")
	}
}

func TestAuditAppendAndList(t *testing.T) {
	st := openTemp(t)
	base := time.Now().UTC().Truncate(time.Second)
	if err := st.AppendAudit(AuditEntry{ID: "a", TS: base, Event: "login"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAudit(AuditEntry{ID: "b", TS: base.Add(time.Second), Event: "logout", Detail: "x"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListAudit(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Event != "logout" {
		t.Fatalf("list newest-first wrong: %+v", got)
	}
}

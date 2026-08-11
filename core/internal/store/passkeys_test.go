package store

import (
	"path/filepath"
	"testing"
	"time"
)

func passkeyStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seed(t *testing.T, s *Store, id, name, rpID string, created time.Time) {
	t.Helper()
	if err := s.InsertPasskey(Passkey{
		CredentialID: id, PublicKey: []byte("cose"), RPID: rpID, Name: name, CreatedAt: created,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// OLDEST FIRST, because the list is a HISTORY an admin reads to decide what to remove — the phone
// registered a year ago and no longer owned is the interesting row, and it belongs at the top
// rather than buried under whatever was added most recently.
func TestListPasskeysIsOldestFirst(t *testing.T) {
	s := passkeyStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, s, "c-new", "laptop", "quince.example.com", base.Add(48*time.Hour))
	seed(t, s, "c-old", "old phone", "quince.example.com", base)
	seed(t, s, "c-mid", "tablet", "quince.example.com", base.Add(24*time.Hour))

	got, err := s.ListPasskeys()
	if err != nil {
		t.Fatalf("ListPasskeys: %v", err)
	}
	want := []string{"old phone", "tablet", "laptop"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i].Name, want[i])
		}
	}
}

// `last_used_at` NULL means NEVER USED rather than "used at the epoch", and the Settings surface has
// to render that difference — a credential nobody has signed in with is exactly the one worth
// removing.
func TestLastUsedIsZeroUntilTouched(t *testing.T) {
	s := passkeyStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	seed(t, s, "c-1", "phone", "quince.example.com", now)

	pk, ok, err := s.GetPasskey("c-1")
	if err != nil || !ok {
		t.Fatalf("GetPasskey: ok=%v err=%v", ok, err)
	}
	if !pk.LastUsedAt.IsZero() {
		t.Errorf("LastUsedAt = %v on a fresh credential, want the zero time", pk.LastUsedAt)
	}

	used := now.Add(time.Hour)
	if err := s.TouchPasskey("c-1", 7, nil, used); err != nil {
		t.Fatalf("TouchPasskey: %v", err)
	}
	pk, _, err = s.GetPasskey("c-1")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if pk.LastUsedAt.IsZero() || !pk.LastUsedAt.Equal(used) {
		t.Errorf("LastUsedAt = %v, want %v", pk.LastUsedAt, used)
	}
	if pk.SignCount != 7 {
		t.Errorf("SignCount = %d, want 7", pk.SignCount)
	}
}

// DELETE IS IDEMPOTENT AND SAYS SO. The handler returns 204 either way; the store is what tells it
// whether a row actually went, and both answers are legitimate.
func TestDeletePasskeyReportsWhetherARowWent(t *testing.T) {
	s := passkeyStore(t)
	seed(t, s, "c-1", "phone", "quince.example.com", time.Now().UTC())

	if gone, err := s.DeletePasskey("c-1"); err != nil || !gone {
		t.Fatalf("first delete: gone=%v err=%v", gone, err)
	}
	if gone, err := s.DeletePasskey("c-1"); err != nil || gone {
		t.Fatalf("second delete: gone=%v err=%v, want false and no error", gone, err)
	}
}

// RENAME TOUCHES ONLY THE NAME. Everything else on the row is the authenticator's or a fact about
// creation, and a rename that moved any of it would let the record disagree with the credential it
// describes.
func TestRenamePasskeyChangesOnlyTheName(t *testing.T) {
	s := passkeyStore(t)
	created := time.Now().UTC().Truncate(time.Second)
	seed(t, s, "c-1", "phone", "quince.example.com", created)
	if err := s.TouchPasskey("c-1", 3, nil, created.Add(time.Minute)); err != nil {
		t.Fatalf("touch: %v", err)
	}
	before, _, _ := s.GetPasskey("c-1")

	if renamed, err := s.RenamePasskey("c-1", "the old phone"); err != nil || !renamed {
		t.Fatalf("rename: renamed=%v err=%v", renamed, err)
	}
	after, _, err := s.GetPasskey("c-1")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}

	if after.Name != "the old phone" {
		t.Errorf("Name = %q, want %q", after.Name, "the old phone")
	}
	if after.RPID != before.RPID || after.SignCount != before.SignCount ||
		!after.CreatedAt.Equal(before.CreatedAt) || !after.LastUsedAt.Equal(before.LastUsedAt) ||
		string(after.PublicKey) != string(before.PublicKey) {
		t.Errorf("rename moved something other than the name:\n before %+v\n after  %+v", before, after)
	}

	if renamed, err := s.RenamePasskey("c-missing", "x"); err != nil || renamed {
		t.Errorf("renaming an absent credential: renamed=%v err=%v, want false and no error", renamed, err)
	}
}

// THE BUG THAT MADE EVERY SYNCED PASSKEY UNUSABLE, PINNED — measured on hardware, not anticipated.
//
// The library compares BackupEligible on every assertion and refuses a mismatch. 0008 stored no
// flags, so a credential rebuilt from the row reported false while an iCloud-synced passkey asserts
// true, and the server refused with "Backup Eligible flag inconsistency detected during login
// validation" — after registration, autofill and Face ID had all succeeded.
func TestPasskeyFlagsSurviveARoundTrip(t *testing.T) {
	s := passkeyStore(t)
	yes, no := true, false
	now := time.Now().UTC().Truncate(time.Second)

	if err := s.InsertPasskey(Passkey{
		CredentialID: "c-1", PublicKey: []byte("cose"), RPID: "quince.example.com", Name: "phone",
		CreatedAt: now, BackupEligible: &yes, BackupState: &no,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	pk, ok, err := s.GetPasskey("c-1")
	if err != nil || !ok {
		t.Fatalf("GetPasskey: ok=%v err=%v", ok, err)
	}
	if pk.BackupEligible == nil || !*pk.BackupEligible {
		t.Errorf("BackupEligible = %v, want true — a synced passkey can never assert without it", pk.BackupEligible)
	}
	if pk.BackupState == nil || *pk.BackupState {
		t.Errorf("BackupState = %v, want false", pk.BackupState)
	}

	// BackupState CHANGES — a credential can become backed up later, and the library recommends
	// keeping track. BackupEligible must not move.
	if err := s.TouchPasskey("c-1", 4, &yes, now.Add(time.Hour)); err != nil {
		t.Fatalf("TouchPasskey: %v", err)
	}
	pk, _, _ = s.GetPasskey("c-1")
	if pk.BackupState == nil || !*pk.BackupState {
		t.Errorf("BackupState after touch = %v, want true", pk.BackupState)
	}
	if pk.BackupEligible == nil || !*pk.BackupEligible {
		t.Errorf("BackupEligible moved to %v — it is immutable", pk.BackupEligible)
	}
}

// A ROW FROM BEFORE 0009 READS AS UNKNOWN, NOT AS false. False is the value that would make a synced
// credential fail validation for ever; nil lets the caller tell "never recorded" from "recorded as
// not eligible", which is the difference between re-registering and being permanently broken.
func TestPasskeyFlagsAreNilWhenNeverRecorded(t *testing.T) {
	s := passkeyStore(t)
	seed(t, s, "c-old", "pre-0009", "quince.example.com", time.Now().UTC())

	pk, _, err := s.GetPasskey("c-old")
	if err != nil {
		t.Fatalf("GetPasskey: %v", err)
	}
	if pk.BackupEligible != nil || pk.BackupState != nil {
		t.Errorf("got %v/%v, want nil/nil for a row that never carried flags",
			pk.BackupEligible, pk.BackupState)
	}
}

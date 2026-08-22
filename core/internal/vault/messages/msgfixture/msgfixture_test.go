package msgfixture_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"
	backup "github.com/novkostya/ios-backup-parser"
	"github.com/novkostya/ios-backup-parser/messages"
	_ "modernc.org/sqlite"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/messages/msgfixture"
	"github.com/novkostya/quince/core/internal/vault/parserfs"
)

// open builds a backup holding only this fixture and returns the parser's view of it,
// through the REAL parserfs and the real vault — not a stub. The states asserted below are
// produced by ios-backup-parser reacting to real bytes, and a stub would assert my beliefs
// about the parser rather than the parser.
func open(t *testing.T, spec msgfixture.Spec) *messages.Messages {
	t.Helper()
	data, err := msgfixture.Build(t.TempDir(), spec)
	if err != nil {
		t.Fatalf("build fixture: %v", err)
	}
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: []fixture.File{
		{Domain: msgfixture.Domain, RelativePath: msgfixture.RelativePath, Flags: 1, Data: data},
	}}); err != nil {
		t.Fatalf("build backup: %v", err)
	}
	v, err := vault.OpenUnencrypted(dir)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if _, err := v.Unlock(t.Context(), ""); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	fsys, err := parserfs.New(v, t.TempDir())
	if err != nil {
		t.Fatalf("parserfs: %v", err)
	}
	m, err := messages.Open(fsys)
	if err != nil {
		t.Fatalf("messages.Open: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func collect(t *testing.T, m *messages.Messages) []messages.Message {
	t.Helper()
	var out []messages.Message
	for msg, err := range m.Messages() {
		if err != nil {
			var re *backup.RowError
			if ok := asRow(err, &re); ok {
				continue
			}
			t.Fatalf("stream: %v", err)
		}
		out = append(out, msg)
	}
	return out
}

func asRow(err error, target **backup.RowError) bool {
	re, ok := err.(*backup.RowError)
	if ok {
		*target = re
	}
	return ok
}

// The healthy fixture must be FULLY supported. If it degraded, every test below would be
// asserting against a schema the parser only partly understands, and a missing unit would
// look like a behaviour rather than a broken fixture.
func TestHealthyFixtureIsFullySupported(t *testing.T) {
	m := open(t, msgfixture.Spec{})
	c := m.Capability()
	if !c.Supported {
		t.Fatalf("not supported: %+v", c)
	}
	if c.Schema != "messages.1" {
		t.Errorf("schema = %q, want messages.1", c.Schema)
	}
	if len(c.Missing) != 0 {
		t.Errorf("Missing = %v, want empty — the fixture is meant to exercise every unit", c.Missing)
	}
}

// D8's cases, asserted through the parser rather than by reading the SQL back.
func TestFixtureCarriesTheCasesTheSpecNames(t *testing.T) {
	msgs := collect(t, open(t, msgfixture.Spec{}))
	if len(msgs) != 8 {
		t.Fatalf("got %d messages, want 8", len(msgs))
	}
	byGUID := map[string]messages.Message{}
	for _, m := range msgs {
		byGUID[m.GUID] = m
	}

	t.Run("body unknown is not body empty", func(t *testing.T) {
		m := byGUID["invented-msg-3"]
		if !m.BodyUndecoded {
			t.Error("want BodyUndecoded — a surface must not render this as an empty message")
		}
		if m.Text != "" {
			t.Errorf("Text = %q, want empty alongside BodyUndecoded", m.Text)
		}
	})

	t.Run("attachment that resolves", func(t *testing.T) {
		m := byGUID["invented-msg-4"]
		if len(m.Attachments) != 1 {
			t.Fatalf("got %d attachments, want 1", len(m.Attachments))
		}
		if m.Attachments[0].File == nil {
			t.Fatal("File is nil, want a resolved FileRef")
		}
		if got := m.Attachments[0].File.Domain; got != "MediaDomain" {
			t.Errorf("domain = %q, want MediaDomain", got)
		}
	})

	t.Run("attachment whose file is absent", func(t *testing.T) {
		m := byGUID["invented-msg-5"]
		if len(m.Attachments) != 1 {
			t.Fatalf("got %d attachments, want 1", len(m.Attachments))
		}
		if m.Attachments[0].File != nil {
			t.Errorf("File = %+v, want nil — the parser must not fabricate a path", m.Attachments[0].File)
		}
	})

	t.Run("tapback", func(t *testing.T) {
		m := byGUID["invented-msg-6"]
		if !m.IsTapback() {
			t.Errorf("AssociatedType %d is not a tapback", m.AssociatedType)
		}
		if m.AssociatedGUID != "invented-msg-4" {
			t.Errorf("AssociatedGUID = %q", m.AssociatedGUID)
		}
	})

	t.Run("edited and unsent are distinct", func(t *testing.T) {
		if byGUID["invented-msg-7"].DateEdited.IsZero() {
			t.Error("msg-7: want DateEdited set")
		}
		if byGUID["invented-msg-8"].DateRetracted.IsZero() {
			t.Error("msg-8: want DateRetracted set")
		}
	})

	t.Run("sent message has no handle", func(t *testing.T) {
		if h := byGUID["invented-msg-2"].Handle; h != nil {
			t.Errorf("Handle = %+v, want nil for a sent message", h)
		}
	})
}

func TestGroupChatHasNameAndParticipants(t *testing.T) {
	m := open(t, msgfixture.Spec{})
	var group *messages.Chat
	n := 0
	for c, err := range m.Chats() {
		if err != nil {
			continue
		}
		n++
		if c.IsGroup() {
			cp := c
			group = &cp
		}
	}
	if n != 2 {
		t.Fatalf("got %d chats, want 2", n)
	}
	if group == nil {
		t.Fatal("no group chat — participants and display name would be unobservable")
	}
	if group.DisplayName == "" {
		t.Error("group has no display name")
	}
	if len(group.Participants) != 3 {
		t.Errorf("got %d participants, want 3", len(group.Participants))
	}
}

// G2's fixture: a schema without the chat tables must DEGRADE, naming the unit, rather than
// failing to open. A domain that cannot list conversations is still a domain that can list
// messages.
func TestNoChatsDegradesRatherThanFailing(t *testing.T) {
	m := open(t, msgfixture.Spec{NoChats: true})
	c := m.Capability()
	if !c.Supported {
		t.Fatalf("not supported: %+v — absence of an OPTIONAL unit must not reject the database", c)
	}
	if !slices.Contains(c.Missing, "chats") {
		t.Errorf("Missing = %v, want it to name \"chats\"", c.Missing)
	}
	if msgs := collect(t, m); len(msgs) != 8 {
		t.Errorf("got %d messages, want 8 — messages must survive the loss of chats", len(msgs))
	}
}

// G4's fixture, and the CONTROL for it. The stale-cache database must differ from the
// healthy one in exactly one observable way: join rows exist that the parser will not
// surface. Without the control this test could pass on a fixture that simply has no
// attachments at all, which would prove nothing.
func TestStaleAttachmentCacheHidesJoinRowsThatStillExist(t *testing.T) {
	healthy := 0
	for _, m := range collect(t, open(t, msgfixture.Spec{})) {
		healthy += len(m.Attachments)
	}
	if healthy == 0 {
		t.Fatal("control failed: the healthy fixture yields no attachments, so this test would pass vacuously")
	}

	stale := 0
	for _, m := range collect(t, open(t, msgfixture.Spec{NoAttachedCache: true})) {
		stale += len(m.Attachments)
	}
	if stale != 0 {
		t.Errorf("stale-cache fixture yielded %d attachments, want 0", stale)
	}

	// ...and the join rows are still THERE. That is what makes it a silent drop rather
	// than an empty table, and what qn.10 D5 reconciles against.
	if got := joinRowCount(t, msgfixture.Spec{NoAttachedCache: true}); got != healthy {
		t.Errorf("message_attachment_join has %d rows, want %d — the rows must survive", got, healthy)
	}
}

func joinRowCount(t *testing.T, spec msgfixture.Spec) int {
	t.Helper()
	dir := t.TempDir()
	data, err := msgfixture.Build(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "read.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM message_attachment_join").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The join indexes are a REQUIREMENT on anything added here, not a description of what the
// builder happens to do — 127× (qn.10 spec fact 3). This test is what keeps that true when
// somebody edits the DDL.
func TestFixtureCarriesTheJoinIndexesRealIOSHas(t *testing.T) {
	dir := t.TempDir()
	data, err := msgfixture.Build(dir, msgfixture.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "read.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// The two the parser hits once PER MESSAGE.
	for _, want := range []struct{ table, column string }{
		{"chat_message_join", "message_id"},
		{"message_attachment_join", "message_id"},
	} {
		if !hasIndexOn(t, db, want.table, want.column) {
			t.Errorf("no index on %s(%s) — the parser queries it once per message, which is the 127x case",
				want.table, want.column)
		}
	}
}

func hasIndexOn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_index_list(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	for _, n := range names {
		cols, err := db.Query(`SELECT name FROM pragma_index_info(?)`, n)
		if err != nil {
			t.Fatal(err)
		}
		for cols.Next() {
			var c string
			if err := cols.Scan(&c); err != nil {
				t.Fatal(err)
			}
			if c == column {
				_ = cols.Close()
				return true
			}
		}
		_ = cols.Close()
	}
	return false
}

// Padding must not disturb the named cases, or a paging test would silently change what the
// other tests assert.
func TestPaddingPreservesTheNamedCases(t *testing.T) {
	msgs := collect(t, open(t, msgfixture.Spec{Messages: 60}))
	if len(msgs) != 60 {
		t.Fatalf("got %d messages, want 60", len(msgs))
	}
	found := 0
	for _, m := range msgs {
		if m.GUID == "invented-msg-3" && m.BodyUndecoded {
			found++
		}
		if m.GUID == "invented-msg-4" && len(m.Attachments) == 1 {
			found++
		}
	}
	if found != 2 {
		t.Errorf("named cases survived padding: %d of 2", found)
	}
}

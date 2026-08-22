// Package msgfixture builds synthetic Messages databases for quince's own tests.
//
// EVERY IDENTIFIER IN EVERY FIXTURE IS INVENTED — not trimmed from a real backup, not
// anonymised, made up (qn.10 spec D8; precedent quince#1425, "INVENTED PATHS, ALWAYS").
// This domain's subject matter is personal message content, so a fixture carrying a real
// handle, phone number, group name or message body would put device content into public git
// forever. Nothing here may be derived from a device.
//
// THE JOIN INDEXES ARE NOT DECORATION. ios-backup-parser resolves chat membership and
// attachments with one query PER MESSAGE, so a database without indexes on the join tables
// goes quadratic: measured at 2707.7 µs/row against 21.3 with them, a 127× penalty (qn.10
// spec fact 3). Real iOS ships 82 indexes and the parser's own fixture ships none, so a
// quince fixture that copied the fixture rather than the reality would make CI glacial and
// mis-measure every timing resting on it.
package msgfixture

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Domain and RelativePath locate the Messages database inside a backup, matching
// ios-backup-parser's messages package.
const (
	Domain       = "HomeDomain"
	RelativePath = "Library/SMS/sms.db"
)

// schemaDDL is the messages.1 structure: the tables and columns the parser's fingerprint
// looks for. Extra columns never disqualify a fingerprint, and absent optional ones degrade
// the capability report — so what is here decides what Capability.Missing reports.
const schemaDDL = `
CREATE TABLE attachment (ROWID INTEGER PRIMARY KEY AUTOINCREMENT, guid TEXT UNIQUE NOT NULL, filename TEXT, uti TEXT, mime_type TEXT, transfer_name TEXT, total_bytes INTEGER DEFAULT 0, is_sticker INTEGER DEFAULT 0, hide_attachment INTEGER DEFAULT 0);
CREATE TABLE chat (ROWID INTEGER PRIMARY KEY AUTOINCREMENT, guid TEXT UNIQUE NOT NULL, style INTEGER, chat_identifier TEXT, service_name TEXT, display_name TEXT, room_name TEXT, group_id TEXT, account_id TEXT);
CREATE TABLE chat_handle_join (chat_id INTEGER, handle_id INTEGER);
CREATE TABLE chat_message_join (chat_id INTEGER, message_id INTEGER, message_date INTEGER DEFAULT 0);
CREATE TABLE handle (ROWID INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL, service TEXT, country TEXT, uncanonicalized_id TEXT, person_centric_id TEXT);
CREATE TABLE message (ROWID INTEGER PRIMARY KEY AUTOINCREMENT, guid TEXT UNIQUE NOT NULL, text TEXT, attributedBody BLOB, handle_id INTEGER DEFAULT 0, service TEXT, date INTEGER, date_read INTEGER, date_delivered INTEGER, is_from_me INTEGER DEFAULT 0, is_read INTEGER DEFAULT 0, associated_message_type INTEGER DEFAULT 0, associated_message_guid TEXT, associated_message_emoji TEXT, date_edited INTEGER, date_retracted INTEGER, thread_originator_guid TEXT, reply_to_guid TEXT, balloon_bundle_id TEXT, payload_data BLOB, item_type INTEGER DEFAULT 0, group_title TEXT, group_action_type INTEGER DEFAULT 0, cache_has_attachments INTEGER DEFAULT 0);
CREATE TABLE message_attachment_join (message_id INTEGER, attachment_id INTEGER);
`

// joinIndexes mirror what a real sms.db carries on the tables the parser joins per message.
const joinIndexes = `
CREATE INDEX chat_message_join_idx_message_id ON chat_message_join(message_id);
CREATE INDEX chat_message_join_idx_chat_id ON chat_message_join(chat_id);
CREATE INDEX message_attachment_join_idx_message_id ON message_attachment_join(message_id);
CREATE INDEX chat_handle_join_idx_chat_id ON chat_handle_join(chat_id);
`

// Spec describes a database to build. The zero Spec is the ordinary, healthy case.
type Spec struct {
	// NoChats omits the chat, chat_message_join and chat_handle_join tables, so the
	// parser's "chats" unit lands in Capability.Missing. This is the schema a domain
	// must degrade over rather than fail on.
	NoChats bool

	// NoAttachedCache leaves message.cache_has_attachments at 0 for messages that DO have
	// join rows. ios-backup-parser gates fillAttachments on that column, so the
	// attachments become unreachable while the join rows still exist — the silent drop
	// qn.10 D5 reconciles against.
	NoAttachedCache bool

	// NoJoinIndexes omits the indexes above. For asserting the guard, never for a fixture
	// under test: see the package doc.
	NoJoinIndexes bool

	// Messages, when non-zero, pads the conversation up to this many messages. The named
	// cases below are always present regardless.
	Messages int
}

// cocoaNanos converts a whole-second offset into the Cocoa-epoch NANOsecond value the
// message.date column holds. Messages is the only domain in nanoseconds, which is the
// cross-domain unit trap the parser's docs name.
func cocoaNanos(sec int64) int64 { return sec * 1_000_000_000 }

// Build writes a synthetic sms.db and returns its bytes, ready to hand to
// ios-backup-crypt/fixture as a File's Data.
//
// The content is chosen for the cases that matter rather than for volume: a direct chat and
// a group chat, a sent and a received message, a tapback, an edited message, an unsent one,
// an attachment that resolves and an attachment whose file is absent, and a message whose
// body is UNKNOWN rather than empty.
func Build(dir string, spec Spec) ([]byte, error) {
	path := filepath.Join(dir, "sms.db")
	_ = os.Remove(path)

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()

	ddl := schemaDDL
	if spec.NoChats {
		ddl = stripChatTables(ddl)
	}
	if _, err := db.Exec(ddl); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}

	if err := populate(db, spec); err != nil {
		return nil, err
	}

	if !spec.NoJoinIndexes {
		idx := joinIndexes
		if spec.NoChats {
			idx = "CREATE INDEX message_attachment_join_idx_message_id ON message_attachment_join(message_id);"
		}
		if _, err := db.Exec(idx); err != nil {
			return nil, fmt.Errorf("indexes: %w", err)
		}
	}

	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("close: %w", err)
	}
	return os.ReadFile(path)
}

func stripChatTables(ddl string) string {
	var out string
	for _, line := range splitLines(ddl) {
		if hasPrefix(line, "CREATE TABLE chat ") ||
			hasPrefix(line, "CREATE TABLE chat_handle_join ") ||
			hasPrefix(line, "CREATE TABLE chat_message_join ") {
			continue
		}
		out += line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// WithWAL is a database captured while its write-ahead log is still LIVE — the -wal holds
// committed pages the main file does not yet have.
//
// It exists because NO REAL BACKUP CARRIES ONE. All three of the Operator's device trees
// have sms.db and no sidecar (qn.10 spec fact 1), so parserfs's sidecar copy — the code that
// keeps *never mutate a committed version* true, by ensuring SQLite replays the log into a
// PRIVATE copy rather than into the committed tree — is exercised by this fixture or by
// nothing at all.
type WithWAL struct {
	Main []byte
	WAL  []byte
}

// BuildWithWAL writes the same content as Build in WAL mode and captures both files WITHOUT
// checkpointing.
//
// The capture must happen while the connection is OPEN: closing it checkpoints the log into
// the main database and folds the -wal away, which is exactly the fixture we do not want.
// That ordering is the whole trick, so it is spelled out rather than left to a reader.
func BuildWithWAL(dir string, spec Spec) (WithWAL, error) {
	path := filepath.Join(dir, "sms-wal.db")
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return WithWAL{}, fmt.Errorf("open: %w", err)
	}
	// One connection only: with a pool, the PRAGMA and the writes can land on different
	// connections and the capture races the checkpoint.
	db.SetMaxOpenConns(1)

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		_ = db.Close()
		return WithWAL{}, fmt.Errorf("journal_mode: %w", err)
	}
	if mode != "wal" {
		_ = db.Close()
		return WithWAL{}, fmt.Errorf("journal_mode is %q, want wal — a fixture without a live log proves nothing", mode)
	}
	// autocheckpoint would fold the log away underneath us at 1000 pages.
	if _, err := db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		_ = db.Close()
		return WithWAL{}, fmt.Errorf("wal_autocheckpoint: %w", err)
	}

	ddl := schemaDDL
	if spec.NoChats {
		ddl = stripChatTables(ddl)
	}
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		return WithWAL{}, fmt.Errorf("schema: %w", err)
	}
	if err := populate(db, spec); err != nil {
		_ = db.Close()
		return WithWAL{}, err
	}
	if !spec.NoJoinIndexes {
		idx := joinIndexes
		if spec.NoChats {
			idx = "CREATE INDEX message_attachment_join_idx_message_id ON message_attachment_join(message_id);"
		}
		if _, err := db.Exec(idx); err != nil {
			_ = db.Close()
			return WithWAL{}, fmt.Errorf("indexes: %w", err)
		}
	}

	// CAPTURE BEFORE CLOSE. This is the ordering the doc comment above is about.
	main, err := os.ReadFile(path)
	if err != nil {
		_ = db.Close()
		return WithWAL{}, fmt.Errorf("read main: %w", err)
	}
	wal, err := os.ReadFile(path + "-wal")
	if err != nil {
		_ = db.Close()
		return WithWAL{}, fmt.Errorf("read wal: %w", err)
	}
	if err := db.Close(); err != nil {
		return WithWAL{}, fmt.Errorf("close: %w", err)
	}
	if len(wal) == 0 {
		return WithWAL{}, fmt.Errorf("captured -wal is empty — nothing would be proven by this fixture")
	}
	return WithWAL{Main: main, WAL: wal}, nil
}

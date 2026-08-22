package messages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	backup "github.com/novkostya/ios-backup-parser"
	parser "github.com/novkostya/ios-backup-parser/messages"
)

// projectionDDL is what the surfaces read. It holds the fields D2 names and nothing else:
// the projection is a serving shape, not a second copy of the format.
//
// msg_chat is separate from msg because chat_message_join is many-to-many — a message can
// appear in more than one conversation, and flattening it would silently drop the second.
const projectionDDL = `
CREATE TABLE msg (
  id INTEGER PRIMARY KEY,
  guid TEXT NOT NULL,
  date INTEGER NOT NULL,
  from_me INTEGER NOT NULL,
  handle TEXT,
  body TEXT,
  body_undecoded INTEGER NOT NULL,
  n_attachments INTEGER NOT NULL,
  assoc_type INTEGER NOT NULL,
  assoc_guid TEXT,
  item_type INTEGER NOT NULL,
  balloon TEXT,
  edited INTEGER NOT NULL,
  retracted INTEGER NOT NULL
);
CREATE TABLE msg_chat (chat_id INTEGER NOT NULL, msg_id INTEGER NOT NULL, date INTEGER NOT NULL);
CREATE TABLE att (
  msg_id INTEGER NOT NULL, domain TEXT, rel_path TEXT,
  mime TEXT, name TEXT, bytes INTEGER NOT NULL, sticker INTEGER NOT NULL
);
`

// projectionIndexes make a thread page a SEEK. Without the composite the page is a scan of
// every link in the conversation, which is the cost this whole package exists to remove.
const projectionIndexes = `
CREATE INDEX msg_chat_thread ON msg_chat(chat_id, date DESC, msg_id DESC);
CREATE INDEX att_msg ON att(msg_id);
`

// progressEvery is how often onProgress fires during the scan. At ~25 µs a row this is about
// every quarter second on a real backup — often enough that a surface looks alive, rare
// enough that the callback is not the bottleneck.
const progressEvery = 10000

// build performs THE ONE SCAN. Everything the surfaces need is written as it goes; the
// parser is read exactly once.
func (r *Reader) build(ctx context.Context, onProgress func(Progress)) error {
	m, err := parser.Open(r.fsys)
	if err != nil {
		return translate(err)
	}
	defer func() { _ = m.Close() }()

	_ = os.Remove(r.projPath())
	db, err := sql.Open("sqlite", "file:"+r.projPath())
	if err != nil {
		return fmt.Errorf("messages: open projection: %w", err)
	}
	r.proj = db
	// The projection is derived and disposable: durability guarantees would buy nothing a
	// rescan does not already provide, and they cost most of the build.
	for _, p := range []string{"PRAGMA journal_mode=OFF", "PRAGMA synchronous=OFF"} {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("messages: %s: %w", p, err)
		}
	}
	if _, err := db.Exec(projectionDDL); err != nil {
		return fmt.Errorf("messages: projection schema: %w", err)
	}

	scanned, err := r.scan(ctx, m, db, onProgress)
	if err != nil {
		return err
	}
	if _, err := db.Exec(projectionIndexes); err != nil {
		return fmt.Errorf("messages: projection indexes: %w", err)
	}
	return r.reconcile(m, db, scanned)
}

func (r *Reader) scan(ctx context.Context, m *parser.Messages, db *sql.DB, onProgress func(Progress)) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	msgIns, err := tx.Prepare(`INSERT INTO msg VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	chatIns, err := tx.Prepare(`INSERT INTO msg_chat VALUES (?,?,?)`)
	if err != nil {
		return 0, err
	}
	attIns, err := tx.Prepare(`INSERT INTO att VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}

	var n, rowErrs int64
	for msg, err := range m.Messages() {
		// A cancelled context must stop an 18-second scan promptly. Checking per row is
		// cheap beside the ~25 µs the parser already spends on one.
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err != nil {
			var re *backup.RowError
			if errors.As(err, &re) {
				// Row-scoped: this message only. The stream continues, and the
				// count is surfaced rather than swallowed.
				rowErrs++
				continue
			}
			return 0, fmt.Errorf("messages: scan: %w", err)
		}

		handle := ""
		if msg.Handle != nil {
			handle = msg.Handle.Identifier
		}
		if _, err := msgIns.Exec(msg.ID, msg.GUID, msg.Time.UnixNano(), boolInt(msg.IsFromMe),
			handle, msg.Text, boolInt(msg.BodyUndecoded), len(msg.Attachments),
			msg.AssociatedType, msg.AssociatedGUID, msg.ItemType, msg.BalloonBundleID,
			msg.DateEdited.UnixNano(), msg.DateRetracted.UnixNano()); err != nil {
			return 0, fmt.Errorf("messages: insert %d: %w", msg.ID, err)
		}
		for _, c := range msg.ChatIDs {
			if _, err := chatIns.Exec(c, msg.ID, msg.Time.UnixNano()); err != nil {
				return 0, fmt.Errorf("messages: link %d: %w", msg.ID, err)
			}
		}
		for _, a := range msg.Attachments {
			domain, rel := "", ""
			if a.File != nil {
				domain, rel = a.File.Domain, a.File.RelativePath
			}
			if _, err := attIns.Exec(msg.ID, domain, rel, a.MIMEType, a.TransferName,
				a.TotalBytes, boolInt(a.IsSticker)); err != nil {
				return 0, fmt.Errorf("messages: attachment %d: %w", msg.ID, err)
			}
		}

		n++
		if onProgress != nil && n%progressEvery == 0 {
			onProgress(Progress{Messages: n})
		}
	}

	if rowErrs > 0 {
		r.warnings = append(r.warnings,
			fmt.Sprintf("%d message(s) could not be read and are missing from this view", rowErrs))
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if onProgress != nil {
		onProgress(Progress{Messages: n})
	}
	return n, nil
}

// reconcile is qn.10 D5. ios-backup-parser calls fillAttachments only when
// message.cache_has_attachments is non-zero, so a database whose cache is stale has join rows
// the parser never surfaces — measured with a control: five valid, non-dangling join rows
// with the column at its default yield 1000 messages and 0 attachments.
//
// On the Operator's real backup there is NO shortfall (21,777 join rows, 21,777 yielded), so
// this is a GUARD rather than a fix for an observed defect. It costs one COUNT(*) per session
// and turns a silent drop into a sentence naming both numbers, which is what "no silent caps
// or fallbacks" asks for.
func (r *Reader) reconcile(m *parser.Messages, db *sql.DB, scanned int64) error {
	if !supportsAttachments(m.Capability()) {
		return nil
	}
	path, err := r.fsys.Materialize(parser.Domain, parser.RelativePath)
	if err != nil {
		// Not fatal: the projection is built and usable. Losing the CHECK is worth a
		// warning, never the page.
		r.warnings = append(r.warnings, "attachment totals could not be verified for this backup")
		return nil
	}
	src, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		r.warnings = append(r.warnings, "attachment totals could not be verified for this backup")
		return nil
	}
	defer func() { _ = src.Close() }()

	var joins int64
	if err := src.QueryRow(`SELECT count(*) FROM message_attachment_join`).Scan(&joins); err != nil {
		r.warnings = append(r.warnings, "attachment totals could not be verified for this backup")
		return nil
	}
	var yielded int64
	if err := db.QueryRow(`SELECT count(*) FROM att`).Scan(&yielded); err != nil {
		return err
	}
	if joins > yielded {
		r.warnings = append(r.warnings, fmt.Sprintf(
			"this backup lists %d attachment(s) but only %d could be linked to a message; the rest are not shown",
			joins, yielded))
	}
	return nil
}

func supportsAttachments(c backup.Capability) bool {
	for _, m := range c.Missing {
		if m == "attachments" {
			return false
		}
	}
	return true
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

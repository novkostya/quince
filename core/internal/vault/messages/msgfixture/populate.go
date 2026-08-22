package msgfixture

import (
	"database/sql"
	"fmt"
)

// The invented cast. Nothing here corresponds to a real person, number or conversation.
const (
	handleAlex = "+15550100001"
	handleSam  = "+15550100002"
	handleRee  = "invented.reeve@example.invalid"

	groupName = "invented weekend plans"
)

// populate writes the cases the fixture exists to exercise. Each insert is named by the
// behaviour it proves, so a reader can tell why a row is here rather than guessing.
func populate(db *sql.DB, spec Spec) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertHandles(tx, spec); err != nil {
		return err
	}
	if err := insertChats(tx, spec); err != nil {
		return err
	}
	if err := insertAttachments(tx); err != nil {
		return err
	}
	if err := insertMessages(tx, spec); err != nil {
		return err
	}
	return tx.Commit()
}

func insertHandles(tx *sql.Tx, spec Spec) error {
	for i, id := range []string{handleAlex, handleSam, handleRee} {
		svc := "iMessage"
		if id == handleSam {
			svc = "SMS"
		}
		if _, err := tx.Exec(
			`INSERT INTO handle (ROWID, id, service, country) VALUES (?,?,?,?)`,
			i+1, id, svc, "us"); err != nil {
			return fmt.Errorf("handle %d: %w", i+1, err)
		}
	}
	return nil
}

// chat 1 is a 1:1 conversation; chat 2 is a group, which is what makes participants and a
// display name observable at all.
func insertChats(tx *sql.Tx, spec Spec) error {
	if spec.NoChats {
		return nil
	}
	rows := []struct {
		id      int64
		guid    string
		style   int
		ident   string
		display string
	}{
		{1, "iMessage;-;+15550100001", styleDirect, handleAlex, ""},
		{2, "iMessage;+;chat999000111", styleGroup, "chat999000111", groupName},
	}
	for _, c := range rows {
		if _, err := tx.Exec(
			`INSERT INTO chat (ROWID, guid, style, chat_identifier, service_name, display_name, room_name, group_id)
			 VALUES (?,?,?,?,?,?,?,?)`,
			c.id, c.guid, c.style, c.ident, "iMessage", c.display, "", ""); err != nil {
			return fmt.Errorf("chat %d: %w", c.id, err)
		}
	}
	// The group has all three participants; the direct chat has one.
	for _, j := range [][2]int64{{1, 1}, {2, 1}, {2, 2}, {2, 3}} {
		if _, err := tx.Exec(`INSERT INTO chat_handle_join (chat_id, handle_id) VALUES (?,?)`, j[0], j[1]); err != nil {
			return fmt.Errorf("chat_handle_join: %w", err)
		}
	}
	return nil
}

// chat.style values, per ios-backup-parser.
const (
	styleGroup  = 43
	styleDirect = 45
)

// Attachment 1 resolves to a file; attachment 2 has a NULL filename, which is the
// not-downloaded / purged / iCloud-only case the parser reports as File == nil. A surface
// must say "not in this backup" rather than offer a link that 404s.
func insertAttachments(tx *sql.Tx) error {
	if _, err := tx.Exec(
		`INSERT INTO attachment (ROWID, guid, filename, uti, mime_type, transfer_name, total_bytes, is_sticker)
		 VALUES (1,'invented-att-1','~/Library/SMS/Attachments/aa/00/invented-photo.heic','public.heic','image/heic','invented-photo.heic',4096,0)`); err != nil {
		return fmt.Errorf("attachment 1: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO attachment (ROWID, guid, filename, uti, mime_type, transfer_name, total_bytes, is_sticker)
		 VALUES (2,'invented-att-2',NULL,'public.jpeg','image/jpeg','invented-missing.jpg',8192,0)`); err != nil {
		return fmt.Errorf("attachment 2: %w", err)
	}
	return nil
}

type msgRow struct {
	id        int64
	guid      string
	text      any
	body      []byte
	handleID  int64
	fromMe    int
	date      int64
	assocType int64
	assocGUID string
	edited    int64
	retracted int64
	chatID    int64
	attachID  int64 // 0 = none
	why       string
}

func insertMessages(tx *sql.Tx, spec Spec) error {
	rows := []msgRow{
		{id: 1, guid: "invented-msg-1", text: "first message in the direct chat",
			handleID: 1, date: cocoaNanos(700000001), chatID: 1,
			why: "an ordinary received message with a plain text body"},
		{id: 2, guid: "invented-msg-2", text: "a reply that I sent",
			fromMe: 1, date: cocoaNanos(700000002), chatID: 1,
			why: "is_from_me, so Handle is nil and the counterpart lives on the chat"},
		{id: 3, guid: "invented-msg-3", text: nil, body: undecodableBody(),
			handleID: 1, date: cocoaNanos(700000003), chatID: 1,
			why: "text NULL and attributedBody undecodable: the body is UNKNOWN, not empty"},
		{id: 4, guid: "invented-msg-4", text: "look at this",
			handleID: 2, date: cocoaNanos(700000004), chatID: 2, attachID: 1,
			why: "an attachment whose file RESOLVES into MediaDomain"},
		{id: 5, guid: "invented-msg-5", text: "and this one never downloaded",
			handleID: 2, date: cocoaNanos(700000005), chatID: 2, attachID: 2,
			why: "an attachment whose filename is NULL, so File is nil"},
		{id: 6, guid: "invented-msg-6", text: nil, handleID: 3,
			date: cocoaNanos(700000006), chatID: 2, assocType: 2000, assocGUID: "invented-msg-4",
			why: "a tapback (2000 range) reacting to message 4"},
		{id: 7, guid: "invented-msg-7", text: "edited after sending",
			fromMe: 1, date: cocoaNanos(700000007), chatID: 2, edited: cocoaNanos(700000008),
			why: "date_edited set, so the surface must mark it edited"},
		{id: 8, guid: "invented-msg-8", text: "", fromMe: 1,
			date: cocoaNanos(700000009), chatID: 2, retracted: cocoaNanos(700000010),
			why: "date_retracted set: unsent, which is not the same as an empty message"},
	}

	for _, r := range rows {
		cache := 0
		if r.attachID != 0 && !spec.NoAttachedCache {
			cache = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO message (ROWID, guid, text, attributedBody, handle_id, service, date,
			   date_read, date_delivered, is_from_me, associated_message_type, associated_message_guid,
			   date_edited, date_retracted, item_type, cache_has_attachments)
			 VALUES (?,?,?,?,?,?,?,0,0,?,?,?,?,?,0,?)`,
			r.id, r.guid, r.text, r.body, r.handleID, "iMessage", r.date,
			r.fromMe, r.assocType, r.assocGUID, r.edited, r.retracted, cache); err != nil {
			return fmt.Errorf("message %d (%s): %w", r.id, r.why, err)
		}
		if !spec.NoChats {
			if _, err := tx.Exec(
				`INSERT INTO chat_message_join (chat_id, message_id, message_date) VALUES (?,?,?)`,
				r.chatID, r.id, r.date); err != nil {
				return fmt.Errorf("chat_message_join %d: %w", r.id, err)
			}
		}
		if r.attachID != 0 {
			if _, err := tx.Exec(
				`INSERT INTO message_attachment_join (message_id, attachment_id) VALUES (?,?)`,
				r.id, r.attachID); err != nil {
				return fmt.Errorf("message_attachment_join %d: %w", r.id, err)
			}
		}
	}

	return pad(tx, spec, int64(len(rows)))
}

// pad tops the direct chat up to Spec.Messages so a caller can exercise paging without the
// named cases above changing meaning.
func pad(tx *sql.Tx, spec Spec, from int64) error {
	for i := from + 1; i <= int64(spec.Messages); i++ {
		date := cocoaNanos(700001000 + i)
		if _, err := tx.Exec(
			`INSERT INTO message (ROWID, guid, text, handle_id, service, date, is_from_me, cache_has_attachments)
			 VALUES (?,?,?,?,?,?,?,0)`,
			i, fmt.Sprintf("invented-pad-%d", i), fmt.Sprintf("invented padding message %d", i),
			1, "iMessage", date, int(i%2)); err != nil {
			return fmt.Errorf("pad %d: %w", i, err)
		}
		if !spec.NoChats {
			if _, err := tx.Exec(
				`INSERT INTO chat_message_join (chat_id, message_id, message_date) VALUES (1,?,?)`,
				i, date); err != nil {
				return fmt.Errorf("pad join %d: %w", i, err)
			}
		}
	}
	return nil
}

// undecodableBody is a blob that is NOT a decodable typedstream. It exists so a fixture can
// produce BodyUndecoded — the state where a body is unknown rather than absent, and the one
// a surface must not render as an empty bubble.
func undecodableBody() []byte {
	return []byte{0x04, 0x0b, 'n', 'o', 't', 'a', 't', 'y', 'p', 'e', 'd', 's', 't', 'r', 'e', 'a', 'm'}
}

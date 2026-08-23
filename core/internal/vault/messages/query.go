package messages

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	backup "github.com/novkostya/ios-backup-parser"
	parser "github.com/novkostya/ios-backup-parser/messages"
)

// maxLimit clamps a page. The clamp is DISCLOSED on the page rather than applied quietly,
// exactly as browse does — a truncated list that does not say so is a silent cap.
const maxLimit = 200

// defaultLimit is what a caller asking for nothing gets.
const defaultLimit = 50

// Chat is one conversation, as a surface needs it.
type Chat struct {
	ID           int64
	GUID         string
	DisplayName  string
	Identifier   string
	IsGroup      bool
	Participants []string
}

// Message is one message, as a surface needs it. Body is empty for BOTH an empty message and
// an undecodable one, which is why BodyUnknown exists: a surface must render "unknown"
// rather than an empty bubble, and the two are not the same fact.
type Message struct {
	ID          int64
	GUID        string
	Time        time.Time
	FromMe      bool
	Handle      string
	Body        string
	BodyUnknown bool
	Attachments []Attachment
	IsTapback   bool
	ReactsTo    string
	Edited      bool
	Retracted   bool
	Balloon     string
}

// Attachment is a message's file. Domain and RelativePath are EMPTY when the backup does not
// hold the bytes — not downloaded, purged, or iCloud-only. A surface says "not in this
// backup" rather than offering a link that cannot resolve.
type Attachment struct {
	Domain       string
	RelativePath string
	MIMEType     string
	Name         string
	Bytes        int64
	IsSticker    bool
	Present      bool
}

// Page carries a slice of results plus what the envelope needs. Warnings and LimitClamped
// travel with every page because the alternative is a client that has to ask.
type Page struct {
	Messages     []Message
	NextCursor   string
	Warnings     []string
	LimitClamped bool
}

// Chats lists conversations WITHOUT building the projection.
//
// This is the refinement of D2 that falls out of building it: the chats list is answerable
// live — 10 ms for 390 chats on the real backup — so the first Messages screen costs nothing,
// and the 18 s build is deferred again, to opening an actual conversation. D2's rule is
// "nothing needs it until something reads it"; this is that rule applied one level finer.
func (r *Reader) Chats(ctx context.Context) ([]Chat, []string, error) {
	m, err := parser.Open(r.fsys)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer func() { _ = m.Close() }()

	var out []Chat
	var warnings []string
	var rowErrs int
	for c, err := range m.Chats() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err != nil {
			var re *backup.RowError
			if asRowError(err, &re) {
				rowErrs++
				continue
			}
			// ErrUnavailable here means the schema has no chat tables. That is a
			// capability fact, not an empty list.
			return nil, nil, translate(err)
		}
		participants := make([]string, 0, len(c.Participants))
		for _, p := range c.Participants {
			participants = append(participants, p.Identifier)
		}
		out = append(out, Chat{
			ID: c.ID, GUID: c.GUID, DisplayName: c.DisplayName,
			Identifier: c.Identifier, IsGroup: c.IsGroup(), Participants: participants,
		})
	}
	if rowErrs > 0 {
		warnings = append(warnings,
			fmt.Sprintf("%d conversation(s) could not be read and are not listed", rowErrs))
	}
	return out, warnings, nil
}

// threadCursor is the position of the last row a page returned. Ordering is (date DESC,
// msg_id DESC) — the parser's own order reversed, so newest first, which is where a reader
// starts. msg_id breaks ties: message dates are not unique and a date-only cursor would skip
// or repeat rows at a tie boundary.
type threadCursor struct {
	Date int64 `json:"d"`
	ID   int64 `json:"i"`
}

func encodeThreadCursor(c threadCursor) string {
	b, err := json.Marshal(c)
	if err != nil {
		panic("messages: encoding a cursor failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeThreadCursor(s string) (threadCursor, bool, error) {
	if s == "" {
		return threadCursor{}, false, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return threadCursor{}, false, fmt.Errorf("%w", ErrBadCursor)
	}
	var c threadCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return threadCursor{}, false, fmt.Errorf("%w", ErrBadCursor)
	}
	return c, true, nil
}

// Thread returns one page of a conversation, newest first, and BUILDS THE PROJECTION if this
// is the first read that needs it. onProgress may be nil.
func (r *Reader) Thread(ctx context.Context, chatID int64, cursor string, limit int, onProgress func(Progress)) (Page, error) {
	cur, hasCur, err := decodeThreadCursor(cursor)
	if err != nil {
		return Page{}, err
	}
	limit, clamped := clampLimit(limit)

	if err := r.ensure(ctx, onProgress); err != nil {
		return Page{}, err
	}

	r.mu.Lock()
	db, warnings := r.proj, append([]string(nil), r.warnings...)
	r.mu.Unlock()

	// One row beyond the page, so "is there a next page" is answered by the query rather
	// than by a second count — and an exactly-full last page does not advertise a next one
	// that turns out to be empty.
	q := `SELECT m.id, m.guid, m.date, m.from_me, m.handle, m.body, m.body_undecoded,
	             m.assoc_type, m.assoc_guid, m.edited, m.retracted, m.balloon
	      FROM msg_chat j JOIN msg m ON m.id = j.msg_id
	      WHERE j.chat_id = ?`
	args := []any{chatID}
	if hasCur {
		q += ` AND (j.date < ? OR (j.date = ? AND j.msg_id < ?))`
		args = append(args, cur.Date, cur.Date, cur.ID)
	}
	q += ` ORDER BY j.date DESC, j.msg_id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return Page{}, fmt.Errorf("messages: thread: %w", err)
	}
	defer func() { _ = rows.Close() }()

	page := Page{Warnings: warnings, LimitClamped: clamped}
	var ids []int64
	for rows.Next() {
		var m Message
		var date, edited, retracted int64
		var fromMe, undecoded int64
		var handle, body, assocGUID, balloon *string
		var assocType int64
		if err := rows.Scan(&m.ID, &m.GUID, &date, &fromMe, &handle, &body, &undecoded,
			&assocType, &assocGUID, &edited, &retracted, &balloon); err != nil {
			return Page{}, fmt.Errorf("messages: scan thread row: %w", err)
		}
		m.Time = time.Unix(0, date)
		m.FromMe = fromMe != 0
		m.BodyUnknown = undecoded != 0
		m.Handle = deref(handle)
		m.Body = deref(body)
		m.ReactsTo = deref(assocGUID)
		m.Balloon = deref(balloon)
		m.IsTapback = isTapback(assocType)
		m.Edited = edited != 0
		m.Retracted = retracted != 0
		page.Messages = append(page.Messages, m)
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("messages: thread rows: %w", err)
	}

	if len(page.Messages) > limit {
		page.Messages = page.Messages[:limit]
		ids = ids[:limit]
		last := page.Messages[len(page.Messages)-1]
		page.NextCursor = encodeThreadCursor(threadCursor{Date: last.Time.UnixNano(), ID: last.ID})
	}

	if err := r.attachTo(ctx, db, page.Messages, ids); err != nil {
		return Page{}, err
	}
	return page, nil
}

// attachTo fills the page's attachments in ONE query rather than one per message. The
// per-message shape is what makes the parser itself slow, and repeating it here would import
// the cost this package exists to remove.
// attachTo fills the page's attachments in ONE query rather than one per message. The
// per-message shape is exactly what makes the parser slow — fillAttachments runs a query per
// row, which is the 127× case when the join is unindexed — and repeating it here would import
// the cost this package exists to remove.
func (r *Reader) attachTo(ctx context.Context, db *sql.DB, msgs []Message, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := `SELECT msg_id, domain, rel_path, mime, name, bytes, sticker FROM att WHERE msg_id IN (`
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			q += ","
		}
		q += "?"
		args = append(args, id)
	}
	q += `) ORDER BY msg_id, rowid`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("messages: attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byMsg := map[int64][]Attachment{}
	for rows.Next() {
		var msgID int64
		var a Attachment
		var domain, rel, mime, name *string
		var sticker int64
		if err := rows.Scan(&msgID, &domain, &rel, &mime, &name, &a.Bytes, &sticker); err != nil {
			return fmt.Errorf("messages: scan attachment: %w", err)
		}
		a.Domain, a.RelativePath = deref(domain), deref(rel)
		a.MIMEType, a.Name = deref(mime), deref(name)
		a.IsSticker = sticker != 0
		// Present is the difference between "quince can serve these bytes" and "this
		// file is not in the backup". A surface must not offer a link for the second.
		a.Present = a.Domain != "" && a.RelativePath != ""
		byMsg[msgID] = append(byMsg[msgID], a)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("messages: attachment rows: %w", err)
	}
	for i := range msgs {
		msgs[i].Attachments = byMsg[msgs[i].ID]
	}
	return nil
}

func clampLimit(limit int) (int, bool) {
	switch {
	case limit <= 0:
		return defaultLimit, false
	case limit > maxLimit:
		return maxLimit, true
	default:
		return limit, false
	}
}

// isTapback mirrors ios-backup-parser's ranges: 2000-2007 added, 3000-3007 removed.
func isTapback(t int64) bool {
	return (t >= 2000 && t <= 2007) || (t >= 3000 && t <= 3007)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func asRowError(err error, target **backup.RowError) bool {
	re, ok := err.(*backup.RowError)
	if ok {
		*target = re
	}
	return ok
}

package messages

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// Thread returns one page of a conversation, newest first, and BUILDS NOTHING.
//
// D2 SAID THIS ROUTE PAID FOR THE PROJECTION. It no longer does — ruled 2026-08-23,
// quince#1531, on a measurement that inverted the premise. D2's argument was that search must
// read every message once, so the scan is unavoidable and a projection built from it solves
// paging for free. **"Unavoidable" was only true if the user searches.** Reading a conversation
// needs ~50 rows, and the parser now answers exactly that (`ChatMessages`, v0.4.0): about a
// millisecond of SQL against the database, versus ~11 s to build a projection first.
//
// SO THE SCAN MOVES TO SEARCH, WHICH IS THE ONE ACTION THAT ASKS FOR SOMETHING NEEDING IT.
// `Search` still calls `ensure`; this does not. A reader who never searches never pays.
//
// ONE READ PATH, ALWAYS — this does NOT switch to the projection once a search has built one.
// A session whose read path changes underneath the user is where cursor semantics drift: the
// two orderings would have to agree forever, and the projection's `(date, ROWID)` is a copy of
// the parser's. One path cannot disagree with itself.
//
// `onProgress` IS ACCEPTED AND UNUSED, deliberately rather than removed. The signature is
// shared with `Search`, which still reports; a nil-vs-set caller should not have to know which
// route builds. There is no progress to report because there is no build.
func (r *Reader) Thread(ctx context.Context, chatID int64, cursor string, limit int, _ func(Progress)) (Page, error) {
	cur, hasCur, err := decodeThreadCursor(cursor)
	if err != nil {
		return Page{}, err
	}
	limit, clamped := clampLimit(limit)

	m, err := parser.Open(r.fsys)
	if err != nil {
		return Page{}, translate(err)
	}
	defer func() { _ = m.Close() }()

	var before parser.ChatCursor
	if hasCur {
		// The cursor stores Unix nanoseconds; ChatCursor takes a time. The parser converts to
		// the stored Cocoa epoch itself, which is why this cannot get the 31-year offset wrong.
		before = parser.ChatCursor{At: time.Unix(0, cur.Date), ID: cur.ID}
	}

	page := Page{Warnings: []string{}, LimitClamped: clamped}
	var rowErrs int
	// ONE BEYOND THE PAGE, so "is there a next page" is answered by the read rather than by a
	// second count — and an exactly-full last page does not advertise an empty next one.
	for msg, err := range m.ChatMessages(chatID, before, limit+1) {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		if err != nil {
			var re *backup.RowError
			if asRowError(err, &re) {
				rowErrs++
				continue
			}
			return Page{}, translate(err)
		}
		page.Messages = append(page.Messages, fromParser(msg))
	}
	if rowErrs > 0 {
		// THE SAME SENTENCE THE BUILD USED, now scoped to the page rather than the database.
		// It means what it says either way: these messages exist and quince could not read
		// them, so the page is short and says so.
		page.Warnings = append(page.Warnings,
			fmt.Sprintf("%d message(s) could not be read and are missing from this view", rowErrs))
	}

	if len(page.Messages) > limit {
		page.Messages = page.Messages[:limit]
		last := page.Messages[len(page.Messages)-1]
		page.NextCursor = encodeThreadCursor(threadCursor{Date: last.Time.UnixNano(), ID: last.ID})
	}
	return page, nil
}

// fromParser maps a parser message onto the domain type. SHARED SHAPE WITH THE PROJECTION READ
// used by search, so a field that carries a distinction cannot be mapped on one path and dropped
// on the other.
func fromParser(msg parser.Message) Message {
	m := Message{
		ID: msg.ID, GUID: msg.GUID, Time: msg.Time,
		FromMe: msg.IsFromMe, Body: msg.Text, BodyUnknown: msg.BodyUndecoded,
		ReactsTo: msg.AssociatedGUID, Balloon: msg.BalloonBundleID,
		IsTapback: isTapback(msg.AssociatedType),
		// NOT `.IsZero()` INVERTED CARELESSLY: an absent edit date is the zero time, and
		// `time.Time{}.UnixNano()` is not zero — that mistake cost every message its body once
		// already (quince#1528). Asking the time itself is the form that cannot repeat it.
		Edited:    !msg.DateEdited.IsZero(),
		Retracted: !msg.DateRetracted.IsZero(),
	}
	if msg.Handle != nil {
		m.Handle = msg.Handle.Identifier
	}
	for _, a := range msg.Attachments {
		att := Attachment{MIMEType: a.MIMEType, Name: a.TransferName, IsSticker: a.IsSticker}
		if a.File != nil {
			att.Domain, att.RelativePath, att.Present = a.File.Domain, a.File.RelativePath, true
		}
		m.Attachments = append(m.Attachments, att)
	}
	return m
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

// SearchHit is one message matching a query, with the conversation it belongs to so a surface
// can offer "go to this message" rather than a body with no home.
type SearchHit struct {
	Message
	ChatIDs []int64
}

// SearchResult carries hits plus whether searching was possible at all.
type SearchResult struct {
	Hits         []SearchHit
	Warnings     []string
	LimitClamped bool

	// Searchable is false when this session has no full-text index. A caller must
	// distinguish it from an empty Hits: NO INDEX is a fact about quince, and NO MATCHES is
	// a fact about the user's messages. Reporting the first as the second tells someone
	// their messages do not contain a word nobody looked for.
	Searchable bool
}

// ErrEmptyQuery is returned for a blank search term. It is separate from "no matches" for the
// same reason everything else on this rung is: a caller that submitted nothing gets told so,
// rather than being shown an empty result that looks like an answer.
var ErrEmptyQuery = errors.New("messages: empty search query")

// Search finds messages whose body matches term, newest first, and BUILDS THE PROJECTION if
// this is the first read that needs it.
//
// THE TERM IS PASSED AS A BOUND PARAMETER, never interpolated. FTS5 has its own query syntax
// — prefixes, NEAR, boolean operators, column filters — so a term is still interpreted by the
// matcher, but it cannot escape into SQL.
func (r *Reader) Search(ctx context.Context, term string, limit int, onProgress func(Progress)) (SearchResult, error) {
	if strings.TrimSpace(term) == "" {
		return SearchResult{}, ErrEmptyQuery
	}
	limit, clamped := clampLimit(limit)

	if err := r.ensure(ctx, onProgress); err != nil {
		return SearchResult{}, err
	}

	r.mu.Lock()
	db, warnings, searchable := r.proj, append([]string(nil), r.warnings...), r.searchable
	r.mu.Unlock()

	out := SearchResult{Warnings: warnings, LimitClamped: clamped, Searchable: searchable}
	if !searchable {
		// NOT an error and NOT an empty page: the caller reads Searchable and says so.
		return out, nil
	}

	rows, err := db.QueryContext(ctx,
		`SELECT m.id, m.guid, m.date, m.from_me, m.handle, m.body, m.body_undecoded,
		        m.assoc_type, m.assoc_guid, m.edited, m.retracted, m.balloon
		 FROM msg_fts f JOIN msg m ON m.id = f.rowid
		 WHERE msg_fts MATCH ?
		 ORDER BY m.date DESC, m.id DESC LIMIT ?`, term, limit)
	if err != nil {
		// A malformed FTS5 expression is the CALLER's, not the backup's — an unbalanced
		// quote or a stray operator. Saying "could not read this backup" would send
		// someone to look for a damaged database.
		return SearchResult{}, fmt.Errorf("%w: %v", ErrBadQuery, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var m Message
		var date, edited, retracted, fromMe, undecoded, assocType int64
		var handle, body, assocGUID, balloon *string
		if err := rows.Scan(&m.ID, &m.GUID, &date, &fromMe, &handle, &body, &undecoded,
			&assocType, &assocGUID, &edited, &retracted, &balloon); err != nil {
			return SearchResult{}, fmt.Errorf("messages: scan search row: %w", err)
		}
		m.Time = time.Unix(0, date)
		m.FromMe, m.BodyUnknown = fromMe != 0, undecoded != 0
		m.Handle, m.Body = deref(handle), deref(body)
		m.ReactsTo, m.Balloon = deref(assocGUID), deref(balloon)
		m.IsTapback = isTapback(assocType)
		m.Edited, m.Retracted = edited != 0, retracted != 0
		out.Hits = append(out.Hits, SearchHit{Message: m})
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, fmt.Errorf("messages: search rows: %w", err)
	}

	if err := r.chatsFor(ctx, db, out.Hits, ids); err != nil {
		return SearchResult{}, err
	}
	return out, nil
}

// ErrBadQuery is returned for a search term FTS5 cannot parse.
var ErrBadQuery = errors.New("messages: malformed search query")

// chatsFor fills each hit's conversation ids in ONE query rather than one per hit — the same
// reason attachTo is batched.
func (r *Reader) chatsFor(ctx context.Context, db *sql.DB, hits []SearchHit, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := `SELECT msg_id, chat_id FROM msg_chat WHERE msg_id IN (`
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			q += ","
		}
		q += "?"
		args = append(args, id)
	}
	q += `) ORDER BY msg_id, chat_id`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("messages: search chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	byMsg := map[int64][]int64{}
	for rows.Next() {
		var msgID, chatID int64
		if err := rows.Scan(&msgID, &chatID); err != nil {
			return fmt.Errorf("messages: scan search chat: %w", err)
		}
		byMsg[msgID] = append(byMsg[msgID], chatID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("messages: search chat rows: %w", err)
	}
	for i := range hits {
		hits[i].ChatIDs = byMsg[hits[i].ID]
	}
	return nil
}

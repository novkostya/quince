// Package messages reads an unlocked backup's Messages domain for quince's surfaces.
//
// WHY THERE IS A PROJECTION AT ALL. ios-backup-parser streams every message row in
// (date, ROWID) order and offers no per-chat filter, no cursor and no by-id accessor. On the
// Operator's real backup that scan is 8.437 s over 254,949 messages, so serving a thread page
// straight from the parser costs the whole table PER PAGE — 8.4 s to open a conversation, and
// 8.4 s again for a page at the far end of it. So this package scans ONCE and writes what the
// surfaces need into a session-scoped SQLite projection. A deep thread page then costs 265 µs
// (qn.10 spec D2, measured).
//
// THE SCAN IS LAZY AND THAT IS A RULING, NOT AN OPTIMISATION. qn.8's file browser is shipped
// and in use, and an unlock is not a request for messages: building at unlock would charge
// every unlock 18.256 s for a domain the user may never open. So nothing here scans until
// something asks for data that needs it (quince#1491 review).
//
// NOTHING SURVIVES THE LOCK. The projection is written into the session's own scratch, which
// qn.8's teardown already reclaims on lock, TTL and shutdown. It is derived and
// reconstructible: losing it costs a rescan and never a version. Ruled at spec review not to
// be a storage-semantics change.
package messages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	backup "github.com/novkostya/ios-backup-parser"
	parser "github.com/novkostya/ios-backup-parser/messages"
	_ "modernc.org/sqlite"
)

// Reader serves the Messages domain for ONE unlocked session.
//
// It is safe for concurrent use: the projection is built at most once, under a mutex, and a
// caller arriving mid-build waits rather than starting a second scan. That matters because
// the build is ~18 s on a large backup and an impatient second request is the ordinary case,
// not the edge one.
type Reader struct {
	fsys    backup.FS
	scratch string

	mu       sync.Mutex
	proj     *sql.DB
	built    bool
	buildErr error

	// warnings accumulate during the build and are reported with every page, per the
	// frozen domain envelope (contracts §1).
	warnings []string
}

// New returns a Reader over an unlocked session's FS, writing into scratchDir, which must
// already exist and belong to that session.
func New(fsys backup.FS, scratchDir string) (*Reader, error) {
	if fsys == nil {
		return nil, errors.New("messages: nil FS")
	}
	info, err := os.Stat(scratchDir)
	if err != nil {
		return nil, fmt.Errorf("messages: scratch: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("messages: scratch %s is not a directory", scratchDir)
	}
	return &Reader{fsys: fsys, scratch: scratchDir}, nil
}

// Available reports whether this backup has a Messages database quince can read, and why not
// when it cannot.
//
// IT DOES NOT SCAN. Opening the domain fingerprints the schema; it does not enumerate. On the
// real backup that is 11 ms against the scan's 8.437 s, which is what makes the lazy ruling
// free: "does this backup have messages" is answerable without paying for "what are they".
func (r *Reader) Available(ctx context.Context) (backup.Capability, error) {
	m, err := parser.Open(r.fsys)
	if err != nil {
		return backup.Capability{}, translate(err)
	}
	defer func() { _ = m.Close() }()
	return m.Capability(), nil
}

// Close releases the projection handle. The scratch files themselves belong to the session.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proj == nil {
		return nil
	}
	err := r.proj.Close()
	r.proj = nil
	return err
}

// Progress reports how far a build has got. Total is 0 until it is known, which for this
// scan is never — the parser does not count rows up front, so a caller renders indeterminate
// progress with a live count rather than a percentage that would be a lie.
type Progress struct {
	Messages int64
}

// ErrUnsupported is returned when this backup has no readable Messages database. Callers map
// it to the envelope's unsupported_reason rather than to an empty page: a domain quince
// cannot read is a fact about quince, and reporting it as "you have no messages" would be a
// fact about the user's data that nobody checked.
var ErrUnsupported = errors.New("messages: this backup has no readable Messages database")

// ErrChatsUnavailable is returned when the database IS readable but its schema carries no
// conversation tables. Messages still stream; they just cannot be grouped.
//
// IT IS DELIBERATELY NOT ErrUnsupported, and the distinction is the point. "quince cannot
// read your messages" and "this backup stores messages without conversations" have different
// remedies and different screens, and *troubleshooting is actionable* names collapsing two
// distinguishable causes into one sentence as a defect even when every word is true. A test
// asserted these were the same error and the test was wrong, not the code.
var ErrChatsUnavailable = errors.New("messages: this backup's schema has no conversations")

// ensure builds the projection if it has not been built, and is idempotent.
//
// A FAILED BUILD IS REMEMBERED AND RETRIED, not cached as a permanent failure: the causes are
// transient (scratch full, a cancelled context) far more often than they are structural, and
// a session that can never retry would have to be locked and unlocked to recover.
func (r *Reader) ensure(ctx context.Context, onProgress func(Progress)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.built {
		return nil
	}
	if err := r.build(ctx, onProgress); err != nil {
		r.buildErr = err
		if r.proj != nil {
			_ = r.proj.Close()
			r.proj = nil
		}
		// A half-written projection must never be read from.
		_ = os.Remove(r.projPath())
		return err
	}
	r.built = true
	r.buildErr = nil
	return nil
}

func (r *Reader) projPath() string { return filepath.Join(r.scratch, "messages-projection.db") }

// translate maps the parser's vocabulary onto this package's, keeping causes DISTINCT. Every
// branch here is a different sentence on a screen with a different remedy; a default that
// swallowed them into one would satisfy *state honesty* and fail *troubleshooting is
// actionable*.
func translate(err error) error {
	switch {
	case errors.Is(err, backup.ErrUnsupportedSchema), errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %v", ErrUnsupported, err)
	case errors.Is(err, backup.ErrUnavailable):
		return fmt.Errorf("%w: %v", ErrChatsUnavailable, err)
	default:
		return err
	}
}

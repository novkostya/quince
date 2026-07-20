package store

import (
	"database/sql"
	"time"
)

// JobRow is one backup-attempt registry row (contracts §2 Job + JobProgress, design §4). The
// DB is quince's durable record of a job; the live log tail lives in the engine (in-memory ring).
// Pointer fields are the contract's nullables. No secret is ever stored on a job.
type JobRow struct {
	ID            string
	UDID          string
	Kind          string // "backup"
	Transport     string // usb | wifi
	State         string
	Phase         string
	Percent       *float64 // nil = indeterminate
	BytesDone     int64
	BytesTotal    int64
	FilesReceived int64
	Liveness      string
	StartedAt     time.Time
	FinishedAt    *time.Time // nil until terminal
	ErrorCode     string     // "" = no error
	ErrorMessage  string
	RetryOf       *string // nil unless a manual retry
	IntentID      string
	Attempt       int
	VersionID     *string // set on succeeded
}

// terminalJobStates are the states a job never leaves. Startup reconciliation flips every OTHER
// state to connection_lost (design §2 job engine).
var terminalJobStates = map[string]bool{
	"succeeded": true, "failed": true, "cancelled": true, "connection_lost": true,
}

// JobIsTerminal reports whether a state is terminal.
func JobIsTerminal(state string) bool { return terminalJobStates[state] }

// InsertJob records a new job at its initial state.
func (s *Store) InsertJob(j JobRow) error {
	_, err := s.db.Exec(`INSERT INTO jobs
		(id, udid, kind, transport, state, phase, percent, bytes_done, bytes_total, files_received,
		 liveness, started_at, finished_at, error_code, error_message, retry_of, intent_id, attempt,
		 version_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.UDID, j.Kind, j.Transport, j.State, j.Phase, nullFloat(j.Percent),
		j.BytesDone, j.BytesTotal, j.FilesReceived, j.Liveness, fmtTime(j.StartedAt),
		nullTime(j.FinishedAt), nullEmpty(j.ErrorCode), nullEmpty(j.ErrorMessage),
		nullStr(j.RetryOf), j.IntentID, j.Attempt, nullStr(j.VersionID))
	return err
}

// UpdateJob writes the mutable columns of an existing job (state/progress/finished/error/version).
// The engine persists via this on every transition BEFORE emitting the event (crash-safe).
func (s *Store) UpdateJob(j JobRow) error {
	_, err := s.db.Exec(`UPDATE jobs SET
		state = ?, phase = ?, percent = ?, bytes_done = ?, bytes_total = ?, files_received = ?,
		liveness = ?, finished_at = ?, error_code = ?, error_message = ?, version_id = ?
		WHERE id = ?`,
		j.State, j.Phase, nullFloat(j.Percent), j.BytesDone, j.BytesTotal, j.FilesReceived,
		j.Liveness, nullTime(j.FinishedAt), nullEmpty(j.ErrorCode), nullEmpty(j.ErrorMessage),
		nullStr(j.VersionID), j.ID)
	return err
}

// GetJob returns one job by id; ok=false when absent.
func (s *Store) GetJob(id string) (JobRow, bool, error) {
	row := s.db.QueryRow(jobSelect+` WHERE id = ?`, id)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return JobRow{}, false, nil
	}
	if err != nil {
		return JobRow{}, false, err
	}
	return j, true, nil
}

// ListJobs returns jobs for a udid ("" = all), newest first, with cursor pagination. The cursor
// is the last id of the previous page; because ULIDs sort by time, `id < cursor` pages backwards
// through history deterministically. nextCursor is "" on the last page.
func (s *Store) ListJobs(udid, cursor string, limit int) ([]JobRow, string, error) {
	if limit <= 0 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	// limit+1 to detect whether a further page exists.
	switch {
	case udid == "" && cursor == "":
		rows, err = s.db.Query(jobSelect+` ORDER BY id DESC LIMIT ?`, limit+1)
	case udid == "":
		rows, err = s.db.Query(jobSelect+` WHERE id < ? ORDER BY id DESC LIMIT ?`, cursor, limit+1)
	case cursor == "":
		rows, err = s.db.Query(jobSelect+` WHERE udid = ? ORDER BY id DESC LIMIT ?`, udid, limit+1)
	default:
		rows, err = s.db.Query(jobSelect+` WHERE udid = ? AND id < ? ORDER BY id DESC LIMIT ?`,
			udid, cursor, limit+1)
	}
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()

	var out []JobRow
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		next = out[limit-1].ID
		out = out[:limit]
	}
	return out, next, nil
}

// ListNonTerminalJobs returns every job not in a terminal state — the crash-orphan set startup
// reconciliation flips to connection_lost (design §2).
func (s *Store) ListNonTerminalJobs() ([]JobRow, error) {
	rows, err := s.db.Query(jobSelect +
		` WHERE state NOT IN ('succeeded','failed','cancelled','connection_lost') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []JobRow
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

const jobSelect = `SELECT id, udid, kind, transport, state, phase, percent, bytes_done,
	bytes_total, files_received, liveness, started_at, finished_at, error_code, error_message,
	retry_of, intent_id, attempt, version_id FROM jobs`

func scanJob(sc rowScanner) (JobRow, error) {
	var (
		j        JobRow
		percent  sql.NullFloat64
		started  string
		finished sql.NullString
		errCode  sql.NullString
		errMsg   sql.NullString
		retryOf  sql.NullString
		version  sql.NullString
	)
	if err := sc.Scan(&j.ID, &j.UDID, &j.Kind, &j.Transport, &j.State, &j.Phase, &percent,
		&j.BytesDone, &j.BytesTotal, &j.FilesReceived, &j.Liveness, &started, &finished,
		&errCode, &errMsg, &retryOf, &j.IntentID, &j.Attempt, &version); err != nil {
		return JobRow{}, err
	}
	j.Percent = floatPtrOrNil(percent)
	t, err := parseTime(started)
	if err != nil {
		return JobRow{}, err
	}
	j.StartedAt = t
	if j.FinishedAt, err = timePtrOrNil(finished); err != nil {
		return JobRow{}, err
	}
	j.ErrorCode = errCode.String
	j.ErrorMessage = errMsg.String
	j.RetryOf = strPtrOrNil(retryOf)
	j.VersionID = strPtrOrNil(version)
	return j, nil
}

// --- job-specific nullable helpers ---

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func floatPtrOrNil(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

// nullEmpty stores "" as SQL NULL (error_code/message are absent, not empty, when there's no error).
func nullEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

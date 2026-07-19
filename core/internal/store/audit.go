package store

import "time"

// AuditEntry is one security-audit row (contracts/design §6).
type AuditEntry struct {
	ID     string
	TS     time.Time
	Event  string
	Detail string
}

// AppendAudit inserts an audit row. Callers must never pass secrets in Detail.
func (s *Store) AppendAudit(e AuditEntry) error {
	_, err := s.db.Exec(
		`INSERT INTO audit (id, ts, event, detail) VALUES (?, ?, ?, ?)`,
		e.ID, fmtTime(e.TS), e.Event, e.Detail)
	return err
}

// ListAudit returns the most recent entries, newest first, capped at limit.
func (s *Store) ListAudit(limit int) ([]AuditEntry, error) {
	rows, err := s.db.Query(`SELECT id, ts, event, detail FROM audit ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Event, &e.Detail); err != nil {
			return nil, err
		}
		if e.TS, err = parseTime(ts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

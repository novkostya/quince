// Package store is quince's own SQLite app database (stack D8): it records what quince
// did (settings, auth sessions, audit trail; device/job/version registries land with
// their rungs) and never mirrors backup content. Driver is modernc.org/sqlite (pure Go,
// no cgo) in WAL mode. Migrations are embedded plain-SQL files applied forward-only.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver (no cgo)
)

// Store wraps the app database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite DB at path, applies pragmas and migrations.
// MaxOpenConns is 1: with a single writer connection the pragmas stick and modernc avoids
// "database is locked" churn — fine for a single-user daemon.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: %s: %w", pragma, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// fmtTime / parseTime centralize the RFC3339 UTC representation used for time columns.
func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

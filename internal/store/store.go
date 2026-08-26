// Package store is the persistence and recovery boundary for the whole
// workflow. It stores append-only events, snapshots, idempotency records,
// leases, retry tasks and the unique finality barrier in an embedded SQLite
// database, using transactions and unique constraints for atomicity and the
// single-writer guarantees described in the project document.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"lyophilizer-sterilization-validation/internal/domain"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Store owns the embedded SQL database connection. It is safe for concurrent
// use: SQLite is configured with WAL journaling and a busy timeout so that
// concurrent write transactions serialize on the single writer rather than
// failing with SQLITE_BUSY.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at path, applies the schema and returns
// a ready Store. The caller must Close the returned Store.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// A single writer keeps SQLite's locking model simple and deterministic for
	// the concurrency tests, while WAL and a generous busy timeout let readers
	// proceed during a write.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: %s: %w", p, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// InTx runs fn inside a single transaction. If fn returns an error the
// transaction is rolled back and the error is returned unchanged; otherwise it
// is committed. This is the atomic boundary for every multi-write workflow
// operation.
func (s *Store) InTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	t := &Tx{tx: tx}
	if err := fn(t); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Read runs fn against the shared connection outside a transaction, for
// read-only projections that do not need a snapshot.
func (s *Store) Read(ctx context.Context, fn func(*Tx) error) error {
	t := &Tx{tx: s.db}
	return fn(t)
}

// Recover validates every stored snapshot checksum at startup, proving that no
// torn or corrupted projection survived a crash. It returns the number of
// snapshots validated, or an error identifying the first corruption.
func (s *Store) Recover(ctx context.Context) (int, error) {
	var snapshots []domain.CycleSnapshot
	err := s.Read(ctx, func(t *Tx) error {
		rows, err := t.tx.QueryContext(ctx, `
			SELECT cycle_id, validation_id, generation, cursor, status, checksum
			FROM cycle_snapshots`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var snap domain.CycleSnapshot
			if err := rows.Scan(&snap.CycleID, &snap.ValidationID, &snap.Generation, &snap.Cursor, &snap.Status, &snap.Checksum); err != nil {
				return err
			}
			snapshots = append(snapshots, snap)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, fmt.Errorf("store: recover: %w", err)
	}
	for _, snap := range snapshots {
		want := domain.Checksum(snap.CycleID, snap.ValidationID, snap.Generation, snap.Cursor, snap.Status)
		if snap.Checksum != want {
			return 0, fmt.Errorf("store: recover: snapshot checksum mismatch for cycle %s", snap.CycleID)
		}
	}
	return len(snapshots), nil
}

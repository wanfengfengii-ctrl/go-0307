package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned by read methods when the requested entity does not
// exist. Services translate it into an appropriate domain error where needed.
var ErrNotFound = errors.New("store: not found")

// Tx is a single unit of work against the database. It may wrap either a real
// transaction (from Store.InTx) or the shared connection (from Store.Read). All
// write methods enforce uniqueness through the primary-key constraints defined
// in the schema, which are the single-writer barriers of the workflow.
type Tx struct {
	tx queryer
}

// queryer abstracts *sql.Tx and *sql.DB so one Tx type serves both
// transactional and read-only use.
type queryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// mapNotFound converts sql.ErrNoRows into the package sentinel so callers can
// use errors.Is against ErrNotFound without importing database/sql.
func mapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}

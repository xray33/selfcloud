package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type sqlTransactor struct {
	db *sql.DB
}

// NewTransactor creates a new Transactor instance managing transactions against a *sql.DB pool.
func NewTransactor(db *sql.DB) Transactor {
	return &sqlTransactor{db: db}
}

// WithTx executes the provided callback inside a database transaction.
// It automatically rolls back if an error occurs or a panic is caught, and commits upon success.
func (t *sqlTransactor) WithTx(ctx context.Context, fn func(txRepo *Repositories) error) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	txRepos := NewRepositories(tx)
	if err := fn(txRepos); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

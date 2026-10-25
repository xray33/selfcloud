package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/mysql"

	"selfcloud/internal/db"
	"selfcloud/internal/domain"
)

type sessionRepo struct {
	db db.DBTX
}

func NewSessionRepository(d db.DBTX) SessionRepository {
	return &sessionRepo{db: d}
}

func (r *sessionRepo) Create(ctx context.Context, session *domain.Session) error {
	query, args, err := dialect.Insert("sessions").
		Cols("token", "user_id", "expires_at", "created_at").
		Vals(goqu.Vals{session.Token, session.UserID, session.ExpiresAt, session.CreatedAt}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Create session: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to insert session: %w", err)
	}

	return nil
}

func (r *sessionRepo) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	query, args, err := dialect.From("sessions").
		Select("token", "user_id", "expires_at", "created_at").
		Where(goqu.Ex{colToken: token}).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for GetByToken: %w", err)
	}

	s := &domain.Session{}
	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&s.Token, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query session: %w", err)
	}

	return s, nil
}

func (r *sessionRepo) deleteSessionsWhere(ctx context.Context, where goqu.Ex) error {
	query, args, err := dialect.Delete("sessions").
		Where(where).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for delete session: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

func (r *sessionRepo) DeleteByToken(ctx context.Context, token string) error {
	return r.deleteSessionsWhere(ctx, goqu.Ex{colToken: token})
}

func (r *sessionRepo) DeleteByUserID(ctx context.Context, userID int64) error {
	return r.deleteSessionsWhere(ctx, goqu.Ex{colUserID: userID})
}

func (r *sessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	query, args, err := dialect.Delete("sessions").
		Where(goqu.Ex{"expires_at": goqu.Op{"lt": time.Now()}}).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("failed to build SQL for DeleteExpired: %w", err)
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired sessions: %w", err)
	}

	return res.RowsAffected()
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/mysql"

	"selfcloud/internal/db"
	"selfcloud/internal/domain"
)

type shareRepo struct {
	db db.DBTX
}

func NewShareRepository(d db.DBTX) ShareRepository {
	return &shareRepo{db: d}
}

func (r *shareRepo) Create(ctx context.Context, share *domain.Share) error {
	query, args, err := dialect.Insert("shares").
		Cols("token", "user_id", "path", "is_dir", "expires_at", "created_at").
		Vals(goqu.Vals{share.Token, share.UserID, share.Path, share.IsDir, share.ExpiresAt, share.CreatedAt}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Create share: %w", err)
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert share: %w", err)
	}

	if id, err := res.LastInsertId(); err == nil {
		share.ID = id
	}

	return nil
}

func (r *shareRepo) GetByToken(ctx context.Context, token string) (*domain.Share, error) {
	query, args, err := dialect.From("shares").
		Select("id", "token", "user_id", "path", "is_dir", "expires_at", "created_at").
		Where(goqu.Ex{colToken: token}).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for GetByToken: %w", err)
	}

	s := &domain.Share{}
	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&s.ID, &s.Token, &s.UserID, &s.Path, &s.IsDir, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query share: %w", err)
	}

	return s, nil
}

func (r *shareRepo) GetByPath(ctx context.Context, userID int64, path string) (*domain.Share, error) {
	query, args, err := dialect.From("shares").
		Select("id", "token", "user_id", "path", "is_dir", "expires_at", "created_at").
		Where(goqu.Ex{colUserID: userID, colPath: path}).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for GetByPath share: %w", err)
	}

	s := &domain.Share{}
	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&s.ID, &s.Token, &s.UserID, &s.Path, &s.IsDir, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query share: %w", err)
	}

	return s, nil
}

func (r *shareRepo) ListByUserID(ctx context.Context, userID int64) ([]*domain.Share, error) {
	query, args, err := dialect.From("shares").
		Select("id", "token", "user_id", "path", "is_dir", "expires_at", "created_at").
		Where(goqu.Ex{colUserID: userID}).
		Order(goqu.I("created_at").Desc()).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for ListByUserID shares: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query shares: %w", err)
	}
	defer rows.Close()

	var shares []*domain.Share
	for rows.Next() {
		s := &domain.Share{}
		if err := rows.Scan(&s.ID, &s.Token, &s.UserID, &s.Path, &s.IsDir, &s.ExpiresAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan share: %w", err)
		}
		shares = append(shares, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during shares iteration: %w", err)
	}

	return shares, nil
}

func (r *shareRepo) DeleteByToken(ctx context.Context, token string, userID int64) error {
	query, args, err := dialect.Delete("shares").
		Where(goqu.Ex{colToken: token, colUserID: userID}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for DeleteByToken share: %w", err)
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete share: %w", err)
	}

	if affected, _ := res.RowsAffected(); affected == 0 {
		return fmt.Errorf("share %q not found or access denied", token)
	}

	return nil
}

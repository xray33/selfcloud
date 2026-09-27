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

type identityRepo struct {
	db db.DBTX
}

func NewIdentityRepository(d db.DBTX) IdentityRepository {
	return &identityRepo{db: d}
}

func (r *identityRepo) Create(ctx context.Context, identity *domain.UserIdentity) error {
	query, args, err := dialect.Insert("user_identities").
		Cols("user_id", "provider", "provider_user_id", "email", "created_at").
		Vals(goqu.Vals{identity.UserID, identity.Provider, identity.ProviderUserID, identity.Email, identity.CreatedAt}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Create identity: %w", err)
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert user identity: %w", err)
	}

	if id, err := res.LastInsertId(); err == nil {
		identity.ID = id
	}

	return nil
}

func (r *identityRepo) GetByProviderUID(ctx context.Context, provider, providerUserID string) (*domain.UserIdentity, error) {
	query, args, err := dialect.From("user_identities").
		Select("id", "user_id", "provider", "provider_user_id", "email", "created_at").
		Where(goqu.Ex{"provider": provider, "provider_user_id": providerUserID}).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for GetByProviderUID: %w", err)
	}

	id := &domain.UserIdentity{}
	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&id.ID, &id.UserID, &id.Provider, &id.ProviderUserID, &id.Email, &id.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query identity: %w", err)
	}

	return id, nil
}

func (r *identityRepo) ListByUserID(ctx context.Context, userID int64) ([]*domain.UserIdentity, error) {
	query, args, err := dialect.From("user_identities").
		Select("id", "user_id", "provider", "provider_user_id", "email", "created_at").
		Where(goqu.Ex{"user_id": userID}).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for ListByUserID identities: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query identities: %w", err)
	}
	defer rows.Close()

	var identities []*domain.UserIdentity
	for rows.Next() {
		id := &domain.UserIdentity{}
		if err := rows.Scan(&id.ID, &id.UserID, &id.Provider, &id.ProviderUserID, &id.Email, &id.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan identity: %w", err)
		}
		identities = append(identities, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during identities iteration: %w", err)
	}

	return identities, nil
}

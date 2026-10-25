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

type userRepo struct {
	db db.DBTX
}

func NewUserRepository(d db.DBTX) UserRepository {
	return &userRepo{db: d}
}

func (r *userRepo) getUserWhere(ctx context.Context, where goqu.Ex) (*domain.User, error) {
	query, args, err := dialect.From("users").
		Select("id", "username", "password_hash", "email", "created_at", "updated_at").
		Where(where).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for query user: %w", err)
	}

	user := &domain.User{}
	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Email, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	return user, nil
}

func (r *userRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return r.getUserWhere(ctx, goqu.Ex{"username": username})
}

func (r *userRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	if email == "" {
		return nil, nil
	}
	return r.getUserWhere(ctx, goqu.Ex{"email": email})
}

func (r *userRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return r.getUserWhere(ctx, goqu.Ex{"id": id})
}

func (r *userRepo) Create(ctx context.Context, user *domain.User) error {
	query, args, err := dialect.Insert("users").
		Cols("username", "password_hash", "email", "created_at", "updated_at").
		Vals(goqu.Vals{user.Username, user.PasswordHash, user.Email, time.Now(), time.Now()}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Create user: %w", err)
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert user: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		user.ID = id
	}

	return nil
}

func (r *userRepo) Update(ctx context.Context, user *domain.User) error {
	user.UpdatedAt = time.Now()
	query, args, err := dialect.Update("users").
		Set(goqu.Record{
			"password_hash": user.PasswordHash,
			"email":         user.Email,
			colUpdatedAt:    user.UpdatedAt,
		}).
		Where(goqu.Ex{"id": user.ID}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Update user: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

func (r *userRepo) ListAll(ctx context.Context) ([]*domain.User, error) {
	query, args, err := dialect.From("users").
		Select("id", "username", "password_hash", "email", "created_at", "updated_at").
		Order(goqu.I("id").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for ListAll: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		user := &domain.User{}
		if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Email, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during users iteration: %w", err)
	}
	return users, nil
}

func (r *userRepo) Delete(ctx context.Context, username string) error {
	query, args, err := dialect.Delete("users").
		Where(goqu.Ex{"username": username}).
		ToSQL()
	if err != nil {
		return fmt.Errorf("failed to build SQL for Delete user: %w", err)
	}
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("user %q not found", username)
	}
	return nil
}

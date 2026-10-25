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

type OAuthRepository interface {
	CreateAuthCode(ctx context.Context, code *domain.OAuthAuthCode) error
	GetAuthCode(ctx context.Context, code string) (*domain.OAuthAuthCode, error)
	DeleteAuthCode(ctx context.Context, code string) error

	CreateToken(ctx context.Context, token *domain.OAuthToken) error
	GetTokenByAccessToken(ctx context.Context, accessToken string) (*domain.OAuthToken, error)
	GetTokenByRefreshToken(ctx context.Context, refreshToken string) (*domain.OAuthToken, error)
	DeleteTokenByAccessToken(ctx context.Context, accessToken string) error
	DeleteTokensByUserID(ctx context.Context, userID int64) error
}

type oauthRepo struct {
	db db.DBTX
}

func NewOAuthRepository(d db.DBTX) OAuthRepository {
	return &oauthRepo{db: d}
}

func (r *oauthRepo) CreateAuthCode(ctx context.Context, code *domain.OAuthAuthCode) error {
	query, args, err := dialect.Insert("oauth_auth_codes").
		Cols("code", "client_id", "user_id", "redirect_uri", "code_challenge", "code_challenge_method", "scope", "expires_at", "created_at").
		Vals(goqu.Vals{
			code.Code,
			code.ClientID,
			code.UserID,
			code.RedirectURI,
			code.CodeChallenge,
			code.CodeChallengeMethod,
			code.Scope,
			code.ExpiresAt,
			code.CreatedAt,
		}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for CreateAuthCode: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to insert auth code: %w", err)
	}

	return nil
}

func (r *oauthRepo) GetAuthCode(ctx context.Context, code string) (*domain.OAuthAuthCode, error) {
	query, args, err := dialect.From("oauth_auth_codes").
		Select("code", "client_id", "user_id", "redirect_uri", "code_challenge", "code_challenge_method", "scope", "expires_at", "created_at").
		Where(goqu.Ex{"code": code}).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for GetAuthCode: %w", err)
	}

	c := &domain.OAuthAuthCode{}
	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&c.Code, &c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &c.CodeChallengeMethod, &c.Scope, &c.ExpiresAt, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query auth code: %w", err)
	}

	return c, nil
}

func (r *oauthRepo) DeleteAuthCode(ctx context.Context, code string) error {
	query, args, err := dialect.Delete("oauth_auth_codes").
		Where(goqu.Ex{"code": code}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for DeleteAuthCode: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete auth code: %w", err)
	}

	return nil
}

func (r *oauthRepo) CreateToken(ctx context.Context, token *domain.OAuthToken) error {
	query, args, err := dialect.Insert("oauth_tokens").
		Cols("client_id", "user_id", "access_token", "refresh_token", "access_token_expires_at", "refresh_token_expires_at", "created_at").
		Vals(goqu.Vals{
			token.ClientID,
			token.UserID,
			token.AccessToken,
			token.RefreshToken,
			token.AccessTokenExpiresAt,
			token.RefreshTokenExpiresAt,
			token.CreatedAt,
		}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for CreateToken: %w", err)
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert oauth token: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		token.ID = id
	}

	return nil
}

func (r *oauthRepo) getTokenWhere(ctx context.Context, where goqu.Ex) (*domain.OAuthToken, error) {
	query, args, err := dialect.From("oauth_tokens").
		Select("id", "client_id", "user_id", "access_token", "refresh_token", "access_token_expires_at", "refresh_token_expires_at", "created_at").
		Where(where).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for oauth token query: %w", err)
	}

	t := &domain.OAuthToken{}
	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&t.ID, &t.ClientID, &t.UserID, &t.AccessToken, &t.RefreshToken, &t.AccessTokenExpiresAt, &t.RefreshTokenExpiresAt, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query oauth token: %w", err)
	}

	return t, nil
}

func (r *oauthRepo) deleteTokensWhere(ctx context.Context, where goqu.Ex) error {
	query, args, err := dialect.Delete("oauth_tokens").
		Where(where).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for delete oauth tokens: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete oauth tokens: %w", err)
	}

	return nil
}

func (r *oauthRepo) GetTokenByAccessToken(ctx context.Context, accessToken string) (*domain.OAuthToken, error) {
	return r.getTokenWhere(ctx, goqu.Ex{"access_token": accessToken})
}

func (r *oauthRepo) GetTokenByRefreshToken(ctx context.Context, refreshToken string) (*domain.OAuthToken, error) {
	return r.getTokenWhere(ctx, goqu.Ex{"refresh_token": refreshToken})
}

func (r *oauthRepo) DeleteTokenByAccessToken(ctx context.Context, accessToken string) error {
	return r.deleteTokensWhere(ctx, goqu.Ex{"access_token": accessToken})
}

func (r *oauthRepo) DeleteTokensByUserID(ctx context.Context, userID int64) error {
	return r.deleteTokensWhere(ctx, goqu.Ex{colUserID: userID})
}

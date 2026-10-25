package repository

import (
	"context"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/mysql"

	"selfcloud/internal/db"
	"selfcloud/internal/domain"
)

// dialect is the shared MySQL goqu dialect builder for all repositories.
var dialect = goqu.Dialect("mysql")

const (
	colUserID    = "user_id"
	colPath      = "path"
	colUpdatedAt = "updated_at"
	colToken     = "token"
)

// Repositories holds all domain repositories configured against a common DBTX.
type Repositories struct {
	Users      UserRepository
	Nodes      NodeRepository
	Sessions   SessionRepository
	Shares     ShareRepository
	Identities IdentityRepository
	OAuth      OAuthRepository
}

// NewRepositories creates a bundle of repositories bound to the given DBTX (*sql.DB or *sql.Tx).
func NewRepositories(d db.DBTX) *Repositories {
	return &Repositories{
		Users:      NewUserRepository(d),
		Nodes:      NewNodeRepository(d),
		Sessions:   NewSessionRepository(d),
		Shares:     NewShareRepository(d),
		Identities: NewIdentityRepository(d),
		OAuth:      NewOAuthRepository(d),
	}
}

// Transactor manages atomic transactions, creating a transaction-bound Repositories bundle.
type Transactor interface {
	WithTx(ctx context.Context, fn func(txRepo *Repositories) error) error
}

type UserRepository interface {
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	Create(ctx context.Context, user *domain.User) error
	Update(ctx context.Context, user *domain.User) error
	ListAll(ctx context.Context) ([]*domain.User, error)
	Delete(ctx context.Context, username string) error
}

type SessionRepository interface {
	Create(ctx context.Context, session *domain.Session) error
	GetByToken(ctx context.Context, token string) (*domain.Session, error)
	DeleteByToken(ctx context.Context, token string) error
	DeleteByUserID(ctx context.Context, userID int64) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type NodeRepository interface {
	GetByPath(ctx context.Context, userID int64, path string) (*domain.Node, error)
	ListByParentPath(ctx context.Context, userID int64, parentPath string) ([]*domain.Node, error)
	Create(ctx context.Context, node *domain.Node) error
	Update(ctx context.Context, node *domain.Node) error
	Delete(ctx context.Context, userID int64, path string) error
	DeletePrefix(ctx context.Context, userID int64, pathPrefix string) error
	MovePrefix(ctx context.Context, userID int64, oldPrefix, newPrefix string) error
	EnsureParentDirectoryExists(ctx context.Context, userID int64, parentPath string) error
	TouchAncestors(ctx context.Context, userID int64, paths ...string) error
	RecalculateDirectorySizes(ctx context.Context, userID int64) error
}

type ShareRepository interface {
	Create(ctx context.Context, share *domain.Share) error
	GetByToken(ctx context.Context, token string) (*domain.Share, error)
	GetByPath(ctx context.Context, userID int64, path string) (*domain.Share, error)
	ListByUserID(ctx context.Context, userID int64) ([]*domain.Share, error)
	DeleteByToken(ctx context.Context, token string, userID int64) error
}

type IdentityRepository interface {
	Create(ctx context.Context, identity *domain.UserIdentity) error
	GetByProviderUID(ctx context.Context, provider, providerUserID string) (*domain.UserIdentity, error)
	ListByUserID(ctx context.Context, userID int64) ([]*domain.UserIdentity, error)
}



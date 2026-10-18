package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// DBTX is the common interface satisfied by both *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// PoolConfig holds connection pool settings for the database.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func Connect(driver, dsn string, poolCfgs ...PoolConfig) (*sql.DB, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	maxOpen := 25
	maxIdle := 10
	maxLifetime := 5 * time.Minute
	maxIdleTime := 1 * time.Minute

	if len(poolCfgs) > 0 {
		cfg := poolCfgs[0]
		if cfg.MaxOpenConns > 0 {
			maxOpen = cfg.MaxOpenConns
		}
		if cfg.MaxIdleConns > 0 {
			maxIdle = cfg.MaxIdleConns
		}
		if cfg.ConnMaxLifetime > 0 {
			maxLifetime = cfg.ConnMaxLifetime
		}
		if cfg.ConnMaxIdleTime > 0 {
			maxIdleTime = cfg.ConnMaxIdleTime
		}
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifetime)
	db.SetConnMaxIdleTime(maxIdleTime)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

type migration struct {
	version     int
	description string
	run         func(db *sql.DB) error
}

func Migrate(db *sql.DB) error {
	// Ensure schema_migrations version table exists
	initTable := `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INT PRIMARY KEY,
		description VARCHAR(255) NOT NULL,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`
	if _, err := db.Exec(initTable); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	migrations := []migration{
		{
			version:     1,
			description: "Initial schema: users, nodes, sessions, shares, user_identities",
			run: func(db *sql.DB) error {
				queries := []string{
					`CREATE TABLE IF NOT EXISTS users (
						id BIGINT AUTO_INCREMENT PRIMARY KEY,
						username VARCHAR(255) NOT NULL UNIQUE,
						password_hash VARCHAR(255) NOT NULL,
						email VARCHAR(255) NOT NULL DEFAULT '',
						created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
						updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
					) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

					`CREATE TABLE IF NOT EXISTS nodes (
						id BIGINT AUTO_INCREMENT PRIMARY KEY,
						user_id BIGINT NOT NULL,
						path VARCHAR(700) NOT NULL,
						parent_path VARCHAR(700) NOT NULL,
						name VARCHAR(255) NOT NULL,
						is_dir TINYINT(1) NOT NULL DEFAULT 0,
						size BIGINT NOT NULL DEFAULT 0,
						etag VARCHAR(255) NOT NULL DEFAULT '',
						file_id VARCHAR(255) NOT NULL DEFAULT '',
						mtime DATETIME(6) NOT NULL,
						content_type VARCHAR(255) NOT NULL DEFAULT '',
						created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
						updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
						UNIQUE KEY idx_user_path (user_id, path),
						KEY idx_user_parent (user_id, parent_path),
						CONSTRAINT fk_nodes_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
					) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

					`CREATE TABLE IF NOT EXISTS sessions (
						token VARCHAR(64) PRIMARY KEY,
						user_id BIGINT NOT NULL,
						expires_at DATETIME NOT NULL,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						KEY idx_sessions_user (user_id),
						CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
					) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

					`CREATE TABLE IF NOT EXISTS shares (
						id BIGINT AUTO_INCREMENT PRIMARY KEY,
						token VARCHAR(64) NOT NULL UNIQUE,
						user_id BIGINT NOT NULL,
						path VARCHAR(700) NOT NULL,
						is_dir TINYINT(1) NOT NULL DEFAULT 0,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						KEY idx_shares_token (token),
						KEY idx_shares_user (user_id),
						CONSTRAINT fk_shares_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
					) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

					`CREATE TABLE IF NOT EXISTS user_identities (
						id BIGINT AUTO_INCREMENT PRIMARY KEY,
						user_id BIGINT NOT NULL,
						provider VARCHAR(50) NOT NULL,
						provider_user_id VARCHAR(255) NOT NULL,
						email VARCHAR(255) NOT NULL DEFAULT '',
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						UNIQUE KEY idx_provider_uid (provider, provider_user_id),
						KEY idx_identities_user (user_id),
						CONSTRAINT fk_identities_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
					) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
				}

				for _, q := range queries {
					if _, err := db.Exec(q); err != nil {
						return fmt.Errorf("migration query failed: %w", err)
					}
				}

				var fkExists int
				checkFKQuery := `SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS 
					WHERE TABLE_SCHEMA = DATABASE() AND CONSTRAINT_NAME = 'fk_nodes_user' AND TABLE_NAME = 'nodes'`
				if err := db.QueryRow(checkFKQuery).Scan(&fkExists); err == nil && fkExists == 0 {
					_, _ = db.Exec(`DELETE FROM nodes WHERE user_id NOT IN (SELECT id FROM users)`)
					_, _ = db.Exec(`ALTER TABLE nodes ADD CONSTRAINT fk_nodes_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE`)
				}
				return nil
			},
		},
		{
			version:     2,
			description: "Add expires_at column and index to shares table",
			run: func(db *sql.DB) error {
				var colExists int
				checkColQuery := `SELECT COUNT(*) FROM information_schema.COLUMNS 
					WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'shares' AND COLUMN_NAME = 'expires_at'`
				if err := db.QueryRow(checkColQuery).Scan(&colExists); err == nil && colExists == 0 {
					if _, err := db.Exec(`ALTER TABLE shares ADD COLUMN expires_at DATETIME NULL DEFAULT NULL AFTER is_dir`); err != nil {
						return fmt.Errorf("failed to add expires_at to shares: %w", err)
					}
					_, _ = db.Exec(`CREATE INDEX idx_shares_expires ON shares (expires_at)`)
				}
				return nil
			},
		},
		{
			version:     3,
			description: "Add idx_sessions_expires index to sessions table for cleanup",
			run: func(db *sql.DB) error {
				var idxExists int
				checkIdxQuery := `SELECT COUNT(*) FROM information_schema.STATISTICS 
					WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'sessions' AND INDEX_NAME = 'idx_sessions_expires'`
				if err := db.QueryRow(checkIdxQuery).Scan(&idxExists); err == nil && idxExists == 0 {
					_, _ = db.Exec(`CREATE INDEX idx_sessions_expires ON sessions (expires_at)`)
				}
				return nil
			},
		},
		{
			version:     4,
			description: "Create oauth_auth_codes and oauth_tokens tables for OAuth2/OIDC support",
			run: func(db *sql.DB) error {
				queries := []string{
					`CREATE TABLE IF NOT EXISTS oauth_auth_codes (
						code VARCHAR(128) PRIMARY KEY,
						client_id VARCHAR(255) NOT NULL,
						user_id BIGINT NOT NULL,
						redirect_uri VARCHAR(1024) NOT NULL,
						code_challenge VARCHAR(255) NOT NULL DEFAULT '',
						code_challenge_method VARCHAR(32) NOT NULL DEFAULT '',
						scope VARCHAR(255) NOT NULL DEFAULT '',
						expires_at DATETIME NOT NULL,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						CONSTRAINT fk_oauth_codes_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
					) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,

					`CREATE TABLE IF NOT EXISTS oauth_tokens (
						id BIGINT AUTO_INCREMENT PRIMARY KEY,
						client_id VARCHAR(255) NOT NULL,
						user_id BIGINT NOT NULL,
						access_token VARCHAR(255) NOT NULL UNIQUE,
						refresh_token VARCHAR(255) NOT NULL UNIQUE,
						access_token_expires_at DATETIME NOT NULL,
						refresh_token_expires_at DATETIME NOT NULL,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						CONSTRAINT fk_oauth_tokens_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
					) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`,
				}

				for _, q := range queries {
					if _, err := db.Exec(q); err != nil {
						return fmt.Errorf("migration query failed: %w", err)
					}
				}
				return nil
			},
		},
	}

	for _, m := range migrations {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check migration status for version %d: %w", m.version, err)
		}
		if count > 0 {
			continue
		}

		if err := m.run(db); err != nil {
			return fmt.Errorf("failed applying migration %d (%s): %w", m.version, m.description, err)
		}

		if _, err := db.Exec(`INSERT INTO schema_migrations (version, description, applied_at) VALUES (?, ?, ?)`,
			m.version, m.description, time.Now()); err != nil {
			return fmt.Errorf("failed to record migration %d: %w", m.version, err)
		}
	}

	return nil
}

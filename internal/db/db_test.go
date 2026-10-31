package db

import (
	"os"
	"testing"
)

func TestMigrations(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "selfcloud:selfcloud_pass@tcp(127.0.0.1:3306)/selfcloud?parseTime=true"
	}

	sqlDB, err := Connect("mysql", dsn)
	if err != nil {
		t.Skipf("Skipping db migration test: MariaDB connection failed: %v", err)
		return
	}
	defer sqlDB.Close()

	if err := Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// Reset migration 5 record under the migration lock so runMigrations exercises it safely
	err = withMigrationLock(sqlDB, func() error {
		_, _ = sqlDB.Exec("DELETE FROM schema_migrations WHERE version = 5")
		return runMigrations(sqlDB)
	})
	if err != nil {
		t.Fatalf("Re-running migration 5 failed: %v", err)
	}
}

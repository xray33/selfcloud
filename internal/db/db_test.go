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

	// Reset migration 5 record so Migrate runs it
	_, _ = sqlDB.Exec("DELETE FROM schema_migrations WHERE version = 5")
	if err := Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
}

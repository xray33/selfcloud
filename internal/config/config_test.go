package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg := LoadConfig()
	if cfg.ServerPort != "8080" {
		t.Errorf("expected default ServerPort 8080, got %s", cfg.ServerPort)
	}
	if cfg.DBDriver != "mysql" {
		t.Errorf("expected default DBDriver mysql, got %s", cfg.DBDriver)
	}
	if cfg.DBMaxOpenConns != 25 {
		t.Errorf("expected default DBMaxOpenConns 25, got %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 10 {
		t.Errorf("expected default DBMaxIdleConns 10, got %d", cfg.DBMaxIdleConns)
	}
	if !cfg.TrustProxy {
		t.Errorf("expected default TrustProxy true, got %v", cfg.TrustProxy)
	}
	if cfg.CookieSecure != "auto" {
		t.Errorf("expected default CookieSecure auto, got %s", cfg.CookieSecure)
	}
	if cfg.ServerReadTimeout != 30*time.Minute {
		t.Errorf("expected default ServerReadTimeout 30m, got %v", cfg.ServerReadTimeout)
	}
	if cfg.RateLimitLoginRequests != 15 {
		t.Errorf("expected default RateLimitLoginRequests 15, got %d", cfg.RateLimitLoginRequests)
	}
	if cfg.EnableSpaces {
		t.Errorf("expected default EnableSpaces false, got %v", cfg.EnableSpaces)
	}
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	os.Setenv("SERVER_PORT", "9090")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("DB_MAX_OPEN_CONNS", "50")
	os.Setenv("DB_MAX_IDLE_CONNS", "20")
	os.Setenv("DB_CONN_MAX_LIFETIME", "10m")
	os.Setenv("DB_CONN_MAX_IDLE_TIME", "2m")
	os.Setenv("TRUST_PROXY", "false")
	os.Setenv("COOKIE_SECURE", "true")
	os.Setenv("SERVER_READ_TIMEOUT", "45m")
	os.Setenv("RATE_LIMIT_LOGIN_REQUESTS", "30")
	defer func() {
		os.Unsetenv("SERVER_PORT")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("DB_MAX_OPEN_CONNS")
		os.Unsetenv("DB_MAX_IDLE_CONNS")
		os.Unsetenv("DB_CONN_MAX_LIFETIME")
		os.Unsetenv("DB_CONN_MAX_IDLE_TIME")
		os.Unsetenv("TRUST_PROXY")
		os.Unsetenv("COOKIE_SECURE")
		os.Unsetenv("SERVER_READ_TIMEOUT")
		os.Unsetenv("RATE_LIMIT_LOGIN_REQUESTS")
	}()

	cfg := LoadConfig()
	if cfg.ServerPort != "9090" {
		t.Errorf("expected ServerPort 9090, got %s", cfg.ServerPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel debug, got %s", cfg.LogLevel)
	}
	if cfg.DBMaxOpenConns != 50 {
		t.Errorf("expected DBMaxOpenConns 50, got %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 20 {
		t.Errorf("expected DBMaxIdleConns 20, got %d", cfg.DBMaxIdleConns)
	}
	if cfg.TrustProxy {
		t.Errorf("expected TrustProxy false, got %v", cfg.TrustProxy)
	}
	if cfg.CookieSecure != "true" {
		t.Errorf("expected CookieSecure true, got %s", cfg.CookieSecure)
	}
	if cfg.ServerReadTimeout != 45*time.Minute {
		t.Errorf("expected ServerReadTimeout 45m, got %v", cfg.ServerReadTimeout)
	}
	if cfg.RateLimitLoginRequests != 30 {
		t.Errorf("expected RateLimitLoginRequests 30, got %d", cfg.RateLimitLoginRequests)
	}
}

func TestAuthMethodsConfig(t *testing.T) {
	t.Run("Default configuration enables both basic and oauth", func(t *testing.T) {
		cfg := LoadConfig()
		if !cfg.IsBasicAuthEnabled() {
			t.Errorf("expected Basic auth to be enabled by default")
		}
		if !cfg.IsOAuthEnabled() {
			t.Errorf("expected OAuth to be enabled by default")
		}
		expectedHdr := `Bearer realm="selfcloud", Basic realm="selfcloud"`
		if cfg.WWWAuthenticateHeader() != expectedHdr {
			t.Errorf("expected header %q, got %q", expectedHdr, cfg.WWWAuthenticateHeader())
		}
	})

	t.Run("Basic only configuration", func(t *testing.T) {
		os.Setenv("AUTH_METHODS", "basic")
		defer os.Unsetenv("AUTH_METHODS")

		cfg := LoadConfig()
		if !cfg.IsBasicAuthEnabled() {
			t.Errorf("expected Basic auth enabled")
		}
		if cfg.IsOAuthEnabled() {
			t.Errorf("expected OAuth disabled")
		}
		expectedHdr := `Basic realm="selfcloud"`
		if cfg.WWWAuthenticateHeader() != expectedHdr {
			t.Errorf("expected header %q, got %q", expectedHdr, cfg.WWWAuthenticateHeader())
		}
	})

	t.Run("OAuth only configuration", func(t *testing.T) {
		os.Setenv("AUTH_METHODS", "oauth")
		defer os.Unsetenv("AUTH_METHODS")

		cfg := LoadConfig()
		if cfg.IsBasicAuthEnabled() {
			t.Errorf("expected Basic auth disabled")
		}
		if !cfg.IsOAuthEnabled() {
			t.Errorf("expected OAuth enabled")
		}
		expectedHdr := `Bearer realm="selfcloud"`
		if cfg.WWWAuthenticateHeader() != expectedHdr {
			t.Errorf("expected header %q, got %q", expectedHdr, cfg.WWWAuthenticateHeader())
		}
	})
}


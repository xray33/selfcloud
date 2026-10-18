package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServerPort            string
	StorageDir            string
	DBDriver              string
	DBDSN                 string
	DBMaxOpenConns        int
	DBMaxIdleConns        int
	DBConnMaxLifetime     time.Duration
	DBConnMaxIdleTime     time.Duration
	LogLevel              string
	ServeStatic           bool
	AutoMigrate           bool
	SSOGoogleClientID     string
	SSOGoogleClientSecret string
	SSORedirectBaseURL    string
	MaxUploadSize         int64
	TrustProxy            bool
	CookieSecure          string
	ServerReadTimeout     time.Duration
	RateLimitLoginRequests int
	RateLimitLoginWindow  time.Duration
	MetricsAuthToken      string
	AuthMethods           []string
	EnableSpaces          bool
}

// DefaultMaxUploadSize is the default maximum upload limit across WebDAV and REST API (10 GB).
const DefaultMaxUploadSize int64 = 10 << 30

func LoadConfig() *Config {
	return &Config{
		ServerPort:            getEnv("SERVER_PORT", "8080"),
		StorageDir:            getEnv("STORAGE_DIR", "./data"),
		DBDriver:              getEnv("DB_DRIVER", "mysql"),
		DBDSN:                 getEnv("DB_DSN", "selfcloud:selfcloud_pass@tcp(127.0.0.1:3306)/selfcloud?parseTime=true"),
		DBMaxOpenConns:        getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:        getEnvAsInt("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLifetime:     getEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		DBConnMaxIdleTime:     getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 1*time.Minute),
		LogLevel:              getEnv("LOG_LEVEL", "info"),
		ServeStatic:           getEnvAsBool("SERVE_STATIC", true),
		AutoMigrate:           getEnvAsBool("AUTO_MIGRATE", true),
		SSOGoogleClientID:     getEnv("SSO_GOOGLE_CLIENT_ID", ""),
		SSOGoogleClientSecret: getEnv("SSO_GOOGLE_CLIENT_SECRET", ""),
		SSORedirectBaseURL:    getEnv("SSO_REDIRECT_BASE_URL", "http://localhost:8080"),
		MaxUploadSize:         getEnvAsInt64("MAX_UPLOAD_SIZE_BYTES", DefaultMaxUploadSize),
		TrustProxy:            getEnvAsBool("TRUST_PROXY", true),
		CookieSecure:          getEnv("COOKIE_SECURE", "auto"),
		ServerReadTimeout:     getEnvAsDuration("SERVER_READ_TIMEOUT", 30*time.Minute),
		RateLimitLoginRequests: getEnvAsInt("RATE_LIMIT_LOGIN_REQUESTS", 15),
		RateLimitLoginWindow:  getEnvAsDuration("RATE_LIMIT_LOGIN_WINDOW", 1*time.Minute),
		MetricsAuthToken:      getEnv("METRICS_AUTH_TOKEN", ""),
		AuthMethods:           parseAuthMethods(getEnv("AUTH_METHODS", "basic,oauth")),
		EnableSpaces:          getEnvAsBool("ENABLE_SPACES", false),
	}
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	return val == "true" || val == "1" || val == "yes"
}

func getEnvAsInt(key string, defaultValue int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	if i, err := strconv.Atoi(val); err == nil && i > 0 {
		return i
	}
	return defaultValue
}

func getEnvAsInt64(key string, defaultValue int64) int64 {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	if i, err := strconv.ParseInt(val, 10, 64); err == nil && i > 0 {
		return i
	}
	return defaultValue
}


func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	if d, err := time.ParseDuration(val); err == nil && d > 0 {
		return d
	}
	return defaultValue
}

func parseAuthMethods(val string) []string {
	if val == "" {
		return []string{"basic", "oauth"}
	}
	raw := strings.Split(val, ",")
	methods := make([]string, 0, len(raw))
	for _, m := range raw {
		trimmed := strings.ToLower(strings.TrimSpace(m))
		if trimmed != "" {
			methods = append(methods, trimmed)
		}
	}
	if len(methods) == 0 {
		return []string{"basic", "oauth"}
	}
	return methods
}

func (c *Config) IsBasicAuthEnabled() bool {
	for _, m := range c.AuthMethods {
		if m == "basic" {
			return true
		}
	}
	return false
}

func (c *Config) IsOAuthEnabled() bool {
	for _, m := range c.AuthMethods {
		if m == "oauth" || m == "oauth2" || m == "bearer" {
			return true
		}
	}
	return false
}

func (c *Config) WWWAuthenticateHeader() string {
	var parts []string
	if c.IsOAuthEnabled() {
		parts = append(parts, `Bearer realm="selfcloud"`)
	}
	if c.IsBasicAuthEnabled() {
		parts = append(parts, `Basic realm="selfcloud"`)
	}
	if len(parts) == 0 {
		return `Basic realm="selfcloud"`
	}
	return strings.Join(parts, ", ")
}



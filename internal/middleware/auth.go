package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"selfcloud/internal/audit"
	"selfcloud/internal/config"
	"selfcloud/internal/domain"
	"selfcloud/internal/repository"
)

const UserObjKey contextKey = "user_obj"

func UserFromContext(ctx context.Context) *domain.User {
	if u, ok := ctx.Value(UserObjKey).(*domain.User); ok {
		return u
	}
	return nil
}

// WithUser returns a copy of parent ctx with the user and username injected.
func WithUser(ctx context.Context, user *domain.User) context.Context {
	ctx = context.WithValue(ctx, UserKey, user.Username)
	return context.WithValue(ctx, UserObjKey, user)
}

func BasicAuthMiddleware(userRepo repository.UserRepository, rateLimiters ...*IPRateLimiter) func(http.Handler) http.Handler {
	var limiter *IPRateLimiter
	if len(rateLimiters) > 0 {
		limiter = rateLimiters[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getClientIP(r)
			if limiter != nil {
				if allowed, retryAfter := limiter.Check(ip); !allowed {
					audit.RecordFailure(r.Context(), "auth_rate_limited", "",
						fmt.Sprintf("Request rejected: rate limit exceeded for %s", ip),
					)
					w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())))
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
					return
				}
			}

			username, password, ok := r.BasicAuth()
			if !ok {
				audit.RecordFailure(r.Context(), "auth_missing", "",
					fmt.Sprintf("Request rejected: missing Basic Auth credentials from %s", ip),
				)
				w.Header().Set("WWW-Authenticate", `Bearer realm="selfcloud", Basic realm="selfcloud"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			user, err := userRepo.GetByUsername(r.Context(), username)
			if err != nil || user == nil {
				if limiter != nil {
					limiter.RecordFailure(ip)
				}
				domain.CheckDummyPassword(password)
				audit.RecordFailure(r.Context(), "auth_unknown_user", username,
					fmt.Sprintf("Authentication failed: user %q not found", username),
				)
				w.Header().Set("WWW-Authenticate", `Basic realm="selfcloud"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			if !user.CheckPassword(password) {
				if limiter != nil {
					limiter.RecordFailure(ip)
				}
				audit.RecordFailure(r.Context(), "auth_bad_password", username,
					fmt.Sprintf("Authentication failed: invalid password for user %q", username),
				)
				w.Header().Set("WWW-Authenticate", `Basic realm="selfcloud"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			if limiter != nil {
				limiter.Reset(ip)
			}

			ctx := context.WithValue(r.Context(), UserKey, user.Username)
			ctx = context.WithValue(ctx, UserObjKey, user)
			// Write username into the shared *audit.Event so LoggingMiddleware can
			// read it after ServeHTTP returns, without needing to inspect child contexts.
			audit.SetUsername(r.Context(), user.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// DualAuthMiddleware authenticates requests supporting both Bearer token and HTTP Basic Auth based on configuration.
func DualAuthMiddleware(userRepo repository.UserRepository, sessionRepo repository.SessionRepository, cfg *config.Config, rateLimiters ...*IPRateLimiter) func(http.Handler) http.Handler {
	var limiter *IPRateLimiter
	if len(rateLimiters) > 0 {
		limiter = rateLimiters[0]
	}

	oauthEnabled := true
	basicEnabled := true
	wwwAuth := `Bearer realm="selfcloud", Basic realm="selfcloud"`
	if cfg != nil {
		oauthEnabled = cfg.IsOAuthEnabled()
		basicEnabled = cfg.IsBasicAuthEnabled()
		wwwAuth = cfg.WWWAuthenticateHeader()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getClientIP(r)

			// 1. If user is already authenticated in context, proceed.
			if user := UserFromContext(r.Context()); user != nil {
				next.ServeHTTP(w, r)
				return
			}

			// 2. Check for Bearer token
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				if !oauthEnabled {
					audit.RecordFailure(r.Context(), "auth_oauth_disabled", "",
						fmt.Sprintf("Request rejected: OAuth/Bearer auth disabled from %s", ip),
					)
					w.Header().Set("WWW-Authenticate", wwwAuth)
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}

				token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
				if token != "" && sessionRepo != nil {
					session, err := sessionRepo.GetByToken(r.Context(), token)
					if err == nil && session != nil {
						if session.IsExpired() {
							_ = sessionRepo.DeleteByToken(r.Context(), token)
						} else {
							user, err := userRepo.GetByID(r.Context(), session.UserID)
							if err == nil && user != nil {
								ctx := context.WithValue(r.Context(), UserKey, user.Username)
								ctx = context.WithValue(ctx, UserObjKey, user)
								audit.SetUsername(r.Context(), user.Username)
								next.ServeHTTP(w, r.WithContext(ctx))
								return
							}
						}
					}
				}
				audit.RecordFailure(r.Context(), "auth_bad_bearer", "",
					fmt.Sprintf("Request rejected: invalid or expired Bearer token from %s", ip),
				)
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", realm="selfcloud"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// 3. Check for Basic Auth
			username, password, ok := r.BasicAuth()
			if ok {
				if !basicEnabled {
					audit.RecordFailure(r.Context(), "auth_basic_disabled", "",
						fmt.Sprintf("Request rejected: Basic auth disabled from %s", ip),
					)
					w.Header().Set("WWW-Authenticate", wwwAuth)
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}

				// Rate limiting for Basic Auth attempts
				if limiter != nil {
					if allowed, retryAfter := limiter.Check(ip); !allowed {
						audit.RecordFailure(r.Context(), "auth_rate_limited", "",
							fmt.Sprintf("Request rejected: rate limit exceeded for %s", ip),
						)
						w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())))
						http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
						return
					}
				}

				user, err := userRepo.GetByUsername(r.Context(), username)
				if err != nil || user == nil {
					if limiter != nil {
						limiter.RecordFailure(ip)
					}
					domain.CheckDummyPassword(password)
					audit.RecordFailure(r.Context(), "auth_unknown_user", username,
						fmt.Sprintf("Authentication failed: user %q not found", username),
					)
					w.Header().Set("WWW-Authenticate", `Basic realm="selfcloud"`)
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}

				if !user.CheckPassword(password) {
					if limiter != nil {
						limiter.RecordFailure(ip)
					}
					audit.RecordFailure(r.Context(), "auth_bad_password", username,
						fmt.Sprintf("Authentication failed: invalid password for user %q", username),
					)
					w.Header().Set("WWW-Authenticate", `Basic realm="selfcloud"`)
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}

				if limiter != nil {
					limiter.Reset(ip)
				}

				ctx := context.WithValue(r.Context(), UserKey, user.Username)
				ctx = context.WithValue(ctx, UserObjKey, user)
				audit.SetUsername(r.Context(), user.Username)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 4. Missing credentials
			audit.RecordFailure(r.Context(), "auth_missing", "",
				fmt.Sprintf("Request rejected: missing credentials from %s", ip),
			)
			w.Header().Set("WWW-Authenticate", wwwAuth)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
}

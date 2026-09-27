package middleware

import (
	"context"
	"fmt"
	"net/http"

	"selfcloud/internal/audit"
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
				w.Header().Set("WWW-Authenticate", `Basic realm="selfcloud"`)
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

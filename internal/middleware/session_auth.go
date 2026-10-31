package middleware

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"selfcloud/internal/audit"
	"selfcloud/internal/domain"
	"selfcloud/internal/repository"
)

const SessionCookieName = "selfcloud_session"

func resolveSessionUser(ctx context.Context, sessionRepo repository.SessionRepository, userRepo repository.UserRepository, token string) *domain.User {
	if token == "" || sessionRepo == nil || userRepo == nil {
		return nil
	}
	session, err := sessionRepo.GetByToken(ctx, token)
	if err != nil || session == nil {
		return nil
	}
	if session.IsExpired() {
		_ = sessionRepo.DeleteByToken(ctx, token)
		return nil
	}
	user, err := userRepo.GetByID(ctx, session.UserID)
	if err != nil || user == nil {
		return nil
	}
	return user
}

func SessionAuthMiddleware(sessionRepo repository.SessionRepository, userRepo repository.UserRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ExtractSessionToken(r)
			if user := resolveSessionUser(r.Context(), sessionRepo, userRepo, token); user != nil {
				ctx := context.WithValue(r.Context(), UserKey, user.Username)
				ctx = context.WithValue(ctx, UserObjKey, user)
				audit.SetUsername(ctx, user.Username)
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireSessionAuthMiddleware enforces that a valid session or Bearer token is present, returning HTTP 401 JSON if not.
func RequireSessionAuthMiddleware(sessionRepo repository.SessionRepository, userRepo repository.UserRepository) func(http.Handler) http.Handler {
	authenticate := SessionAuthMiddleware(sessionRepo, userRepo)
	return func(next http.Handler) http.Handler {
		return authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ValidateCSRF(r) {
				audit.RecordFailure(r.Context(), "csrf_rejected", r.URL.Path, "CSRF check failed: cross-origin or mismatched origin/referer")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Forbidden: CSRF check failed"}`))
				return
			}

			if UserFromContext(r.Context()) == nil {
				audit.RecordFailure(r.Context(), "api_unauthorized", r.URL.Path, "API request rejected: missing or invalid session credentials")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// ValidateCSRF checks Origin / Referer for cookie-authenticated mutating requests.
func ValidateCSRF(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return true
	}

	// Requests authenticated via Bearer token are not subject to browser CSRF.
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return true
	}

	// If no session cookie is present, CSRF is not applicable here (will fail auth later).
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return true
	}

	// Reject cross-site requests signaled by modern browsers.
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "null" {
		return false
	}

	targetHost := r.Host
	trustedForwardedHost := ""
	if IsProxyTrusted() {
		trustedForwardedHost = r.Header.Get("X-Forwarded-Host")
	}

	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			return false
		}
		return isHostAllowed(u.Host, targetHost, trustedForwardedHost)
	}

	referer := r.Header.Get("Referer")
	if referer != "" {
		u, err := url.Parse(referer)
		if err != nil || u.Host == "" {
			return false
		}
		return isHostAllowed(u.Host, targetHost, trustedForwardedHost)
	}

	// Neither Origin nor Referer provided (e.g. non-browser HTTP clients like curl or Go tests).
	return true
}

func isHostAllowed(sourceHost, targetHost, forwardedHost string) bool {
	if strings.EqualFold(sourceHost, targetHost) {
		return true
	}
	if forwardedHost != "" && strings.EqualFold(sourceHost, forwardedHost) {
		return true
	}

	// Allow cross-port communication on loopback for local development (e.g. Vite on 5173, backend on 8080).
	srcH, _, err := net.SplitHostPort(sourceHost)
	if err != nil {
		srcH = sourceHost
	}
	tgtH, _, err := net.SplitHostPort(targetHost)
	if err != nil {
		tgtH = targetHost
	}
	if isLoopback(srcH) && isLoopback(tgtH) {
		return true
	}

	return false
}

func isLoopback(h string) bool {
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") || strings.EqualFold(h, "127.0.0.1") || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ExtractSessionToken extracts the session token from either the selfcloud_session cookie or the Authorization Bearer header.
func ExtractSessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	authHeader := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(authHeader, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

var (
	trustProxyMu sync.RWMutex
	trustProxy   = true
	cookieSecure = "auto"
)

// ConfigureSecurity configures proxy trust and cookie secure policy.
func ConfigureSecurity(trustedProxy bool, secureFlag string) {
	trustProxyMu.Lock()
	defer trustProxyMu.Unlock()
	trustProxy = trustedProxy
	if secureFlag != "" {
		cookieSecure = strings.ToLower(strings.TrimSpace(secureFlag))
	}
}

// IsProxyTrusted returns whether reverse proxy headers are trusted.
func IsProxyTrusted() bool {
	trustProxyMu.RLock()
	defer trustProxyMu.RUnlock()
	return trustProxy
}

// IsSecureRequest returns true if the request was made via TLS or forwarded by a trusted secure proxy.
func IsSecureRequest(r *http.Request) bool {
	trustProxyMu.RLock()
	defer trustProxyMu.RUnlock()

	switch cookieSecure {
	case "true":
		return true
	case "false":
		return false
	default:
		if r.TLS != nil {
			return true
		}
		if trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return false
	}
}

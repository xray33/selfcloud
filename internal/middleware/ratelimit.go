package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

type ipRecord struct {
	count     int
	resetTime time.Time
}

// IPRateLimiter tracks request counts per IP within a sliding time window.
type IPRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	records map[string]*ipRecord
	stopCh  chan struct{}
}

// NewIPRateLimiter creates a new rate limiter allowing up to limit requests per window per IP.
// If limit <= 0, rate limiting is disabled.
func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	rl := &IPRateLimiter{
		limit:   limit,
		window:  window,
		records: make(map[string]*ipRecord),
		stopCh:  make(chan struct{}),
	}
	if limit > 0 && window > 0 {
		go rl.cleanupLoop()
	}
	return rl
}

// Close stops the background cleanup goroutine.
func (rl *IPRateLimiter) Close() {
	select {
	case <-rl.stopCh:
	default:
		close(rl.stopCh)
	}
}

func (rl *IPRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.window * 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, rec := range rl.records {
				if now.After(rec.resetTime) {
					delete(rl.records, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCh:
			return
		}
	}
}

// Allow reports whether the given IP is allowed to make a request, and returns retry-after duration if denied.
func (rl *IPRateLimiter) Allow(ip string) (bool, time.Duration) {
	if rl.limit <= 0 {
		return true, 0
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rec, exists := rl.records[ip]
	if !exists || now.After(rec.resetTime) {
		rl.records[ip] = &ipRecord{
			count:     1,
			resetTime: now.Add(rl.window),
		}
		return true, 0
	}

	if rec.count < rl.limit {
		rec.count++
		return true, 0
	}

	retryAfter := rec.resetTime.Sub(now)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return false, retryAfter
}

// Check reports whether the given IP is currently allowed (i.e. has not exceeded the limit).
// It does not increment the counter.
func (rl *IPRateLimiter) Check(ip string) (bool, time.Duration) {
	if rl == nil || rl.limit <= 0 {
		return true, 0
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rec, exists := rl.records[ip]
	if !exists || now.After(rec.resetTime) {
		return true, 0
	}

	if rec.count < rl.limit {
		return true, 0
	}

	retryAfter := rec.resetTime.Sub(now)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return false, retryAfter
}

// RecordFailure increments the failure count for an IP and reports whether the IP is still under the limit.
func (rl *IPRateLimiter) RecordFailure(ip string) (bool, time.Duration) {
	if rl == nil {
		return true, 0
	}
	return rl.Allow(ip)
}

// Reset clears the rate limit counter for the given IP.
func (rl *IPRateLimiter) Reset(ip string) {
	if rl == nil || rl.limit <= 0 {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.records, ip)
}

// LimitMiddleware wraps an http.Handler with rate limiting per client IP.
func (rl *IPRateLimiter) LimitMiddleware(next http.Handler) http.Handler {
	if rl.limit <= 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		allowed, retryAfter := rl.Allow(ip)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again later."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

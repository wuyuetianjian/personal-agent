package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"agent/internal/config"
	"agent/internal/observability"
)

type SecurityPolicy struct {
	AuthToken          string
	AllowedOrigins     []string
	MaxBodyBytes       int64
	RateLimitPerMinute int
	Now                func() time.Time
}

func NewSecurityPolicy(cfg config.APISecurityConfig) SecurityPolicy {
	token := ""
	if cfg.AuthTokenEnv != "" {
		token, _ = config.EnvValue(cfg.AuthTokenEnv)
	}
	return SecurityPolicy{
		AuthToken:          token,
		AllowedOrigins:     append([]string(nil), cfg.AllowedOrigins...),
		MaxBodyBytes:       cfg.MaxBodyBytes,
		RateLimitPerMinute: cfg.RateLimitPerMinute,
	}
}

func (p SecurityPolicy) Middleware(next http.Handler) http.Handler {
	limiter := newRateLimiter(p.RateLimitPerMinute, p.Now)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSecurityHeaders(w)
		if !p.applyCORS(w, r) {
			writeError(w, http.StatusForbidden, "origin_denied")
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if p.MaxBodyBytes > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, p.MaxBodyBytes)
		}
		if p.AuthToken != "" && !authorized(r, p.AuthToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !limiter.Allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func redactForAPI(value string) string {
	return observability.Redact(value)
}

func writeSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
}

func (p SecurityPolicy) applyCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range p.AllowedOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			return true
		}
	}
	return len(p.AllowedOrigins) == 0
}

func authorized(r *http.Request, token string) bool {
	return r.Header.Get("Authorization") == "Bearer "+token
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return "local"
	}
	return host
}

type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	now     func() time.Time
	windows map[string]rateWindow
}

type rateWindow struct {
	minute int64
	count  int
}

func newRateLimiter(limit int, now func() time.Time) *rateLimiter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &rateLimiter{limit: limit, now: now, windows: map[string]rateWindow{}}
}

func (l *rateLimiter) Allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "local"
	}
	minute := l.now().Unix() / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.windows[key]
	if window.minute != minute {
		window = rateWindow{minute: minute}
	}
	window.count++
	l.windows[key] = window
	return window.count <= l.limit
}

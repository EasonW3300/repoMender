package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/metrics"
)

// net/http parses request metadata while sync protects the fixed-window map;
// the metrics registry records rejected requests without coupling policy to
// Prometheus implementation details.

type window struct {
	started time.Time
	count   int
}

type Limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	clients map[string]window
	metrics *metrics.Registry
}

func New(perMinute int, registry *metrics.Registry) *Limiter {
	if perMinute <= 0 {
		perMinute = 120
	}
	return &Limiter{limit: perMinute, window: time.Minute, clients: make(map[string]window), metrics: registry}
}

// Middleware exempts probes and scraping from application traffic quotas, but
// keeps webhooks and authenticated API endpoints under the same client budget.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l == nil || exempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		allowed, retryAfter := l.allow(clientKey(r), time.Now())
		if !allowed {
			if l.metrics != nil {
				l.metrics.RecordRateLimitRejection()
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) allow(client string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	item, ok := l.clients[client]
	if !ok || now.Sub(item.started) >= l.window {
		l.clients[client] = window{started: now, count: 1}
		return true, 0
	}
	if item.count >= l.limit {
		remaining := l.window - now.Sub(item.started)
		seconds := int((remaining + time.Second - 1) / time.Second)
		return false, seconds
	}
	item.count++
	l.clients[client] = item
	return true, 0
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func exempt(path string) bool {
	return path == "/health/live" || path == "/health/ready" || path == "/metrics"
}

// Sweep drops inactive clients so a long-lived API process does not retain one
// map entry per historical address forever.
func (l *Limiter) Sweep(now time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, item := range l.clients {
		if now.Sub(item.started) >= l.window {
			delete(l.clients, key)
		}
	}
}

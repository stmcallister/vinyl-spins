package app

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ipBucket is a token bucket for a single IP address.
type ipBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

func (b *ipBucket) allow(ratePerSec, burst float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens = min(burst, b.tokens+elapsed*ratePerSec)
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// rateLimiter holds per-IP buckets.
type rateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*ipBucket
	ratePerSec float64
	burst      float64
}

func newRateLimiter(ratePerSec, burst float64) *rateLimiter {
	rl := &rateLimiter{
		buckets:    make(map[string]*ipBucket),
		ratePerSec: ratePerSec,
		burst:      burst,
	}
	go rl.cleanup()
	return rl
}

// cleanup removes stale buckets every 5 minutes.
func (rl *rateLimiter) cleanup() {
	for range time.Tick(5 * time.Minute) {
		cutoff := time.Now().Add(-10 * time.Minute)
		rl.mu.Lock()
		for ip, b := range rl.buckets {
			b.mu.Lock()
			if b.lastSeen.Before(cutoff) {
				delete(rl.buckets, ip)
			}
			b.mu.Unlock()
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) bucket(ip string) *ipBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &ipBucket{tokens: rl.burst, lastSeen: time.Now()}
		rl.buckets[ip] = b
	}
	return b
}

// Middleware returns a chi-compatible rate-limiting middleware.
func (rl *rateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !rl.bucket(ip).allow(rl.ratePerSec, rl.burst) {
				w.Header().Set("Retry-After", "1")
				writeJSONError(w, http.StatusTooManyRequests, errors.New("rate limit exceeded; slow down"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the real client IP, honouring X-Forwarded-For from
// trusted reverse proxies (the first address in the chain).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

package auth

import (
	"sync"
	"time"
)

// loginLimiter is a per-IP fixed-window counter guarding the login endpoint. Every attempt
// consumes a token so brute force is throttled before the (deliberately expensive) argon2
// verify runs.
type loginLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	buckets map[string]*loginBucket
}

type loginBucket struct {
	count       int
	windowStart time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, buckets: map[string]*loginBucket{}}
}

// allow records an attempt from ip and reports whether it is under the limit.
func (l *loginLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[ip]
	if b == nil || now.Sub(b.windowStart) > l.window {
		b = &loginBucket{windowStart: now}
		l.buckets[ip] = b
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}

// reset clears an IP's counter (called on a successful login so a legitimate user isn't
// throttled by their own earlier typos).
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, ip)
}

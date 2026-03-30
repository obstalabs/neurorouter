package neurorouter

import (
	"context"
	"sync"
	"time"
)

// RateLimit configures request rate limiting.
type RateLimit struct {
	RequestsPerMinute int // steady-state rate
	BurstSize         int // max burst above steady rate; 0 = RequestsPerMinute
}

// rateLimiter is a token bucket rate limiter.
type rateLimiter struct {
	rate     float64 // tokens per second
	burst    int
	tokens   float64
	lastTime time.Time
	mu       sync.Mutex
}

// newRateLimiter creates a rate limiter from config.
func newRateLimiter(cfg RateLimit) *rateLimiter {
	burst := cfg.BurstSize
	if burst <= 0 {
		burst = cfg.RequestsPerMinute
	}
	return &rateLimiter{
		rate:     float64(cfg.RequestsPerMinute) / 60.0,
		burst:    burst,
		tokens:   float64(burst),
		lastTime: time.Now(),
	}
}

// Allow returns true if a request is allowed, consuming one token.
func (r *rateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.refill()

	if r.tokens >= 1 {
		r.tokens--
		return true
	}
	return false
}

// Wait blocks until a token is available or ctx is cancelled.
func (r *rateLimiter) Wait(ctx context.Context) error {
	for {
		if r.Allow() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.nextTokenDuration()):
		}
	}
}

// refill adds tokens based on elapsed time. Must be called with mu held.
func (r *rateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(r.lastTime).Seconds()
	r.lastTime = now
	r.tokens += elapsed * r.rate
	if r.tokens > float64(r.burst) {
		r.tokens = float64(r.burst)
	}
}

// nextTokenDuration returns how long until the next token is available.
func (r *rateLimiter) nextTokenDuration() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.rate <= 0 {
		return time.Second
	}
	return time.Duration(float64(time.Second) / r.rate)
}

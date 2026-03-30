package neurorouter

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiter_AllowsBurst(t *testing.T) {
	rl := newRateLimiter(RateLimit{RequestsPerMinute: 60, BurstSize: 5})

	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Fatalf("burst request %d denied", i)
		}
	}
}

func TestRateLimiter_DeniesOverBurst(t *testing.T) {
	rl := newRateLimiter(RateLimit{RequestsPerMinute: 60, BurstSize: 3})

	// Drain burst.
	for i := 0; i < 3; i++ {
		rl.Allow()
	}

	if rl.Allow() {
		t.Fatal("request over burst should be denied")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := newRateLimiter(RateLimit{RequestsPerMinute: 600, BurstSize: 1})

	// Drain.
	rl.Allow()
	if rl.Allow() {
		t.Fatal("should be denied after drain")
	}

	// Simulate time passing (600 RPM = 10/sec = 100ms per token).
	rl.mu.Lock()
	rl.lastTime = rl.lastTime.Add(-200 * time.Millisecond)
	rl.mu.Unlock()

	if !rl.Allow() {
		t.Fatal("should be allowed after refill")
	}
}

func TestRateLimiter_DefaultBurst(t *testing.T) {
	rl := newRateLimiter(RateLimit{RequestsPerMinute: 10})

	// BurstSize defaults to RequestsPerMinute.
	for i := 0; i < 10; i++ {
		if !rl.Allow() {
			t.Fatalf("burst request %d denied (default burst)", i)
		}
	}
	if rl.Allow() {
		t.Fatal("over-burst should be denied")
	}
}

func TestRateLimiter_WaitRespectsContext(t *testing.T) {
	rl := newRateLimiter(RateLimit{RequestsPerMinute: 1, BurstSize: 1})

	// Drain the token.
	rl.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := rl.Wait(ctx)
	if err == nil {
		t.Fatal("Wait should fail when context expires")
	}
}

func TestRateLimiter_WaitSucceeds(t *testing.T) {
	rl := newRateLimiter(RateLimit{RequestsPerMinute: 600, BurstSize: 1})

	// Drain.
	rl.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := rl.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait should succeed after refill: %v", err)
	}
}

package neurorouter

import (
	"sync"
	"time"
)

// Default health tracker settings.
const (
	defaultFailThreshold = 3
	defaultCooldown      = 30 * time.Second
)

// HealthTracker tracks target health based on consecutive failures.
// Implements a simple circuit breaker pattern:
//
//	closed (healthy) → open (unhealthy after N fails) → half-open (after cooldown) → closed (on success)
type HealthTracker struct {
	mu        sync.Mutex
	targets   map[string]*targetHealth
	threshold int
	cooldown  time.Duration
}

type targetHealth struct {
	consecutiveFails int
	lastFailure      time.Time
	healthy          bool
}

// NewHealthTracker creates a health tracker. Zero values use defaults
// (3 failures, 30s cooldown).
func NewHealthTracker(threshold int, cooldown time.Duration) *HealthTracker {
	if threshold <= 0 {
		threshold = defaultFailThreshold
	}
	if cooldown <= 0 {
		cooldown = defaultCooldown
	}
	return &HealthTracker{
		targets:   make(map[string]*targetHealth),
		threshold: threshold,
		cooldown:  cooldown,
	}
}

// RecordSuccess marks a target as healthy, resetting its failure count.
func (h *HealthTracker) RecordSuccess(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	t := h.getOrCreate(key)
	t.consecutiveFails = 0
	t.healthy = true
}

// RecordFailure records a failure for a target. After threshold
// consecutive failures, the target is marked unhealthy.
func (h *HealthTracker) RecordFailure(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	t := h.getOrCreate(key)
	t.consecutiveFails++
	t.lastFailure = time.Now()
	if t.consecutiveFails >= h.threshold {
		t.healthy = false
	}
}

// IsHealthy returns true if the target is healthy or if the cooldown
// has elapsed (half-open state, allowing a probe request).
func (h *HealthTracker) IsHealthy(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	t, exists := h.targets[key]
	if !exists {
		return true // unknown targets are assumed healthy
	}
	if t.healthy {
		return true
	}
	// Half-open: allow probe after cooldown.
	return time.Since(t.lastFailure) >= h.cooldown
}

// getOrCreate returns the health state for a target, creating it if needed.
// Must be called with mu held.
func (h *HealthTracker) getOrCreate(key string) *targetHealth {
	t, exists := h.targets[key]
	if !exists {
		t = &targetHealth{healthy: true}
		h.targets[key] = t
	}
	return t
}

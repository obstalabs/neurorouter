package neurorouter

import (
	"testing"
	"time"
)

func TestHealthTracker_StartsHealthy(t *testing.T) {
	h := NewHealthTracker(3, time.Second)

	if !h.IsHealthy("target-a") {
		t.Fatal("unknown target should be healthy")
	}
}

func TestHealthTracker_HealthyAfterSuccess(t *testing.T) {
	h := NewHealthTracker(3, time.Second)
	h.RecordSuccess("target-a")

	if !h.IsHealthy("target-a") {
		t.Fatal("target with success should be healthy")
	}
}

func TestHealthTracker_UnhealthyAfterThreshold(t *testing.T) {
	h := NewHealthTracker(3, time.Hour) // long cooldown so it stays unhealthy

	h.RecordFailure("target-a")
	h.RecordFailure("target-a")
	if !h.IsHealthy("target-a") {
		t.Fatal("should still be healthy below threshold")
	}

	h.RecordFailure("target-a")
	if h.IsHealthy("target-a") {
		t.Fatal("should be unhealthy at threshold")
	}
}

func TestHealthTracker_ResetsOnSuccess(t *testing.T) {
	h := NewHealthTracker(3, time.Hour)

	// Hit threshold.
	for i := 0; i < 3; i++ {
		h.RecordFailure("target-a")
	}
	if h.IsHealthy("target-a") {
		t.Fatal("should be unhealthy")
	}

	// Success resets.
	h.RecordSuccess("target-a")
	if !h.IsHealthy("target-a") {
		t.Fatal("should be healthy after success")
	}
}

func TestHealthTracker_CooldownAllowsProbe(t *testing.T) {
	h := NewHealthTracker(3, 50*time.Millisecond)

	for i := 0; i < 3; i++ {
		h.RecordFailure("target-a")
	}
	if h.IsHealthy("target-a") {
		t.Fatal("should be unhealthy immediately")
	}

	// Wait for cooldown.
	time.Sleep(100 * time.Millisecond)
	if !h.IsHealthy("target-a") {
		t.Fatal("should allow probe after cooldown")
	}
}

func TestHealthTracker_IndependentTargets(t *testing.T) {
	h := NewHealthTracker(2, time.Hour)

	h.RecordFailure("target-a")
	h.RecordFailure("target-a")

	if h.IsHealthy("target-a") {
		t.Fatal("target-a should be unhealthy")
	}
	if !h.IsHealthy("target-b") {
		t.Fatal("target-b should be healthy (independent)")
	}
}

func TestHealthTracker_Defaults(t *testing.T) {
	h := NewHealthTracker(0, 0)

	if h.threshold != defaultFailThreshold {
		t.Fatalf("threshold = %d, want %d", h.threshold, defaultFailThreshold)
	}
	if h.cooldown != defaultCooldown {
		t.Fatalf("cooldown = %v, want %v", h.cooldown, defaultCooldown)
	}
}

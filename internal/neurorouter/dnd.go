package neurorouter

import (
	"sync"
	"time"
)

// DNDSource identifies why DND is active.
type DNDSource string

const (
	DNDSourceOff    DNDSource = "off"
	DNDSourceManual DNDSource = "manual"
	DNDSourceAuto   DNDSource = "auto"
)

// DNDSnapshot is the externally visible runtime DND state.
type DNDSnapshot struct {
	Active bool      `json:"active"`
	Manual bool      `json:"manual"`
	Source DNDSource `json:"source"`
	Status string    `json:"status"`
}

// DNDConfig controls do-not-disturb behavior.
type DNDConfig struct {
	Enabled bool
}

// DND tracks frustration signals and suppresses suggestions when active.
// When DND is active, only critical credential leaks break through.
type DND struct {
	mu sync.Mutex

	// Manual toggle.
	manualOn bool

	// Auto-detection state.
	autoOn        bool
	autoTriggered time.Time // when auto-DND was activated
	autoExpiry    time.Duration

	// Frustration signal counters (rolling window).
	errorBurst     int       // errors in current window
	windowStart    time.Time // current window start
	windowSize     time.Duration
	errorThreshold int // errors to trigger DND

	// Request rate tracking (thrashing detection).
	recentRequests  []time.Time // timestamps of recent requests
	thrashWindow    time.Duration
	thrashThreshold int // requests in window to trigger DND
}

// NewDND creates a new DND tracker with default thresholds.
func NewDND() *DND {
	return &DND{
		autoExpiry:      30 * time.Minute,
		windowSize:      2 * time.Minute,
		errorThreshold:  5, // 5 errors in 2 minutes
		thrashWindow:    30 * time.Second,
		thrashThreshold: 10, // 10 requests in 30 seconds
	}
}

// SetManual toggles DND manually. Pass true to enable, false to disable.
func (d *DND) SetManual(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.manualOn = on
	if on {
		return
	}

	// A manual "off" is an explicit resume signal, so clear any active
	// auto-DND state and restart the rolling counters.
	d.autoOn = false
	d.autoTriggered = time.Time{}
	d.errorBurst = 0
	d.windowStart = time.Time{}
	d.recentRequests = nil
}

// IsActive returns true if DND is currently active (manual or auto).
func (d *DND) IsActive() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked().Active
}

// ShouldSuppress returns true if the suggestion should be suppressed.
// Critical credential leaks (severity "critical") always break through.
func (d *DND) ShouldSuppress(suggestion Suggestion) bool {
	if !d.IsActive() {
		return false
	}
	// Only critical severity breaks through DND.
	return suggestion.Severity != SeverityHigh || suggestion.Category != CategoryFilter
}

// Snapshot returns the current structured DND state.
func (d *DND) Snapshot() DNDSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked()
}

// RecordRequest tracks a request for thrashing detection.
func (d *DND) RecordRequest() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	d.recentRequests = append(d.recentRequests, now)

	// Prune old entries.
	cutoff := now.Add(-d.thrashWindow)
	start := 0
	for start < len(d.recentRequests) && d.recentRequests[start].Before(cutoff) {
		start++
	}
	d.recentRequests = d.recentRequests[start:]

	// Check thrashing.
	if len(d.recentRequests) >= d.thrashThreshold && !d.autoOn && !d.manualOn {
		d.autoOn = true
		d.autoTriggered = now
	}
}

// RecordError tracks an error for burst detection.
func (d *DND) RecordError() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// Reset window if expired.
	if now.Sub(d.windowStart) > d.windowSize {
		d.errorBurst = 0
		d.windowStart = now
	}

	d.errorBurst++

	// Check error burst.
	if d.errorBurst >= d.errorThreshold && !d.autoOn && !d.manualOn {
		d.autoOn = true
		d.autoTriggered = now
	}
}

// Status returns human-readable DND status.
func (d *DND) Status() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked().Status
}

func (d *DND) snapshotLocked() DNDSnapshot {
	if d.manualOn {
		return DNDSnapshot{
			Active: true,
			Manual: true,
			Source: DNDSourceManual,
			Status: "on (manual)",
		}
	}

	if d.autoOn {
		remaining := d.autoExpiry - time.Since(d.autoTriggered)
		if remaining <= 0 {
			d.autoOn = false
			return DNDSnapshot{
				Source: DNDSourceOff,
				Status: "off",
			}
		}
		return DNDSnapshot{
			Active: true,
			Source: DNDSourceAuto,
			Status: "on (auto, " + remaining.Round(time.Minute).String() + " remaining)",
		}
	}

	return DNDSnapshot{
		Source: DNDSourceOff,
		Status: "off",
	}
}

package neurorouter

import (
	"testing"
	"time"
)

func TestDND_ManualToggle(t *testing.T) {
	d := NewDND()

	if d.IsActive() {
		t.Error("DND should be off by default")
	}

	d.SetManual(true)
	if !d.IsActive() {
		t.Error("DND should be on after manual enable")
	}

	d.SetManual(false)
	if d.IsActive() {
		t.Error("DND should be off after manual disable")
	}
}

func TestDND_ErrorBurstTrigger(t *testing.T) {
	d := NewDND()

	// 4 errors should NOT trigger.
	for i := 0; i < 4; i++ {
		d.RecordError()
	}
	if d.IsActive() {
		t.Error("DND should not trigger on 4 errors (threshold is 5)")
	}

	// 5th error triggers.
	d.RecordError()
	if !d.IsActive() {
		t.Error("DND should trigger after 5 errors in window")
	}
}

func TestDND_ThrashingTrigger(t *testing.T) {
	d := NewDND()
	d.thrashThreshold = 5 // lower for test
	d.thrashWindow = 1 * time.Second

	for i := 0; i < 4; i++ {
		d.RecordRequest()
	}
	if d.IsActive() {
		t.Error("DND should not trigger on 4 requests")
	}

	d.RecordRequest()
	if !d.IsActive() {
		t.Error("DND should trigger after 5 rapid requests")
	}
}

func TestDND_AutoExpiry(t *testing.T) {
	d := NewDND()
	d.autoExpiry = 10 * time.Millisecond // short for test

	// Trigger via error burst.
	for i := 0; i < 5; i++ {
		d.RecordError()
	}
	if !d.IsActive() {
		t.Fatal("DND should be active after burst")
	}

	// Wait for expiry.
	time.Sleep(20 * time.Millisecond)
	if d.IsActive() {
		t.Error("DND should auto-expire after cooldown")
	}
}

func TestDND_ShouldSuppress(t *testing.T) {
	d := NewDND()
	d.SetManual(true)

	// Regular suggestion should be suppressed.
	regular := Suggestion{
		Type:     "stale_reads",
		Category: CategoryHook,
		Severity: SeverityLow,
	}
	if !d.ShouldSuppress(regular) {
		t.Error("regular suggestion should be suppressed during DND")
	}

	// Medium severity filter should also be suppressed.
	medium := Suggestion{
		Type:     "thinking_bloat",
		Category: CategoryFilter,
		Severity: SeverityMedium,
	}
	if !d.ShouldSuppress(medium) {
		t.Error("medium severity should be suppressed during DND")
	}
}

func TestDND_NotActiveNoSuppress(t *testing.T) {
	d := NewDND()

	s := Suggestion{Type: "stale_reads", Category: CategoryHook, Severity: SeverityLow}
	if d.ShouldSuppress(s) {
		t.Error("should not suppress when DND is off")
	}
}

func TestDND_Status(t *testing.T) {
	d := NewDND()

	if d.Status() != "off" {
		t.Errorf("expected 'off', got %q", d.Status())
	}

	d.SetManual(true)
	if d.Status() != "on (manual)" {
		t.Errorf("expected 'on (manual)', got %q", d.Status())
	}

	d.SetManual(false)
	for i := 0; i < 5; i++ {
		d.RecordError()
	}
	status := d.Status()
	if status == "off" {
		t.Error("expected auto DND status, got 'off'")
	}
}

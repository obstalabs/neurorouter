package neurorouter

import (
	"strings"
	"testing"
)

func TestProgressTracker_EmptyReport(t *testing.T) {
	pt := NewProgressTracker()
	r := pt.Report()

	if r.Requests != 0 {
		t.Errorf("expected 0 requests, got %d", r.Requests)
	}
	if r.OPS != 100 {
		t.Errorf("expected 100%% OPS with no data, got %.0f", r.OPS)
	}

	human := r.FormatHuman()
	if !strings.Contains(human, "No requests yet") {
		t.Error("empty report should say no requests")
	}
}

func TestProgressTracker_BasicMetrics(t *testing.T) {
	pt := NewProgressTracker()

	pt.RecordRequest(&PipelineResult{
		BytesBefore:  1000,
		BytesAfter:   700,
		FiltersRun:   []string{"thinking", "system_reminders"},
		SecretsFound: 1,
	})
	pt.RecordRequest(&PipelineResult{
		BytesBefore:  800,
		BytesAfter:   600,
		FiltersRun:   []string{"thinking"},
		SecretsFound: 0,
	})

	r := pt.Report()

	if r.Requests != 2 {
		t.Errorf("expected 2 requests, got %d", r.Requests)
	}
	if r.BytesSaved != 500 {
		t.Errorf("expected 500 bytes saved, got %d", r.BytesSaved)
	}
	if r.TokensSaved != 125 {
		t.Errorf("expected 125 tokens saved, got %d", r.TokensSaved)
	}
	if r.SecretsCaught != 1 {
		t.Errorf("expected 1 secret, got %d", r.SecretsCaught)
	}
	if r.TopFilter != "thinking" {
		t.Errorf("expected top filter 'thinking', got %q", r.TopFilter)
	}
	if r.TopFilterHits != 2 {
		t.Errorf("expected 2 hits for thinking, got %d", r.TopFilterHits)
	}
}

func TestProgressTracker_OPS(t *testing.T) {
	pt := NewProgressTracker()

	// 50% noise.
	pt.RecordRequest(&PipelineResult{BytesBefore: 1000, BytesAfter: 500})

	r := pt.Report()
	if r.OPS != 50 {
		t.Errorf("expected OPS 50%%, got %.0f", r.OPS)
	}
}

func TestProgressTracker_WorstPattern(t *testing.T) {
	pt := NewProgressTracker()

	pt.RecordSuggestions([]Suggestion{
		{Type: "stale_reads"},
		{Type: "stale_reads"},
		{Type: "thinking_bloat"},
	})

	r := pt.Report()
	if r.WorstPattern != "stale_reads" {
		t.Errorf("expected worst pattern 'stale_reads', got %q", r.WorstPattern)
	}
}

func TestProgressTracker_FormatHuman(t *testing.T) {
	pt := NewProgressTracker()

	pt.RecordRequest(&PipelineResult{
		BytesBefore:  10240,
		BytesAfter:   7168,
		FiltersRun:   []string{"thinking"},
		SecretsFound: 2,
	})

	r := pt.Report()
	human := r.FormatHuman()

	if !strings.Contains(human, "OPS:") {
		t.Error("expected OPS in output")
	}
	if !strings.Contains(human, "Context:") {
		t.Error("expected Context in output")
	}
	if !strings.Contains(human, "Secrets:") {
		t.Error("expected Secrets in output")
	}
	if !strings.Contains(human, "thinking") {
		t.Error("expected filter name in output")
	}
}

func TestProgressTracker_NilResult(t *testing.T) {
	pt := NewProgressTracker()
	pt.RecordRequest(nil) // should not panic
	r := pt.Report()
	if r.Requests != 0 {
		t.Error("nil result should not count as request")
	}
}

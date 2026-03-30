package neurorouter

import (
	"os"
	"path/filepath"
	"testing"
)

func tempDB(t *testing.T) *StateStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := OpenStateStore(path)
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStateStore_OpenClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	s, err := OpenStateStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// File should exist.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("database file not created")
	}
}

func TestStateStore_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "test.db")

	s, err := OpenStateStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = s.Close()
}

func TestStateStore_Patterns(t *testing.T) {
	s := tempDB(t)

	// Record patterns.
	if err := s.IncrementPattern("stale_reads", 1000); err != nil {
		t.Fatalf("increment: %v", err)
	}
	if err := s.IncrementPattern("stale_reads", 500); err != nil {
		t.Fatalf("increment: %v", err)
	}
	if err := s.IncrementPattern("thinking_bloat", 2000); err != nil {
		t.Fatalf("increment: %v", err)
	}

	// Query.
	stats, err := s.PatternStats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 pattern types, got %d", len(stats))
	}

	// Thinking bloat should be first (most tokens wasted).
	if stats[0].Type != "thinking_bloat" {
		t.Errorf("expected thinking_bloat first, got %s", stats[0].Type)
	}
	if stats[0].TotalTokens != 2000 {
		t.Errorf("expected 2000 tokens, got %d", stats[0].TotalTokens)
	}

	// Stale reads second.
	if stats[1].Type != "stale_reads" {
		t.Errorf("expected stale_reads second, got %s", stats[1].Type)
	}
	if stats[1].TotalCount != 2 {
		t.Errorf("expected count 2, got %d", stats[1].TotalCount)
	}
	if stats[1].TotalTokens != 1500 {
		t.Errorf("expected 1500 tokens, got %d", stats[1].TotalTokens)
	}
}

func TestStateStore_Sessions(t *testing.T) {
	s := tempDB(t)

	// Start session.
	id, err := s.StartSession()
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive session ID, got %d", id)
	}

	// Update.
	if err := s.UpdateSession(id, 10, 5000, 3500, 1, 3); err != nil {
		t.Fatalf("update session: %v", err)
	}

	// End.
	if err := s.EndSession(id); err != nil {
		t.Fatalf("end session: %v", err)
	}

	// Stats.
	stats, err := s.SessionStats()
	if err != nil {
		t.Fatalf("session stats: %v", err)
	}
	if stats.TotalSessions != 1 {
		t.Errorf("expected 1 session, got %d", stats.TotalSessions)
	}
	if stats.TotalRequests != 10 {
		t.Errorf("expected 10 requests, got %d", stats.TotalRequests)
	}
	if stats.TotalTokensSaved != 375 { // (5000-3500)/4
		t.Errorf("expected 375 tokens saved, got %d", stats.TotalTokensSaved)
	}
	if stats.TotalSecretsCaught != 1 {
		t.Errorf("expected 1 secret, got %d", stats.TotalSecretsCaught)
	}
}

func TestStateStore_Workflows(t *testing.T) {
	s := tempDB(t)

	// Record same workflow 3 times.
	for i := 0; i < 3; i++ {
		if err := s.RecordWorkflow("abc123", "Read→Edit→Bash"); err != nil {
			t.Fatalf("record workflow: %v", err)
		}
	}

	// Query frequent.
	wfs, err := s.FrequentWorkflows(3)
	if err != nil {
		t.Fatalf("frequent workflows: %v", err)
	}
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}
	if wfs[0].Frequency != 3 {
		t.Errorf("expected frequency 3, got %d", wfs[0].Frequency)
	}
	if wfs[0].Steps != "Read→Edit→Bash" {
		t.Errorf("expected steps, got %q", wfs[0].Steps)
	}
}

func TestStateStore_WorkflowsBelowThreshold(t *testing.T) {
	s := tempDB(t)

	if err := s.RecordWorkflow("xyz", "Read→Bash"); err != nil {
		t.Fatalf("record: %v", err)
	}

	wfs, err := s.FrequentWorkflows(3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(wfs) != 0 {
		t.Error("expected no workflows below threshold")
	}
}

func TestStateStore_GC(t *testing.T) {
	s := tempDB(t)
	s.retention = 0 // expire everything immediately

	// Add data.
	if err := s.IncrementPattern("test", 100); err != nil {
		t.Fatalf("increment: %v", err)
	}
	if _, err := s.StartSession(); err != nil {
		t.Fatalf("start session: %v", err)
	}

	// GC should remove everything.
	removed, err := s.GC()
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if removed == 0 {
		t.Error("expected GC to remove entries")
	}

	// Verify empty.
	stats, _ := s.PatternStats()
	if len(stats) != 0 {
		t.Error("expected no patterns after GC")
	}
}

func TestStateStore_EmptyStats(t *testing.T) {
	s := tempDB(t)

	stats, err := s.SessionStats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalSessions != 0 {
		t.Errorf("expected 0 sessions, got %d", stats.TotalSessions)
	}
	if stats.AvgOPS != 100 {
		t.Errorf("expected 100 OPS with no data, got %.0f", stats.AvgOPS)
	}
}

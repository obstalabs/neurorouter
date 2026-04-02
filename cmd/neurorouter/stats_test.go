package main

import (
	"testing"
	"time"

	"github.com/ppiankov/neurorouter/internal/neurorouter"
)

func TestTopRecurringFingerprint(t *testing.T) {
	entries := []neurorouter.AuditEntry{
		{Timestamp: time.Now(), BytesBefore: 72628, BytesAfter: 64030, FiltersRun: []string{"oversized_blocks"}},
		{Timestamp: time.Now(), BytesBefore: 72628, BytesAfter: 64030, FiltersRun: []string{"oversized_blocks"}},
		{Timestamp: time.Now(), BytesBefore: 50000, BytesAfter: 49000, FiltersRun: []string{"stale_reads"}},
	}

	got := topRecurringFingerprint(entries)
	want := "fixed-delta oversized_blocks/8598B x2"
	if got != want {
		t.Fatalf("top recurring fingerprint: got %q, want %q", got, want)
	}
}

func TestTopRecurringFingerprint_EmptyWithoutRepeats(t *testing.T) {
	entries := []neurorouter.AuditEntry{
		{Timestamp: time.Now(), BytesBefore: 72628, BytesAfter: 64030, FiltersRun: []string{"oversized_blocks"}},
	}

	if got := topRecurringFingerprint(entries); got != "" {
		t.Fatalf("expected empty fingerprint, got %q", got)
	}
}

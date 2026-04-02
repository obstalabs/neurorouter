package neurorouter

import (
	"fmt"
	"strings"
	"sync"
)

// ProgressTracker accumulates session-level metrics for the progress report.
// Currently in-memory (session-scoped). Will read from SQLite when WO-15 lands.
type ProgressTracker struct {
	mu sync.Mutex

	// Cumulative metrics.
	totalRequests    int
	totalBytesBefore int64
	totalBytesAfter  int64
	totalSecrets     int
	filterHits       map[string]int // filter name → activation count

	// Per-pattern tracking.
	patternCounts map[string]int // pattern type → occurrence count
}

// NewProgressTracker creates a new session tracker.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{
		filterHits:    make(map[string]int),
		patternCounts: make(map[string]int),
	}
}

// RecordRequest records a proxied request's pipeline result.
func (pt *ProgressTracker) RecordRequest(result *PipelineResult) {
	if result == nil {
		return
	}

	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.totalRequests++
	pt.totalBytesBefore += int64(result.BytesBefore)
	pt.totalBytesAfter += int64(result.BytesAfter)
	pt.totalSecrets += result.SecretsFound

	for _, f := range result.FiltersRun {
		pt.filterHits[f]++
	}
}

// RecordSuggestions records which patterns were detected.
func (pt *ProgressTracker) RecordSuggestions(suggestions []Suggestion) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	for _, s := range suggestions {
		pt.patternCounts[s.Type]++
	}
}

// ProgressReport is the data model for the progress command output.
type ProgressReport struct {
	Requests      int     `json:"requests"`
	BytesSaved    int64   `json:"bytes_saved"`
	TokensSaved   int     `json:"tokens_saved"`
	MoneySaved    float64 `json:"money_saved_usd"`
	OPS           float64 `json:"ops_percent"` // signal percentage
	SecretsCaught int     `json:"secrets_caught"`
	TopFilter     string  `json:"top_filter"`
	TopFilterHits int     `json:"top_filter_hits"`
	WorstPattern  string  `json:"worst_pattern"`
}

// Report generates the current progress report.
func (pt *ProgressTracker) Report() ProgressReport {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	bytesSaved := pt.totalBytesBefore - pt.totalBytesAfter
	tokensSaved := TokensFromBytes(bytesSaved)
	moneySaved := MoneySavedUSD(tokensSaved, DefaultInputPricePerMillionUSD)

	ops := 100.0
	if pt.totalBytesBefore > 0 {
		ops = float64(pt.totalBytesAfter) / float64(pt.totalBytesBefore) * 100
	}

	// Find top filter.
	topFilter := ""
	topHits := 0
	for f, count := range pt.filterHits {
		if count > topHits {
			topFilter = f
			topHits = count
		}
	}

	// Find worst pattern (most frequent).
	worstPattern := ""
	worstCount := 0
	for p, count := range pt.patternCounts {
		if count > worstCount {
			worstPattern = p
			worstCount = count
		}
	}

	return ProgressReport{
		Requests:      pt.totalRequests,
		BytesSaved:    bytesSaved,
		TokensSaved:   tokensSaved,
		MoneySaved:    moneySaved,
		OPS:           ops,
		SecretsCaught: pt.totalSecrets,
		TopFilter:     topFilter,
		TopFilterHits: topHits,
		WorstPattern:  worstPattern,
	}
}

// FormatHuman returns a human-readable progress report.
func (r ProgressReport) FormatHuman() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Session progress (%d requests)\n", r.Requests)

	if r.Requests == 0 {
		b.WriteString("  No requests yet. Send traffic through the proxy to see stats.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "  OPS:     %.0f%% signal (%.0f%% noise removed)\n", r.OPS, 100-r.OPS)
	fmt.Fprintf(&b, "  Saved:   %dKB / %d tokens / $%.4f\n", r.BytesSaved/1024, r.TokensSaved, r.MoneySaved)

	if r.SecretsCaught > 0 {
		fmt.Fprintf(&b, "  Secrets: %d caught\n", r.SecretsCaught)
	}

	if r.TopFilter != "" {
		fmt.Fprintf(&b, "  Top:     %s (%d activations)\n", r.TopFilter, r.TopFilterHits)
	}

	if r.WorstPattern != "" {
		fmt.Fprintf(&b, "  Fix:     %s is your biggest remaining waste\n", r.WorstPattern)
	}

	return b.String()
}

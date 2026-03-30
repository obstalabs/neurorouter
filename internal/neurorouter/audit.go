package neurorouter

import (
	"sync"
	"time"
)

// timeNow is a package-level function for getting current time (testable).
var timeNow = time.Now

// AuditEntry records what happened to a single request.
type AuditEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	Model        string    `json:"model"`
	BytesBefore  int       `json:"bytes_before"`
	BytesAfter   int       `json:"bytes_after"`
	BytesRemoved int       `json:"bytes_removed"`
	FiltersRun   []string  `json:"filters_run"`
	SecretsFound int       `json:"secrets_found"`
	SecretPolicy string    `json:"secret_policy"`
	Blocked      bool      `json:"blocked"`
}

// auditLog keeps a bounded ring buffer of recent transformation records.
type auditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
	maxSize int
}

func newAuditLog(maxSize int) *auditLog {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &auditLog{
		entries: make([]AuditEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

func (a *auditLog) Record(entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.entries) >= a.maxSize {
		// Drop oldest entry.
		copy(a.entries, a.entries[1:])
		a.entries = a.entries[:len(a.entries)-1]
	}
	a.entries = append(a.entries, entry)
}

func (a *auditLog) Entries() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]AuditEntry, len(a.entries))
	copy(out, a.entries)
	return out
}

// DryRunResult is returned when --dry-run is enabled instead of forwarding.
type DryRunResult struct {
	Original     []ChatMessage `json:"original"`
	Filtered     []ChatMessage `json:"filtered"`
	BytesBefore  int           `json:"bytes_before"`
	BytesAfter   int           `json:"bytes_after"`
	BytesRemoved int           `json:"bytes_removed"`
	FiltersRun   []string      `json:"filters_run"`
	SecretsFound int           `json:"secrets_found"`
	Blocked      bool          `json:"blocked"`
}

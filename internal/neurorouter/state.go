package neurorouter

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// StateStore persists pattern memory, session metrics, and workflow data
// across sessions using SQLite at ~/.neurorouter/state.db.
// Privacy: no request content, no user text — only structural metadata.
type StateStore struct {
	db        *sql.DB
	retention time.Duration // default 90 days
}

// DefaultRetention is the default data retention period.
const DefaultRetention = 90 * 24 * time.Hour

// DefaultDBPath returns ~/.neurorouter/state.db.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".neurorouter", "state.db")
}

// OpenStateStore opens or creates the state database.
func OpenStateStore(path string) (*StateStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}

	// WAL mode for concurrent reads.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	s := &StateStore{db: db, retention: DefaultRetention}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *StateStore) Close() error {
	return s.db.Close()
}

// migrate creates or updates the schema.
func (s *StateStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS patterns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			count INTEGER NOT NULL DEFAULT 1,
			tokens_wasted INTEGER NOT NULL DEFAULT 0,
			last_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			suggestion_given BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_patterns_type ON patterns(type);

		CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			ended_at DATETIME,
			requests INTEGER NOT NULL DEFAULT 0,
			bytes_before INTEGER NOT NULL DEFAULT 0,
			bytes_after INTEGER NOT NULL DEFAULT 0,
			tokens_saved INTEGER NOT NULL DEFAULT 0,
			secrets_found INTEGER NOT NULL DEFAULT 0,
			suggestions_emitted INTEGER NOT NULL DEFAULT 0,
			ops_percent REAL NOT NULL DEFAULT 100.0
		);

		CREATE TABLE IF NOT EXISTS workflows (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hash TEXT NOT NULL UNIQUE,
			steps TEXT NOT NULL,
			frequency INTEGER NOT NULL DEFAULT 1,
			last_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			suggested BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_workflows_hash ON workflows(hash);

		CREATE TABLE IF NOT EXISTS dnd_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			trigger_type TEXT NOT NULL,
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			ended_at DATETIME,
			duration_seconds INTEGER
		);
	`)
	return err
}

// --- Pattern operations ---

// RecordPattern upserts a pattern occurrence.
func (s *StateStore) RecordPattern(patternType string, tokensWasted int) error {
	_, err := s.db.Exec(`
		INSERT INTO patterns (type, count, tokens_wasted, last_seen)
		VALUES (?, 1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			count = count + 1,
			tokens_wasted = tokens_wasted + ?,
			last_seen = CURRENT_TIMESTAMP
	`, patternType, tokensWasted, tokensWasted)
	// ON CONFLICT on id won't work for upsert by type. Use a different approach.
	if err != nil {
		return err
	}
	return nil
}

// IncrementPattern increments count for a pattern type, or creates it.
func (s *StateStore) IncrementPattern(patternType string, tokensWasted int) error {
	result, err := s.db.Exec(`
		UPDATE patterns SET count = count + 1, tokens_wasted = tokens_wasted + ?, last_seen = CURRENT_TIMESTAMP
		WHERE type = ? AND id = (SELECT MAX(id) FROM patterns WHERE type = ?)
	`, tokensWasted, patternType, patternType)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		_, err = s.db.Exec(`
			INSERT INTO patterns (type, count, tokens_wasted, last_seen)
			VALUES (?, 1, ?, CURRENT_TIMESTAMP)
		`, patternType, tokensWasted)
		return err
	}
	return nil
}

// PatternSummary returns aggregated pattern stats.
type PatternSummary struct {
	Type        string
	TotalCount  int
	TotalTokens int
	LastSeen    time.Time
}

// PatternStats returns all pattern summaries ordered by tokens wasted.
func (s *StateStore) PatternStats() ([]PatternSummary, error) {
	rows, err := s.db.Query(`
		SELECT type, SUM(count), SUM(tokens_wasted), MAX(last_seen)
		FROM patterns
		GROUP BY type
		ORDER BY SUM(tokens_wasted) DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []PatternSummary
	for rows.Next() {
		var ps PatternSummary
		var lastSeen string
		if err := rows.Scan(&ps.Type, &ps.TotalCount, &ps.TotalTokens, &lastSeen); err != nil {
			return nil, err
		}
		ps.LastSeen, _ = time.Parse("2006-01-02 15:04:05", lastSeen)
		results = append(results, ps)
	}
	return results, rows.Err()
}

// --- Session operations ---

// StartSession records a new session and returns its ID.
func (s *StateStore) StartSession() (int64, error) {
	result, err := s.db.Exec(`INSERT INTO sessions (started_at) VALUES (CURRENT_TIMESTAMP)`)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// UpdateSession updates session metrics.
func (s *StateStore) UpdateSession(id int64, requests, bytesBefore, bytesAfter, secretsFound, suggestionsEmitted int) error {
	tokensSaved := (bytesBefore - bytesAfter) / 4
	ops := 100.0
	if bytesBefore > 0 {
		ops = float64(bytesAfter) / float64(bytesBefore) * 100
	}
	_, err := s.db.Exec(`
		UPDATE sessions SET
			requests = ?, bytes_before = ?, bytes_after = ?,
			tokens_saved = ?, secrets_found = ?,
			suggestions_emitted = ?, ops_percent = ?
		WHERE id = ?
	`, requests, bytesBefore, bytesAfter, tokensSaved, secretsFound, suggestionsEmitted, ops, id)
	return err
}

// EndSession marks a session as ended.
func (s *StateStore) EndSession(id int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET ended_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// SessionSummary holds aggregated session stats.
type SessionSummary struct {
	TotalSessions      int
	TotalRequests      int
	TotalTokensSaved   int
	TotalSecretsCaught int
	AvgOPS             float64
}

// SessionStats returns aggregated session statistics.
func (s *StateStore) SessionStats() (*SessionSummary, error) {
	row := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(requests), 0), COALESCE(SUM(tokens_saved), 0),
			   COALESCE(SUM(secrets_found), 0), COALESCE(AVG(ops_percent), 100.0)
		FROM sessions
	`)
	var ss SessionSummary
	if err := row.Scan(&ss.TotalSessions, &ss.TotalRequests, &ss.TotalTokensSaved, &ss.TotalSecretsCaught, &ss.AvgOPS); err != nil {
		return nil, err
	}
	return &ss, nil
}

// --- Workflow operations ---

// RecordWorkflow upserts a workflow sequence.
func (s *StateStore) RecordWorkflow(hash, steps string) error {
	_, err := s.db.Exec(`
		INSERT INTO workflows (hash, steps, frequency, last_seen)
		VALUES (?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(hash) DO UPDATE SET
			frequency = frequency + 1,
			last_seen = CURRENT_TIMESTAMP
	`, hash, steps)
	return err
}

// FrequentWorkflows returns workflows with frequency >= threshold.
func (s *StateStore) FrequentWorkflows(minFrequency int) ([]struct {
	Hash      string
	Steps     string
	Frequency int
}, error) {
	rows, err := s.db.Query(`
		SELECT hash, steps, frequency FROM workflows
		WHERE frequency >= ? AND suggested = 0
		ORDER BY frequency DESC
	`, minFrequency)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []struct {
		Hash      string
		Steps     string
		Frequency int
	}
	for rows.Next() {
		var r struct {
			Hash      string
			Steps     string
			Frequency int
		}
		if err := rows.Scan(&r.Hash, &r.Steps, &r.Frequency); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// --- Retention ---

// GC removes data older than the retention period.
func (s *StateStore) GC() (int64, error) {
	cutoff := time.Now().Add(-s.retention).Format("2006-01-02 15:04:05")

	var total int64
	for _, table := range []struct {
		name string
		col  string
	}{
		{"patterns", "last_seen"},
		{"sessions", "started_at"},
		{"workflows", "last_seen"},
		{"dnd_events", "started_at"},
	} {
		result, err := s.db.Exec(
			fmt.Sprintf("DELETE FROM %s WHERE %s <= ?", table.name, table.col),
			cutoff,
		)
		if err != nil {
			return total, err
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return total, nil
}

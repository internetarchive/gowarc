package warc

import (
	"context"
	"log/slog"
	"sync"
)

// LogBackend provides a pluggable logging interface compatible with slog.
// Users can implement this interface to integrate their preferred logging solution.
// The interface matches slog.Logger method signatures for easy wrapping.
//
// Users control the log level cutoff by configuring their logger implementation.
// For example, when wrapping slog.Logger, set the level in the handler:
//
//	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
//	logger := slog.New(handler)
//
// If no LogBackend is provided, a no-op logger is used by default.
type LogBackend interface {
	// Debug logs a message at Debug level with optional key-value pairs
	Debug(msg string, args ...any)

	// Info logs a message at Info level with optional key-value pairs
	Info(msg string, args ...any)

	// Warn logs a message at Warn level with optional key-value pairs
	Warn(msg string, args ...any)

	// Error logs a message at Error level with optional key-value pairs
	Error(msg string, args ...any)
}

// noopLogger is a no-op implementation of LogBackend used when no logger is provided.
type noopLogger struct{}

func (n *noopLogger) Debug(_ string, _ ...any)                                {}
func (n *noopLogger) Info(_ string, _ ...any)                                 {}
func (n *noopLogger) Warn(_ string, _ ...any)                                 {}
func (n *noopLogger) Error(_ string, _ ...any)                                {}
func (n *noopLogger) Log(_ context.Context, _ slog.Level, _ string, _ ...any) {}

// LogEntry represents a single log entry captured by TestLogger
type LogEntry struct {
	Level   slog.Level
	Message string
	Args    []any
}

// testLogger is a thread-safe logger implementation for unit tests that captures
// all log messages for verification. Messages can be retrieved sequentially.
type testLogger struct {
	entries []LogEntry
	mu      sync.Mutex
}

// NewTestLogger creates a new TestLogger for use in unit tests
func NewTestLogger() *testLogger {
	return &testLogger{
		entries: make([]LogEntry, 0),
	}
}

func (t *testLogger) Debug(msg string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, LogEntry{Level: slog.LevelDebug, Message: msg, Args: args})
}

func (t *testLogger) Info(msg string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, LogEntry{Level: slog.LevelInfo, Message: msg, Args: args})
}

func (t *testLogger) Warn(msg string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, LogEntry{Level: slog.LevelWarn, Message: msg, Args: args})
}

func (t *testLogger) Error(msg string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, LogEntry{Level: slog.LevelError, Message: msg, Args: args})
}

func (t *testLogger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, LogEntry{Level: level, Message: msg, Args: args})
}

// Entries returns all captured log entries
func (t *testLogger) Entries() []LogEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Return a copy to prevent external modification
	result := make([]LogEntry, len(t.entries))
	copy(result, t.entries)
	return result
}

// Next returns the next log entry and removes it from the queue.
// Returns nil if no entries are available.
func (t *testLogger) Next() *LogEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.entries) == 0 {
		return nil
	}
	entry := t.entries[0]
	t.entries = t.entries[1:]
	return &entry
}

// Count returns the number of captured log entries
func (t *testLogger) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// Clear removes all captured log entries
func (t *testLogger) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = make([]LogEntry, 0)
}

// HasLevel returns true if any log entry with the specified level exists
func (t *testLogger) HasLevel(level slog.Level) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, entry := range t.entries {
		if entry.Level == level {
			return true
		}
	}
	return false
}

// FindByMessage returns all log entries that contain the specified message
func (t *testLogger) FindByMessage(msg string) []LogEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]LogEntry, 0)
	for _, entry := range t.entries {
		if entry.Message == msg {
			result = append(result, entry)
		}
	}
	return result
}

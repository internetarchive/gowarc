package warc

import (
	"context"
	"log/slog"
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

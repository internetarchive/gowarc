package warc

import (
	"context"
	"log/slog"
	"testing"
)

func TestTestLogger_BasicFunctionality(t *testing.T) {
	logger := NewTestLogger()

	// Log some messages
	logger.Debug("debug message", "key1", "value1")
	logger.Info("info message", "key2", "value2")
	logger.Warn("warn message", "key3", "value3")
	logger.Error("error message", "key4", "value4")

	// Check count
	if logger.Count() != 4 {
		t.Errorf("Expected 4 log entries, got %d", logger.Count())
	}

	// Test Next() - sequential retrieval
	entry := logger.Next()
	if entry == nil {
		t.Fatal("Expected first entry, got nil")
	}
	if entry.Level != slog.LevelDebug || entry.Message != "debug message" {
		t.Errorf("Expected debug message, got %v: %s", entry.Level, entry.Message)
	}

	entry = logger.Next()
	if entry == nil {
		t.Fatal("Expected second entry, got nil")
	}
	if entry.Level != slog.LevelInfo || entry.Message != "info message" {
		t.Errorf("Expected info message, got %v: %s", entry.Level, entry.Message)
	}

	// Count should be 2 now (2 consumed)
	if logger.Count() != 2 {
		t.Errorf("Expected 2 remaining entries, got %d", logger.Count())
	}
}

func TestTestLogger_HasLevel(t *testing.T) {
	logger := NewTestLogger()

	logger.Debug("test")
	logger.Info("test")

	if !logger.HasLevel(slog.LevelDebug) {
		t.Error("Expected to find Debug level")
	}

	if !logger.HasLevel(slog.LevelInfo) {
		t.Error("Expected to find Info level")
	}

	if logger.HasLevel(slog.LevelError) {
		t.Error("Did not expect to find Error level")
	}
}

func TestTestLogger_FindByMessage(t *testing.T) {
	logger := NewTestLogger()

	logger.Info("test message 1")
	logger.Info("test message 2")
	logger.Info("test message 1")
	logger.Debug("other message")

	entries := logger.FindByMessage("test message 1")
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries with 'test message 1', got %d", len(entries))
	}

	entries = logger.FindByMessage("other message")
	if len(entries) != 1 {
		t.Errorf("Expected 1 entry with 'other message', got %d", len(entries))
	}

	entries = logger.FindByMessage("nonexistent")
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries with 'nonexistent', got %d", len(entries))
	}
}

func TestTestLogger_Clear(t *testing.T) {
	logger := NewTestLogger()

	logger.Info("test 1")
	logger.Info("test 2")
	logger.Info("test 3")

	if logger.Count() != 3 {
		t.Errorf("Expected 3 entries before clear, got %d", logger.Count())
	}

	logger.Clear()

	if logger.Count() != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", logger.Count())
	}

	// Next should return nil after clear
	if entry := logger.Next(); entry != nil {
		t.Error("Expected nil after clear, got entry")
	}
}

func TestTestLogger_LogMethod(t *testing.T) {
	logger := NewTestLogger()

	ctx := context.Background()
	logger.Log(ctx, slog.LevelWarn, "custom level message", "key", "value")

	if logger.Count() != 1 {
		t.Errorf("Expected 1 entry, got %d", logger.Count())
	}

	entry := logger.Next()
	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}
	if entry.Level != slog.LevelWarn {
		t.Errorf("Expected Warn level, got %v", entry.Level)
	}
	if entry.Message != "custom level message" {
		t.Errorf("Expected 'custom level message', got %s", entry.Message)
	}
}

func TestTestLogger_Entries(t *testing.T) {
	logger := NewTestLogger()

	logger.Debug("msg1")
	logger.Info("msg2")
	logger.Error("msg3")

	// Get all entries
	entries := logger.Entries()
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Verify it's a copy (modifying shouldn't affect logger)
	entries[0].Message = "modified"
	allEntries := logger.Entries()
	if allEntries[0].Message == "modified" {
		t.Error("Entries() should return a copy, but original was modified")
	}

	// Count should still be 3 (Entries doesn't consume)
	if logger.Count() != 3 {
		t.Errorf("Expected count to remain 3, got %d", logger.Count())
	}
}

func TestTestLogger_ArgsCapture(t *testing.T) {
	logger := NewTestLogger()

	logger.Info("test message", "key1", "value1", "key2", 42, "key3", true)

	entry := logger.Next()
	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}

	if len(entry.Args) != 6 {
		t.Errorf("Expected 6 args, got %d", len(entry.Args))
	}

	// Verify args are captured correctly
	if entry.Args[0] != "key1" || entry.Args[1] != "value1" {
		t.Error("Args not captured correctly")
	}
	if entry.Args[2] != "key2" || entry.Args[3] != 42 {
		t.Error("Args not captured correctly")
	}
	if entry.Args[4] != "key3" || entry.Args[5] != true {
		t.Error("Args not captured correctly")
	}
}

func TestTestLogger_ThreadSafety(t *testing.T) {
	logger := NewTestLogger()

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				logger.Info("concurrent message")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 1000 entries
	if logger.Count() != 1000 {
		t.Errorf("Expected 1000 entries, got %d", logger.Count())
	}
}

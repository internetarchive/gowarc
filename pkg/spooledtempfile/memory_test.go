package spooledtempfile

import (
	"testing"
	"time"
)

// TestGetCachedMemoryUsage verifies that the caching mechanism works correctly.
func TestGetCachedMemoryUsage(t *testing.T) {
	// Save original function
	originalFn := getSystemMemoryUsedFraction

	// Track how many times the function is called
	callCount := 0
	getSystemMemoryUsedFraction = func() (float64, error) {
		callCount++
		return 0.5, nil
	}

	// Restore at the end
	defer func() {
		getSystemMemoryUsedFraction = originalFn
	}()

	// Reset cache
	memoryUsageCache.lastChecked = time.Time{}
	memoryUsageCache.lastFraction = 0

	// First call should invoke the function
	frac1, err := getCachedMemoryUsage()
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if frac1 != 0.5 {
		t.Fatalf("expected 0.5, got %v", frac1)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Second immediate call should use cache
	frac2, err := getCachedMemoryUsage()
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if frac2 != 0.5 {
		t.Fatalf("expected 0.5, got %v", frac2)
	}
	if callCount != 1 {
		t.Fatalf("expected still 1 call (cached), got %d", callCount)
	}

	// Simulate cache expiration
	memoryUsageCache.lastChecked = time.Now().Add(-memoryCheckInterval - time.Millisecond)

	// Next call should invoke the function again
	frac3, err := getCachedMemoryUsage()
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}
	if frac3 != 0.5 {
		t.Fatalf("expected 0.5, got %v", frac3)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (cache expired), got %d", callCount)
	}
}

// TestGetSystemMemoryUsedFraction_Integration verifies that the actual implementation
// returns a valid value on the current platform.
func TestGetSystemMemoryUsedFraction_Integration(t *testing.T) {
	fraction, err := getSystemMemoryUsedFraction()
	if err != nil {
		t.Fatalf("getSystemMemoryUsedFraction() failed: %v", err)
	}

	if fraction < 0 || fraction > 1 {
		t.Fatalf("memory fraction out of range: got %v, want 0.0-1.0", fraction)
	}

	t.Logf("Current system memory usage: %.2f%%", fraction*100)
}

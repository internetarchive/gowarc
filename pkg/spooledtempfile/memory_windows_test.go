//go:build windows

package spooledtempfile

import (
	"testing"
)

// TestGetSystemMemoryUsedFraction verifies that the Windows implementation returns a valid memory fraction between 0 and 1.
func TestGetSystemMemoryUsedFraction(t *testing.T) {
	fraction, err := getSystemMemoryUsedFraction()
	if err != nil {
		t.Fatalf("getSystemMemoryUsedFraction() failed: %v", err)
	}

	if fraction < 0 || fraction > 1 {
		t.Fatalf("memory fraction out of range: got %v, want 0.0-1.0", fraction)
	}

	t.Logf("Current system memory usage: %.2f%%", fraction*100)
}

// TestGlobalMemoryStatusEx verifies that we can successfully retrieve memory values via globalMemoryStatusEx.
func TestGlobalMemoryStatusEx(t *testing.T) {
	totalPhys, availPhys, err := globalMemoryStatusEx()
	if err != nil {
		t.Fatalf("globalMemoryStatusEx failed: %v", err)
	}

	if totalPhys == 0 {
		t.Fatal("total physical memory is 0")
	}

	t.Logf("Total physical memory: %d bytes (%.2f GB)", totalPhys, float64(totalPhys)/(1024*1024*1024))
	t.Logf("Available physical memory: %d bytes (%.2f GB)", availPhys, float64(availPhys)/(1024*1024*1024))

	usedPhys := totalPhys - availPhys
	t.Logf("Used physical memory: %d bytes (%.2f GB)", usedPhys, float64(usedPhys)/(1024*1024*1024))

	usedPercent := float64(usedPhys) / float64(totalPhys) * 100
	t.Logf("Memory usage: %.2f%%", usedPercent)
}

// TestMemoryFractionConsistency verifies that multiple calls return consistent values.
func TestMemoryFractionConsistency(t *testing.T) {
	const calls = 5
	var fractions [calls]float64

	for i := 0; i < calls; i++ {
		frac, err := getSystemMemoryUsedFraction()
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		fractions[i] = frac
	}

	// Check that all values are within a reasonable range of each other
	// Memory usage shouldn't vary wildly between consecutive calls
	for i := 1; i < calls; i++ {
		diff := fractions[i] - fractions[i-1]
		if diff < -0.2 || diff > 0.2 {
			t.Errorf("memory fraction changed too much between calls: %v -> %v (diff: %v)",
				fractions[i-1], fractions[i], diff)
		}
	}
}

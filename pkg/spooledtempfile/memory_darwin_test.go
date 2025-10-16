//go:build darwin

package spooledtempfile

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestGetSystemMemoryUsedFraction verifies that the macOS implementation
// returns a valid memory fraction between 0 and 1.
func TestGetSystemMemoryUsedFraction(t *testing.T) {
	fraction, err := getSystemMemoryUsedFraction()
	if err != nil {
		t.Fatalf("getSystemMemoryUsedFraction() failed: %v", err)
	}

	if fraction < 0 || fraction > 1 {
		t.Fatalf("memory fraction out of range: got %v, want 0.0-1.0", fraction)
	}

	// Log the result for informational purposes
	t.Logf("Current system memory usage: %.2f%%", fraction*100)
}

// TestSysctlMemoryValues verifies that we can successfully retrieve memory values via sysctl.
func TestSysctlMemoryValues(t *testing.T) {
	// Test hw.memsize
	totalBytes, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		t.Fatalf("failed to get hw.memsize: %v", err)
	}
	if totalBytes == 0 {
		t.Fatal("hw.memsize returned 0")
	}
	t.Logf("Total memory: %d bytes (%.2f GB)", totalBytes, float64(totalBytes)/(1024*1024*1024))

	// Test vm.pagesize
	pageSize, err := unix.SysctlUint32("vm.pagesize")
	if err != nil {
		t.Fatalf("failed to get vm.pagesize: %v", err)
	}
	if pageSize == 0 {
		t.Fatal("vm.pagesize returned 0")
	}
	t.Logf("Page size: %d bytes", pageSize)

	// Test page counts
	freePages, err := getSysctlUint32("vm.page_free_count")
	if err != nil {
		t.Fatalf("failed to get vm.page_free_count: %v", err)
	}
	t.Logf("Free pages: %d (%.2f MB)", freePages, float64(freePages*pageSize)/(1024*1024))

	pageableInternal, err := getSysctlUint32("vm.page_pageable_internal_count")
	if err != nil {
		t.Fatalf("failed to get vm.page_pageable_internal_count: %v", err)
	}
	t.Logf("Pageable internal pages: %d (%.2f MB)", pageableInternal, float64(pageableInternal*pageSize)/(1024*1024))

	pageableExternal, err := getSysctlUint32("vm.page_pageable_external_count")
	if err != nil {
		t.Fatalf("failed to get vm.page_pageable_external_count: %v", err)
	}
	t.Logf("Pageable external pages: %d (%.2f MB)", pageableExternal, float64(pageableExternal*pageSize)/(1024*1024))
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

// TestGetSysctlUint32 tests the helper function
func TestGetSysctlUint32(t *testing.T) {
	// Test with a known sysctl value
	pageSize, err := getSysctlUint32("vm.pagesize")
	if err != nil {
		t.Fatalf("getSysctlUint32(\"vm.pagesize\") failed: %v", err)
	}

	if pageSize == 0 {
		t.Fatal("vm.pagesize returned 0")
	}

	// pageSize should be a power of 2 and reasonable (4096 or 16384 typically)
	if pageSize != 4096 && pageSize != 16384 {
		t.Logf("Warning: unusual page size: %d", pageSize)
	}

	t.Logf("Page size: %d bytes", pageSize)
}

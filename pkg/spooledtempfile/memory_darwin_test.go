//go:build darwin

package spooledtempfile

import (
	"errors"
	"math"
	"testing"
)

// almostEq compares floats with a tiny epsilon to avoid flaky tests on CI.
func almostEq(a, b float64) bool {
	const eps = 1e-12
	return math.Abs(a-b) <= eps
}

// withHooks temporarily overrides the sysctl/page size hooks so tests can feed
// synthetic counters and page sizes. It returns a restore function to defer.
func withHooks(t *testing.T, sys map[string]uint64, pgSize int, errFor string) func() {
	t.Helper()

	origSys := sysctlUint64Hook
	origPg := getpagesizeHook

	sysctlUint64Hook = func(name string) (uint64, error) {
		if errFor != "" && name == errFor {
			return 0, errors.New("injected sysctl error")
		}
		v, ok := sys[name]
		if !ok {
			// Absent keys act like 0 for our code-paths in tests.
			return 0, nil
		}
		return v, nil
	}
	getpagesizeHook = func() int { return pgSize }

	return func() {
		sysctlUint64Hook = origSys
		getpagesizeHook = origPg
	}
}

// TestDarwinFraction_BasicCounts checks the core math: used = active+inactive+speculative+wired,
// total = used + free. This mirrors the Linux cgroup usage/limit fraction idea but with page classes.
func TestDarwinFraction_BasicCounts(t *testing.T) {
	sys := map[string]uint64{
		"vm.page_free_count":        100,
		"vm.page_active_count":      200,
		"vm.page_inactive_count":    300,
		"vm.page_speculative_count": 50,
		"vm.page_wire_count":        25,
	}
	restore := withHooks(t, sys, 4096, "")
	defer restore()

	got, err := getSystemMemoryUsedFraction()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	used := float64(200 + 300 + 50 + 25) // 575
	total := float64(100) + used         // 675
	want := used / total
	if !almostEq(got, want) {
		t.Fatalf("got %.15f, want %.15f", got, want)
	}
}

// TestDarwinFraction_IncludesWired ensures wired pages are counted as "used" memory,
// akin to how Linux would treat non-freeable memory when computing usage.
func TestDarwinFraction_IncludesWired(t *testing.T) {
	sys := map[string]uint64{
		"vm.page_free_count":        100,
		"vm.page_active_count":      200,
		"vm.page_inactive_count":    300,
		"vm.page_speculative_count": 50,
		"vm.page_wire_count":        125, // bumped by +100 vs the basic test
	}
	restore := withHooks(t, sys, 4096, "")
	defer restore()

	got, err := getSystemMemoryUsedFraction()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	used := float64(200 + 300 + 50 + 125) // 675
	total := float64(100) + used          // 775
	want := used / total
	if !almostEq(got, want) {
		t.Fatalf("got %.15f, want %.15f", got, want)
	}
}

// TestDarwinFraction_ZeroTotalSafe verifies we handle the degenerate case where
// all page counters are zero, returning fraction 0 and no error instead of dividing by zero.
func TestDarwinFraction_ZeroTotalSafe(t *testing.T) {
	sys := map[string]uint64{
		"vm.page_free_count":        0,
		"vm.page_active_count":      0,
		"vm.page_inactive_count":    0,
		"vm.page_speculative_count": 0,
		"vm.page_wire_count":        0,
	}
	restore := withHooks(t, sys, 4096, "")
	defer restore()

	got, err := getSystemMemoryUsedFraction()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %.15f, want 0", got)
	}
}

// TestDarwinFraction_ErrorPropagation confirms that a failing sysctl read is surfaced
// to the caller (like Linux read errors from cgroup/proc files).
func TestDarwinFraction_ErrorPropagation(t *testing.T) {
	sys := map[string]uint64{
		"vm.page_free_count":        100,
		"vm.page_active_count":      200,
		"vm.page_inactive_count":    300,
		"vm.page_speculative_count": 50,
		"vm.page_wire_count":        25,
	}
	restore := withHooks(t, sys, 4096, "vm.page_active_count") // inject error on active
	defer restore()

	if _, err := getSystemMemoryUsedFraction(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestDarwinFraction_PageSizeCancels demonstrates that changing the page size
// (e.g., 4K vs 16K systems) does not change the fraction, mirroring Linux where
// /proc/meminfo units (kB) cancel out in ratios.
func TestDarwinFraction_PageSizeCancels(t *testing.T) {
	sys := map[string]uint64{
		"vm.page_free_count":        100,
		"vm.page_active_count":      200,
		"vm.page_inactive_count":    300,
		"vm.page_speculative_count": 50,
		"vm.page_wire_count":        25,
	}

	// 4K
	restore := withHooks(t, sys, 4096, "")
	got4k, err := getSystemMemoryUsedFraction()
	restore()
	if err != nil {
		t.Fatalf("unexpected error (4k): %v", err)
	}

	// 16K
	restore = withHooks(t, sys, 16384, "")
	defer restore()
	got16k, err := getSystemMemoryUsedFraction()
	if err != nil {
		t.Fatalf("unexpected error (16k): %v", err)
	}

	if !almostEq(got4k, got16k) {
		t.Fatalf("fraction should be independent of page size: 4k=%.15f 16k=%.15f", got4k, got16k)
	}
}

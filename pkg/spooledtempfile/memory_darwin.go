//go:build darwin

package spooledtempfile

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// getSystemMemoryUsedFraction returns the fraction of system memory currently in use on macOS.
// It uses sysctl to query system memory statistics.
var getSystemMemoryUsedFraction = func() (float64, error) {
	// Get total physical memory using sysctl
	totalBytes, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, fmt.Errorf("failed to get hw.memsize: %w", err)
	}

	if totalBytes == 0 {
		return 0, fmt.Errorf("hw.memsize returned 0")
	}

	// Get page size
	pageSize, err := unix.SysctlUint32("vm.pagesize")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.pagesize: %w", err)
	}

	// Get page counts using the sysctl values that actually exist on macOS
	// Note: macOS doesn't expose vm.page_active_count or vm.page_wire_count via sysctl
	// We use the available values:
	// - vm.page_free_count: free pages
	// - vm.page_purgeable_count: purgeable pages (can be reclaimed)
	// - vm.page_speculative_count: speculative pages

	freePages, err := unix.SysctlUint32("vm.page_free_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_free_count: %w", err)
	}

	purgeablePages, err := unix.SysctlUint32("vm.page_purgeable_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_purgeable_count: %w", err)
	}

	speculativePages, err := unix.SysctlUint32("vm.page_speculative_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_speculative_count: %w", err)
	}

	// Calculate used memory
	// Used = Total - (Free + Purgeable + Speculative)
	totalPages := totalBytes / uint64(pageSize)
	reclaimablePages := uint64(freePages) + uint64(purgeablePages) + uint64(speculativePages)

	// Clamp to prevent underflow: if reclaimable > total, use total
	var usedPages uint64
	if reclaimablePages < totalPages {
		usedPages = totalPages - reclaimablePages
	} else {
		usedPages = 0
	}

	usedBytes := usedPages * uint64(pageSize)

	// Calculate fraction
	fraction := float64(usedBytes) / float64(totalBytes)

	// Sanity check: fraction should be between 0 and 1
	if fraction < 0 || fraction > 1 {
		return 0, fmt.Errorf("calculated memory fraction out of range: %v (used: %d, total: %d)",
			fraction, usedBytes, totalBytes)
	}

	return fraction, nil
}

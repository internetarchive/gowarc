//go:build darwin

package spooledtempfile

import (
	"encoding/binary"
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
	// - vm.page_pageable_internal_count: internal pageable (active anonymous pages)
	// - vm.page_pageable_external_count: external pageable (file-backed pages)
	// - vm.page_purgeable_count: purgeable pages (can be reclaimed)
	// - vm.page_speculative_count: speculative pages

	freePages, err := getSysctlUint32("vm.page_free_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_free_count: %w", err)
	}

	pageableInternal, err := getSysctlUint32("vm.page_pageable_internal_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_pageable_internal_count: %w", err)
	}

	pageableExternal, err := getSysctlUint32("vm.page_pageable_external_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_pageable_external_count: %w", err)
	}

	purgeablePages, err := getSysctlUint32("vm.page_purgeable_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_purgeable_count: %w", err)
	}

	speculativePages, err := getSysctlUint32("vm.page_speculative_count")
	if err != nil {
		return 0, fmt.Errorf("failed to get vm.page_speculative_count: %w", err)
	}

	// Calculate used memory
	// Used = Total - (Free + Purgeable + Speculative)
	// Or: Used = Pageable Internal + Pageable External + (overhead)
	// We'll use the first approach as it's more straightforward
	totalPages := totalBytes / uint64(pageSize)
	reclaimablePages := uint64(freePages) + uint64(purgeablePages) + uint64(speculativePages)
	usedPages := totalPages - reclaimablePages

	usedBytes := usedPages * uint64(pageSize)

	// Calculate fraction
	fraction := float64(usedBytes) / float64(totalBytes)

	// Sanity check: fraction should be between 0 and 1
	if fraction < 0 || fraction > 1 {
		return 0, fmt.Errorf("calculated memory fraction out of range: %v (used: %d, total: %d, pageable_int: %d, pageable_ext: %d)",
			fraction, usedBytes, totalBytes, pageableInternal, pageableExternal)
	}

	return fraction, nil
}

// getSysctlUint32 gets a uint32 value from sysctl
func getSysctlUint32(name string) (uint32, error) {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return 0, err
	}
	if len(raw) < 4 {
		return 0, fmt.Errorf("sysctl %s returned insufficient data", name)
	}
	// Parse as native-endian uint32 (ARM64 macOS is little-endian)
	return binary.LittleEndian.Uint32(raw), nil
}

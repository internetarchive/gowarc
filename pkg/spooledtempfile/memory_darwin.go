//go:build darwin

package spooledtempfile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// testability hooks (defaults use real syscalls)
var sysctlUint64Hook = sysctlUint64
var getpagesizeHook = func() int { return unix.Getpagesize() }

// getSystemMemoryUsedFraction returns the fraction of used memory on macOS.
// Uses sysctl page counters (no subprocesses, no text parsing).
var getSystemMemoryUsedFraction = func() (float64, error) {
	// Page counts
	free, err := sysctlUint64Hook("vm.page_free_count")
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.page_free_count: %w", err)
	}
	spec, err := sysctlUint64Hook("vm.page_speculative_count")
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.page_speculative_count: %w", err)
	}
	inactive, err := sysctlUint64Hook("vm.page_inactive_count")
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.page_inactive_count: %w", err)
	}
	active, err := sysctlUint64Hook("vm.page_active_count")
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.page_active_count: %w", err)
	}
	wired, err := sysctlUint64Hook("vm.page_wire_count")
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.page_wire_count: %w", err)
	}

	// Page size is kept for clarity (and test hook), but cancels in the ratio.
	_ = getpagesizeHook()

	// Used = active + inactive + speculative + wired (all non-free)
	usedPages := active + inactive + spec + wired
	totalPages := usedPages + free

	if totalPages == 0 {
		return 0, nil
	}

	// Return the ratio directly in pages, avoiding potential overflow.
	return float64(usedPages) / float64(totalPages), nil
}

// sysctlUint64 gets a uint64 sysctl value robustly.
// Order: Uint64 -> Uint32 -> Raw bytes (native endian) -> ASCII digits.
func sysctlUint64(name string) (uint64, error) {
	// 1) Fast path: dedicated uint64
	if v, err := unix.SysctlUint64(name); err == nil {
		return v, nil
	}

	// 2) Some nodes are 32-bit
	if v32, err := unix.SysctlUint32(name); err == nil {
		return uint64(v32), nil
	}

	// 3) Raw buffer: interpret as native-endian integer (Darwin is little-endian).
	if raw, err := unix.SysctlRaw(name, []int{}...); err == nil && len(raw) > 0 {
		switch len(raw) {
		case 1:
			return uint64(raw[0]), nil
		case 2:
			return uint64(binary.LittleEndian.Uint16(raw[:2])), nil
		case 4:
			return uint64(binary.LittleEndian.Uint32(raw[:4])), nil
		default:
			// Most counters are <=8 bytes; if longer, take the first 8.
			if len(raw) >= 8 {
				return binary.LittleEndian.Uint64(raw[:8]), nil
			}
			// Fallback to try ASCII if very odd length.
		}
	}

	// 4) Last resort: treat as ASCII digits (some sysctls are strings)
	if s, err := unix.Sysctl(name); err == nil {
		// Trim whitespace and any trailing NULs just in case.
		s = strings.TrimRight(strings.TrimSpace(s), "\x00")
		if s != "" {
			if v, perr := strconv.ParseUint(s, 10, 64); perr == nil {
				return v, nil
			}
		}
	}

	return 0, errors.New("unable to read sysctl as integer")
}

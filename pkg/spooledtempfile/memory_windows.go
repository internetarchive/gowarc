//go:build windows

package spooledtempfile

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// globalMemoryStatusEx calls the Windows API function to retrieve memory status.
// This is not currently implemented by the Golang native Windows libraries.
func globalMemoryStatusEx() (totalPhys, availPhys uint64, err error) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")

	// Define the MEMORYSTATUSEX structure matching the Windows API
	// See: https://docs.microsoft.com/en-us/windows/win32/api/sysinfoapi/ns-sysinfoapi-memorystatusex
	type memoryStatusEx struct {
		dwLength                uint32
		dwMemoryLoad            uint32
		ullTotalPhys            uint64
		ullAvailPhys            uint64
		ullTotalPageFile        uint64
		ullAvailPageFile        uint64
		ullTotalVirtual         uint64
		ullAvailVirtual         uint64
		ullAvailExtendedVirtual uint64
	}

	var memStatus memoryStatusEx
	memStatus.dwLength = 64

	ret, _, err := proc.Call(uintptr(unsafe.Pointer(&memStatus)))
	if ret == 0 {
		return 0, 0, fmt.Errorf("GlobalMemoryStatusEx failed: %w", err)
	}

	return memStatus.ullTotalPhys, memStatus.ullAvailPhys, nil
}

// getSystemMemoryUsedFraction returns the fraction of physical memory currently in use on Windows.
// It uses the GlobalMemoryStatusEx Windows API to query system memory statistics.
var getSystemMemoryUsedFraction = func() (float64, error) {
	totalPhys, availPhys, err := globalMemoryStatusEx()
	if err != nil {
		return 0, err
	}

	if totalPhys == 0 {
		return 0, fmt.Errorf("total physical memory is 0")
	}

	// Calculate used memory from total and available
	usedPhys := totalPhys - availPhys
	fraction := float64(usedPhys) / float64(totalPhys)

	// Sanity check: fraction should be between 0 and 1
	if fraction < 0 || fraction > 1 {
		return 0, fmt.Errorf("calculated memory fraction out of range: %v (used: %d, total: %d)",
			fraction, usedPhys, totalPhys)
	}

	return fraction, nil
}

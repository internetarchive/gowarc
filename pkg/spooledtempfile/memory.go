package spooledtempfile

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Overridable in tests:
var (
	cgv2UsagePath = "/sys/fs/cgroup/memory.current"
	cgv2HighPath  = "/sys/fs/cgroup/memory.high"
	cgv2MaxPath   = "/sys/fs/cgroup/memory.max"

	cgv1UsagePath = "/sys/fs/cgroup/memory/memory.usage_in_bytes"
	cgv1LimitPath = "/sys/fs/cgroup/memory/memory.limit_in_bytes"

	procMeminfoPath = "/proc/meminfo"
)

// getSystemMemoryUsedFraction returns used/limit for the container if
// cgroup limits are set; otherwise falls back to host /proc/meminfo.
var getSystemMemoryUsedFraction = func() (float64, error) {
	probes := []func() (float64, bool, error){
		cgroupV2UsedFraction,
		cgroupV1UsedFraction,
	}

	for _, p := range probes {
		if frac, ok, err := p(); err != nil {
			return 0, err
		} else if ok {
			return frac, nil
		}
	}

	return hostMeminfoUsedFraction()
}

func cgroupV2UsedFraction() (frac float64, ok bool, err error) {
	usage, uok, err := readUint64FileIfExists(cgv2UsagePath)
	if err != nil {
		return 0, false, err
	}
	if !uok {
		return 0, false, nil // not cgroup v2 (or not accessible)
	}

	// Try memory.high first
	highStr, hok, err := readStringFileIfExists(cgv2HighPath)
	if err != nil {
		return 0, false, err
	}

	var high, max uint64
	var haveHigh bool
	if hok {
		hs := strings.TrimSpace(highStr)
		if hs != "" && hs != "max" {
			if v, e := strconv.ParseUint(hs, 10, 64); e == nil && v > 0 {
				high, haveHigh = v, true
			}
		}
	}

	// Always read memory.max as fallback (and for sanity checks)
	maxStr, mok, err := readStringFileIfExists(cgv2MaxPath)
	if err != nil {
		return 0, false, err
	}
	var haveMax bool
	if mok {
		ms := strings.TrimSpace(maxStr)
		if ms != "" && ms != "max" {
			if v, e := strconv.ParseUint(ms, 10, 64); e == nil && v > 0 {
				max, haveMax = v, true
			}
		}
	}

	// Choose denominator: prefer valid 'high' unless it is >= max.
	switch {
	case haveHigh && haveMax && high < max:
		return float64(usage) / float64(high), true, nil
	case haveMax:
		return float64(usage) / float64(max), true, nil
	case haveHigh:
		return float64(usage) / float64(high), true, nil
	default:
		return 0, false, nil // no effective limit
	}
}

func cgroupV1UsedFraction() (frac float64, ok bool, err error) {
	usage, uok, err := readUint64FileIfExists(cgv1UsagePath)
	if err != nil {
		return 0, false, err
	}

	limit, lok, err := readUint64FileIfExists(cgv1LimitPath)
	if err != nil {
		return 0, false, err
	}
	if !uok || !lok || limit == 0 {
		return 0, false, nil
	}

	// Some kernels report a huge limit (e.g., ~max uint64) to mean "no limit"
	if limit > (1 << 60) { // heuristic ~ 1 exabyte
		return 0, false, nil
	}

	return float64(usage) / float64(limit), true, nil
}

func hostMeminfoUsedFraction() (float64, error) {
	f, err := os.Open(procMeminfoPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open /proc/meminfo: %v", err)
	}
	defer f.Close()

	var memTotal, memAvailable, memFree, buffers, cached uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimRight(fields[0], ":")
		val, _ := strconv.ParseUint(fields[1], 10, 64) // kB
		switch key {
		case "MemTotal":
			memTotal = val
		case "MemAvailable":
			memAvailable = val
		case "MemFree":
			memFree = val
		case "Buffers":
			buffers = val
		case "Cached":
			cached = val
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("scanner error reading /proc/meminfo: %v", err)
	}
	if memTotal == 0 {
		return 0, fmt.Errorf("could not find MemTotal in /proc/meminfo")
	}

	var used uint64
	if memAvailable > 0 {
		used = memTotal - memAvailable
	} else {
		approxAvailable := memFree + buffers + cached
		used = memTotal - approxAvailable
	}

	// meminfo is in kB; unit cancels in the fraction
	return float64(used) / float64(memTotal), nil
}

func readUint64FileIfExists(path string) (val uint64, ok bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}

	// v2 may use "max"; caller handles that as not-ok
	v, perr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if perr != nil {
		return 0, false, nil
	}

	return v, true, nil
}

func readStringFileIfExists(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}

	return string(data), true, nil
}

package spooledtempfile

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// getSystemMemoryUsedFraction returns used/limit for the container if
// cgroup limits are set; otherwise falls back to host /proc/meminfo.
var getSystemMemoryUsedFraction = func() (float64, error) {
	// 1) Try cgroup v2 (unified hierarchy)
	if frac, ok, err := cgroupV2UsedFraction(); err != nil {
		return 0, err
	} else if ok {
		return frac, nil
	}

	// 2) Try cgroup v1 (legacy)
	if frac, ok, err := cgroupV1UsedFraction(); err != nil {
		return 0, err
	} else if ok {
		return frac, nil
	}

	// 3) Fallback to host view
	return hostMeminfoUsedFraction()
}

func cgroupV2UsedFraction() (frac float64, ok bool, err error) {
	// Common modern path with systemd/docker/containerd/Nomad on cgroup v2
	const (
		usagePath = "/sys/fs/cgroup/memory.current"
		limitPath = "/sys/fs/cgroup/memory.max"
	)

	usage, uok, err := readUint64FileIfExists(usagePath)
	if err != nil {
		return 0, false, err
	}

	limitStr, lok, err := readStringFileIfExists(limitPath)
	if err != nil {
		return 0, false, err
	}
	if !uok || !lok {
		return 0, false, nil // not v2 (or not accessible)
	}

	limitStr = strings.TrimSpace(limitStr)
	if limitStr == "max" || limitStr == "" {
		// No effective limit -> not useful for container fraction
		return 0, false, nil
	}

	limit, err := strconv.ParseUint(limitStr, 10, 64)
	if err != nil || limit == 0 {
		return 0, false, nil
	}

	return float64(usage) / float64(limit), true, nil
}

func cgroupV1UsedFraction() (frac float64, ok bool, err error) {
	const (
		usagePath = "/sys/fs/cgroup/memory/memory.usage_in_bytes"
		limitPath = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	)

	usage, uok, err := readUint64FileIfExists(usagePath)
	if err != nil {
		return 0, false, err
	}

	limit, lok, err := readUint64FileIfExists(limitPath)
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
	f, err := os.Open("/proc/meminfo")
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

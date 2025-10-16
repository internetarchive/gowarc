package spooledtempfile

import (
	"sync"
	"time"
)

const (
	// memoryCheckInterval defines how often we check system memory usage.
	memoryCheckInterval = 500 * time.Millisecond
)

type globalMemoryCache struct {
	sync.Mutex
	lastChecked  time.Time
	lastFraction float64
}

var (
	memoryUsageCache = &globalMemoryCache{}
)

// getCachedMemoryUsage returns the cached memory usage fraction, or fetches a new one
// if the cache has expired. This reduces the overhead of checking memory usage on every
// write operation.
func getCachedMemoryUsage() (float64, error) {
	memoryUsageCache.Lock()
	defer memoryUsageCache.Unlock()

	if time.Since(memoryUsageCache.lastChecked) < memoryCheckInterval {
		return memoryUsageCache.lastFraction, nil
	}

	fraction, err := getSystemMemoryUsedFraction()
	if err != nil {
		return 0, err
	}

	memoryUsageCache.lastChecked = time.Now()
	memoryUsageCache.lastFraction = fraction

	return fraction, nil
}

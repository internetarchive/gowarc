package warc

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGetNextWarcFileName(t *testing.T) {
	var serial atomic.Uint64
	result := getNextWARCFilename("", "test", "gzip", &serial)
	split := []string{"test", "00000", ".warc.gz.open"}

	if strings.Contains(result, split[0]) && strings.Contains(result, split[1]) && strings.Contains(result, split[2]) {
		t.Errorf("Expected filename to contain %s, %s, and %s, but got %s", split[0], split[1], split[2], result)
	}
}

func TestSequentialGetNextWarcFileName(t *testing.T) {
	// test if the serials increment correctly when called sequentially
	var serial atomic.Uint64
	var prevSerial uint64

	for range 100 {
		result := getNextWARCFilename("", "test", "gzip", &serial)
		split := strings.Split(result, "-")
		if len(split) < 3 {
			t.Errorf("Unexpected filename format: %s", result)
			continue
		}
		serialStr := split[2]
		serialNum, err := strconv.ParseUint(serialStr, 10, 64)
		if err != nil {
			t.Errorf("Error parsing serial number from filename %s: %v", result, err)
			continue
		}
		if serialNum != prevSerial+1 {
			t.Errorf("Expected serial %d, got %d in filename %s", prevSerial+1, serialNum, result)
		}
		prevSerial = serialNum
	}
}

func TestConcurrentGetNextWarcFileName(t *testing.T) {
	// test if the serials increment correctly when called concurrently
	var serial atomic.Uint64
	var prevSerial uint64
	const goroutines = 10
	const iterations = 20

	results := make(chan string, goroutines*iterations)
	errors := make(chan error, goroutines*iterations)

	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < iterations; i++ {
				result := getNextWARCFilename("", "test", "gzip", &serial)
				results <- result
			}
		}()
	}

	serials := make([]uint64, 0, goroutines*iterations)
	for i := 0; i < goroutines*iterations; i++ {
		select {
		case result := <-results:
			split := strings.Split(result, "-")
			if len(split) < 3 {
				errors <- fmt.Errorf("unexpected filename format: %s", result)
				continue
			}
			serialStr := split[2]
			serialNum, err := strconv.ParseUint(serialStr, 10, 64)
			if err != nil {
				errors <- fmt.Errorf("error parsing serial number from filename %s: %v", result, err)
				continue
			}
			serials = append(serials, serialNum)
		case err := <-errors:
			t.Error(err)
		}
	}

	slices.Sort(serials)

	prevSerial = 0
	for _, s := range serials {
		if s != prevSerial+1 {
			t.Errorf("Expected serial %d, got %d", prevSerial+1, s)
		}
		prevSerial = s
	}

	if serials[0] != 1 {
		t.Errorf("Expected first serial to be 1, got %d", serials[0])
	}

	if serials[len(serials)-1] != uint64(goroutines*iterations) {
		t.Errorf("Expected last serial to be %d, got %d", goroutines*iterations, serials[len(serials)])
	}

	close(results)
	close(errors)
}

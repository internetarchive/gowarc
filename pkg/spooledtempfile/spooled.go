package spooledtempfile

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// InitialBufferSize is the initial pre-allocated buffer size for in-memory writes
	InitialBufferSize = 64 * 1024 // 64 KB initial buffer size
	// MaxInMemorySize is the max number of bytes (currently 1MB) to hold in memory before starting to write to disk
	MaxInMemorySize = 1024 * 1024
	// DefaultMaxRAMUsageFraction is the default fraction of system RAM above which we'll force spooling to disk
	DefaultMaxRAMUsageFraction = 0.50
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
	spooledPool      = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, InitialBufferSize) // Small initial buffer
		},
	}
)

// ReaderAt is the interface for ReadAt - read at position, without moving pointer.
type ReaderAt interface {
	ReadAt(p []byte, off int64) (n int, err error)
}

// ReadSeekCloser is an io.Reader + ReaderAt + io.Seeker + io.Closer + Stat
type ReadSeekCloser interface {
	io.ReadSeekCloser
	ReaderAt
	FileName() string
	Len() int
}

// spooledTempFile writes to memory (or to disk if
// over MaxInMemorySize) and deletes the file on Close
type spooledTempFile struct {
	buf                 []byte        // Use []byte instead of bytes.Buffer
	mem                 *bytes.Reader // Reader for in-memory data
	file                *os.File
	filePrefix          string
	tempDir             string
	maxInMemorySize     int
	fullOnDisk          bool
	reading             bool // transitions at most once from false -> true
	closed              bool
	maxRAMUsageFraction float64 // fraction above which we skip in-memory buffering
}

// ReadWriteSeekCloser is an io.Writer + io.Reader + io.Seeker + io.Closer.
type ReadWriteSeekCloser interface {
	ReadSeekCloser
	io.Writer
}

// NewSpooledTempFile returns an ReadWriteSeekCloser,
// with some important constraints:
//   - You can Write into it, but whenever you call Read or Seek on it,
//     subsequent Write calls will panic.
//   - If threshold is -1, then the default MaxInMemorySize is used.
//   - If maxRAMUsageFraction <= 0, we default to DefaultMaxRAMUsageFraction. E.g. 0.5 = 50%.
//
// If the system memory usage is above maxRAMUsageFraction, we skip writing
// to memory and spool directly on disk to avoid OOM scenarios in high concurrency.
//
// If threshold is less than InitialBufferSize, we default to InitialBufferSize.
// This can cause a buffer not to spool to disk as expected given the threshold passed in.
// e.g.: If threshold is 100, it will default to InitialBufferSize (64KB), then 150B are written effectively crossing the passed threshold,
// but the buffer will not spool to disk as expected. Only when the buffer grows beyond 64KB will it spool to disk.
func NewSpooledTempFile(filePrefix string, tempDir string, threshold int, fullOnDisk bool, maxRAMUsageFraction float64) ReadWriteSeekCloser {
	if threshold < 0 {
		threshold = MaxInMemorySize
	}

	if maxRAMUsageFraction <= 0 {
		maxRAMUsageFraction = DefaultMaxRAMUsageFraction
	}

	if threshold <= InitialBufferSize {
		threshold = InitialBufferSize
	}

	return &spooledTempFile{
		filePrefix:          filePrefix,
		tempDir:             tempDir,
		buf:                 spooledPool.Get().([]byte), // Get a []byte from the pool
		maxInMemorySize:     threshold,
		fullOnDisk:          fullOnDisk,
		maxRAMUsageFraction: maxRAMUsageFraction,
	}
}

func (s *spooledTempFile) prepareRead() error {
	if s.closed {
		return io.EOF
	}

	if s.reading && (s.file != nil || s.buf == nil || s.mem != nil) {
		return nil
	}

	s.reading = true
	if s.file != nil {
		if _, err := s.file.Seek(0, 0); err != nil {
			return fmt.Errorf("file=%v: %w", s.file, err)
		}
		return nil
	}

	s.mem = bytes.NewReader(s.buf) // Create a reader from the []byte slice
	return nil
}

func (s *spooledTempFile) Len() int {
	if s.file != nil {
		fi, err := s.file.Stat()
		if err != nil {
			return -1
		}
		return int(fi.Size())
	}
	return len(s.buf) // Return the length of the []byte slice
}

func (s *spooledTempFile) Read(p []byte) (n int, err error) {
	if err := s.prepareRead(); err != nil {
		return 0, err
	}

	if s.file != nil {
		return s.file.Read(p)
	}

	return s.mem.Read(p)
}

func (s *spooledTempFile) ReadAt(p []byte, off int64) (n int, err error) {
	if err := s.prepareRead(); err != nil {
		return 0, err
	}

	if s.file != nil {
		return s.file.ReadAt(p, off)
	}

	return s.mem.ReadAt(p, off)
}

func (s *spooledTempFile) Seek(offset int64, whence int) (int64, error) {
	if err := s.prepareRead(); err != nil {
		return 0, err
	}

	if s.file != nil {
		return s.file.Seek(offset, whence)
	}
	return s.mem.Seek(offset, whence)
}

func (s *spooledTempFile) Write(p []byte) (n int, err error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.reading {
		panic("write after read")
	}

	if s.file != nil {
		return s.file.Write(p)
	}

	aboveRAMThreshold := s.isSystemMemoryUsageHigh()
	if aboveRAMThreshold || s.fullOnDisk || (len(s.buf)+len(p) > s.maxInMemorySize) {
		// Switch to file if we haven't already
		s.file, err = os.CreateTemp(s.tempDir, s.filePrefix+"-")
		if err != nil {
			return 0, err
		}

		// Copy what we already had in the buffer
		_, err = s.file.Write(s.buf)
		if err != nil {
			s.file.Close()
			s.file = nil
			return 0, err
		}

		// Release the buffer back to the pool
		if s.buf != nil && cap(s.buf) <= InitialBufferSize && cap(s.buf) > 0 {
			spooledPool.Put(s.buf[:0]) // Reset the buffer before returning it to the pool
		}
		s.buf = nil
		s.mem = nil // Discard the bytes.Reader

		// Write incoming bytes directly to file
		n, err = s.file.Write(p)
		if err != nil {
			s.file.Close()
			s.file = nil
			return n, err
		}
		return n, nil
	}

	// Grow the buffer if necessary, but never exceed MaxInMemorySize
	if len(s.buf)+len(p) > cap(s.buf) {
		newCap := len(s.buf) + len(p)
		if newCap > s.maxInMemorySize {
			newCap = s.maxInMemorySize
		}

		// Allocate a new buffer with the increased capacity
		newBuf := make([]byte, len(s.buf), newCap)
		copy(newBuf, s.buf)

		// Release the old buffer to the pool
		if s.buf != nil && cap(s.buf) <= InitialBufferSize && cap(s.buf) > 0 {
			spooledPool.Put(s.buf[:0]) // Reset the buffer before returning it to the pool
		}
		s.buf = newBuf
		s.mem = nil // Discard the old bytes.Reader
	}

	// Append data to the buffer
	s.buf = append(s.buf, p...)
	return len(p), nil
}

func (s *spooledTempFile) Close() error {
	s.closed = true
	s.mem = nil

	// Release the buffer back to the pool
	if s.buf != nil {
		if cap(s.buf) <= InitialBufferSize && cap(s.buf) > 0 {
			spooledPool.Put(s.buf[:0]) // Reset the buffer before returning it to the pool
		}
		s.buf = nil
	}

	if s.file == nil {
		return nil
	}

	s.file.Close()

	if err := os.Remove(s.file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.file = nil
	return nil
}

func (s *spooledTempFile) FileName() string {
	if s.file != nil {
		return s.file.Name()
	}
	return ""
}

// isSystemMemoryUsageHigh returns true if current memory usage
// exceeds s.maxRAMUsageFraction of total system memory.
// This implementation is Linux-specific via /proc/meminfo.
func (s *spooledTempFile) isSystemMemoryUsageHigh() bool {
	usedFraction, err := getCachedMemoryUsage()
	if err != nil {
		// If we fail to get memory usage info, we conservatively return false,
		// or you may choose to return true to avoid in-memory usage.
		return false
	}
	return usedFraction >= s.maxRAMUsageFraction
}

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

var getSystemMemoryUsedFraction = func() (float64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("failed to open /proc/meminfo: %v", err)
	}
	defer f.Close()

	var memTotal, memAvailable, memFree, buffers, cached uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimRight(fields[0], ":")
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		switch key {
		case "MemTotal":
			memTotal = value
		case "MemAvailable":
			memAvailable = value
		case "MemFree":
			memFree = value
		case "Buffers":
			buffers = value
		case "Cached":
			cached = value
		}
		if memTotal > 0 && memAvailable > 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
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

	return float64(used) / float64(memTotal), nil
}

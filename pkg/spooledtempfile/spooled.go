package spooledtempfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/valyala/bytebufferpool"
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
	buf                 *bytebufferpool.ByteBuffer
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
		buf:                 bytebufferpool.Get(),
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

	s.mem = bytes.NewReader(s.buf.Bytes())
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
	return s.buf.Len()
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
	if aboveRAMThreshold || s.fullOnDisk || (s.buf.Len()+len(p) > s.maxInMemorySize) {
		// Switch to file if we haven't already
		s.file, err = os.CreateTemp(s.tempDir, s.filePrefix+"-")
		if err != nil {
			return 0, err
		}

		// Copy what we already had in the buffer
		_, err = s.buf.WriteTo(s.file)
		if err != nil {
			s.file.Close()
			s.file = nil
			return 0, err
		}

		// Release the buffer back to the pool
		if s.buf != nil {
			bytebufferpool.Put(s.buf)
		}
		s.buf = nil

		// Write incoming bytes directly to file
		n, err = s.file.Write(p)
		if err != nil {
			s.file.Close()
			s.file = nil
			return n, err
		}
		return n, nil
	}

	// Append data to the buffer
	s.buf.Write(p)
	return len(p), nil
}

func (s *spooledTempFile) Close() error {
	s.closed = true

	if s.mem != nil {
		s.mem.Reset([]byte{})
		s.mem = nil
	}

	// Release the buffer back to the pool
	if s.buf != nil {
		bytebufferpool.Put(s.buf)
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
// This implementation is Linux-specific via cgroup1/2 & /proc/meminfo.
func (s *spooledTempFile) isSystemMemoryUsageHigh() bool {
	usedFraction, err := getCachedMemoryUsage()
	if err != nil {
		// Log the error since this should never happen
		log.Printf("spooledtempfile: error getting memory usage: %v", err)
		// Conservatively return true to trigger spilling to disk
		return true
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

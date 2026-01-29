package spooledtempfile

import (
	"bytes"
	"crypto/sha256"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkWrite_InMemory_Small(b *testing.B) {
	// 256 KiB total, threshold 1 MiB => stays in memory
	const total = 256 << 10
	const chunk = 32 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	b.ReportAllocs()
	b.SetBytes(total)
	for b.Loop() {
		s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
		repeatWrite(b, s, total, chunk, data)
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWrite_SpillToDisk(b *testing.B) {
	// 4 MiB total, threshold 1 MiB => will spill
	const total = 4 << 20
	const chunk = 128 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	b.ReportAllocs()
	b.SetBytes(total)
	for b.Loop() {
		s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
		repeatWrite(b, s, total, chunk, data)
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWrite_FullOnDisk(b *testing.B) {
	// 4 MiB total, fullOnDisk=true => always file
	const total = 4 << 20
	const chunk = 128 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	b.ReportAllocs()
	b.SetBytes(total)
	for b.Loop() {
		s := NewSpooledTempFile("bench", tempDir, 1<<20, true, DefaultMaxRAMUsageFraction)
		repeatWrite(b, s, total, chunk, data)
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWrite_SpillDueToHighMemSignal(b *testing.B) {
	// Simulate high system memory usage to force disk path, even below threshold
	const total = 256 << 10
	const chunk = 32 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	orig := getSystemMemoryUsedFraction
	getSystemMemoryUsedFraction = func() (float64, error) { return 0.99, nil }
	defer func() { getSystemMemoryUsedFraction = orig }()

	b.ReportAllocs()
	b.SetBytes(total)
	for b.Loop() {
		s := NewSpooledTempFile("bench", tempDir, 1<<20, false, 0.50)
		repeatWrite(b, s, total, chunk, data)
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRead_AfterWrite_InMemory(b *testing.B) {
	// Prepare an in-memory object then benchmark full read
	const total = 256 << 10
	const chunk = 32 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
	repeatWrite(b, s, total, chunk, data)

	// seal writing; first Read triggers in-memory reader
	buf := make([]byte, 64<<10)
	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Seek(0, io.SeekStart); err != nil {
			b.Fatal(err)
		}
		read := 0
		for {
			n, err := s.Read(buf)
			if n > 0 {
				read += n
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkRead_AfterWrite_OnDisk(b *testing.B) {
	// Prepare an on-disk object then benchmark full read
	const total = 8 << 20
	const chunk = 128 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
	repeatWrite(b, s, total, chunk, data)

	buf := make([]byte, 128<<10)
	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Seek(0, io.SeekStart); err != nil {
			b.Fatal(err)
		}
		read := 0
		for {
			n, err := s.Read(buf)
			if n > 0 {
				read += n
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkReadAt_OnDisk(b *testing.B) {
	// Prepare on-disk and then exercise ReadAt in random-ish blocks
	const total = 16 << 20
	const block = 64 << 10
	tempDir := makeTempDir(&testing.T{})
	payload := bytes.Repeat([]byte{0xAB}, 256<<10)

	s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
	repeatWrite(b, s, total, len(payload), payload)

	buf := make([]byte, block)
	b.ReportAllocs()
	b.SetBytes(block)
	b.ResetTimer()
	pos := int64(0)
	for b.Loop() {
		if pos >= total-int64(block) {
			pos = 0
		}
		if _, err := s.ReadAt(buf, pos); err != nil && err != io.EOF {
			b.Fatal(err)
		}
		pos += int64(block)
	}
	b.StopTimer()
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkParallel_CreateAndWrite_512KB(b *testing.B) {
	// Measures throughput when many goroutines create/write their own instances.
	const total = 512 << 10
	const chunk = 32 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	b.ReportAllocs()
	b.SetBytes(total)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
			repeatWrite(b, s, total, chunk, data)
			if err := s.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Optional: end-to-end hash read to ensure we're not optimizing away I/O in benches.
func BenchmarkRead_Hash_OnDisk(b *testing.B) {
	const total = 32 << 20
	const chunk = 256 << 10
	data := randBytes(chunk)
	tempDir := makeTempDir(&testing.T{})

	s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
	repeatWrite(b, s, total, chunk, data)

	buf := make([]byte, 256<<10)
	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for b.Loop() {
		h := sha256.New()
		if _, err := s.Seek(0, io.SeekStart); err != nil {
			b.Fatal(err)
		}
		for {
			n, err := s.Read(buf)
			if n > 0 {
				_, _ = h.Write(buf[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		_ = h.Sum(nil)
	}
	b.StopTimer()
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
}

// (Optional) micro-benchmark of create+close cost (no writes).
func BenchmarkCreateClose_NoWrite(b *testing.B) {
	tempDir := makeTempDir(&testing.T{})
	b.ReportAllocs()
	for b.Loop() {
		s := NewSpooledTempFile("bench", tempDir, 1<<20, false, DefaultMaxRAMUsageFraction)
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// Guard to avoid unused import complaints in case some paths compile out.
var _ = time.Now
var _ = os.ErrNotExist

func randBytes(n int) []byte {
	// Deterministic for stable benches
	r := rand.New(rand.NewSource(42))
	b := make([]byte, n)
	_, _ = r.Read(b)
	return b
}

func repeatWrite(t *testing.B, w io.Writer, total, chunk int, payload []byte) {
	written := 0
	for written < total {
		to := chunk
		if written+to > total {
			to = total - written
		}
		if _, err := w.Write(payload[:to]); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		written += to
	}
}

func makeTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Ensure absolute
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

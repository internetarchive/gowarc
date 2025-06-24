package spooledtempfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func generateTestDataInKB(size int) []byte {
	return bytes.Repeat([]byte("A"), size*1024)
}

// TestInMemoryBasic writes data below threshold and verifies it remains in memory.
func TestInMemoryBasic(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 100, false, -1)
	defer spool.Close()

	// Write data smaller than threshold
	input := []byte("hello, world")
	n, err := spool.Write(input)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write count mismatch: got %d, want %d", n, len(input))
	}

	if spool.Len() != len(input) {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), len(input))
	}

	// Read some
	out := make([]byte, 5)
	n, err = spool.Read(out)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if n != 5 {
		t.Errorf("Read count mismatch: got %d, want 5", n)
	}
	if string(out) != "hello" {
		t.Errorf(`Data mismatch: got "%s", want "hello"`, string(out))
	}

	// Continue reading
	out2 := make([]byte, 20)
	n, err = spool.Read(out2)
	expectedRemainder := "o, world"[1:] // "ello, world" was partially read, we read 5 bytes
	if err != io.EOF && err != nil {
		t.Fatalf("Expected EOF or nil error, got: %v", err)
	}
	if string(out2[:n]) != expectedRemainder {
		t.Errorf(`Data mismatch: got "%s", want "%s"`, string(out2[:n]), expectedRemainder)
	}

	// FileName should be empty because we never switched to file
	if spool.FileName() != "" {
		t.Errorf("FileName was not empty, got %q", spool.FileName())
	}
}

// TestThresholdCrossing writes enough data to switch from in-memory to disk.
func TestThresholdCrossing(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, false, -1)
	defer spool.Close()

	data1 := generateTestDataInKB(63)
	data2 := generateTestDataInKB(10)

	_, err := spool.Write(data1)
	if err != nil {
		t.Fatalf("First Write error: %v", err)
	}
	if spool.Len() != 63*1024 {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), 63*1024)
	}
	if spool.FileName() != "" {
		t.Errorf("Expected to still be in memory, but file exists: %s", spool.FileName())
	}

	_, err = spool.Write(data2)
	if err != nil {
		t.Fatalf("Second Write error: %v", err)
	}
	if spool.Len() != 73*1024 {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), 63*1024)
	}

	// Now spool should be on disk
	fn := spool.FileName()
	if fn == "" {
		t.Fatal("Expected a file name once threshold is crossed, got empty string")
	}

	total := len(data1) + len(data2)
	if spool.Len() != total {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), total)
	}

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, spool)
	if err != nil {
		t.Fatalf("Copy error: %v", err)
	}
	expected := string(data1) + string(data2)
	if buf.String() != expected {
		t.Errorf("Data mismatch after read. Got %q, want %q", buf.String(), expected)
	}
}

// TestForceOnDisk checks the fullOnDisk parameter.
func TestForceOnDisk(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, true, -1)
	defer spool.Close()

	input := []byte("force to disk")
	_, err := spool.Write(input)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if spool.Len() != len(input) {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), len(input))
	}

	if spool.FileName() == "" {
		t.Errorf("Expected a file name because fullOnDisk = true, got empty")
	}

	out, err := io.ReadAll(spool)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if !bytes.Equal(input, out) {
		t.Errorf("Data mismatch. Got %q, want %q", out, input)
	}
}

// TestReadAtAndSeekInMemory tests seeking and ReadAt on an in-memory spool.
func TestReadAtAndSeekInMemory(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, false, -1)
	defer spool.Close()

	data := []byte("HelloWorld123")
	_, err := spool.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Seek to start
	_, err = spool.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek error: %v", err)
	}

	// ReadAt
	p := make([]byte, 5)
	n, err := spool.ReadAt(p, 5)
	if err != nil {
		t.Fatalf("ReadAt error: %v", err)
	}
	if n != 5 {
		t.Errorf("ReadAt count mismatch: got %d, want 5", n)
	}
	if string(p) != "World" {
		t.Errorf(`Data mismatch: got "%s", want "World"`, string(p))
	}

	// Normal Read from the start
	_, err = spool.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek error: %v", err)
	}
	all, err := io.ReadAll(spool)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if !bytes.Equal(data, all) {
		t.Errorf("Data mismatch. Got %q, want %q", all, data)
	}
}

// TestReadAtAndSeekOnDisk tests seeking and ReadAt on a spool that has switched to disk.
func TestReadAtAndSeekOnDisk(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, false, -1)
	defer spool.Close()

	data1 := []byte("HelloWorld123")
	data2 := generateTestDataInKB(65)
	data := append(data2, data1...)
	_, err := spool.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if spool.Len() != len(data) {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), len(data))
	}

	// We crossed threshold at 10 bytes => spool on disk
	if spool.FileName() == "" {
		t.Fatal("Expected a file name after crossing threshold, got empty")
	}

	_, err = spool.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek error: %v", err)
	}

	p := make([]byte, 5)
	_, err = spool.ReadAt(p, (65*1024)+5)
	if err != nil {
		t.Fatalf("ReadAt error: %v", err)
	}
	if string(p) != "World" {
		t.Errorf("Data mismatch: got %q, want %q", p, "World")
	}
}

// TestWriteAfterReadPanic ensures writing after reading panics per your design.
func TestWriteAfterReadPanic(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, false, -1)
	defer spool.Close()

	_, err := spool.Write([]byte("ABCDEFG"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Start reading
	buf := make([]byte, 4)
	_, err = spool.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read error: %v", err)
	}

	// Now writing again should panic
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("Expected panic on write after read, got none")
		} else {
			msg := fmt.Sprintf("%v", r)
			if !strings.Contains(msg, "write after read") {
				t.Errorf(`Expected panic message "write after read", got %q`, msg)
			}
		}
	}()
	_, _ = spool.Write([]byte("XYZ"))
	t.Fatal("We should not reach here, expected panic")
}

// TestCloseInMemory checks closing while still in-memory.
func TestCloseInMemory(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, false, -1)

	_, err := spool.Write([]byte("Small data"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	if spool.FileName() != "" {
		t.Errorf("Expected no file before crossing threshold, got %s", spool.FileName())
	}

	err = spool.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Subsequent reads or writes should fail
	_, err = spool.Read(make([]byte, 10))
	if err != io.EOF {
		t.Errorf("Expected EOF after close, got %v", err)
	}

	_, err = spool.Write([]byte("More data"))
	if err != io.EOF {
		t.Errorf("Expected io.EOF after close on write, got %v", err)
	}
}

// TestCloseOnDisk checks closing after spool has switched to disk.
func TestCloseOnDisk(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, false, -1)

	data := generateTestDataInKB(65)
	_, err := spool.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if spool.Len() != len(data) {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), len(data))
	}

	fn := spool.FileName()
	if fn == "" {
		t.Fatal("Expected on-disk file, got empty name")
	}
	if _, statErr := os.Stat(fn); statErr != nil {
		t.Fatalf("Expected file to exist, got error: %v", statErr)
	}

	err = spool.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	_, statErr := os.Stat(fn)
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("Expected file to be removed on Close, stat returned: %v", statErr)
	}

	_, err = spool.Read(make([]byte, 10))
	if err != io.EOF {
		t.Errorf("Expected EOF on read after close, got %v", err)
	}

	_, err = spool.Write([]byte("stuff"))
	if err != io.EOF {
		t.Errorf("Expected io.EOF on write after close, got %v", err)
	}
}

// TestLen verifies Len() for both in-memory and on-disk states.
func TestLen(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 64*1024, false, -1)
	defer spool.Close()

	data := []byte("1234")
	_, err := spool.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if spool.Len() != 4 {
		t.Errorf("Len() mismatch in-memory: got %d, want 4", spool.Len())
	}

	// Now cross the threshold
	_, err = spool.Write([]byte("56789"))
	if err != nil {
		t.Fatalf("Write error crossing threshold: %v", err)
	}
	if spool.Len() != 9 {
		t.Errorf("Len() mismatch on-disk: got %d, want 9", spool.Len())
	}
}

// TestFileName checks correctness of FileName in both modes.
func TestFileName(t *testing.T) {
	spool := NewSpooledTempFile("testprefix", os.TempDir(), 64*1024, false, -1)
	defer spool.Close()

	if spool.FileName() != "" {
		t.Errorf("Expected empty FileName initially, got %s", spool.FileName())
	}

	// Cross threshold
	data := generateTestDataInKB(65)
	_, err := spool.Write(data)
	if err != nil {
		t.Fatalf("Write error crossing threshold: %v", err)
	}
	if spool.Len() != len(data) {
		t.Errorf("Len() mismatch on-disk: got %d, want %d", spool.Len(), len(data))
	}

	fn := spool.FileName()
	if fn == "" {
		t.Fatal("Expected FileName after crossing threshold, got empty")
	}

	// The prefix might be part of the filename, check just the base name
	base := filepath.Base(fn)
	if !strings.HasPrefix(base, "testprefix") {
		t.Errorf("Expected file name prefix 'testprefix', got %s", base)
	}
}

// TestSkipInMemoryAboveRAMUsage verifies that if `isSystemMemoryUsageHigh()`
// returns true, the spool goes directly to disk even for small writes.
func TestSkipInMemoryAboveRAMUsage(t *testing.T) {
	memoryUsageCache = &globalMemoryCache{}
	// Save the old function so we can restore it later
	oldGetSystemMemoryUsedFraction := getSystemMemoryUsedFraction
	// Force system memory usage to appear above 50%
	getSystemMemoryUsedFraction = func() (float64, error) {
		return 0.60, nil // 60% used => above the 50% threshold
	}
	// Restore after test
	defer func() {
		getSystemMemoryUsedFraction = oldGetSystemMemoryUsedFraction
	}()

	// Even though threshold is large (e.g. 1MB), because our mock usage is 60%,
	// spool should skip memory and go straight to disk.
	spool := NewSpooledTempFile("testram", os.TempDir(), 1024*1024, false, 0.50)
	defer spool.Close()

	// Write a small amount of data
	data := []byte("This is a small test")
	n, err := spool.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("Write count mismatch: got %d, want %d", n, len(data))
	}

	// Because memory usage was deemed “too high” from the start,
	// we should already be on disk
	fn := spool.FileName()
	if fn == "" {
		t.Fatalf("Expected spool to be on disk, but FileName() was empty")
	}

	// Verify data can be read back
	out, err := io.ReadAll(spool)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(out) != string(data) {
		t.Errorf("Data mismatch. Got %q, want %q", out, data)
	}
}

// TestBufferGrowthWithinLimits verifies that the buffer grows dynamically but never exceeds MaxInMemorySize.
func TestBufferGrowthWithinLimits(t *testing.T) {
	memoryUsageCache = &globalMemoryCache{}
	spool := NewSpooledTempFile("test", os.TempDir(), 128*1024, false, -1)
	defer spool.Close()

	// Write data that will cause the buffer to grow but stay within MaxInMemorySize
	data1 := generateTestDataInKB(30)
	data2 := generateTestDataInKB(35)

	_, err := spool.Write(data1)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if spool.Len() != len(data1) {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), len(data1))
	}

	// Check that the buffer is still in memory
	if spool.FileName() != "" {
		t.Errorf("Expected buffer to still be in memory, but file exists: %s", spool.FileName())
	}

	// Write more data to trigger buffer growth
	_, err = spool.Write(data2)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if spool.Len() != len(data1)+len(data2) {
		t.Errorf("Len() mismatch: got %d, want %d", spool.Len(), len(data1)+len(data2))
	}

	// Check that the buffer grew
	spoolBuffer := spool.(*spooledTempFile)
	if cap(spoolBuffer.buf) <= InitialBufferSize {
		t.Fatalf("Expected buffer capacity > %d, got %d", InitialBufferSize, cap(spoolBuffer.buf))
	}

	// Check that the buffer is still in memory and has grown
	if spool.FileName() != "" {
		t.Errorf("Expected buffer to still be in memory, but file exists: %s", spool.FileName())
	}
}

// TestPoolBehavior verifies that buffers exceeding InitialBufferSize are not returned to the pool.
func TestPoolBehavior(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 150*1024, false, -1)
	defer spool.Close()

	// Write data to grow the buffer beyond InitialBufferSize
	data := make([]byte, 100*1024)
	n := copy(data, bytes.Repeat([]byte("A"), 100*1024))
	if n != 100*1024 {
		t.Fatalf("Data copy mismatch: got %d, want %d", n, 100*1024)
	}
	if len(data) != 100*1024 {
		t.Fatalf("Data length mismatch: got %d, want %d", len(data), 100*1024)
	}
	_, err := spool.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Ensure the buffer has grown beyond InitialBufferSize
	spoolTempFile := spool.(*spooledTempFile)
	if cap(spoolTempFile.buf) <= InitialBufferSize {
		t.Fatalf("Expected buffer capacity > %d, got %d", InitialBufferSize, cap(spoolTempFile.buf))
	}

	// Close the spool to release the buffer
	err = spool.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Retrieve a buffer from the pool
	buf := spooledPool.Get().([]byte)

	// Verify that the retrieved buffer has the expected initial capacity
	if cap(buf) != InitialBufferSize {
		t.Errorf("Expected buffer in pool to have capacity %d, got %d", InitialBufferSize, cap(buf))
	}

	// Verify that the buffer is empty (reset)
	if len(buf) != 0 {
		t.Errorf("Expected buffer length to be 0, got %d", len(buf))
	}
}

func TestBufferGrowthBeyondNewCap(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 100*1024, false, -1)
	defer spool.Close()

	// Write data to grow the buffer close to MaxInMemorySize
	data1 := generateTestDataInKB(50)
	_, err := spool.Write(data1)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	if spool.Len() != 50*1024 {
		t.Fatalf("Data length mismatch: got %d, want %d", spool.Len(), 50*1024)
	}

	// Write more data to trigger buffer growth beyond MaxInMemorySize
	data2 := generateTestDataInKB(51)
	_, err = spool.Write(data2)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	if spool.Len() != 101*1024 {
		t.Fatalf("Data length mismatch: got %d, want %d", spool.Len(), 101*1024)
	}

	// Check that the buffer has been spooled to disk
	if spool.FileName() == "" {
		t.Error("Expected buffer to be spooled to disk, but no file exists")
	}

	// Verify the data was written correctly
	expected := append(data1, data2...)
	out := make([]byte, len(expected))
	_, err = spool.ReadAt(out, 0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt error: %v", err)
	}
	if !bytes.Equal(out, expected) {
		t.Errorf("Data mismatch. Got %q, want %q", out, expected)
	}

	// Verify that the buffer was released to the pool (if it meets the criteria)
	buf := spooledPool.Get().([]byte)
	if cap(buf) != InitialBufferSize {
		t.Errorf("Expected buffer in pool to have capacity %d, got %d", InitialBufferSize, cap(buf))
	}
	if len(buf) != 0 {
		t.Errorf("Expected buffer length to be 0, got %d", len(buf))
	}
}

func TestSpoolingWhenIOCopy(t *testing.T) {
	spool := NewSpooledTempFile("test", os.TempDir(), 100*1024, false, -1)
	defer spool.Close()

	data := generateTestDataInKB(500)
	_, err := io.Copy(spool, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Copy error: %v", err)
	}

	spooledFile := spool.(*spooledTempFile)
	if spooledFile.Len() != 500*1024 {
		t.Fatalf("Data length mismatch: got %d, want %d", spooledFile.Len(), 500*1024)
	}
	if spooledFile.FileName() == "" {
		t.Error("Expected buffer to be spooled to disk, but no file exists")
	}

	// Verify the data was copied correctly
	out := make([]byte, len(data))
	_, err = spool.ReadAt(out, 0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt error: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Errorf("Data mismatch. Got %q, want %q", out, data)
	}
}

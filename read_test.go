package warc

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func testFileHash(t *testing.T, path string) {
	t.Logf("checking 'WARC-Block-Digest' on %q", path)

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %q: %v", path, err)
	}
	defer file.Close()

	reader, err := NewReader(file)
	if err != nil {
		t.Fatalf("warc.NewReader failed for %q: %v", path, err)
	}

	for {
		record, size, err := reader.ReadRecord()
		if size == 0 {
			break
		}
		if err != nil {
			t.Fatalf("failed to read all record content: %v", err)
			break
		}

		hash := fmt.Sprintf("sha1:%s", GetSHA1(record.Content))
		if hash != record.Header["WARC-Block-Digest"] {
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			t.Fatalf("expected %s, got %s", record.Header.Get("WARC-Block-Digest"), hash)
		}
		err = record.Content.Close()
		if err != nil {
			t.Fatalf("failed to close record content: %v", err)
		}
	}
}

func testFileScan(t *testing.T, path string) {
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %q: %v", path, err)
	}
	defer file.Close()

	reader, err := NewReader(file)
	if err != nil {
		t.Fatalf("warc.NewReader failed for %q: %v", path, err)
	}

	total := 0
	for {
		_, size, err := reader.ReadRecord()
		if size == 0 {
			break
		}
		if err != nil {
			t.Fatalf("failed to read all record content: %v", err)
			break
		}
		total++
	}

	if total != 3 {
		t.Fatalf("expected 3 records, got %v", total)
	}
}

func testFileSingleHashCheck(t *testing.T, path string, hash string, expectedContentLength []string, expectedTotal int, expectedURL string) int {
	// The below function validates the Block-Digest per record while the function we are in checks for a specific Payload-Digest in records :)
	testFileHash(t, path)

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %q: %v", path, err)
	}
	defer file.Close()

	t.Logf("checking 'WARC-Payload-Digest', 'Content-Length', and 'WARC-Target-URI' on %q", path)

	reader, err := NewReader(file)
	if err != nil {
		t.Fatalf("warc.NewReader failed for %q: %v", path, err)
	}

	totalRead := 0

	for {
		record, size, err := reader.ReadRecord()
		if size == 0 {
			if expectedTotal == -1 {
				// This is expected for multiple file WARCs as we need to count the total count outside of this function.
				return totalRead
			}

			if totalRead == expectedTotal {
				// We've read the expected amount and reached the end of the WARC file. Time to break out.
				break
			} else {
				t.Fatalf("unexpected number of records read, read: %d but expected: %d", totalRead, expectedTotal)
				return -1
			}
		}

		if err != nil {
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			t.Fatalf("warc.ReadRecord failed: %v", err)
			break
		}

		if record.Header.Get("WARC-Type") != "response" && record.Header.Get("WARC-Type") != "revisit" {
			// We're not currently interesting in anything but response and revisit records at the moment.
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			continue
		}

		if record.Header.Get("WARC-Payload-Digest") != hash {
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			t.Fatalf("WARC-Payload-Digest doesn't match intended result %s != %s", record.Header.Get("WARC-Payload-Digest"), hash)
		}

		// We can't check the validity of a body that does not exist (revisit records)
		if record.Header.Get("WARC-Type") == "response" {
			_, err = record.Content.Seek(0, 0)
			if err != nil {
				t.Fatal("failed to seek record content", "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
			}

			resp, err := http.ReadResponse(bufio.NewReader(record.Content), nil)
			if err != nil {
				t.Fatal("failed to seek record content", "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
			}
			defer resp.Body.Close()
			defer record.Content.Seek(0, 0)

			calculatedRecordHash := fmt.Sprintf("sha1:%s", GetSHA1(resp.Body))
			if record.Header.Get("WARC-Payload-Digest") != calculatedRecordHash {
				err = record.Content.Close()
				if err != nil {
					t.Fatalf("failed to close record content: %v", err)
				}
				t.Fatalf("calculated WARC-Payload-Digest doesn't match intended result %s != %s", record.Header.Get("WARC-Payload-Digest"), calculatedRecordHash)
			}
		}

		badContentLength := false
		for i := 0; i < len(expectedContentLength); i++ {
			if record.Header.Get("Content-Length") != expectedContentLength[i] {
				badContentLength = true
			} else {
				badContentLength = false
				break
			}
		}

		if badContentLength {
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			t.Fatalf("Content-Length doesn't match intended result %s != %s", record.Header.Get("Content-Length"), expectedContentLength)
		}

		if record.Header.Get("WARC-Target-URI") != expectedURL {
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			t.Fatalf("WARC-Target-URI doesn't match intended result %s != %s", record.Header.Get("WARC-Target-URI"), expectedURL)
		}

		err = record.Content.Close()
		if err != nil {
			t.Fatalf("failed to close record content: %v", err)
		}
		totalRead++
	}
	return -1
}

func testFileRevisitVailidity(t *testing.T, path string, originalTime string, originalDigest string, shouldBeEmpty bool) {
	var revisitRecordsFound = false
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %q: %v", path, err)
	}
	defer file.Close()

	t.Logf("checking 'WARC-Refers-To-Date' and 'WARC-Payload-Digest' for revisits on %q", path)

	reader, err := NewReader(file)
	if err != nil {
		t.Fatalf("warc.NewReader failed for %q: %v", path, err)
	}

	for {
		record, size, err := reader.ReadRecord()
		if size == 0 {
			if revisitRecordsFound {
				return
			}
			if shouldBeEmpty {
				t.Logf("No revisit records found. That's expected for this test.")
				break
			}

			t.Fatalf("No revisit records found.")
			break
		}

		if err != nil {
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			t.Fatalf("warc.ReadRecord failed: %v", err)
			break
		}

		if record.Header.Get("WARC-Type") != "response" && record.Header.Get("WARC-Type") != "revisit" {
			// We're not currently interesting in anything but response and revisit records at the moment.
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			continue
		}

		if record.Header.Get("WARC-Type") == "response" {
			originalDigest = record.Header.Get("WARC-Payload-Digest")
			originalTime = record.Header.Get("WARC-Date")
			err = record.Content.Close()
			if err != nil {
				t.Fatalf("failed to close record content: %v", err)
			}
			continue
		}

		if record.Header.Get("WARC-Type") == "revisit" {
			revisitRecordsFound = true
			if record.Header.Get("WARC-Payload-Digest") == originalDigest && record.Header.Get("WARC-Refers-To-Date") == originalTime {
				// Check that WARC-Refers-To-Date is a valid ISO8601 timestamp
				refersToDate := record.Header.Get("WARC-Refers-To-Date")
				if refersToDate != "" {
					_, err := time.Parse(time.RFC3339, refersToDate)
					if err != nil {
						t.Fatalf("WARC-Refers-To-Date is not a valid ISO8601 timestamp: %s", refersToDate)
					}
				}
				err = record.Content.Close()
				if err != nil {
					t.Fatalf("failed to close record content: %v", err)
				}
				continue
			} else {
				err = record.Content.Close()
				if err != nil {
					t.Fatalf("failed to close record content: %v", err)
				}
				t.Fatalf("Revisit digest or date does not match doesn't match intended result %s != %s (or %s != %s)", record.Header.Get("WARC-Payload-Digest"), originalDigest, record.Header.Get("WARC-Refers-To-Date"), originalTime)
			}
		}

	}
}

func testFileEarlyEOF(t *testing.T, path string) {
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %q: %v", path, err)
	}
	reader, err := NewReader(file)
	if err != nil {
		t.Fatalf("warc.NewReader failed for %q: %v", path, err)
	}
	// read the file into memory
	data, err := io.ReadAll(reader.bufReader)
	if err != nil {
		t.Fatalf("failed to read %q: %v", path, err)
	}
	// delete the last two bytes (\r\n)
	if data[len(data)-2] != '\r' || data[len(data)-1] != '\n' {
		t.Fatalf("expected \\r\\n, got %q", data[len(data)-2:])
	}
	data = data[:len(data)-2]
	// new reader
	reader, err = NewReader(io.NopCloser(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("warc.NewReader failed for %q: %v", path, err)
	}
	// read the records
	for {
		_, size, err := reader.ReadRecord()
		if size == 0 {
			break
		}
		if err != nil {
			if strings.Contains(err.Error(), "early EOF record boundary") {
				return // ok
			} else {
				t.Fatalf("expected early EOF record boundary, got %v", err)
			}
		}
	}
	t.Fatalf("expected early EOF record boundary, got none")
}

func TestReader(t *testing.T) {
	var paths = []string{
		"testdata/test.warc.gz",
	}
	for _, path := range paths {
		testFileHash(t, path)
		testFileScan(t, path)
		// testFileEarlyEOF(t, path)
	}
}

func TestReaderNoContentOpt(t *testing.T) {
	var paths = []string{
		"testdata/test.warc.gz",
	}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("failed to open %q: %v", path, err)
		}
		defer file.Close()

		reader, err := NewReader(file)
		if err != nil {
			t.Fatalf("warc.NewReader failed for %q: %v", path, err)
		}

		for {
			record, size, err := reader.ReadRecord(ReadOptsNoContentOutput)
			if size == 0 {
				break
			}
			if err != nil {
				t.Fatalf("failed to read all record content: %v", err)
				break
			}

			if record.Content.Len() > 0 {
				t.Fatal("expected no content, got content")
			}
		}
	}
}

func TestReaderSize(t *testing.T) {
	paths := []string{
		"testdata/test.warc.gz",
	}

	for _, path := range paths {
		expFile, err := os.Open(path)
		if err != nil {
			t.Fatalf("failed to open %q for expected size: %v", path, err)
		}
		expectedSize, err := io.Copy(io.Discard, expFile)
		expFile.Close()
		if err != nil {
			t.Fatalf("failed to read decompressed content for %q: %v", path, err)
		}

		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("failed to open %q: %v", path, err)
		}
		defer file.Close()

		reader, err := NewReader(file)
		if err != nil {
			t.Fatalf("warc.NewReader failed for %q: %v", path, err)
		}

		var totalSize int64
		for {
			_, size, err := reader.ReadRecord()
			if err != nil {
				t.Fatalf("failed while reading record content: %v", err)
			}
			if size == 0 { // clean EOF
				break
			}
			totalSize += size
		}

		if totalSize != expectedSize {
			t.Fatalf("expected total size to be %d, got %d", expectedSize, totalSize)
		}
	}
}

func BenchmarkBasicRead(b *testing.B) {
	// default test warc location
	path := "testdata/test.warc.gz"

	for n := 0; n < b.N; n++ {
		b.Logf("checking 'WARC-Block-Digest' on %q", path)

		file, err := os.Open(path)
		if err != nil {
			b.Fatalf("failed to open %q: %v", path, err)
		}
		defer file.Close()

		reader, err := NewReader(file)
		if err != nil {
			b.Fatalf("warc.NewReader failed for %q: %v", path, err)
		}

		for {
			record, size, err := reader.ReadRecord()
			if size == 0 {
				break
			}
			if err != nil {
				b.Fatalf("failed to read all record content: %v", err)
				break
			}

			hash := fmt.Sprintf("sha1:%s", GetSHA1(record.Content))
			if hash != record.Header["WARC-Block-Digest"] {
				err = record.Content.Close()
				if err != nil {
					b.Fatalf("failed to close record content: %v", err)
				}
				b.Fatalf("expected %s, got %s", record.Header.Get("WARC-Block-Digest"), hash)
			}
			err = record.Content.Close()
			if err != nil {
				b.Fatalf("failed to close record content: %v", err)
			}
		}
	}
}

type readerFn func(*bufio.Reader, []byte) ([]byte, int64, error)

var (
	sinkN   int64
	sinkErr error
)

// ---------------- Synthetic reader ----------------

type synthReader struct {
	total      int64
	pos        int64
	delim      []byte
	delimStart int64 // -1 means no delimiter
}

func newSynthReader(total int64, delim []byte, placement string) *synthReader {
	s := &synthReader{
		total:      total,
		delim:      delim,
		delimStart: -1,
	}
	if len(delim) == 0 {
		return s
	}
	switch placement {
	case "head":
		start := int64(8)
		if start+int64(len(delim)) <= total {
			s.delimStart = start
		}
	case "mid":
		start := total/2 - int64(len(delim))/2
		if start < 0 {
			start = 0
		}
		if start+int64(len(delim)) > total {
			start = total - int64(len(delim))
		}
		s.delimStart = start
	case "end":
		if total >= int64(len(delim)) {
			s.delimStart = total - int64(len(delim))
		}
	case "none":
		// leave at -1
	default:
		if total >= int64(len(delim)) {
			s.delimStart = total - int64(len(delim))
		}
	}
	return s
}

func (s *synthReader) Read(p []byte) (int, error) {
	if s.pos >= s.total {
		return 0, io.EOF
	}
	// Before delimiter
	if s.delimStart >= 0 && s.pos < s.delimStart {
		max := min64(int64(len(p)), s.delimStart-s.pos)
		fillA(p[:max])
		s.pos += max
		if s.pos >= s.total {
			return int(max), io.EOF
		}
		return int(max), nil
	}
	// Delimiter itself
	if s.delimStart >= 0 && s.pos >= s.delimStart && s.pos < s.delimStart+int64(len(s.delim)) {
		off := s.pos - s.delimStart
		max := min64(int64(len(p)), int64(len(s.delim))-off)
		copy(p[:max], s.delim[off:])
		s.pos += max
		if s.pos >= s.total {
			return int(max), io.EOF
		}
		return int(max), nil
	}
	// After delimiter or no delimiter
	max := min64(int64(len(p)), s.total-s.pos)
	fillA(p[:max])
	s.pos += max
	if s.pos >= s.total {
		return int(max), io.EOF
	}
	return int(max), nil
}

func fillA(b []byte) {
	for i := range b {
		b[i] = 'a'
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// ---------------- Bench helpers ----------------

func makeStream(totalSize int64, delim []byte, placement string) (io.Reader, int64) {
	var wantN int64
	switch placement {
	case "head":
		wantN = 8 + int64(len(delim))
	case "mid":
		pos := totalSize/2 - int64(len(delim))/2
		if pos < 0 {
			pos = 0
		}
		if pos+int64(len(delim)) > totalSize {
			pos = totalSize - int64(len(delim))
		}
		wantN = pos + int64(len(delim))
	case "end":
		wantN = totalSize
	case "none":
		wantN = totalSize
	default:
		wantN = totalSize
	}
	return newSynthReader(totalSize, delim, placement), wantN
}

func benchReadUntil(b *testing.B, name string, fn readerFn) {
	delimCases := [][]byte{
		[]byte("\r\n"),
	}

	sizes := []int64{
		1 << 10,   // 1 KiB
		16 << 10,  // 16 KiB
		64 << 10,  // 64 KiB
		256 << 10, // 256 KiB
		1 << 20,   // 1 MiB
		4 << 20,   // 4 MiB
		16 << 20,  // 16 MiB
		64 << 20,  // 64 MiB
		256 << 20, // 256 MiB
		1 << 30,   // 1 GiB
	}

	basePlacements := []string{"head", "mid", "end", "none"}

	for _, d := range delimCases {
		for _, sz := range sizes {
			var placements []string
			for _, p := range basePlacements {
				if ci := os.Getenv("CI"); ci != "" && sz >= 64<<20 {
					// skip large sizes on CI to avoid long test times
					continue
				}
				placements = append(placements, p)
			}

			for _, place := range placements {
				_, wantN := makeStream(sz, d, place)
				caseName := fmt.Sprintf("%s/delim=%s/size=%s/place=%s", name, prettyDelim(d), human(sz), place)

				b.Run(caseName, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(wantN)

					// correctness check
					rdr := bufio.NewReaderSize(newSynthReader(sz, d, place), 64<<10)
					_, n, err := fn(rdr, d)
					if place == "none" {
						if err != io.EOF {
							b.Fatalf("expected EOF (none), got %v", err)
						}
					} else if err != nil {
						b.Fatalf("unexpected err: %v", err)
					}
					if n != wantN {
						b.Fatalf("n mismatch: got %d want %d", n, wantN)
					}

					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						rdr := bufio.NewReaderSize(newSynthReader(sz, d, place), 64<<10)
						_, n, err = fn(rdr, d)
						sinkN, sinkErr = n, err
					}
				})
			}
		}
	}
}

func prettyDelim(d []byte) string {
	switch string(d) {
	case "\r\n":
		return `\r\n`
	default:
		return string(d)
	}
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return "1GiB"
	case n >= 256<<20:
		return "256MiB"
	case n >= 64<<20:
		return "64MiB"
	case n >= 16<<20:
		return "16MiB"
	case n >= 4<<20:
		return "4MiB"
	case n >= 1<<20:
		return "1MiB"
	case n >= 256<<10:
		return "256KiB"
	case n >= 64<<10:
		return "64KiB"
	case n >= 16<<10:
		return "16KiB"
	case n >= 1<<10:
		return "1KiB"
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// ---------------- Entry points ----------------

func BenchmarkReadUntilDelim_Bytewise(b *testing.B) {
	benchReadUntil(b, "bytewise", readUntilDelim)
}

func BenchmarkReadUntilDelim_Chunked(b *testing.B) {
	benchReadUntil(b, "chunked", readUntilDelimChunked)
}

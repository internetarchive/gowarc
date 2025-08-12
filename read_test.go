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

	"github.com/klauspost/compress/gzip"
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
		testFileEarlyEOF(t, path)
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
		gr, err := gzip.NewReader(expFile)
		if err != nil {
			expFile.Close()
			t.Fatalf("failed to create gzip reader for %q: %v", path, err)
		}
		expectedSize, err := io.Copy(io.Discard, gr)
		gr.Close()
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

// ---------- Bench helpers ----------

type readerFn func(*bufio.Reader, []byte) ([]byte, int64, error)

var (
	sinkLine []byte
	sinkN    int64
	sinkErr  error
)

// replace makePayload with this version:
func makePayload(totalSize int, delim []byte, placement string) (payload []byte, wantN int) {
	if totalSize < len(delim) {
		totalSize = len(delim)
	}
	buf := bytes.Repeat([]byte{'a'}, totalSize)

	switch placement {
	case "end":
		pos := totalSize - len(delim) // where delim starts
		copy(buf[pos:], delim)
		return buf, pos + len(delim) // bytes consumed incl. delim

	case "mid":
		pos := totalSize/2 - len(delim)/2 // center-ish, clamped below
		if pos < 0 {
			pos = 0
		}
		if pos+len(delim) > totalSize {
			pos = totalSize - len(delim)
		}
		copy(buf[pos:], delim)
		return buf, pos + len(delim) // bytes consumed incl. delim

	case "none":
		// No delimiter found: we consume everything and return io.EOF.
		return buf, totalSize

	default:
		// default to "end"
		pos := totalSize - len(delim)
		copy(buf[pos:], delim)
		return buf, pos + len(delim)
	}
}

func benchReadUntil(b *testing.B, name string, fn readerFn) {
	delimCases := [][]byte{
		[]byte("\r\n"), // CRLF
	}
	sizes := []int{1 << 10, 1 << 16, 1 << 20} // 1 KiB, 64 KiB, 1 MiB
	placements := []string{"end", "mid", "none"}

	for _, d := range delimCases {
		for _, sz := range sizes {
			for _, place := range placements {
				payload, wantN := makePayload(sz, d, place)
				caseName := name + "/delim=" + prettyDelim(d) + "/size=" + human(sz) + "/place=" + place

				b.Run(caseName, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(wantN))
					// quick correctness check only once to avoid heavy overhead
					r := bufio.NewReader(bytes.NewReader(payload))
					line, n, err := fn(r, d)
					if place == "none" {
						if err != io.EOF {
							b.Fatalf("expected EOF (none), got %v", err)
						}
					} else if err != nil {
						b.Fatalf("unexpected err: %v", err)
					}
					if n != int64(wantN) {
						b.Fatalf("n mismatch: got %d want %d", n, wantN)
					}
					_ = line // ignore length validation to keep overhead minimal

					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						r := bufio.NewReader(bytes.NewReader(payload))
						line, n, err = fn(r, d)
						// store to sinks to avoid dead-code elimination
						sinkLine, sinkN, sinkErr = line, n, err
					}
				})
			}
		}
	}
}

func prettyDelim(d []byte) string {
	switch string(d) {
	case "\r\n":
		return "\\r\\n"
	default:
		return string(d)
	}
}

func human(n int) string {
	switch {
	case n >= 1<<20:
		return "1MiB"
	case n >= 1<<16:
		return "64KiB"
	case n >= 1<<10:
		return "1KiB"
	default:
		return "bytes"
	}
}

// ---------- Bench entry points ----------

func BenchmarkReadUntilDelim_Bytewise(b *testing.B) {
	benchReadUntil(b, "bytewise", readUntilDelim)
}

func BenchmarkReadUntilDelim_Chunked(b *testing.B) {
	benchReadUntil(b, "chunked", readUntilDelimChunked)
}

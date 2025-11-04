package warc

import (
	"io"
	"os"
	"strconv"
	"testing"
)

// TestSmokeWARCFormatRegression validates that the WARC format remains consistent
// by checking a frozen reference file (testdata/test.warc.gz) against known-good values.
//
// This test serves as a regression detector for WARC format changes, complementing the
// dynamic tests in client_test.go. It addresses the concern that byte-level format
// changes should be explicitly validated against a known-good snapshot.
//
// If this test fails, it indicates that either:
// 1. The WARC writing logic has changed in a way that affects the format
// 2. The reference file has been modified
// 3. There's a bug in the record serialization
func TestSmokeWARCFormatRegression(t *testing.T) {
	const testFile = "testdata/warcs/test.warc.gz"

	// Expected file-level metrics
	const expectedFileSize = 22350 // bytes (compressed)
	const expectedTotalRecords = 3
	const expectedTotalContentLength = 22083 // sum of all Content-Length values

	// Expected record-level metrics
	// These values were extracted from a known-good WARC file and serve as
	// a snapshot of correct format behavior.
	expectedRecords := []struct {
		warcType        string
		contentLength   int64
		blockDigest     string
		payloadDigest   string // only for response records
		targetURI       string // only for response records
	}{
		{
			warcType:      "warcinfo",
			contentLength: 143,
			blockDigest:   "sha1:IYWIATZSPEOF7U5W7VGGJOSQTIWUDXQ6",
		},
		{
			warcType:      "request",
			contentLength: 110,
			blockDigest:   "sha1:JNDMG56JVTVVOQSDQRD25XWTGMRQAQDB",
		},
		{
			warcType:      "response",
			contentLength: 21830,
			blockDigest:   "sha1:LCKC4TTRSBWYHGYT5P22ON4DWY65WHDZ",
			targetURI:     "https://apis.google.com/js/platform.js",
		},
	}

	// Validate file size
	stat, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat test file: %v", err)
	}
	if stat.Size() != expectedFileSize {
		t.Errorf("file size mismatch: expected %d bytes, got %d bytes", expectedFileSize, stat.Size())
	}

	// Open and read WARC file
	file, err := os.Open(testFile)
	if err != nil {
		t.Fatalf("failed to open test file: %v", err)
	}
	defer file.Close()

	reader, err := NewReader(file)
	if err != nil {
		t.Fatalf("failed to create WARC reader: %v", err)
	}

	var recordCount int
	var totalContentLength int64

	// Read and validate each record
	for recordCount < expectedTotalRecords {
		record, err := reader.ReadRecord()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("failed to read record %d: %v", recordCount+1, err)
		}
		if record == nil {
			break
		}

		expected := expectedRecords[recordCount]

		// Validate WARC-Type
		warcType := record.Header.Get("WARC-Type")
		if warcType != expected.warcType {
			t.Errorf("record %d: WARC-Type mismatch: expected %q, got %q",
				recordCount+1, expected.warcType, warcType)
		}

		// Validate Content-Length
		contentLengthStr := record.Header.Get("Content-Length")
		contentLength, err := strconv.ParseInt(contentLengthStr, 10, 64)
		if err != nil {
			t.Errorf("record %d: failed to parse Content-Length %q: %v",
				recordCount+1, contentLengthStr, err)
		} else {
			if contentLength != expected.contentLength {
				t.Errorf("record %d: Content-Length mismatch: expected %d, got %d",
					recordCount+1, expected.contentLength, contentLength)
			}
			totalContentLength += contentLength
		}

		// Validate WARC-Block-Digest
		blockDigest := record.Header.Get("WARC-Block-Digest")
		if blockDigest != expected.blockDigest {
			t.Errorf("record %d: WARC-Block-Digest mismatch: expected %q, got %q",
				recordCount+1, expected.blockDigest, blockDigest)
		}

		// Validate response-specific fields
		if warcType == "response" {
			if expected.targetURI != "" {
				targetURI := record.Header.Get("WARC-Target-URI")
				if targetURI != expected.targetURI {
					t.Errorf("record %d: WARC-Target-URI mismatch: expected %q, got %q",
						recordCount+1, expected.targetURI, targetURI)
				}
			}
		}

		// Close record content
		if err := record.Content.Close(); err != nil {
			t.Errorf("record %d: failed to close content: %v", recordCount+1, err)
		}

		recordCount++
	}

	// Validate total record count
	if recordCount != expectedTotalRecords {
		t.Errorf("total record count mismatch: expected %d, got %d",
			expectedTotalRecords, recordCount)
	}

	// Validate total content length
	if totalContentLength != expectedTotalContentLength {
		t.Errorf("total content length mismatch: expected %d bytes, got %d bytes",
			expectedTotalContentLength, totalContentLength)
	}
}

package warc

import (
    "os"
	"strings"
	"sync/atomic"
    "testing"
)

func TestGenerateWarcFileName(t *testing.T) {
	serial := &atomic.Uint64{}
    serial.Store(5)
	fname1 := generateWarcFileName("youtube", "GZIP", serial)
	if !strings.HasSuffix(fname1, ".warc.gz.open") {
		t.Errorf("expected filename suffix: .warc.gz.open, got: %v", fname1)
	}
	if !strings.HasPrefix(fname1, "youtube-") {
		t.Errorf("expected filename prefix: youtube-, got: %v", fname1)
	}
	if !strings.Contains(fname1, "-00006-") {
		t.Errorf("expected filename containing serial+1: -00006-, got: %v", fname1)
	}
}

func TestIsFileSizeExceeded(t *testing.T) {
    tests := []struct {
        name     string
        sizeMB   int64     // size in megabytes
        maxSize  float64   // max allowed size
        expected bool
    }{
        {"Below limit", 1, 2.0, false},
        {"Above limit", 3, 2.0, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tmpFile, err := os.CreateTemp("", "testfile")
            if err != nil {
                t.Fatal(err)
            }
            defer os.Remove(tmpFile.Name())
            defer tmpFile.Close()

            // Truncate file to desired size
            if err := tmpFile.Truncate(tt.sizeMB * 1024 * 1024); err != nil {
                t.Fatal(err)
            }

            result := isFileSizeExceeded(tmpFile, tt.maxSize)
            if result != tt.expected {
                t.Errorf("expected %v, got %v", tt.expected, result)
            }
        })
    }
}


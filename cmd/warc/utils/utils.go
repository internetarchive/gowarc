package utils

import (
    "bytes"
    "compress/gzip"
    "log/slog"
    "os"
    "strings"

    warc "github.com/internetarchive/gowarc"
    "github.com/klauspost/compress/zstd"
    "github.com/spf13/cobra"
)


// GetThreadsFlag extracts the threads flag value from a cobra command
// Cobra already validates that it's a valid integer, but we still check for errors
func GetThreadsFlag(cmd *cobra.Command) int {
	threads, err := cmd.Flags().GetInt("threads")
	if err != nil {
		// This should never happen if the flag is properly defined, so it's a programming error
		slog.Error("failed to get threads flag - this indicates a programming error", "err", err.Error())
		os.Exit(1)
	}
	return threads
}

// OpenWARCFile opens a WARC file (supports: gzip, zstd, uncompressed)
func OpenWARCFile(filepath string) (*warc.Reader, *os.File, error) {
    f, err := os.Open(filepath)
    if err != nil {
        slog.Error("unable to open file", "err", err.Error(), "file", filepath)
        return nil, nil, err
    }

    // Read magic bytes
    magic := make([]byte, 4)
    n, err := f.Read(magic)
    if err != nil || n < 4 {
        slog.Error("failed to read magic bytes", "file", filepath)
        f.Close()
        return nil, nil, err
    }
    f.Seek(0, 0)

    // GZIP magic bytes: 1F 8B
    if magic[0] == 0x1F && magic[1] == 0x8B {
        gz, err := gzip.NewReader(f)
        if err != nil {
            slog.Error("gzip reader failed", "err", err.Error(), "file", filepath)
            f.Close()
            return nil, nil, err
        }
        r, err := warc.NewReader(gz)
        return r, f, err
    }

    // ZSTD magic bytes: 28 B5 2F FD
    if bytes.Equal(magic, []byte{0x28, 0xB5, 0x2F, 0xFD}) {
        dec, err := zstd.NewReader(f)
        if err != nil {
            slog.Error("zstd reader failed", "err", err.Error(), "file", filepath)
            f.Close()
            return nil, nil, err
        }
        r, err := warc.NewReader(dec)
        return r, f, err
    }

    // UNCOMPRESSED fallback
    reader, err := warc.NewReader(f)
    if err != nil {
        slog.Error("warc.NewReader failed", "err", err.Error(), "file", filepath)
        f.Close()
        return nil, nil, err
    }

    return reader, f, nil
}


// ShouldSkipRecord determines if a WARC record should be skipped during processing
func ShouldSkipRecord(record *warc.Record) bool {
	// Skip revisit records
	if record.Header.Get("WARC-Type") == "revisit" {
		slog.Debug("skipping revisit record", "recordID", record.Header.Get("WARC-Record-ID"))
		return true
	}

	// Only process Content-Type: application/http; msgtype=response
	if !strings.Contains(record.Header.Get("Content-Type"), "msgtype=response") {
		slog.Debug("skipping record with Content-Type", "contentType", record.Header.Get("Content-Type"), "recordID", record.Header.Get("WARC-Record-ID"))
		return true
	}

	return false
}

// Constants for file operations
const (
	MaxFilenameLength         = 255
	MaxFilenameWithHashLength = 247
	DefaultDirPermissions     = 0755
	DefaultFilePermissions    = 0644
)

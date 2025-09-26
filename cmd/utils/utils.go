package utils

import (
	"log/slog"
	"os"
	"strings"

	warc "github.com/internetarchive/gowarc"
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

// OpenWARCFile opens a WARC file and returns a reader and file handle
func OpenWARCFile(filepath string) (*warc.Reader, *os.File, error) {
	f, err := os.Open(filepath)
	if err != nil {
		slog.Error("unable to open file", "err", err.Error(), "file", filepath)
		return nil, nil, err
	}

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
	// Only process Content-Type: application/http; msgtype=response
	if !strings.Contains(record.Header.Get("Content-Type"), "msgtype=response") {
		slog.Debug("skipping record with Content-Type", "contentType", record.Header.Get("Content-Type"), "recordID", record.Header.Get("WARC-Record-ID"))
		return true
	}

	// Skip revisit records
	if record.Header.Get("WARC-Type") == "revisit" {
		slog.Debug("skipping revisit record", "recordID", record.Header.Get("WARC-Record-ID"))
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

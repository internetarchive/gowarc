package concat

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// Command represents the concat command
var Command = &cobra.Command{
	Use:   "concat [flags] file1.warc.gz file2.warc.gz ...",
	Short: "Concatenate multiple WARC files into one and delete the originals",
	Long: `Concatenate multiple WARC files into a single output WARC file.

WARC files (including gzip-compressed ones) are simply concatenated at the
byte level.

After a successful concatenation, the original input files are deleted unless
--no-delete is specified.`,
	Args: cobra.MinimumNArgs(2),
	Run:  concat,
}

func init() {
	Command.Flags().StringP("output", "o", "", "Output WARC file path (required)")
	Command.Flags().Bool("no-delete", false, "Keep original files after concatenation")
	_ = Command.MarkFlagRequired("output")
}

func concat(cmd *cobra.Command, files []string) {
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		slog.Error("failed to get output flag", "error", err)
		return
	}

	noDelete, err := cmd.Flags().GetBool("no-delete")
	if err != nil {
		slog.Error("failed to get no-delete flag", "error", err)
		return
	}

	startTime := time.Now()

	// Verify all input files exist, check for dictionary-compressed zstd, and collect sizes
	var totalInputBytes int64
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			slog.Error("input file not accessible", "file", f, "error", err)
			return
		}
		totalInputBytes += info.Size()

		if hasDictionaryFrame(f) {
			slog.Error("file uses a zstd dictionary frame and cannot be safely concatenated at the byte level",
				"file", f,
			)
			return
		}
	}

	// Resolve absolute output path for clear logging
	absOutput, err := filepath.Abs(output)
	if err != nil {
		absOutput = output
	}

	slog.Info("concatenating WARC files",
		"inputs", len(files),
		"output", absOutput,
		"totalInputBytes", totalInputBytes,
	)

	// Ensure the output directory exists
	outputDir := filepath.Dir(absOutput)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		slog.Error("failed to create output directory", "dir", outputDir, "error", err)
		return
	}

	// Detect if any input file is also the output path to prevent self-overwrite
	for _, f := range files {
		absInput, err := filepath.Abs(f)
		if err != nil {
			absInput = f
		}
		if absInput == absOutput {
			slog.Error("output file is the same as one of the input files", "file", f)
			return
		}
	}

	// Create (or truncate) the output file
	out, err := os.Create(absOutput)
	if err != nil {
		slog.Error("failed to create output file", "file", absOutput, "error", err)
		return
	}

	// Track whether we completed successfully so we can clean up the output file on failure
	success := false
	defer func() {
		if !success {
			out.Close()
			if removeErr := os.Remove(absOutput); removeErr != nil && !os.IsNotExist(removeErr) {
				slog.Warn("failed to remove partial output file", "file", absOutput, "error", removeErr)
			}
		}
	}()

	var totalWritten int64
	for _, f := range files {
		written, err := copyFile(out, f)
		if err != nil {
			slog.Error("failed to copy file to output", "file", f, "output", absOutput, "error", err)
			return
		}
		totalWritten += written
		slog.Debug("appended file", "file", f, "bytes", written)
	}

	if err := out.Close(); err != nil {
		slog.Error("failed to close output file", "file", absOutput, "error", err)
		return
	}

	success = true

	slog.Info(fmt.Sprintf("concatenated in %s", time.Since(startTime).String()),
		"output", absOutput,
		"files", len(files),
		"bytesWritten", totalWritten,
	)

	// Delete original files after successful concatenation
	if !noDelete {
		for _, f := range files {
			if err := os.Remove(f); err != nil {
				slog.Error("failed to delete original file", "file", f, "error", err)
			} else {
				slog.Info("deleted original file", "file", f)
			}
		}
	}
}

// hasDictionaryFrame reports whether a file begins with the zstd skippable dictionary
// frame written by gowarc (magic 0x184D2A5D, little-endian). Concatenating such files
// at the byte level is unsafe because each file embeds its own dictionary context.
func hasDictionaryFrame(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	var magic uint32
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		return false
	}

	// 0x184D2A5D is the skippable-frame magic reserved for zstd dictionaries by the
	// WARC-zstd spec: https://iipc.github.io/warc-specifications/specifications/warc-zstd/
	return magic == 0x184D2A5D
}

// copyFile copies the contents of src into dst, returning the number of bytes written.
func copyFile(dst *os.File, src string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("failed to open source file: %w", err)
	}
	defer in.Close()

	n, err := io.Copy(dst, in)
	if err != nil {
		return n, fmt.Errorf("failed to copy data: %w", err)
	}
	return n, nil
}

package mend

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/internetarchive/gowarc/cmd/warc/utils"
	"github.com/spf13/cobra"
)

// Command represents the mend command
var Command = &cobra.Command{
	Use:   "mend [flags] file1.warc.gz.open file2.warc.gz.open ...",
	Short: "Mend and close incomplete gzip-compressed WARC files (.gz.open)",
	Long: `Mend and close incomplete gzip-compressed WARC files (.gz.open) by:
  - Truncating files with extra trailing bytes
  - Truncating at the last valid record when corruption is detected
  - Removing .open suffix from files that need to be closed
  
By default, only processes .open files that need to be closed. Use --force to 
verify and fix any gzip WARC files, including completed archives.
File contents are verified to be gzip format.`,
	Args: cobra.MinimumNArgs(1),
	Run:  mend,
}

func init() {
	Command.Flags().Bool("dry-run", false, "Show what would be done without making changes")
	Command.Flags().BoolP("yes", "y", false, "Assume yes to all mend confirmations")
	Command.Flags().Bool("force", false, "Process all gzip WARC files, not just .open files")
}

type mendResult struct {
	filepath        string
	needsTruncate   bool
	truncateAt      int64
	needsRename     bool
	newName         string
	lastValidPos    int64
	fileSize        int64
	recordCount     int
	firstRecordType string
	errorAt         int64
	errorMsg        string
}

type mendStats struct {
	totalFiles          int
	processedFiles      int
	skippedFiles        int
	truncatedFiles      int
	renamedFiles        int
	deletedFiles        int
	errorFiles          int
	totalBytesTruncated int64
	totalRecords        int
	startTime           time.Time
	dryRun              bool
}

func mend(cmd *cobra.Command, files []string) {
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		slog.Error("failed to get dry-run flag", "error", err)
		return
	}

	autoYes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		slog.Error("failed to get yes flag", "error", err)
		return
	}

	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		slog.Error("failed to get force flag", "error", err)
		return
	}

	verbose, err := cmd.Root().Flags().GetBool("verbose")
	if err != nil {
		slog.Error("failed to get verbose flag", "error", err)
		return
	}

	// Initialize statistics
	stats := mendStats{
		totalFiles: len(files),
		startTime:  time.Now(),
		dryRun:     dryRun,
	}

	for _, filepath := range files {
		result := analyzeWARCFile(filepath, verbose, force)

		if !result.needsTruncate && !result.needsRename {
			if result.recordCount > 1 || (result.recordCount == 1 && result.firstRecordType != "warcinfo") {
				slog.Info("file is ok", "file", filepath, "records", result.recordCount)
				stats.processedFiles++
				stats.totalRecords += result.recordCount
			} else if result.recordCount == 1 && result.firstRecordType == "warcinfo" {
				// File has only warcinfo record - ask user if it should be deleted
				slog.Warn("unused WARC file detected", "file", filepath, "records", 1, "type", "warcinfo-only")

				if !dryRun {
					if confirmAction(fmt.Sprintf("delete unused WARC file %s? [y/N] ", filepath), autoYes) {
						if err := os.Remove(filepath); err != nil {
							slog.Error("failed to delete file", "file", filepath, "error", err)
							stats.errorFiles++
						} else {
							slog.Info("deleted unused WARC file", "file", filepath)
							stats.deletedFiles++
						}
					} else {
						slog.Info("keeping unused WARC file", "file", filepath)
						stats.processedFiles++
						stats.totalRecords += result.recordCount
					}
				} else {
					slog.Info("would delete unused WARC file", "file", filepath, "dryRun", true)
					stats.deletedFiles++
				}
			} else {
				// File has 0 records - ask user if it should be deleted
				slog.Warn("empty file detected", "file", filepath, "records", 0)

				if !dryRun {
					if confirmAction(fmt.Sprintf("delete empty file %s? [y/N] ", filepath), autoYes) {
						if err := os.Remove(filepath); err != nil {
							slog.Error("failed to delete file", "file", filepath, "error", err)
							stats.errorFiles++
						} else {
							slog.Info("deleted empty file", "file", filepath)
							stats.deletedFiles++
						}
					} else {
						slog.Info("keeping empty file", "file", filepath)
						stats.processedFiles++
					}
				} else {
					slog.Info("would delete empty file", "file", filepath, "dryRun", true)
					stats.deletedFiles++
				}
			}
			continue
		}

		// Report issues found
		if result.needsTruncate {
			if result.errorMsg != "" {
				slog.Warn("corruption detected",
					"file", filepath,
					"error", result.errorMsg,
					"lastValidPos", formatBytes(result.lastValidPos),
					"extraBytes", formatBytes(result.fileSize-result.lastValidPos))
			} else {
				slog.Warn("extra trailing bytes detected",
					"file", filepath,
					"lastValidPos", formatBytes(result.lastValidPos),
					"extraBytes", formatBytes(result.fileSize-result.lastValidPos))
			}
		}

		if result.needsRename {
			slog.Warn("file has .open suffix", "file", filepath)
		}

		// Track statistics
		stats.processedFiles++
		stats.totalRecords += result.recordCount
		if result.errorMsg != "" {
			stats.errorFiles++
		}

		// Perform repairs if not dry-run
		if !dryRun {
			if result.needsTruncate {
				if confirmAction(fmt.Sprintf("truncate %s at position %d? [y/N] ", filepath, result.truncateAt), autoYes) {
					if err := truncateFile(filepath, result.truncateAt); err != nil {
						slog.Error("failed to truncate file", "file", filepath, "error", err)
						continue
					}
					slog.Info("truncated file", "file", filepath, "at", result.truncateAt)
					stats.truncatedFiles++
					stats.totalBytesTruncated += result.fileSize - result.truncateAt
				}
			}

			if result.needsRename {
				// Auto-rename if no truncation was needed (file is valid, just needs .open suffix removed)
				if !result.needsTruncate || confirmAction(fmt.Sprintf("remove .open suffix from %s? [y/N] ", filepath), autoYes) {
					if err := os.Rename(filepath, result.newName); err != nil {
						slog.Error("failed to rename file", "file", filepath, "error", err)
						continue
					}
					slog.Info("removed .open suffix", "from", filepath, "to", result.newName)
					stats.renamedFiles++
				}
			}
		} else {
			// Dry run - just report what would be done
			if result.needsTruncate {
				slog.Info("would truncate file", "file", filepath, "at", result.truncateAt, "dryRun", true)
				stats.truncatedFiles++
				stats.totalBytesTruncated += result.fileSize - result.truncateAt
			}
			if result.needsRename {
				slog.Info("would rename file", "from", filepath, "to", result.newName, "dryRun", true)
				stats.renamedFiles++
			}
		}
	}

	// Display summary statistics
	displaySummary(stats)
}

// isGzipFile checks if a file is actually gzip compressed by reading the magic bytes
func isGzipFile(filepath string) bool {
	file, err := os.Open(filepath)
	if err != nil {
		return false
	}
	defer file.Close()

	// Read first 2 bytes to check gzip magic number (0x1f 0x8b)
	header := make([]byte, 2)
	n, err := file.Read(header)
	if err != nil || n < 2 {
		return false
	}

	return header[0] == 0x1f && header[1] == 0x8b
}

func isCompressedFile(filepath string) bool {
    file, err := os.Open(filepath)
    if err != nil {
        return false
    }
    defer file.Close()

    header := make([]byte, 4)
    n, err := file.Read(header)
    if err != nil || n < 4 {
        return false
    }

    // gzip magic
    if header[0] == 0x1f && header[1] == 0x8b {
        return true
    }

    // zstd magic 28 B5 2F FD
    if header[0] == 0x28 && header[1] == 0xB5 && header[2] == 0x2F && header[3] == 0xFD {
        return true
    }

    return false
}


func analyzeWARCFile(filepath string, verbose bool, force bool) mendResult {
	result := mendResult{
		filepath: filepath,
	}

	// Check file suffix requirements based on force flag
	if !force {
		// Only process .open files (files being written that need to be closed)
		if !strings.HasSuffix(strings.ToLower(filepath), ".open") {
			if verbose {
				slog.Debug("skipping non-.open file", "file", filepath)
			}
			return result
		}

		// Support gzip (.gz.open) and zstd (.zst.open)
		lower := strings.ToLower(filepath)
		if !(strings.HasSuffix(lower, ".gz.open") || strings.HasSuffix(lower, ".zst.open")) {
			slog.Error("only gzip or zstd WARC files (.gz.open or .zst.open) are supported", "file", filepath)
			return result
		}else {
		// With --force, process any gzip WARC file (.gz or .gz.open)
		if !(strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".gz.open") || strings.HasSuffix(lower, ".zst") || strings.HasSuffix(lower, ".zst.open")) {
		slog.Error("only gzip or zstd WARC files (.gz/.zst/.gz.open/.zst.open) are supported", "file", filepath)
		return result
}

	}

	// Verify the file is actually gzip compressed by checking magic bytes
	if !isCompressedFile(filepath) {
    slog.Error("file is not gzip or zstd compressed", "file", filepath)
    return result
	}


	// Get file size
	fileInfo, err := os.Stat(filepath)
	if err != nil {
		slog.Error("failed to stat file", "file", filepath, "error", err)
		return result
	}
	result.fileSize = fileInfo.Size()

	// Check for .open suffix (always check, regardless of force flag)
	if strings.HasSuffix(filepath, ".open") {
		result.needsRename = true
		result.newName = strings.TrimSuffix(filepath, ".open")
	}

	// Open and analyze WARC file
	reader, f, err := utils.OpenWARCFile(filepath)
	if err != nil {
		slog.Error("failed to open WARC file", "file", filepath, "error", err)
		return result
	}
	defer f.Close()

	var lastValidEndPos int64 = 0

	// Read through all records
	for {
		record, err := reader.ReadRecord()
		if err != nil {
			if err == io.EOF {
				// Normal end of file - check for extra trailing bytes
				if lastValidEndPos > 0 && lastValidEndPos < result.fileSize {
					result.needsTruncate = true
					result.truncateAt = lastValidEndPos
					result.lastValidPos = lastValidEndPos
				}
				break
			} else {
				// Error reading record - corruption detected
				result.errorMsg = err.Error()
				result.errorAt = lastValidEndPos
				result.lastValidPos = lastValidEndPos
				result.needsTruncate = lastValidEndPos > 0
				result.truncateAt = lastValidEndPos

				if verbose {
					slog.Debug("read error details",
						"file", filepath,
						"error", err,
						"records", result.recordCount)
				}
				break
			}
		}

		result.recordCount++

		// Capture the first record's type for special handling
		if result.recordCount == 1 {
			result.firstRecordType = record.Header.Get("WARC-Type")
		}

		// Verify we have valid offset/size from compressed WARC
		if record.Offset < 0 || record.Size <= 0 {
			result.errorMsg = "invalid offset/size in compressed WARC record"
			result.errorAt = lastValidEndPos
			result.lastValidPos = lastValidEndPos
			result.needsTruncate = lastValidEndPos > 0
			result.truncateAt = lastValidEndPos
			record.Content.Close()
			break
		}

		// Read the content to ensure the record is fully valid
		buf := make([]byte, 1024)
		for {
			_, err := record.Content.Read(buf)
			if err == io.EOF {
				break
			}
			if err != nil {
				// Error reading record content
				result.errorMsg = fmt.Sprintf("error reading record content: %v", err)
				result.errorAt = record.Offset
				result.lastValidPos = record.Offset
				result.truncateAt = record.Offset
				result.needsTruncate = true
				record.Content.Close()
				return result
			}
		}

		// Update the end position of this valid record
		lastValidEndPos = record.Offset + record.Size

		record.Content.Close()

		if verbose && result.recordCount%1000 == 0 {
			slog.Debug("progress", "file", filepath, "records", result.recordCount)
		}
	}

	return result
}

func truncateFile(filepath string, position int64) error {
	file, err := os.OpenFile(filepath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open file for truncation: %w", err)
	}
	defer file.Close()

	if err := file.Truncate(position); err != nil {
		return fmt.Errorf("failed to truncate file: %w", err)
	}

	return nil
}

func confirmAction(prompt string, autoYes bool) bool {
	if autoYes {
		fmt.Println("y")
		return true
	}

	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

// displaySummary shows final statistics for the mend operation
func displaySummary(stats mendStats) {
	elapsed := time.Since(stats.startTime)

	// Build summary parts
	parts := []string{}

	if stats.processedFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d processed", stats.processedFiles))
	}

	if stats.truncatedFiles > 0 {
		verb := "truncated"
		if stats.dryRun {
			verb = "would truncate"
		}
		if stats.totalBytesTruncated > 0 {
			parts = append(parts, fmt.Sprintf("%d %s (truncated %s)", stats.truncatedFiles, verb, formatBytes(stats.totalBytesTruncated)))
		} else {
			parts = append(parts, fmt.Sprintf("%d %s", stats.truncatedFiles, verb))
		}
	}

	if stats.renamedFiles > 0 {
		verb := "renamed"
		if stats.dryRun {
			verb = "would rename"
		}
		parts = append(parts, fmt.Sprintf("%d %s", stats.renamedFiles, verb))
	}

	if stats.deletedFiles > 0 {
		verb := "deleted"
		if stats.dryRun {
			verb = "would delete"
		}
		parts = append(parts, fmt.Sprintf("%d %s", stats.deletedFiles, verb))
	}

	if stats.skippedFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", stats.skippedFiles))
	}

	if stats.errorFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d with errors", stats.errorFiles))
	}

	if stats.totalRecords > 0 {
		parts = append(parts, fmt.Sprintf("%d total records", stats.totalRecords))
	}

	// Format final summary
	var summary string
	if len(parts) > 0 {
		summary = strings.Join(parts, ", ")
	} else {
		summary = "no files needed mending"
	}

	// Use structured logging with key fields for filtering
	slog.Info(fmt.Sprintf("mend operation completed: %s in %v", summary, elapsed.Round(time.Millisecond)),
		"dryRun", stats.dryRun,
		"files", stats.totalFiles,
		"records", stats.totalRecords,
		"bytesTruncated", stats.totalBytesTruncated)
}

// formatBytes formats a byte count as a human-readable string
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

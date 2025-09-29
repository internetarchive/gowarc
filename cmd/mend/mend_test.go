package mend

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestAnalyzeWARCFile tests the analysis of different WARC files
func TestAnalyzeWARCFile(t *testing.T) {
	testdataDir := "../../testdata/mend"

	tests := []struct {
		name            string
		filename        string
		expectRename    bool
		expectTruncate  bool
		expectError     bool
		expectedNewName string
		description     string
	}{
		{
			name:            "good_file",
			filename:        "good.warc.gz.open",
			expectRename:    true,
			expectTruncate:  false,
			expectError:     false,
			expectedNewName: "good.warc.gz",
			description:     "A valid WARC file with .open suffix - should only need renaming",
		},
		{
			name:            "corrupted_trailing_bytes",
			filename:        "corrupted-trailing-bytes.warc.gz.open",
			expectRename:    true,
			expectTruncate:  true,
			expectError:     true, // Corrupted files should have errors
			expectedNewName: "corrupted-trailing-bytes.warc.gz",
			description:     "A WARC file with extra garbage bytes at the end - should need truncation and renaming",
		},
		{
			name:            "corrupted_mid_record",
			filename:        "corrupted-mid-record.warc.gz.open",
			expectRename:    true,
			expectTruncate:  true,
			expectError:     true, // Corrupted files should have errors
			expectedNewName: "corrupted-mid-record.warc.gz",
			description:     "A WARC file corrupted mid-record - should need truncation and renaming",
		},
		{
			name:            "empty_file",
			filename:        "empty.warc.gz.open",
			expectRename:    true, // Synthetic empty file has valid gzip headers
			expectTruncate:  false,
			expectError:     false,
			expectedNewName: "empty.warc.gz",
			description:     "A synthetic empty WARC file with .open suffix - valid gzip headers, only needs renaming",
		},
		{
			name:            "skip_non_open",
			filename:        "skip-non-open.warc.gz",
			expectRename:    false,
			expectTruncate:  false,
			expectError:     false,
			expectedNewName: "",
			description:     "A regular .gz file without .open suffix - should be skipped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(testdataDir, tt.filename)

			// Check if test file exists
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				t.Skipf("Test file %s does not exist, skipping test", filePath)
				return
			}

			result := analyzeWARCFile(filePath, false, false)

			// Check rename expectations
			if tt.expectRename != result.needsRename {
				t.Errorf("expected needsRename=%v, got %v", tt.expectRename, result.needsRename)
			}

			if tt.expectRename && filepath.Base(result.newName) != tt.expectedNewName {
				t.Errorf("expected newName=%q, got %q (base: %q)", tt.expectedNewName, result.newName, filepath.Base(result.newName))
			}

			// Check truncate expectations
			if tt.expectTruncate != result.needsTruncate {
				t.Errorf("expected needsTruncate=%v, got %v", tt.expectTruncate, result.needsTruncate)
			}

			// If we expect truncation, check that truncateAt is reasonable
			if tt.expectTruncate && result.truncateAt <= 0 {
				t.Errorf("expected positive truncateAt value when needsTruncate=true, got %d", result.truncateAt)
			}

			// Check error expectations
			hasError := result.errorMsg != ""
			if tt.expectError != hasError {
				t.Errorf("expected error=%v, got error=%v (msg: %q)", tt.expectError, hasError, result.errorMsg)
			}

			t.Logf("test %s: %s", tt.name, tt.description)
			if result.needsTruncate {
				t.Logf("  - Would truncate at: %d bytes", result.truncateAt)
			}
			if result.needsRename {
				t.Logf("  - Would rename to: %s", result.newName)
			}
			if result.recordCount > 0 {
				t.Logf("  - Successfully read: %d records", result.recordCount)
			}
			if result.errorMsg != "" {
				t.Logf("  - Error encountered: %s", result.errorMsg)
			}
		})
	}
}

// TestMendResultValidation tests that mendResult structs are properly populated
func TestMendResultValidation(t *testing.T) {
	testdataDir := "../../testdata/mend"

	// Test a file that should have all fields populated
	filePath := filepath.Join(testdataDir, "corrupted-trailing-bytes.warc.gz.open")

	// Check if test file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Skipf("Test file %s does not exist, skipping test", filePath)
		return
	}

	result := analyzeWARCFile(filePath, false, false)

	// Validate basic fields are populated
	if result.filepath != filePath {
		t.Errorf("expected filepath=%q, got %q", filePath, result.filepath)
	}

	if result.fileSize <= 0 {
		t.Errorf("expected positive fileSize, got %d", result.fileSize)
	}

	// For a corrupted file, we should have some records read
	if result.recordCount <= 0 {
		t.Errorf("expected positive recordCount for corrupted file, got %d", result.recordCount)
	}

	// Should need both truncation and renaming
	if !result.needsTruncate {
		t.Error("expected needsTruncate=true for corrupted file")
	}

	if !result.needsRename {
		t.Error("expected needsRename=true for .open file")
	}

	// Validate truncation position is reasonable
	if result.truncateAt >= result.fileSize {
		t.Errorf("truncateAt (%d) should be less than fileSize (%d)", result.truncateAt, result.fileSize)
	}

	if result.truncateAt <= 0 {
		t.Errorf("truncateAt should be positive, got %d", result.truncateAt)
	}

	// Validate lastValidPos
	if result.lastValidPos != result.truncateAt {
		t.Errorf("lastValidPos (%d) should equal truncateAt (%d)", result.lastValidPos, result.truncateAt)
	}

	t.Logf("validation passed for corrupted file: size=%d bytes, records=%d, truncateAt=%d bytes, newName=%s", result.fileSize, result.recordCount, result.truncateAt, result.newName)
}

// TestAnalyzeWARCFileForceMode tests analyzeWARCFile with force=true on good closed WARC files
func TestAnalyzeWARCFileForceMode(t *testing.T) {
	testdataDir := "../../testdata/mend"

	tests := []struct {
		name            string
		filename        string
		expectedRecords int
		description     string
	}{
		{
			name:            "good_closed_warc_force_mode",
			filename:        "test.warc.gz",
			expectedRecords: 3, // Known from read_test.go
			description:     "A good closed WARC file processed with force=true should be analyzed properly",
		},
		{
			name:            "skip_non_open_force_mode",
			filename:        "skip-non-open.warc.gz",
			expectedRecords: 0, // We don't know the expected count, just verify it's processed
			description:     "Another closed WARC file processed with force=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(testdataDir, tt.filename)

			// Check if test file exists
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				t.Skipf("Test file %s does not exist, skipping test", filePath)
				return
			}

			// Call analyzeWARCFile with force=true
			result := analyzeWARCFile(filePath, false, true)

			// For good closed files, should not need rename or truncation
			if result.needsRename {
				t.Error("expected needsRename=false for closed .gz file")
			}

			if result.needsTruncate {
				t.Error("expected needsTruncate=false for good closed file")
			}

			// Should have no error message for good files
			if result.errorMsg != "" {
				t.Errorf("expected no error message for good file, got: %s", result.errorMsg)
			}

			// File should be processed (not skipped) - fileSize should be > 0
			if result.fileSize <= 0 {
				t.Errorf("expected positive fileSize for processed file, got %d", result.fileSize)
			}

			// Should have processed some records for non-empty files
			if tt.expectedRecords > 0 && result.recordCount != tt.expectedRecords {
				t.Errorf("expected recordCount=%d, got %d", tt.expectedRecords, result.recordCount)
			}

			// For files where we don't know exact count, just verify it's positive
			if tt.expectedRecords == 0 && result.recordCount < 0 {
				t.Errorf("expected non-negative recordCount, got %d", result.recordCount)
			}

			t.Logf("force mode test %s: %s", tt.name, tt.description)
			t.Logf("  - File processed: %d bytes, %d records", result.fileSize, result.recordCount)
		})
	}
}

// TestSkipNonOpenFiles tests that non-.open files are correctly skipped
func TestSkipNonOpenFiles(t *testing.T) {
	testdataDir := "../../testdata/mend"
	filePath := filepath.Join(testdataDir, "skip-non-open.warc.gz")

	// Check if test file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Skipf("Test file %s does not exist, skipping test", filePath)
		return
	}

	result := analyzeWARCFile(filePath, false, false)

	// Should not need any action
	if result.needsRename {
		t.Error("expected needsRename=false for non-.open file")
	}

	if result.needsTruncate {
		t.Error("expected needsTruncate=false for non-.open file")
	}

	// File size should be 0 for skipped files since we don't analyze them
	if result.fileSize != 0 {
		t.Errorf("expected fileSize=0 for skipped file, got %d", result.fileSize)
	}

	// Record count should be 0 since we skip analysis
	if result.recordCount != 0 {
		t.Errorf("expected recordCount=0 for skipped file, got %d", result.recordCount)
	}

	t.Logf("non-.open file correctly skipped")
}

// Expected results from gowarc mend processing of synthetic test data
type expectedResult struct {
	outputFile  string
	sha256      string
	recordCount int
	truncateAt  int64 // 0 if no truncation expected
	description string
}

var mendExpectedResults = map[string]expectedResult{
	"good.warc.gz.open": {
		outputFile:  "good.warc.gz",
		sha256:      "d11735247e89bffdc26886464b05b7b35ffa955f9b8b3ce71ea5ecb49e66d24d",
		recordCount: 50,
		truncateAt:  0, // No truncation needed
		description: "good synthetic file with .open suffix",
	},
	"empty.warc.gz.open": {
		outputFile:  "empty.warc.gz",
		sha256:      "30e6fa98fb48c2b132824d1ac5e2243c0be9e9082ff32598d34d7687ca7f6c7f",
		recordCount: 0,
		truncateAt:  0, // No truncation needed
		description: "empty synthetic file with .open suffix",
	},
	"corrupted-trailing-bytes.warc.gz.open": {
		outputFile:  "corrupted-trailing-bytes.warc.gz",
		sha256:      "b892bbeeab0f5fcf9a2ca451805bcd060c6bbe66e43b7c400bcd52a0a1afa113",
		recordCount: 30,
		truncateAt:  2362, // Truncates trailing garbage
		description: "synthetic file with trailing garbage bytes",
	},
	"corrupted-mid-record.warc.gz.open": {
		outputFile:  "corrupted-mid-record.warc.gz",
		sha256:      "7c7f896ce58404c841a652500efefbba5f4d92ccc6f9161b0b60aa816f542a7c",
		recordCount: 12,
		truncateAt:  1219,
		description: "synthetic file corrupted mid-record",
	},
}

// createMockCobraCommand creates a mock cobra command for testing the mend function directly
func createMockCobraCommand() *cobra.Command {
	// Create root command first
	rootCmd := &cobra.Command{Use: "root"}
	rootCmd.PersistentFlags().Bool("verbose", false, "Enable verbose logging")
	// Also add to regular flags since mend accesses it via cmd.Root().Flags()
	rootCmd.Flags().Bool("verbose", false, "Enable verbose logging")

	// Create mend command as a subcommand
	cmd := &cobra.Command{
		Use: "mend",
	}
	// Add the same flags as the real mend command
	cmd.Flags().Bool("dry-run", false, "Show what would be done without making changes")
	cmd.Flags().BoolP("yes", "y", false, "Assume yes to all mend confirmations")
	cmd.Flags().Bool("force", false, "Process all gzip WARC files, not just .open files")

	// Add mend command to root to inherit persistent flags
	rootCmd.AddCommand(cmd)

	return cmd
}

// TestMendFunctionDirect verifies that the mend function produces
// expected results on synthetic test data by comparing against pre-computed checksums
func TestMendFunctionDirect(t *testing.T) {
	// Get current directory and construct paths relative to workspace root
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	// From cmd/mend, go up to workspace root
	workspaceRoot := filepath.Join(cwd, "../..")
	testdataDir := filepath.Join(workspaceRoot, "testdata/mend")
	outputDir := filepath.Join(testdataDir, "mend_test_output")

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatal(err)
	}

	for sourceFile, expected := range mendExpectedResults {
		t.Run(sourceFile, func(t *testing.T) {
			sourceFilePath := filepath.Join(testdataDir, sourceFile)

			// Check if source file exists
			if _, err := os.Stat(sourceFilePath); os.IsNotExist(err) {
				t.Skipf("source file %s does not exist, skipping test", sourceFilePath)
				return
			}

			// Create a copy for mend function to process
			testFile := filepath.Join(outputDir, sourceFile)
			if err := copyFile(sourceFilePath, testFile); err != nil {
				t.Fatalf("failed to copy test file: %v", err)
			}

			// Create a mock cobra command with the necessary flags
			cmd := createMockCobraCommand()
			cmd.Flags().Set("yes", "true")      // Auto-confirm all operations
			cmd.Flags().Set("dry-run", "false") // Actually perform operations
			cmd.Flags().Set("force", "false")   // Use normal .open file processing

			// Set the root-level verbose flag
			cmd.Root().Flags().Set("verbose", "false")

			// Call mend function directly
			mend(cmd, []string{testFile})

			// Check that the expected output file exists
			outputFile := filepath.Join(outputDir, expected.outputFile)
			if _, err := os.Stat(outputFile); os.IsNotExist(err) {
				t.Errorf("expected output file %s does not exist", outputFile)
				return
			}

			// Calculate checksum of mend output
			actualChecksum, err := calculateChecksum(outputFile)
			if err != nil {
				t.Fatalf("failed to calculate checksum: %v", err)
			}

			// Compare with expected checksum
			if actualChecksum == expected.sha256 {
				t.Logf("checksum matches expected for %s: %s", expected.description, actualChecksum[:16]+"...")
			} else {
				t.Errorf("checksum mismatch for %s:\n  expected: %s\n  actual:   %s",
					expected.description, expected.sha256, actualChecksum)
			}

			// Verify output file was renamed correctly
			expectedBaseName := filepath.Base(expected.outputFile)
			actualBaseName := filepath.Base(outputFile)
			if actualBaseName != expectedBaseName {
				t.Errorf("output filename mismatch: expected %s, got %s", expectedBaseName, actualBaseName)
			}

			t.Logf("test completed for %s", expected.description)
		})
	}

	// Cleanup
	t.Cleanup(func() {
		os.RemoveAll(outputDir)
	})
}

// calculateChecksum calculates SHA256 checksum of a file
func calculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	return destFile.Sync()
}

// TestIsGzipFile tests the gzip file detection function
func TestIsGzipFile(t *testing.T) {
	testdataDir := "../../testdata/mend"

	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{"good_gzip_file", "good.warc.gz.open", true},
		{"empty_gzip_file", "empty.warc.gz.open", true},
		{"corrupted_but_valid_gzip_header", "corrupted-trailing-bytes.warc.gz.open", true},
		{"non_gzip_file_if_exists", "nonexistent.txt", false}, // Will return false due to file not existing
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filepath := filepath.Join(testdataDir, tt.filename)

			// Skip test if file doesn't exist (for non-gzip file test)
			if _, err := os.Stat(filepath); os.IsNotExist(err) && tt.name == "non_gzip_file_if_exists" {
				// Create a temporary non-gzip file for testing
				tempFile, err := os.CreateTemp("", "test_non_gzip_*.txt")
				if err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				defer os.Remove(tempFile.Name())
				defer tempFile.Close()

				// Write some non-gzip content
				tempFile.WriteString("This is not a gzip file")
				tempFile.Close()

				filepath = tempFile.Name()
			}

			if _, err := os.Stat(filepath); os.IsNotExist(err) {
				t.Skipf("Test file %s does not exist, skipping test", filepath)
				return
			}

			result := isGzipFile(filepath)
			if result != tt.expected {
				t.Errorf("isGzipFile(%s) = %v, expected %v", tt.filename, result, tt.expected)
			}
		})
	}
}

// TestTruncateFile tests the file truncation function
func TestTruncateFile(t *testing.T) {
	// Create a temporary file for testing
	tempFile, err := os.CreateTemp("", "test_truncate_*.dat")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write some test data
	testData := "This is a test file with some content that will be truncated."
	_, err = tempFile.WriteString(testData)
	if err != nil {
		t.Fatalf("failed to write test data: %v", err)
	}
	tempFile.Close()

	// Test truncation at position 20
	truncatePos := int64(20)
	err = truncateFile(tempFile.Name(), truncatePos)
	if err != nil {
		t.Errorf("truncateFile failed: %v", err)
	}

	// Verify file was truncated correctly
	stat, err := os.Stat(tempFile.Name())
	if err != nil {
		t.Fatalf("failed to stat truncated file: %v", err)
	}

	if stat.Size() != truncatePos {
		t.Errorf("file size after truncation = %d, expected %d", stat.Size(), truncatePos)
	}

	// Verify content is correct
	truncatedFile, err := os.Open(tempFile.Name())
	if err != nil {
		t.Fatalf("failed to open truncated file: %v", err)
	}
	defer truncatedFile.Close()

	content, err := io.ReadAll(truncatedFile)
	if err != nil {
		t.Fatalf("failed to read truncated file: %v", err)
	}

	expectedContent := testData[:truncatePos]
	if string(content) != expectedContent {
		t.Errorf("truncated content = %q, expected %q", string(content), expectedContent)
	}
}

// TestFormatBytes tests the byte formatting function
func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024*1024 + 512*1024, "1.5 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024*1024*1024*1024 + 512*1024*1024*1024, "1.5 TB"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d_bytes", tt.bytes), func(t *testing.T) {
			result := formatBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatBytes(%d) = %q, expected %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

// TestConfirmAction tests the confirmation prompt function
func TestConfirmAction(t *testing.T) {
	// Test auto-yes functionality
	result := confirmAction("Test prompt", true)
	if !result {
		t.Error("confirmAction with autoYes=true should return true")
	}
}

// TestMendDryRun tests the mend function in dry-run mode
func TestMendDryRun(t *testing.T) {
	testdataDir := "../../testdata/mend"
	tempDir, err := os.MkdirTemp("", "mend_dry_run_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test with a file that needs truncation
	sourceFile := filepath.Join(testdataDir, "corrupted-trailing-bytes.warc.gz.open")
	if _, err := os.Stat(sourceFile); os.IsNotExist(err) {
		t.Skip("Test file does not exist, skipping dry-run test")
	}

	// Copy test file
	testFile := filepath.Join(tempDir, "test.warc.gz.open")
	if err := copyFile(sourceFile, testFile); err != nil {
		t.Fatalf("failed to copy test file: %v", err)
	}

	// Get original file info
	originalInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat original file: %v", err)
	}

	// Create mock command with dry-run enabled
	cmd := createMockCobraCommand()
	cmd.Flags().Set("dry-run", "true")
	cmd.Flags().Set("yes", "true")

	// Set the root-level verbose flag
	cmd.Root().Flags().Set("verbose", "false")

	// Call mend in dry-run mode
	mend(cmd, []string{testFile})

	// Verify file was not actually modified
	afterInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat file after dry-run: %v", err)
	}

	if originalInfo.Size() != afterInfo.Size() {
		t.Errorf("dry-run should not modify file size: original=%d, after=%d", originalInfo.Size(), afterInfo.Size())
	}

	if originalInfo.ModTime() != afterInfo.ModTime() {
		t.Errorf("dry-run should not modify file: original mod time=%v, after=%v", originalInfo.ModTime(), afterInfo.ModTime())
	}

	// Verify .open file still exists (not renamed)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("dry-run should not rename file, but original file is missing")
	}
}

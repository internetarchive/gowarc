package mend

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestAnalyzeWARCFile tests the analysis of different WARC files
func TestAnalyzeWARCFile(t *testing.T) {
	testdataDir := "../../testdata"

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
	testdataDir := "../../testdata"

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
	testdataDir := "../../testdata"

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
	testdataDir := "../../testdata"
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

// TestMendOutputMatchesSynthetic verifies that our mend command produces
// expected results on synthetic test data by comparing against pre-computed checksums
func TestMendOutputMatchesSynthetic(t *testing.T) {
	testdataDir := "../../testdata"
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

			// Note: empty files are now handled with synthetic empty gzip data

			// Create a copy for gowarc to process
			testFile := filepath.Join(outputDir, sourceFile)
			if err := copyFile(sourceFilePath, testFile); err != nil {
				t.Fatalf("failed to copy test file: %v", err)
			}

			// Process with gowarc mend
			cmd := exec.Command("../../cmd/gowarc", "mend", "--yes", testFile)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("gowarc mend failed: %v\nOutput: %s", err, output)
			}

			// Check that the expected output file exists
			outputFile := filepath.Join(outputDir, expected.outputFile)
			if _, err := os.Stat(outputFile); os.IsNotExist(err) {
				t.Errorf("expected output file %s does not exist", outputFile)
				return
			}

			// Calculate checksum of gowarc output
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

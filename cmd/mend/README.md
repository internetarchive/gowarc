# WARC mend command

The `mend` command helps fix corrupted or incomplete gzip-compressed WARC files (usually `.gz.open`) that were left in an invalid state during crawling or due to disk/network issues.

## Features

1. **Truncate Extra Trailing Bytes**: Detects and removes garbage data after the last valid WARC record
2. **Truncate at Corruption Point**: When a corrupted record is detected, truncates the file at the last valid record position
3. **Remove .open Suffix**: Removes the `.open` suffix from WARC files to "close" them
5. **Summary Statistics**: Provides comprehensive statistics on processed files, records, and bytes saved

## Scope

- **By default, only processes `.open` files** - Must be gzip-compressed; regular WARC files are skipped unless `--force` is used
- **With `--force`, processes any gzip WARC files** - Allows verification and repair of completed archives
- **Only supports gzip-compressed WARC files** - Uncompressed and other compression formats are not supported
- **Verifies gzip format** - File contents are checked for valid gzip magic bytes, not just file extension
- **Focuses on closing incomplete files** - Designed for files still being written during crawls, but can verify any gzip WARC with `--force`

## Usage

```bash
# Basic usage - will prompt for confirmation
gowarc mend file1.warc.gz.open file2.warc.gz.open

# Dry run - shows what would be done without making changes
gowarc mend --dry-run corrupted.warc.gz.open

# Auto-yes - automatically confirm all repairs
gowarc mend --yes file.warc.gz.open

# Verbose mode - shows detailed progress
gowarc mend -v large-file.warc.gz.open

# Process multiple files with summary
gowarc mend --dry-run *.warc.gz.open

# Force verification of any gzip WARC files (not just .open)
gowarc mend --force --dry-run archive.warc.gz

# Force verification with auto-confirm
gowarc mend --force --yes corrupted.warc.gz
```

### Force Mode

The `--force` flag allows verification and repair of any gzip WARC files, not just `.open` files:

- **Use case**: Verify integrity of completed WARC archives
- **Behavior**: Processes `.gz` and `.gz.open` files regardless of suffix
- **Safety**: Still requires gzip format validation and user confirmation (unless `--yes`)
- **Example**: Check if downloaded WARC files have corruption or trailing garbage

## Example Output

```bash
# File with extra trailing bytes
$ gowarc mend corrupted.warc.gz.open
time=2025-09-26T10:30:15.123+02:00 level=WARN msg="corruption detected" file=corrupted.warc.gz.open error="gzip reset: gzip: invalid header" lastValidPos="1.0 MB" extraBytes="24 B"
time=2025-09-26T10:30:15.123+02:00 level=WARN msg="file has .open suffix" file=corrupted.warc.gz.open
y
time=2025-09-26T10:30:18.456+02:00 level=INFO msg="truncated file" file=corrupted.warc.gz.open at=1048576
y
time=2025-09-26T10:30:18.789+02:00 level=INFO msg="removed .open suffix" from=corrupted.warc.gz.open to=corrupted.warc.gz
time=2025-09-26T10:30:18.789+02:00 level=INFO msg="mend operation completed: 1 processed, 1 truncated (saved 24 B), 1 renamed, 1 with errors, 1523 total records in 3.666s" dryRun=false files=1 records=1523 bytesSaved=24

# Good file that just needs closing
$ gowarc mend crawl-20250926.warc.gz.open
time=2025-09-26T10:31:22.001+02:00 level=WARN msg="file has .open suffix" file=crawl-20250926.warc.gz.open
y
time=2025-09-26T10:31:25.234+02:00 level=INFO msg="removed .open suffix" from=crawl-20250926.warc.gz.open to=crawl-20250926.warc.gz
time=2025-09-26T10:31:25.234+02:00 level=INFO msg="mend operation completed: 1 processed, 1 renamed, 3256 total records in 3.233s" dryRun=false files=1 records=3256 bytesSaved=0

# Dry run mode with multiple files
$ gowarc mend --dry-run *.warc.gz.open
time=2025-09-26T10:32:10.111+02:00 level=WARN msg="corruption detected" file=damaged.warc.gz.open error="copying content: unexpected EOF" lastValidPos="512.0 KB" extraBytes="100 B"
time=2025-09-26T10:32:10.111+02:00 level=WARN msg="file has .open suffix" file=damaged.warc.gz.open
time=2025-09-26T10:32:10.111+02:00 level=INFO msg="would truncate file" file=damaged.warc.gz.open at=524288 dryRun=true
time=2025-09-26T10:32:10.111+02:00 level=INFO msg="would rename file" from=damaged.warc.gz.open to=damaged.warc.gz dryRun=true
time=2025-09-26T10:32:10.555+02:00 level=INFO msg="would rename file" from=good.warc.gz.open to=good.warc.gz dryRun=true
time=2025-09-26T10:32:10.555+02:00 level=INFO msg="mend operation completed: 2 processed, 1 would truncate (saved 100 B), 2 would rename, 1 with errors, 2048 total records in 444ms" dryRun=true files=2 records=2048 bytesSaved=100

# Regular .gz files are skipped
$ gowarc mend -v completed.warc.gz
time=2025-09-26T10:33:15.789+02:00 level=DEBUG msg="skipping non-.open file" file=completed.warc.gz
time=2025-09-26T10:33:15.789+02:00 level=INFO msg="mend operation completed: no files needed mending in 1ms" dryRun=false files=1 records=0 bytesSaved=0
```

## How It Works

1. **File Filtering**: 
   - By default, only processes files ending with `.open` suffix
   - Further requires those `.open` files to be gzip-compressed (both `.gz.open` extension and gzip magic bytes)
   - With `--force`, processes any gzip WARC files (`.gz` or `.gz.open`)
   - Always verifies gzip magic bytes, not just file extension
   - Skips non-matching files with debug logging if verbose mode is enabled

2. **Analysis Phase**: 
   - Reads through the WARC file record by record using efficient streaming
   - Leverages gzip block boundaries for accurate truncation points
   - Tracks record offsets and sizes for compressed files
   - Detects any read errors or corruption

3. **Detection**:
   - Uses `Record.Offset` and `Record.Size` fields for precise positioning
   - Detects extra trailing bytes beyond the last valid record
   - Identifies corruption points during record reading

4. **Repair Phase**:
   - Prompts user for confirmation (unless `--yes` is specified)
   - Truncates files at the precise end of the last valid record
   - Renames files to remove `.open` suffix
   - Provides detailed summary statistics

## Safety Features

- **Interactive prompts** by default (use `--yes` to skip)
- **Dry-run mode** to preview changes without modifications
- **Verbose logging** for debugging and transparency
- **Non-destructive**: only truncates invalid data or renames files
- **Scope limitation**: only processes `.open` files to prevent accidental modification of completed archives
- **Comprehensive testing**: verified against reference implementation (warctool)

## File Requirements

- **Compressed WARC files**: Must be gzip-compressed
- **Open files**: By default, must have `.open` suffix indicating they need closing (with `--force`, can process any gzip WARC files)
- **Valid structure**: Must be readable as WARC format (corrupted files are handled gracefully)

package verify

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	warc "github.com/internetarchive/gowarc"
	"github.com/internetarchive/gowarc/cmd/utils"
	"github.com/spf13/cobra"
)

// Command represents the verify command
var Command = &cobra.Command{
	Use:   "verify",
	Short: "Verify the validity of one or many WARC file(s)",
	Args:  cobra.MinimumNArgs(1),
	Run:   verify,
}

func init() {
	Command.Flags().IntP("threads", "t", runtime.NumCPU(), "Number of threads to use for verification")
}

type result struct {
	warcVersionValid         bool
	blockDigestErrorsCount   int
	blockDigestValid         bool
	payloadDigestErrorsCount int
	payloadDigestValid       bool
}

func verify(cmd *cobra.Command, files []string) {
	threads := utils.GetThreadsFlag(cmd)

	for _, filepath := range files {
		startTime := time.Now()
		valid := true
		allRecordsRead := false
		errorsCount := 0
		recordCount := 0

		recordChan := make(chan *warc.Record, threads*2)
		results := make(chan result, threads*2)

		var processWg sync.WaitGroup
		var recordReaderWg sync.WaitGroup

		if !cmd.Root().Flags().Lookup("json").Changed {
			// Output the message if not in --json mode
			slog.Info("verifying", "file", filepath, "threads", threads)
		}
		for range threads {
			processWg.Add(1)
			go func() {
				defer processWg.Done()
				for record := range recordChan {
					processVerifyRecord(record, filepath, results)
					record.Content.Close()
				}
			}()
		}

		reader, f, err := utils.OpenWARCFile(filepath)
		if err != nil {
			return
		}
		defer f.Close()

		// Read records and send them to workers
		recordReaderWg.Add(1)
		go func() {
			defer recordReaderWg.Done()
			defer close(recordChan)
			for {
				record, err := reader.ReadRecord()
				if err != nil {
					if err == io.EOF {
						allRecordsRead = true
						break
					}
					if record == nil {
						slog.Error("failed to read record", "err", err.Error(), "file", filepath)
					} else {
						slog.Error("failed to read record", "err", err.Error(), "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
					}
					errorsCount++
					valid = false
					return
				}
				recordCount++

				if utils.ShouldSkipRecord(record) {
					continue
				}

				recordChan <- record
			}
		}()

		// Collect results from workers

		recordReaderWg.Add(1)
		go func() {
			defer recordReaderWg.Done()
			for res := range results {
				if !res.blockDigestValid {
					valid = false
					errorsCount += res.blockDigestErrorsCount
				}
				if !res.payloadDigestValid {
					valid = false
					errorsCount += res.payloadDigestErrorsCount
				}
				if !res.warcVersionValid {
					valid = false
					errorsCount++
				}
			}
		}()

		processWg.Wait()
		close(results)
		recordReaderWg.Wait()

		if recordCount == 0 {
			slog.Error("no record in file", "file", filepath)
		}

		// Ensure there is a visible difference when errors are present.
		if errorsCount > 0 {
			slog.Error(fmt.Sprintf("checked in %s", time.Since(startTime).String()), "file", filepath, "valid", valid, "errors", errorsCount, "count", recordCount, "allRecordsRead", allRecordsRead)
		} else {
			slog.Info(fmt.Sprintf("checked in %s", time.Since(startTime).String()), "file", filepath, "valid", valid, "errors", errorsCount, "count", recordCount, "allRecordsRead", allRecordsRead)
		}

	}
}

func processVerifyRecord(record *warc.Record, filepath string, results chan<- result) {
	var res result
	res.blockDigestErrorsCount, res.blockDigestValid = verifyBlockDigest(record, filepath)
	res.payloadDigestErrorsCount, res.payloadDigestValid = verifyPayloadDigest(record, filepath)
	res.warcVersionValid = verifyWARCVersion(record, filepath)
	results <- res
}

func verifyPayloadDigest(record *warc.Record, filepath string) (errorsCount int, valid bool) {
	valid = true

	// WARC-Payload-Digest is optional in both WARC 1.0 and 1.1 specifications
	// If it's not present, that's perfectly valid - just return success
	if record.Header.Get("WARC-Payload-Digest") == "" {
		slog.Debug("WARC-Payload-Digest not present (optional but highly recommended for deduplication)", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		return errorsCount, valid
	}

	// Calculate expected WARC-Payload-Digest
	_, err := record.Content.Seek(0, 0)
	if err != nil {
		slog.Error("failed to seek record content", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	resp, err := http.ReadResponse(bufio.NewReader(record.Content), nil)
	if err != nil {
		slog.Error("failed to read response", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		valid = false
		errorsCount++
		return errorsCount, valid
	}
	defer resp.Body.Close()
	defer record.Content.Seek(0, 0)

	if resp.Header.Get("X-Crawler-Transfer-Encoding") != "" || resp.Header.Get("X-Crawler-Content-Encoding") != "" {
		// This header being present in the HTTP headers indicates transfer-encoding and/or content-encoding were incorrectly stripped, causing us to not be able to verify the payload digest.
		slog.Error("malformed headers prevent accurate payload digest calculation", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))

		valid = false
		errorsCount++
		return errorsCount, valid
	}

	digestPrefix := strings.SplitN(record.Header.Get("WARC-Payload-Digest"), ":", 2)[0]
	if !warc.IsDigestSupported(digestPrefix) {
		slog.Error("unsupported payload digest algorithm", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "algorithm", digestPrefix)
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	payloadDigest, err := warc.GetDigest(resp.Body, warc.GetDigestFromPrefix(digestPrefix))
	if err != nil {
		slog.Error("failed to calculate payload digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	if payloadDigest != record.Header.Get("WARC-Payload-Digest") {
		slog.Error("payload digests do not match", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", record.Header.Get("WARC-Payload-Digest"), "got", payloadDigest)
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	return errorsCount, valid
}

func verifyBlockDigest(record *warc.Record, filepath string) (errorsCount int, valid bool) {
	valid = true

	// Verify that the WARC-Block-Digest is present
	blockDigest := record.Header.Get("WARC-Block-Digest")
	if blockDigest == "" {
		slog.Error("WARC-Block-Digest is missing", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		return 1, false
	}

	digestPrefix := strings.SplitN(blockDigest, ":", 2)[0]

	if !warc.IsDigestSupported(digestPrefix) {
		slog.Error("WARC-Block-Digest uses unsupported algorithm", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "algorithm", digestPrefix)
		return 1, false
	}

	// Calculate and verify the digest
	return verifyDigest(record, filepath, warc.GetDigestFromPrefix(digestPrefix), blockDigest)
}

func verifyDigest(record *warc.Record, filepath string, algorithm warc.DigestAlgorithm, expectedDigest string) (errorsCount int, valid bool) {
	defer record.Content.Seek(0, 0)

	calculatedDigest, err := warc.GetDigest(record.Content, algorithm)
	if err != nil {
		slog.Error("failed to calculate block digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		return 1, false
	}

	if calculatedDigest != expectedDigest {
		slog.Error("WARC-Block-Digest mismatch", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", calculatedDigest, "got", expectedDigest)
		return 1, false
	}

	return 0, true
}

func verifyWARCVersion(record *warc.Record, filepath string) (valid bool) {
	valid = true

	// Check for corrupted version data (indicates WARC reader parsing bug)
	if strings.Contains(record.Version, "\r") || strings.Contains(record.Version, "\n") {
		slog.Error("WARC version contains invalid characters", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "found", record.Version)
		valid = false
	} else if record.Version != "WARC/1.0" && record.Version != "WARC/1.1" {
		// Normal version validation for properly parsed versions
		slog.Error("invalid WARC version", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "found", record.Version, "expected", "WARC/1.0 or WARC/1.1")
		valid = false
	}

	return valid
}

package main

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	warc "github.com/internetarchive/gowarc"
	"github.com/spf13/cobra"
)

var logger *slog.Logger

func processVerifyRecord(record *warc.Record, filepath string, results chan<- result) {
	var res result
	res.blockDigestErrorsCount, res.blockDigestValid = verifyBlockDigest(record, filepath)
	res.payloadDigestErrorsCount, res.payloadDigestValid = verifyPayloadDigest(record, filepath)
	res.warcVersionValid = verifyWARCVersion(record, filepath)
	results <- res
}

type result struct {
	warcVersionValid         bool
	blockDigestErrorsCount   int
	blockDigestValid         bool
	payloadDigestErrorsCount int
	payloadDigestValid       bool
}

func verify(cmd *cobra.Command, files []string) {
	threads, err := strconv.Atoi(cmd.Flags().Lookup("threads").Value.String())
	if err != nil {
		slog.Error("invalid threads value", "err", err.Error())
		return
	}

	if cmd.Flags().Lookup("json").Changed {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	} else {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	for _, filepath := range files {
		startTime := time.Now()
		valid := true           // The WARC file is valid
		allRecordsRead := false // All records readed successfully
		errorsCount := 0
		recordCount := 0 // Count of records processed

		recordChan := make(chan *warc.Record, threads*2)
		results := make(chan result, threads*2)

		var processWg sync.WaitGroup
		var recordReaderWg sync.WaitGroup

		if !cmd.Flags().Lookup("json").Changed {
			// Output the message if not in --json mode
			logger.Info("verifying", "file", filepath, "threads", threads)
		}
		for i := 0; i < threads; i++ {
			processWg.Add(1)
			go func() {
				defer processWg.Done()
				for record := range recordChan {
					processVerifyRecord(record, filepath, results)
					record.Content.Close()
				}
			}()
		}

		f, err := os.Open(filepath)
		if err != nil {
			logger.Error("unable to open file", "err", err.Error(), "file", filepath)
			return
		}

		reader, err := warc.NewReader(f)
		if err != nil {
			logger.Error("warc.NewReader failed", "err", err.Error(), "file", filepath)
			return
		}

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
						logger.Error("failed to read record", "err", err.Error(), "file", filepath)
					} else {
						logger.Error("failed to read record", "err", err.Error(), "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
					}
					errorsCount++
					valid = false
					return
				}
				recordCount++

				// Only process Content-Type: application/http; msgtype=response (no reason to process requests or other records)
				if !strings.Contains(record.Header.Get("Content-Type"), "msgtype=response") {
					logger.Debug("skipping record with Content-Type", "contentType", record.Header.Get("Content-Type"), "recordID", record.Header.Get("WARC-Record-ID"), "file", filepath)
					continue
				}

				// We cannot verify the validity of Payload-Digest on revisit records yet.
				if record.Header.Get("WARC-Type") == "revisit" {
					logger.Debug("skipping revisit record", "recordID", record.Header.Get("WARC-Record-ID"), "file", filepath)
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
			logger.Error("no record in file", "file", filepath)
		}

		// Ensure there is a visible difference when errors are present.
		if errorsCount > 0 {
			logger.Error(fmt.Sprintf("checked in %s", time.Since(startTime).String()), "file", filepath, "valid", valid, "errors", errorsCount, "count", recordCount, "allRecordsRead", allRecordsRead)
		} else {
			logger.Info(fmt.Sprintf("checked in %s", time.Since(startTime).String()), "file", filepath, "valid", valid, "errors", errorsCount, "count", recordCount, "allRecordsRead", allRecordsRead)
		}

	}
}

func verifyPayloadDigest(record *warc.Record, filepath string) (errorsCount int, valid bool) {
	valid = true

	// Verify that the Payload-Digest field exists
	if record.Header.Get("WARC-Payload-Digest") == "" {
		logger.Error("WARC-Payload-Digest is missing", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	// Calculate expected WARC-Payload-Digest
	_, err := record.Content.Seek(0, 0)
	if err != nil {
		logger.Error("failed to seek record content", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	resp, err := http.ReadResponse(bufio.NewReader(record.Content), nil)
	if err != nil {
		logger.Error("failed to read response", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		valid = false
		errorsCount++
		return errorsCount, valid
	}
	defer resp.Body.Close()
	defer record.Content.Seek(0, 0)

	if resp.Header.Get("X-Crawler-Transfer-Encoding") != "" || resp.Header.Get("X-Crawler-Content-Encoding") != "" || resp.Header.Get("x-orig-content-encoding") != "" || resp.Header.Get("x-orig-transfer-encoding") != "" {
		// This header being present in the HTTP headers indicates transfer-encoding and/or content-encoding were incorrectly stripped, causing us to not be able to verify the payload digest.
		logger.Error("malformed headers prevent accurate payload digest calculation", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))

		valid = false
		errorsCount++
		return errorsCount, valid
	}

	digestPrefix := strings.SplitN(record.Header.Get("WARC-Payload-Digest"), ":", 2)[0]
	if !warc.IsDigestSupported(digestPrefix) {
		logger.Error("unsupported payload digest algorithm", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "algorithm", digestPrefix)
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	payloadDigest, err := warc.GetDigest(resp.Body, warc.GetDigestFromPrefix(digestPrefix))
	if err != nil {
		logger.Error("failed to calculate payload digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	if payloadDigest != record.Header.Get("WARC-Payload-Digest") {
		logger.Error("payload digests do not match", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", record.Header.Get("WARC-Payload-Digest"), "got", payloadDigest)
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
		logger.Error("WARC-Block-Digest is missing", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		return 1, false
	}

	digestPrefix := strings.SplitN(blockDigest, ":", 2)[0]

	if !warc.IsDigestSupported(digestPrefix) {
		logger.Error("WARC-Block-Digest uses unsupported algorithm", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "algorithm", digestPrefix)
		return 1, false
	}

	// Calculate and verify the digest
	return verifyDigest(record, filepath, warc.GetDigestFromPrefix(digestPrefix), blockDigest)
}

func verifyDigest(record *warc.Record, filepath string, algorithm warc.DigestAlgorithm, expectedDigest string) (errorsCount int, valid bool) {
	defer record.Content.Seek(0, 0)

	calculatedDigest, err := warc.GetDigest(record.Content, algorithm)
	if err != nil {
		logger.Error("failed to calculate block digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "err", err.Error())
		return 1, false
	}

	if calculatedDigest != expectedDigest {
		logger.Error("WARC-Block-Digest mismatch", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", calculatedDigest, "got", expectedDigest)
		return 1, false
	}

	return 0, true
}

func verifyWARCVersion(record *warc.Record, filepath string) (valid bool) {
	valid = true
	if record.Version != "WARC/1.0" && record.Version != "WARC/1.1" {
		logger.Error("invalid WARC version", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		valid = false
	}

	return valid
}

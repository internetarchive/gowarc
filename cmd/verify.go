package main

import (
	"bufio"
	"fmt"
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
	res.warcVersionValid = verifyWarcVersion(record, filepath)
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
				record, size, err := reader.ReadRecord()
				if size == 0 {
					allRecordsRead = true
					break
				}
				if err != nil {
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

	payloadDigestSplitted := strings.Split(record.Header.Get("WARC-Payload-Digest"), ":")
	if len(payloadDigestSplitted) != 2 {
		logger.Error("malformed WARC-Payload-Digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
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

	if resp.Header.Get("X-Crawler-Transfer-Encoding") != "" || resp.Header.Get("X-Crawler-Content-Encoding") != "" {
		// This header being present in the HTTP headers indicates transfer-encoding and/or content-encoding were incorrectly stripped, causing us to not be able to verify the payload digest.
		logger.Error("malfomed headers prevent accurate payload digest calculation", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))

		valid = false
		errorsCount++
		return errorsCount, valid
	}

	if payloadDigestSplitted[0] == "sha1" {
		payloadDigest := warc.GetSHA1(resp.Body)
		if payloadDigest == "ERROR" {
			logger.Error("failed to calculate payload digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
			valid = false
			errorsCount++
			return errorsCount, valid
		}

		if payloadDigest != payloadDigestSplitted[1] {
			logger.Error("payload digests do not match", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", payloadDigestSplitted[1], "got", payloadDigest)
			valid = false
			errorsCount++
			return errorsCount, valid
		}
	} else if payloadDigestSplitted[0] == "sha256" {
		payloadDigest := warc.GetSHA256Base16(resp.Body)
		if payloadDigest == "ERROR" {
			logger.Error("failed to calculate payload digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
			valid = false
			errorsCount++
			return errorsCount, valid
		}

		if payloadDigest != payloadDigestSplitted[1] {
			logger.Error("payload digests do not match", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", payloadDigestSplitted[1], "got", payloadDigest)
			valid = false
			errorsCount++
			return errorsCount, valid
		}
	} else {
		logger.Error("WARC-Payload-Digest is not SHA1 or SHA256", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	return errorsCount, valid
}

func verifyBlockDigest(record *warc.Record, filepath string) (errorsCount int, valid bool) {
	valid = true

	// Verify that the WARC-Block-Digest is sha1
	if record.Header.Get("WARC-Block-Digest") == "" {
		logger.Error("WARC-Block-Digest is missing", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	blockDigestSplitted := strings.Split(record.Header.Get("WARC-Block-Digest"), ":")
	if len(blockDigestSplitted) != 2 {
		logger.Error("malformed WARC-Block-Digest", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	defer record.Content.Seek(0, 0)

	if blockDigestSplitted[0] == "sha1" {
		expectedPayloadDigest := warc.GetSHA1(record.Content)
		if expectedPayloadDigest != blockDigestSplitted[1] {
			logger.Error("WARC-Block-Digest mismatch", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", expectedPayloadDigest, "got", blockDigestSplitted[1])
			valid = false
			errorsCount++
		}
	} else if blockDigestSplitted[0] == "sha256" {
		expectedPayloadDigest := warc.GetSHA256Base16(record.Content)
		if expectedPayloadDigest != blockDigestSplitted[1] {
			logger.Error("WARC-Block-Digest mismatch", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"), "expected", expectedPayloadDigest, "got", blockDigestSplitted[1])
			valid = false
			errorsCount++
		}
	} else {
		logger.Error("WARC-Block-Digest is not sha1 or sha256", "file", filepath, "recordID", record.Header.Get("WARC-Record-ID"))
		valid = false
		errorsCount++
		return errorsCount, valid
	}

	return errorsCount, valid
}

func verifyWarcVersion(record *warc.Record, filepath string) (valid bool) {
	valid = true
	if record.Version != "WARC/1.0" && record.Version != "WARC/1.1" {
		logger.Error("invalid WARC version", "file", filepath, "recordID", record.Header.Get("WARC-Record)"))
		valid = false
	}

	return valid
}

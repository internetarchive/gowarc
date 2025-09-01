package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	warc "github.com/internetarchive/gowarc"
	"github.com/remeh/sizedwaitgroup"
	"github.com/spf13/cobra"
)

func extract(cmd *cobra.Command, files []string) {
	threads, err := strconv.Atoi(cmd.Flags().Lookup("threads").Value.String())
	if err != nil {
		slog.Error("invalid threads value", "err", err.Error())
		return
	}

	swg := sizedwaitgroup.New(threads)

	for _, filepath := range files {
		startTime := time.Now()
		resultsChan := make(chan string)
		results := make(map[string]int)

		f, err := os.Open(filepath)
		if err != nil {
			slog.Error("unable to open file", "err", err.Error(), "file", filepath)
			return
		}

		reader, err := warc.NewReader(f)
		if err != nil {
			slog.Error("warc.NewReader failed", "err", err.Error(), "file", filepath)
			return
		}

		go func(c chan string) {
			for result := range c {
				results[result]++
			}
		}(resultsChan)

		for {
			record, eol, err := reader.ReadRecord()
			if eol {
				break
			}
			if err != nil {
				slog.Error("failed to read record", "err", err.Error(), "file", filepath)
				return
			}

			swg.Add()
			go processRecord(cmd, record, &resultsChan, &swg)
		}

		swg.Wait()
		close(resultsChan)

		printExtractReport(filepath, results, time.Since(startTime))
	}
}

func processRecord(cmd *cobra.Command, record *warc.Record, resultsChan *chan string, swg *sizedwaitgroup.SizedWaitGroup) {
	defer record.Content.Close()
	defer swg.Done()

	// Only process Content-Type: application/http; msgtype=response (no reason to process requests or other records)
	if !strings.Contains(record.Header.Get("Content-Type"), "msgtype=response") {
		slog.Debug("skipping record with Content-Type", "contentType", record.Header.Get("Content-Type"), "recordID", record.Header.Get("WARC-Record-ID"))
		return
	}

	if record.Header.Get("WARC-Type") == "revisit" {
		slog.Debug("skipping revisit record", "recordID", record.Header.Get("WARC-Record-ID"))
		return
	}

	// Read the entire record.Content into a bufio.Reader
	response, err := http.ReadResponse(bufio.NewReader(record.Content), nil)
	if err != nil {
		slog.Error("failed to read response", "err", err.Error())
		return
	}

	// If the response's Content-Type match one of the content types to extract, write the file
	contentTypesToExtract := strings.Split(strings.Trim(cmd.Flags().Lookup("content-type").Value.String(), "[]"), ",")

	if slices.ContainsFunc(contentTypesToExtract, func(s string) bool {
		return strings.Contains(response.Header.Get("Content-Type"), s)
	}) {
		err = writeFile(cmd, response, record)
		if err != nil {
			slog.Error("failed to write file", "err", err.Error())
			return
		}

		// Send the result to the results channel
		*resultsChan <- response.Header.Get("Content-Type")
	}
}

func writeFile(vmd *cobra.Command, resp *http.Response, record *warc.Record) error {
	// Find the filename either from the Content-Disposition header or the last part of the URL
	filename := path.Base(record.Header.Get("WARC-Target-URI"))

	if resp.Header.Get("Content-Disposition") != "" {
		_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
		if err == nil {
			if params["filename"] != "" {
				filename = params["filename"]
			}
		} else {
			slog.Debug("failed to parse Content-Disposition header", "err", err.Error())

			if !strings.HasSuffix(filename, ".pdf") {
				filename += ".pdf"
			}
		}
	}

	// Truncate the filename if it's too long (keep the extension)
	if len(filename) > 255 {
		extension := path.Ext(filename)

		filename = filename[:255-len(extension)] + extension
	}

	// Remove any invalid characters from the filename
	filename = strings.ReplaceAll(filename, "/", "_")

	// Check if the file already exists
	outputDir := vmd.Flags().Lookup("output").Value.String()

	// Create the output directory if it doesn't exist.
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		err := os.MkdirAll(outputDir, 0755)
		if err != nil {
			return err
		}
	}

	// Check if --host-sort is enabled, if yes extract the host from the WARC-Target-URI and put the file in a subdirectory
	if vmd.Flags().Lookup("host-sort").Changed {
		URI := record.Header.Get("WARC-Target-URI")
		URL, err := url.Parse(URI)
		if err != nil {
			return err
		}

		err = os.MkdirAll(path.Join(outputDir, URL.Host), 0755)
		if err != nil {
			return err
		}

		outputDir = path.Join(outputDir, URL.Host)
	}

	outputPath := path.Join(outputDir, filename)
	if _, err := os.Stat(outputPath); err == nil {
		if vmd.Flags().Lookup("hash-suffix").Changed {
			// Read the file to check the hash.
			originalFile, err := os.Open(outputPath)
			if err != nil {
				return err
			}

			defer originalFile.Close()

			body, err := io.ReadAll(resp.Body)

			if err != nil {
				return err
			}

			var reader io.Reader

			if resp.Header.Get("Content-Encoding") == "gzip" {
				reader, err = gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					return err
				}
			} else {
				reader = bytes.NewReader(body)
			}

			payloadDigest, err := warc.GetDigest(reader, warc.SHA1)
			if err != nil {
				return err
			}

			// Reset response reader
			resp.Body = io.NopCloser(bytes.NewBuffer(body))

			originalPayloadDigest, err := warc.GetDigest(originalFile, warc.SHA1)
			if err != nil {
				return err
			}

			if originalPayloadDigest != payloadDigest {
				if len(filename) > 247 {
					extension := path.Ext(filename)

					filename = filename[:247-len(extension)] + "[" + payloadDigest[26:] + "]" + extension
				} else {
					extension := path.Ext(filename)

					filename = filename[:len(filename)-len(extension)] + "[" + payloadDigest[26:] + "]" + extension
				}

				outputPath = path.Join(outputDir, filename)
				// Double check that the new file doesn't exist
				if _, err := os.Stat(outputPath); err == nil {
					if !vmd.Flags().Lookup("allow-overwrite").Changed {
						slog.Info("file already exists, skipping", "file", filename)
						return nil
					}
				}
			} else {
				// Matches!
				slog.Info("file already exists and hash matches, skipping", "file", filename)
				return nil
			}

		} else if !vmd.Flags().Lookup("allow-overwrite").Changed {
			slog.Info("file already exists, skipping", "file", filename)
			return nil
		}
	}

	// Create the file
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Close body when finished.
	defer resp.Body.Close()

	var reader io.ReadCloser

	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return err
		}
		defer reader.Close()
	default:
		reader = resp.Body
	}

	// Write the response body to the file
	_, err = io.Copy(file, reader)
	if err != nil {
		return err
	}

	return nil
}

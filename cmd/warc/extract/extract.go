package extract

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	warc "github.com/internetarchive/gowarc"
	"github.com/internetarchive/gowarc/cmd/warc/utils"
	"github.com/remeh/sizedwaitgroup"
	"github.com/spf13/cobra"
)

// Command represents the extract command
var Command = &cobra.Command{
	Use:   "extract",
	Short: "Extracts the URLs from one or many WARC file(s)",
	Long:  `Extracts the URLs from one or many WARC file(s)`,
	Args:  cobra.MinimumNArgs(1),
	Run:   extract,
}

func init() {
	Command.Flags().IntP("threads", "t", 1, "Number of threads to use for extraction")
	Command.Flags().StringP("output", "o", "output", "Output directory for extracted files")
	Command.Flags().StringSliceP("content-type", "c", []string{}, "Content type that should be extracted")
	Command.Flags().Bool("allow-overwrite", false, "Allow overwriting of existing files")
	Command.Flags().Bool("host-sort", false, "Sort the extracted URLs by host")
	Command.Flags().Bool("hash-suffix", false, "When duplicate file names exist, the hash will be added if a duplicate file name exists. ")
}

func extract(cmd *cobra.Command, files []string) {
	threads := utils.GetThreadsFlag(cmd)

	swg := sizedwaitgroup.New(threads)

	for _, filepath := range files {
		startTime := time.Now()
		resultsChan := make(chan string)
		results := make(map[string]int)

		reader, f, err := utils.OpenWARCFile(filepath)
		if err != nil {
			return
		}
		defer f.Close()

		go func(c chan string) {
			for result := range c {
				results[result]++
			}
		}(resultsChan)

		for {
			record, err := reader.ReadRecord()
			if err != nil {
				if err == io.EOF {
					break
				}
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

	if utils.ShouldSkipRecord(record) {
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
	if len(filename) > utils.MaxFilenameLength {
		extension := path.Ext(filename)
		filename = filename[:utils.MaxFilenameLength-len(extension)] + extension
	}

	// Remove any invalid characters from the filename
	filename = strings.ReplaceAll(filename, "/", "_")

	// Check if the file already exists
	outputDir := vmd.Flags().Lookup("output").Value.String()

	// Create the output directory if it doesn't exist.
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		err := os.MkdirAll(outputDir, utils.DefaultDirPermissions)
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

		err = os.MkdirAll(path.Join(outputDir, URL.Host), utils.DefaultDirPermissions)
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
				if len(filename) > utils.MaxFilenameWithHashLength {
					extension := path.Ext(filename)

					filename = filename[:utils.MaxFilenameWithHashLength-len(extension)] + "[" + payloadDigest[26:] + "]" + extension
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
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY, utils.DefaultFilePermissions)
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

func printExtractReport(filePath string, results map[string]int, elapsed time.Duration) {
	total := 0

	for _, v := range results {
		total += v
	}

	slog.Info(fmt.Sprintf("Processed file %s in %s", filePath, elapsed.String()))
	slog.Info(fmt.Sprintf("Number of files extracted: %d", total))
	for k, v := range results {
		slog.Info(fmt.Sprintf("- %s: %d\n", k, v))
	}
}

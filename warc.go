package warc

import (
	"errors"
	"os"
	"path"
	"strings"
	"sync/atomic"
)

// RotatorSettings is used to store the settings
// needed by recordWriter to write WARC files
type RotatorSettings struct {
	// Content of the warcinfo record that will be written
	// to all WARC files
	WarcinfoContent Header
	// Prefix used for WARC filenames, WARC 1.1 specifications
	// recommend to name files this way:
	// Prefix-Timestamp-Serial-Crawlhost.warc.gz
	Prefix string
	// Compression algorithm to use
	Compression string
	// Path to a ZSTD compression dictionary to embed (and use) in .warc.zst files
	CompressionDictionary string
	// Directory where the created WARC files will be stored,
	// default will be the current directory
	OutputDirectory string
	// WarcSize is in Megabytes
	WarcSize float64
	// WARCWriterPoolSize defines the number of parallel WARC writers
	WARCWriterPoolSize int
}

var (
	// Create a couple of counters for tracking various stats
	DataTotal atomic.Int64

	CDXDedupeTotalBytes          atomic.Int64
	DoppelgangerDedupeTotalBytes atomic.Int64
	LocalDedupeTotalBytes        atomic.Int64

	CDXDedupeTotal          atomic.Int64
	DoppelgangerDedupeTotal atomic.Int64
	LocalDedupeTotal        atomic.Int64
)

// NewWARCRotator creates and return a channel that can be used
// to communicate records to be written to WARC files to the
// recordWriter function running in a goroutine
func (s *RotatorSettings) NewWARCRotator() (recordWriterChan chan *RecordBatch, doneChannels []chan bool, err error) {
	recordWriterChan = make(chan *RecordBatch, 1)

	// Create global atomicSerial number for numbering WARC files.
	var serial = new(atomic.Uint64)

	// Check the rotator settings and set default values
	err = checkRotatorSettings(s)
	if err != nil {
		return recordWriterChan, doneChannels, err
	}

	for i := 0; i < s.WARCWriterPoolSize; i++ {
		doneChan := make(chan bool)
		doneChannels = append(doneChannels, doneChan)

		go recordWriter(s, recordWriterChan, doneChan, serial)
	}

	return recordWriterChan, doneChannels, nil
}

func (w *Writer) CloseCompressedWriter() (err error) {
	if w.GZIPWriter != nil {
		err = w.GZIPWriter.Close()
	} else if w.ZSTDWriter != nil {
		err = w.ZSTDWriter.Close()
	}

	return err
}

func getNextWarcFileName(outputDir, prefix, compression string, serial *atomic.Uint64) (nextFileName string) {
	_, err := os.Stat(outputDir + nextFileName)
	for !errors.Is(err, os.ErrNotExist) {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			panic(err)
		}

		nextFileName = generateWarcFileName(prefix, compression, serial)
		_, err = os.Stat(outputDir + nextFileName)
	}

	return
}

func recordWriter(settings *RotatorSettings, records chan *RecordBatch, done chan bool, serial *atomic.Uint64) {
	var (
		currentFileName         = getNextWarcFileName(settings.OutputDirectory, settings.Prefix, settings.Compression, serial)
		currentWarcinfoRecordID string
	)

	// Create and open the initial file
	warcFile, err := os.Create(settings.OutputDirectory + currentFileName)
	if err != nil {
		panic(err)
	}

	var dictionary []byte

	if settings.CompressionDictionary != "" {
		dictionary, err = os.ReadFile(settings.CompressionDictionary)
		if err != nil {
			panic(err)
		}
	}

	// Initialize WARC writer
	warcWriter, err := NewWriter(warcFile, currentFileName, settings.Compression, "", true, dictionary)
	if err != nil {
		panic(err)
	}

	// Write the info record
	currentWarcinfoRecordID, err = warcWriter.WriteInfoRecord(settings.WarcinfoContent)
	if err != nil {
		panic(err)
	}

	// If compression is enabled, we close the record's GZIP chunk
	if settings.Compression != "" {
		err = warcWriter.CloseCompressedWriter()
		if err != nil {
			panic(err)
		}

		warcWriter, err = NewWriter(warcFile, currentFileName, settings.Compression, "", false, dictionary)
		if err != nil {
			panic(err)
		}
	}

	for {
		recordBatch, more := <-records
		if more {
			if isFileSizeExceeded(warcFile, settings.WarcSize) {
				// WARC file size exceeded settings.WarcSize
				// The WARC file is renamed to remove the .open suffix
				err := os.Rename(path.Join(settings.OutputDirectory, currentFileName), strings.TrimSuffix(path.Join(settings.OutputDirectory, currentFileName), ".open"))
				if err != nil {
					panic(err)
				}

				// We flush the data and close the file
				warcWriter.FileWriter.Flush()
				if settings.Compression != "" {
					err = warcWriter.CloseCompressedWriter()
					if err != nil {
						panic(err)
					}
				}

				err = warcFile.Close()
				if err != nil {
					panic(err)
				}

				// Create the new file and automatically increment the serial inside of GenerateWarcFileName
				currentFileName = getNextWarcFileName(settings.OutputDirectory, settings.Prefix, settings.Compression, serial)
				warcFile, err = os.Create(settings.OutputDirectory + currentFileName)
				if err != nil {
					panic(err)
				}

				// Initialize new WARC writer
				warcWriter, err = NewWriter(warcFile, currentFileName, settings.Compression, "", true, dictionary)
				if err != nil {
					panic(err)
				}

				// Write the info record
				currentWarcinfoRecordID, err = warcWriter.WriteInfoRecord(settings.WarcinfoContent)
				if err != nil {
					panic(err)
				}

				// If compression is enabled, we close the record's GZIP chunk
				if settings.Compression != "" {
					err = warcWriter.CloseCompressedWriter()
					if err != nil {
						panic(err)
					}
				}
			}

			// Write all the records of the record batch
			for _, record := range recordBatch.Records {
				warcWriter, err = NewWriter(warcFile, currentFileName, settings.Compression, record.Header.Get("Content-Length"), false, dictionary)
				if err != nil {
					panic(err)
				}

				record.Header.Set("WARC-Date", recordBatch.CaptureTime)
				record.Header.Set("WARC-Warcinfo-ID", "<urn:uuid:"+currentWarcinfoRecordID+">")

				_, err := warcWriter.WriteRecord(record)
				if err != nil {
					panic(err)
				}

				// If compression is enabled, we close the record's GZIP chunk
				if settings.Compression != "" {
					err = warcWriter.CloseCompressedWriter()
					if err != nil {
						panic(err)
					}
				}
			}

			err = warcWriter.FileWriter.Flush()
			if err != nil {
				panic(err)
			}

			if recordBatch.FeedbackChan != nil {
				recordBatch.FeedbackChan <- struct{}{}
				close(recordBatch.FeedbackChan)
			}
		} else {
			// Channel has been closed
			// We flush the data, close the file, and rename it
			warcWriter.FileWriter.Flush()
			if settings.Compression != "" {
				err = warcWriter.CloseCompressedWriter()
				if err != nil {
					panic(err)
				}
			}

			err = warcFile.Close()
			if err != nil {
				panic(err)
			}

			// The WARC file is renamed to remove the .open suffix
			err := os.Rename(settings.OutputDirectory+currentFileName, strings.TrimSuffix(settings.OutputDirectory+currentFileName, ".open"))
			if err != nil {
				panic(err)
			}

			done <- true

			return
		}
	}
}

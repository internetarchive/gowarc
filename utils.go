package warc

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/internetarchive/gowarc/pkg/spooledtempfile"
	gzip "github.com/klauspost/compress/gzip"

	"github.com/klauspost/compress/zstd"
)

func GetSHA1(r io.Reader) string {
	sha := sha1.New()
	_, err := io.Copy(sha, r)
	if err != nil {
		return "ERROR: " + err.Error()
	}
	return base32.StdEncoding.EncodeToString(sha.Sum(nil))
}

func GetSHA256(r io.Reader) string {
	sha := sha256.New()
	_, err := io.Copy(sha, r)
	if err != nil {
		return "ERROR: " + err.Error()
	}
	return base32.StdEncoding.EncodeToString(sha.Sum(nil))
}

func GetSHA256Base16(r io.Reader) string {
	sha := sha256.New()
	_, err := io.Copy(sha, r)
	if err != nil {
		return "ERROR: " + err.Error()
	}
	return hex.EncodeToString(sha.Sum(nil))
}

// splitKeyValue parses WARC record header fields.
func splitKeyValue(line string) (string, string) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], strings.TrimSpace(parts[1])
}

func isHTTPRequest(line string) bool {
	httpMethods := []string{"GET ", "HEAD ", "POST ", "PUT ", "DELETE ", "CONNECT ", "OPTIONS ", "TRACE ", "PATCH "}
	protocols := []string{"HTTP/1.0", "HTTP/1.1"}

	for _, method := range httpMethods {
		if strings.HasPrefix(line, method) {
			for _, protocol := range protocols {
				if strings.HasSuffix(line, protocol) {
					return true
				}
			}
		}
	}
	return false
}

// NewWriter creates a new WARC writer.
func NewWriter(writer io.Writer, fileName string, compression string, contentLengthHeader string, newFileCreation bool, dictionary []byte) (*Writer, error) {
	if compression != "" {
		if compression == "GZIP" {
			gzipWriter := gzip.NewWriter(writer)

			return &Writer{
				FileName:    fileName,
				Compression: compression,
				GZIPWriter:  gzipWriter,
				FileWriter:  bufio.NewWriter(gzipWriter),
			}, nil
		} else if compression == "ZSTD" {
			if newFileCreation && len(dictionary) > 0 {
				dictionaryZstdwriter, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
				if err != nil {
					return nil, err
				}

				// Compress dictionary with ZSTD.
				// TODO: Option to allow uncompressed dictionary (maybe? not sure there's any need.)
				payload := dictionaryZstdwriter.EncodeAll(dictionary, nil)

				// Magic number for skippable dictionary frame (0x184D2A5D).
				// https://github.com/ArchiveTeam/wget-lua/releases/tag/v1.20.3-at.20200401.01
				// https://iipc.github.io/warc-specifications/specifications/warc-zstd/
				magic := uint32(0x184D2A5D)

				// Create the frame header (magic + payload size)
				header := make([]byte, 8)
				binary.LittleEndian.PutUint32(header[:4], magic)
				binary.LittleEndian.PutUint32(header[4:], uint32(len(payload)))

				// Combine header and payload together into a full frame.
				frame := append(header, payload...)

				// Write generated frame directly to WARC file.
				// The regular ZStandard writer will continue afterwards with normal ZStandard frames.
				writer.Write(frame)
			}

			// Create ZStandard writer either with or without the encoder dictionary and return it.
			if len(dictionary) > 0 {
				zstdWriter, err := zstd.NewWriter(writer, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderDict(dictionary))
				if err != nil {
					return nil, err
				}
				return &Writer{
					FileName:    fileName,
					Compression: compression,
					ZSTDWriter:  zstdWriter,
					FileWriter:  bufio.NewWriter(zstdWriter),
				}, nil
			} else {
				zstdWriter, err := zstd.NewWriter(writer, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
				if err != nil {
					return nil, err
				}
				return &Writer{
					FileName:    fileName,
					Compression: compression,
					ZSTDWriter:  zstdWriter,
					FileWriter:  bufio.NewWriter(zstdWriter),
				}, nil
			}
		}
		return nil, errors.New("invalid compression algorithm: " + compression)
	}

	return &Writer{
		FileName:    fileName,
		Compression: "",
		FileWriter:  bufio.NewWriter(writer),
	}, nil
}

// NewRecord creates a new WARC record.
func NewRecord(tempDir string, fullOnDisk bool) *Record {
	return &Record{
		Header:  NewHeader(),
		Content: spooledtempfile.NewSpooledTempFile("warc", tempDir, -1, fullOnDisk, -1),
	}
}

// NewRecordBatch creates a record batch, it also initialize the capture time.
func NewRecordBatch(feedbackChan chan struct{}) *RecordBatch {
	return &RecordBatch{
		CaptureTime:  time.Now().UTC().Format(time.RFC3339Nano),
		FeedbackChan: feedbackChan,
	}
}

// NewRotatorSettings creates a RotatorSettings structure
// and initialize it with default values
func NewRotatorSettings() *RotatorSettings {
	return &RotatorSettings{
		WarcinfoContent:       NewHeader(),
		Prefix:                "WARC",
		WarcSize:              1000,
		Compression:           "GZIP",
		CompressionDictionary: "",
		OutputDirectory:       "./",
	}
}

// checkRotatorSettings validate RotatorSettings settings, and set
// default values if needed
func checkRotatorSettings(settings *RotatorSettings) (err error) {
	// Get host name as reported by the kernel
	hostName, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	// Check if output directory is specified, if not, set it to the current directory
	if settings.OutputDirectory == "" {
		settings.OutputDirectory = "./"
	} else {
		// If it is specified, check if output directory exist
		if _, err := os.Stat(settings.OutputDirectory); os.IsNotExist(err) {
			// If it doesn't exist, create it
			// MkdirAll will create all parent directories if needed
			err = os.MkdirAll(settings.OutputDirectory, os.ModePerm)
			if err != nil {
				return err
			}
		}
	}

	if settings.WARCWriterPoolSize == 0 {
		settings.WARCWriterPoolSize = 1
	}

	// Add a trailing slash to the output directory
	if settings.OutputDirectory[len(settings.OutputDirectory)-1:] != "/" {
		settings.OutputDirectory = settings.OutputDirectory + "/"
	}

	// If prefix isn't specified, set it to "WARC"
	if settings.Prefix == "" {
		settings.Prefix = "WARC"
	}

	// If WARC size isn't specified, set it to 1GB (10^9 bytes) by default
	if settings.WarcSize == 0 {
		settings.WarcSize = 1000
	}

	// Check if the specified compression algorithm is valid
	if settings.Compression != "" && settings.Compression != "GZIP" && settings.Compression != "ZSTD" {
		return errors.New("invalid compression algorithm: " + settings.Compression)
	}

	// Add few headers to the warcinfo payload, to not have it empty
	settings.WarcinfoContent.Set("hostname", hostName)
	settings.WarcinfoContent.Set("format", "WARC file version 1.1")
	settings.WarcinfoContent.Set("conformsTo", "http://iipc.github.io/warc-specifications/specifications/warc-format/warc-1.1/")

	return nil
}

func getContentLength(rwsc spooledtempfile.ReadWriteSeekCloser) int {
	// If the FileName leads to no existing file, it means that the SpooledTempFile
	// never had the chance to buffer to disk instead of memory, in which case we can
	// just read the buffer (which should be <= 2MB) and return the length
	if rwsc.FileName() == "" {
		rwsc.Seek(0, 0)
		buf := new(bytes.Buffer)
		buf.ReadFrom(rwsc)
		return buf.Len()
	} else {
		// Else, we return the size of the file on disk
		fileInfo, err := os.Stat(rwsc.FileName())
		if err != nil {
			panic(err)
		}

		return int(fileInfo.Size())
	}
}

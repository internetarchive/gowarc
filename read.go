package warc

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/internetarchive/gowarc/pkg/spooledtempfile"
)

// Reader stores the bufio.Reader and gzip.Reader for a WARC file
type Reader struct {
	bufReader *bufio.Reader
	record    *Record
	threshold int
}

type reader interface {
	ReadBytes(delim byte) (line []byte, err error)
}

// NewReader returns a new WARC reader
func NewReader(reader io.ReadCloser) (*Reader, error) {
	decReader, err := NewDecompressionReader(reader)
	if err != nil {
		return nil, err
	}
	bufioReader := bufio.NewReader(decReader)
	thresholdString := os.Getenv("WARCMaxInMemorySize")
	threshold := -1
	if thresholdString != "" {
		threshold, err = strconv.Atoi(thresholdString)
		if err != nil {
			return nil, err
		}
	}
	return &Reader{
		bufReader: bufioReader,
		threshold: threshold,
	}, nil
}

// readUntilDelim reads until delim (inclusive) and returns the line (without delim)
// and the number of bytes consumed (including the delim).
func readUntilDelim(r reader, delim []byte) (line []byte, n int64, err error) {
	for {
		var chunk []byte
		chunk, err = r.ReadBytes(delim[len(delim)-1])
		n += int64(len(chunk))
		line = append(line, chunk...)
		if err != nil {
			// return what we have (may be partial) along with the error
			return line, n, err
		}
		if bytes.HasSuffix(line, delim) {
			// drop the delimiter from the returned line
			return line[:len(line)-len(delim)], n, nil
		}
	}
}

// ReadRecord reads the next record from the opened WARC file.
// Returns:
//   - *Record: nil when at clean EOF (no more records).
//   - int64: total bytes consumed to read this record (version + headers + content + trailing CRLF CRLF).
//   - error: any parsing/IO error encountered (nil for clean EOF).
func (r *Reader) ReadRecord(opts ...ReadOpts) (*Record, int64, error) {
	var (
		err            error
		tempReader     = bufio.NewReader(r.bufReader)
		discardContent bool
		bytesRead      int64
	)

	for _, opt := range opts {
		switch opt {
		case ReadOptsNoContentOutput:
			discardContent = true
		}
	}

	// first line: WARC version
	warcVer, n, err := readUntilDelim(tempReader, []byte("\r\n"))
	bytesRead += n
	if err != nil {
		if err == io.EOF && len(warcVer) == 0 {
			// clean EOF: no more records
			return nil, 0, nil
		}
		return nil, bytesRead, fmt.Errorf("reading WARC version: %w", err)
	}

	// Parse the record headers
	header := NewHeader()
	for {
		line, n, err := readUntilDelim(tempReader, []byte("\r\n"))
		bytesRead += n
		if err != nil {
			return nil, bytesRead, fmt.Errorf("reading header: %w", err)
		}
		if len(line) == 0 {
			break
		}
		if key, value := splitKeyValue(string(line)); key != "" {
			header.Set(key, value)
		}
	}

	// Get the Content-Length
	length, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64)
	if err != nil {
		return nil, bytesRead, fmt.Errorf("parsing Content-Length: %w", err)
	}

	// reading doesn't really need to be in TempDir, nor can we access it as it's on the client.
	buf := spooledtempfile.NewSpooledTempFile("warc", "", r.threshold, false, -1)
	var copied int64
	if discardContent {
		copied, err = io.CopyN(io.Discard, tempReader, length)
	} else {
		copied, err = io.CopyN(buf, tempReader, length)
	}
	bytesRead += copied
	if err != nil {
		return nil, bytesRead, fmt.Errorf("copying record content: %w", err)
	}

	r.record = &Record{
		Header:  header,
		Content: buf,
		Version: string(warcVer),
	}

	// Skip two empty lines (record boundary). WARC specifies CRLF, so count +2 per line.
	for i := 0; i < 2; i++ {
		boundary, _, err := r.bufReader.ReadLine()
		// Count consumed boundary line including CRLF. (bufio.ReadLine strips EOL.)
		bytesRead += int64(len(boundary)) + 2
		if err != nil {
			if err == io.EOF {
				// record shall consist of a record header followed by a record content block and two newlines
				return r.record, bytesRead, fmt.Errorf("early EOF record boundary: %w", err)
			}
			return r.record, bytesRead, fmt.Errorf("reading record boundary: %w", err)
		}
		if len(boundary) != 0 {
			return r.record, bytesRead, fmt.Errorf("non-empty record boundary [boundary: %s]", boundary)
		}
	}

	return r.record, bytesRead, nil
}

// ReadOpts are options for ReadRecord
type ReadOpts int

const (
	// ReadOptsNoContentOutput means that the content of the record should not be returned.
	// This is useful for reading only the headers or metadata of the record.
	ReadOptsNoContentOutput ReadOpts = iota
)

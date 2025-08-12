package warc

import (
	"bufio"
	"bytes"
	"errors"
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

// readUntilDelim reads from r until the multi-byte delimiter `delim` is found.
// It returns the bytes BEFORE the delimiter, the total number of bytes consumed
// from r (including the delimiter), and an error. If EOF occurs before seeing
// the delimiter, it returns the data read and io.EOF.
func readUntilDelim(r *bufio.Reader, delim []byte) (line []byte, n int64, err error) {
	if len(delim) == 0 {
		return nil, 0, errors.New("empty delimiter")
	}

	var buf bytes.Buffer
	window := make([]byte, 0, len(delim))

	for {
		b, e := r.ReadByte()
		if e != nil {
			if e == io.EOF {
				if buf.Len() == 0 {
					return nil, n, io.EOF
				}
				return buf.Bytes(), n, io.EOF
			}
			return buf.Bytes(), n, e
		}

		n++
		_ = buf.WriteByte(b)

		if len(window) < len(delim) {
			window = append(window, b)
		} else {
			copy(window, window[1:])
			window[len(window)-1] = b
		}

		if len(window) == len(delim) && bytes.Equal(window, delim) {
			buf.Truncate(buf.Len() - len(delim))
			return buf.Bytes(), n, nil
		}
	}
}

// readUntilDelimChunked reads from r until the multi-byte delimiter `delim` is found.
// It returns the bytes BEFORE the delimiter, the total number of bytes consumed
// from r (including the delimiter), and an error. If EOF occurs before seeing
// the delimiter, it returns the data read and io.EOF.
// This function is designed to handle larger inputs by reading in chunks.
func readUntilDelimChunked(r *bufio.Reader, delim []byte) (line []byte, n int64, err error) {
	if len(delim) == 0 {
		return nil, 0, errors.New("empty delimiter")
	}
	last := delim[len(delim)-1]
	var buf []byte

	for {
		part, e := r.ReadSlice(last)
		n += int64(len(part))
		buf = append(buf, part...)

		start := len(buf) - len(part) - (len(delim) - 1)
		if start < 0 {
			start = 0
		}
		if i := bytes.Index(buf[start:], delim); i >= 0 {
			i += start
			return buf[:i], n, nil
		}
		if e != nil {
			if e == bufio.ErrBufferFull {
				continue
			}
			if e == io.EOF {
				return buf, n, io.EOF
			}
			return buf, n, e
		}
	}
}

// ReadRecord reads the next record from the opened WARC file.
// Returns:
//   - *Record: nil when at clean EOF (no more records).
//   - int64: total bytes of the decompressed record (version + headers + content + trailing CRLF CRLF).
//   - error: any parsing/IO error encountered (nil for clean EOF).
func (r *Reader) ReadRecord(opts ...ReadOpts) (*Record, int64, error) {
	var (
		err            error
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
	warcVer, n, err := readUntilDelimChunked(r.bufReader, []byte("\r\n"))
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
		line, n, err := readUntilDelimChunked(r.bufReader, []byte("\r\n"))
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
		copied, err = io.CopyN(io.Discard, r.bufReader, length)
	} else {
		copied, err = io.CopyN(buf, r.bufReader, length)
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
		boundary, n, err := readUntilDelimChunked(r.bufReader, []byte("\r\n"))
		// Count consumed boundary line including CRLF. (bufio.ReadLine strips EOL.)
		bytesRead += n
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

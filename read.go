package warc

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/internetarchive/gowarc/pkg/spooledtempfile"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Reader stores the bufio.Reader and gzip.Reader for a WARC file
type Reader struct {
	record    *Record
	threshold int

	src       io.ReadCloser   // raw concatenated .gz input
	cr        *countingReader // counts compressed bytes actually consumed
	dec       io.ReadCloser   // current decompressor (gz or plain)
	bufReader *bufio.Reader   // consuming layer

	inited   bool          // lazy init done
	compType decReaderType // compression type
}

// countingReader counts bytes read from the underlying compressed stream.
// It must sit *above* the bufio.Reader used for the decompressor to avoid
// counting upstream prefetch.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
func (c *countingReader) N() int64 { return c.n }

// ReadByte reads a single byte from the underlying reader and counts it. To satisfy the io.ByteReader interface.
func (c *countingReader) ReadByte() (byte, error) {
	b := make([]byte, 1)
	n, err := c.r.Read(b)
	if n == 0 {
		return 0, err
	}
	if err != nil && err != io.EOF {
		return 0, err
	}
	c.n += int64(n)
	return b[0], nil
}

// NewReader returns a new WARC reader
func NewReader(reader io.ReadCloser) (*Reader, error) {
	threshold := -1
	if s := os.Getenv("WARCMaxInMemorySize"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			threshold = v
		} else {
			return nil, err
		}
	}
	return &Reader{
		src:       reader, // keep raw source
		threshold: threshold,
	}, nil
}

func readExactly(r io.Reader, n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(r, b) // ReadFull never over-reads
	if err != nil {
		return nil, err
	}
	return b, nil
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
	// preallocating buffer makes performances worse for head/mid placements
	// due to the unnecessary zeroing of the memory
	// and yields low ot no improvements for end/none placements
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
//
// Returns:
//   - *Record: nil when at clean EOF (no more records).
//   - int64:   COMPRESSED size of the record (gzip member): header + deflate data + trailer.
//   - error:   any parsing/IO error encountered (nil for clean EOF).
func (r *Reader) ReadRecord(opts ...ReadOpts) (*Record, int64, error) {
	var (
		discardContent bool
		readFn         = readUntilDelimChunked
	)
	for _, opt := range opts {
		switch opt {
		case ReadOptsNoContentOutput:
			discardContent = true
		case ReadOptsBytewiseRead:
			readFn = readUntilDelim
		}
	}

	// lazy init
	if r.cr == nil {
		r.cr = &countingReader{r: r.src}
	}

	startCompressed := r.cr.N()

	if !r.inited {
		var err error
		r.dec, r.compType, err = newDecompressionReader(r.cr)
		if err != nil {
			return nil, 0, fmt.Errorf("init decompression reader: %w", err)
		}

		r.bufReader = bufio.NewReader(r.dec) // prefetch is fine on *decompressed* side now
		r.inited = true
	} else {
		if r.compType == decReaderGZip {
			if err := r.dec.(*gzip.Reader).Reset(r.cr); err == io.EOF {
				// No more members: clean EOF.
				return nil, 0, nil
			} else if err != nil {
				return nil, 0, fmt.Errorf("gzip reset: %w", err)
			}
			r.dec.(*gzip.Reader).Multistream(false)
			r.bufReader = bufio.NewReader(r.dec) // fresh buffer for new member
		}
	}

	warcVer, _, err := readFn(r.bufReader, []byte("\r\n"))
	if err != nil {
		if err == io.EOF && len(warcVer) == 0 {
			// treat as EOF for safety if member present but empty
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("reading WARC version: %w", err)
	}

	header := NewHeader()
	for {
		line, _, err := readFn(r.bufReader, []byte("\r\n"))
		if err != nil {
			return nil, 0, fmt.Errorf("reading header: %w", err)
		}
		if len(line) == 0 {
			break
		}
		if key, value := splitKeyValue(string(line)); key != "" {
			header.Set(key, value)
		}
	}

	length, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("parsing Content-Length: %w", err)
	}

	buf := spooledtempfile.NewSpooledTempFile("warc", "", r.threshold, false, -1)
	if discardContent {
		if _, err := io.CopyN(io.Discard, r.bufReader, length); err != nil {
			return nil, 0, fmt.Errorf("copying content (discard): %w", err)
		}
	} else {
		if _, err := io.CopyN(buf, r.bufReader, length); err != nil {
			return nil, 0, fmt.Errorf("copying content: %w", err)
		}
	}

	r.record = &Record{
		Header:  header,
		Content: buf,
		Version: string(warcVer),
	}

	for range 2 {
		boundary, _, err := readFn(r.bufReader, []byte("\r\n"))
		if err != nil {
			return r.record, 0, fmt.Errorf("reading record boundary: %w", err)
		}
		if len(boundary) != 0 {
			return r.record, 0, fmt.Errorf("non-empty record boundary [boundary: %s]", boundary)
		}
	}

	if r.compType == decReaderGZip {
		if _, derr := io.Copy(io.Discard, r.bufReader); derr != nil && derr != io.EOF {
			return r.record, 0, fmt.Errorf("draining gzip member: %w", derr)
		}
	}

	compressedSize := r.cr.N() - startCompressed
	return r.record, compressedSize, nil
}

// ReadOpts are options for ReadRecord
type ReadOpts int

const (
	// ReadOptsNoContentOutput means that the content of the record should not be returned.
	// This is useful for reading only the headers or metadata of the record.
	ReadOptsNoContentOutput ReadOpts = iota
	// ReadOptsBytewiseRead means that the record should be read byte by byte.
	// This is provided for testing purposes as the chunked read is benchmarked to be more efficient.
	ReadOptsBytewiseRead
)

// The following code was copied and adapted from https://github.com/crissyfield/troll-a/blob/main/pkg/fetch/decompression-reader.go , under Apache-2.0 License
// Author: [Crissy Field](https://github.com/crissyfield)

const (
	magicGZip               = "\x1f\x8b"                 // Magic bytes for the Gzip format (RFC 1952, section 2.3.1)
	magicBZip2              = "\x42\x5a"                 // Magic bytes for the BZip2 format (no formal spec exists)
	magicXZ                 = "\xfd\x37\x7a\x58\x5a\x00" // Magic bytes for the XZ format (https://tukaani.org/xz/xz-file-format.txt)
	magicZStdFrame          = "\x28\xb5\x2f\xfd"         // Magic bytes for the ZStd frame format (RFC 8478, section 3.1.1)
	magicZStdSkippableFrame = "\x2a\x4d\x18"             // Magic bytes for the ZStd skippable frame format (RFC 8478, section 3.1.2)
)

type decReaderType int

const (
	decReaderGZip decReaderType = iota
	decReaderBZip2
	decReaderXZ
	decReaderZStd
	decReaderNone
)

// newDecompressionReader will return a new reader transparently doing decompression of GZip, BZip2, XZ, and ZStd.
// Only GZip is tested and used in production, the others are provided for completeness.
func newDecompressionReader(cr *countingReader) (io.ReadCloser, decReaderType, error) {
	magic, err := readExactly(cr, 6)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		// Clean EOF: nothing to read.
		return nil, decReaderNone, nil
	}
	if err != nil {
		return nil, decReaderNone, fmt.Errorf("read magic bytes: %w", err)
	}

	// rebuild stream to include consumed magic bytes
	rest := io.MultiReader(bytes.NewReader(magic), cr)
	cr = &countingReader{r: rest}

	switch {
	case string(magic[0:2]) == magicGZip:
		// GZIP decompression
		dr, err := decompressGZip(cr)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read GZip stream: %w", err)
		}
		return dr, decReaderGZip, nil

	case string(magic[0:2]) == magicBZip2:
		// BZIP2 decompression
		dr, err := decompressBzip2(cr)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read BZip2 stream: %w", err)
		}
		return dr, decReaderBZip2, nil

	case string(magic[0:6]) == magicXZ:
		// XZ decompression
		dr, err := decompressXZ(cr)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read XZ stream: %w", err)
		}
		return dr, decReaderXZ, nil

	case string(magic[0:4]) == magicZStdFrame:
		// ZStd decompression
		dr, err := decompressZStd(cr)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read ZStd stream: %w", err)
		}
		return dr, decReaderZStd, nil

	case (string(magic[1:4]) == magicZStdSkippableFrame) && (magic[0]&0xf0 == 0x50):
		// ZStd decompression with custom dictionary
		dr, err := decompressZStdCustomDict(cr)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read ZStd skippable frame: %w", err)
		}
		return dr, decReaderZStd, nil

	default:
		// Use no decompression
		return io.NopCloser(cr), decReaderNone, nil
	}
}

// decompressGZip decompresses a GZip stream from the given input reader r.
func decompressGZip(br *countingReader) (io.ReadCloser, error) {
	// Open GZip reader
	dr, err := gzip.NewReader(br)
	if err != nil {
		return nil, fmt.Errorf("read GZip stream: %w", err)
	}

	dr.Multistream(false) // prevent crossing into next member on prefetch

	return dr, nil
}

// decompressBZip2 decompresses a BZip2 stream from the given input reader r.
func decompressBzip2(br *countingReader) (io.ReadCloser, error) {
	// Open BZip2 reader
	dr := bzip2.NewReader(br)

	return io.NopCloser(dr), nil
}

// decompressXZ decompresses an XZ stream from the given input reader r.
func decompressXZ(br *countingReader) (io.ReadCloser, error) {
	// Open XZ reader
	dr, err := xz.NewReader(br)
	if err != nil {
		return nil, fmt.Errorf("read XZ stream: %w", err)
	}

	return io.NopCloser(dr), nil
}

// decompressZStd decompresses a ZStd stream from the given input reader r.
func decompressZStd(br *countingReader) (io.ReadCloser, error) {
	// Open ZStd reader
	dr, err := zstd.NewReader(br, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("read ZStd stream: %w", err)
	}

	return dr.IOReadCloser(), nil
}

// decompressZStdCustomDict decompresses a ZStd stream with a prefixed custom dictionary from the given input
// reader r.
func decompressZStdCustomDict(br *countingReader) (io.ReadCloser, error) {
	// Read header
	var header [8]byte

	_, err := br.Read(header[:])
	if err != nil {
		return nil, fmt.Errorf("read ZStd skippable frame header: %w", err)
	}

	magic, length := header[0:4], binary.LittleEndian.Uint32(header[4:8])
	if (string(magic[1:4]) != magicZStdSkippableFrame) || (magic[0]&0xf0 != 0x50) {
		return nil, fmt.Errorf("expected ZStd skippable frame header")
	}

	// Read ZStd compressed custom dictionary
	lr := io.LimitReader(br, int64(length))

	dictr, err := zstd.NewReader(lr)
	if err != nil {
		return nil, fmt.Errorf("read ZStd compressed custom dictionary: %w", err)
	}

	defer dictr.Close()

	dict, err := io.ReadAll(dictr)
	if err != nil {
		return nil, fmt.Errorf("read ZStd compressed custom dictionary: %w", err)
	}

	// Discard remaining bytes, if any
	_, err = io.Copy(io.Discard, lr)
	if err != nil {
		return nil, fmt.Errorf("discard remaining bytes of ZStd compressed custom dictionary: %w", err)
	}

	// Open ZStd reader, with the given dictionary
	dr, err := zstd.NewReader(br, zstd.WithDecoderDicts(dict), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("create ZStd reader: %w", err)
	}

	return dr.IOReadCloser(), nil
}

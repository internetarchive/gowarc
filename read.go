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
	"sync"

	"github.com/internetarchive/gowarc/pkg/spooledtempfile"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// -------- Perf knobs (env tunables) --------

const (
	envMaxInMemSize         = "WARCMaxInMemorySize"        // in bytes; warc record content spooling threshold; if not set, calls NewSpooledTempFile with -1 which will default internally to MaxInMemorySize (1MB)
	envDecompressedBufSize  = "WARCDecompressedBufSize"    // in bytes; defines the bufio reader used by the decompression layer; if not set, defaults to defaultDecompressedSize (256 KiB)
	envZstdDecoderConc      = "WARCZstdDecoderConcurrency" // >1 enables parallel Zstd decode ; if not set, defaults to defaultZstdDecoderConc (1)
	defaultZstdDecoderConc  = 1                            // default Zstd decoder concurrency (1 == no parallelism)
	defaultDecompressedSize = 256 << 10                    // in bytes; defines the default bufReader buffer size; 256 KiB is a good gzip/zstd sweet spot
)

// Reader stores the bufio.Reader and gzip.Reader for a WARC file
type Reader struct {
	threshold int

	src       *bufio.Reader       // raw concatenated .gz input - wrapped in countingReader
	cr        *countingReader     // counts compressed bytes actually consumed
	dec       io.ReadCloser       // current decompressor (gz/plain/…)
	gz        GzipReaderInterface // cached when compType == decReaderGZip
	bufReader *bufio.Reader       // consuming layer (reused via Reset)

	inited   bool
	compType decReaderType

	// perf hints
	decompBufSize int // size for bufReader; env-tunable
}

// countingReader counts bytes read from the underlying compressed stream.
// It must sit *above* the bufio.Reader used for the decompressor to avoid
// counting upstream prefetch.
type countingReader struct {
	r   io.Reader
	n   int64
	tmp [1]byte // avoid alloc in ReadByte
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
func (c *countingReader) Tell() int64 { return c.n }

// ReadByte reads a single byte and counts it. No allocation; tries fast-path if the
// underlying reader already implements io.ByteReader.
func (c *countingReader) ReadByte() (byte, error) {
	if br, ok := c.r.(io.ByteReader); ok {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		c.n++
		return b, nil
	}
	n, err := c.r.Read(c.tmp[:])
	if n == 0 {
		return 0, err
	}
	if err != nil && err != io.EOF {
		return 0, err
	}
	c.n += int64(n)
	return c.tmp[0], nil
}

// NewReader returns a new WARC reader
func NewReader(reader io.Reader) (*Reader, error) {
	threshold := -1
	if s := os.Getenv(envMaxInMemSize); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			threshold = v
		} else {
			return nil, err
		}
	}

	decompSize := defaultDecompressedSize
	if s := os.Getenv(envDecompressedBufSize); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			decompSize = v
		} else if err != nil {
			return nil, err
		}
	}

	return &Reader{
		src:           bufio.NewReaderSize(reader, decompSize), // buffer the source reader (mainly to avoid small syscalls)
		threshold:     threshold,
		decompBufSize: decompSize,
	}, nil
}

// Close closes the WARC reader src and dec readers if they are open.
func (r *Reader) Close() error {
	if !r.inited {
		return nil
	}

	if r.dec != nil {
		if err := r.dec.Close(); err != nil {
			return fmt.Errorf("close decompressor: %w", err)
		}
		r.dec = nil
	}

	return nil
}

var intermediateBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096)
		return &b
	},
}

// readUntilDelim reads from r until the multi-byte delimiter `delim` is found.
// It returns the bytes BEFORE the delimiter, the total number of bytes consumed
// from r (including the delimiter), and an error. If EOF occurs before seeing
// the delimiter, it returns the data read and io.EOF.
// This function is designed to handle larger inputs by reading in chunks.
func readUntilDelim(r *bufio.Reader, delim []byte) (line []byte, n int64, err error) {
	if len(delim) == 0 {
		return nil, 0, errors.New("empty delimiter")
	}

	intermediateBuf := *intermediateBufPool.Get().(*[]byte)
	defer func() {
		intermediateBufPool.Put(&intermediateBuf)
	}()
	intermediateBuf = intermediateBuf[:0]
	last := delim[len(delim)-1]

	for {
		// Read a chunk of data
		chunk, e := r.ReadSlice(last)
		n += int64(len(chunk))
		intermediateBuf = append(intermediateBuf, chunk...)

		// Search for the delimiter starting from a position that accounts for overlap
		start := len(intermediateBuf) - len(chunk) - (len(delim) - 1)
		if start < 0 {
			start = 0
		}

		if i := bytes.Index(intermediateBuf[start:], delim); i >= 0 {
			i += start
			return intermediateBuf[:i], n, nil
		}

		if e != nil {
			if e == bufio.ErrBufferFull {
				continue // Continue reading if buffer is full
			}
			if e == io.EOF {
				return intermediateBuf, n, io.EOF
			}
			return intermediateBuf, n, e
		}
	}
}

// discardN efficiently skips exactly n bytes from a bufio.Reader.
// It leverages Reader.Discard which pulls from the underlying reader as needed.
func discardN(r *bufio.Reader, n int64) error {
	for n > 0 {
		// Discard takes int; chunk to avoid int overflow on 32-bit.
		chunk := int(n)
		const maxInt = int(^uint(0) >> 1)
		if chunk > maxInt {
			chunk = maxInt
		}
		d, err := r.Discard(chunk)
		n -= int64(d)
		if err != nil {
			return err
		}
	}
	return nil
}

// ReadRecord reads the next record from the opened WARC file.
//
// Returns:
//   - *Record: Record guaranteed to be non-nil if no errors occurred.
//   - error:   any parsing/IO error encountered (io.EOF for clean EOF).
func (r *Reader) ReadRecord(opts ...ReadOpts) (*Record, error) {
	var (
		discardContent bool
	)

	for _, opt := range opts {
		switch opt {
		case ReadOptsNoContentOutput:
			discardContent = true
		}
	}

	// lazy init of counting reader
	if r.cr == nil {
		r.cr = &countingReader{r: r.src}
	}

	offset := r.cr.Tell()

	// lazy init decompressor and decompressed-side buffer
	if !r.inited {
		var err error
		r.dec, r.compType, err = r.cr.newDecompressionReader()
		if err != nil {
			return nil, fmt.Errorf("init decompression reader: %w", err)
		}
		// If stream is clean EOF at start, no decompressor is returned.
		if r.dec == nil && r.compType == decReaderNone {
			return nil, io.EOF // clean EOF
		}

		if r.compType == decReaderGZip {
			r.gz = r.dec.(GzipReaderInterface)
			r.gz.Multistream(false)
		}

		r.bufReader = bufio.NewReaderSize(r.dec, r.decompBufSize)
		r.inited = true
	} else {
		if r.compType == decReaderGZip {
			// Move to next member (Reset requires the previous one to be fully consumed).
			if err := r.gz.Reset(r.cr); err == io.EOF {
				// No more members: clean EOF.
				return nil, io.EOF
			} else if err != nil {
				return nil, fmt.Errorf("gzip reset: %w", err)
			}
			r.gz.Multistream(false)
			r.bufReader.Reset(r.dec) // reuse buffer; avoids allocation per member
		}
	}

	_warcVer, _, err := readUntilDelim(r.bufReader, []byte("\r\n"))
	warcVer := string(_warcVer) // clone to avoid changes in underlying buffer
	if err != nil {
		if err == io.EOF && len(warcVer) == 0 {
			// treat as EOF for safety if member present but empty
			return nil, io.EOF
		}
		return nil, fmt.Errorf("reading WARC version: %w", err)
	}

	header := NewHeader()
	for {
		line, _, err := readUntilDelim(r.bufReader, []byte("\r\n"))
		if err != nil {
			return nil, fmt.Errorf("reading header: %w", err)
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
		return nil, fmt.Errorf("parsing Content-Length: %w", err)
	}

	buf := spooledtempfile.NewSpooledTempFile("warc", "", r.threshold, false, -1)
	bufOK := false
	defer func() { // close spooledtempfile if error occurs
		if !bufOK {
			buf.Close()
		}
	}()

	if discardContent {
		// Fast skip: avoids moving bytes to io.Discard through extra copying.
		if err := discardN(r.bufReader, length); err != nil {
			return nil, fmt.Errorf("discarding content: %w", err)
		}
	} else {
		if _, err := io.CopyN(buf, r.bufReader, length); err != nil {
			return nil, fmt.Errorf("copying content: %w", err)
		}
	}

	for range 2 {
		boundary, _, err := readUntilDelim(r.bufReader, []byte("\r\n"))
		if err != nil {
			return nil, fmt.Errorf("reading record boundary: %w", err)
		}
		if len(boundary) != 0 {
			return nil, fmt.Errorf("non-empty record boundary [boundary: %s]", boundary)
		}
	}

	if r.compType == decReaderGZip {
		// Ensure we fully consume the current gzip member so Reset() sees the next one.
		// Must drain via bufReader (not r.dec) to consume any bytes it prefetched.
		if _, derr := io.Copy(io.Discard, r.bufReader); derr != nil && derr != io.EOF {
			return nil, fmt.Errorf("draining gzip member: %w", derr)
		}
	}

	bufOK = true
	size := r.cr.Tell() - offset

	if r.compType != decReaderGZip {
		offset = -1
		size = -1
	}

	record := &Record{
		Header:  header,
		Content: buf,
		Version: string(warcVer),
		Offset:  offset,
		Size:    size,
	}

	return record, nil
}

// ReadOpts are options for ReadRecord
type ReadOpts int

const (
	// ReadOptsNoContentOutput means that the content of the record should not be returned.
	// This is useful for reading only the headers or metadata of the record.
	ReadOptsNoContentOutput ReadOpts = iota
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
func (c *countingReader) newDecompressionReader() (io.ReadCloser, decReaderType, error) {
	// Read just 6 bytes w/out allocation; reinsert them with MultiReader so counting is still correct.
	var magic [6]byte
	_, err := io.ReadFull(c.r, magic[:])
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		// Clean EOF: nothing to read.
		return nil, decReaderNone, nil
	}
	if err != nil {
		return nil, decReaderNone, fmt.Errorf("read magic bytes: %w", err)
	}

	// Rebuild stream to include consumed magic bytes
	c.r = io.MultiReader(bytes.NewReader(magic[:]), c.r)

	switch {
	case string(magic[0:2]) == magicGZip:
		// GZIP decompression
		dr, err := decompressGZip(c)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read GZip stream: %w", err)
		}
		return dr, decReaderGZip, nil

	case string(magic[0:2]) == magicBZip2:
		// BZIP2 decompression
		dr, err := decompressBzip2(c)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read BZip2 stream: %w", err)
		}
		return dr, decReaderBZip2, nil

	case string(magic[0:6]) == magicXZ:
		// XZ decompression
		dr, err := decompressXZ(c)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read XZ stream: %w", err)
		}
		return dr, decReaderXZ, nil

	case string(magic[0:4]) == magicZStdFrame:
		// ZStd decompression
		dr, err := decompressZStd(c)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read ZStd stream: %w", err)
		}
		return dr, decReaderZStd, nil

	case (string(magic[1:4]) == magicZStdSkippableFrame) && (magic[0]&0xf0 == 0x50):
		// ZStd decompression with custom dictionary
		dr, err := decompressZStdCustomDict(c)
		if err != nil {
			return nil, decReaderNone, fmt.Errorf("read ZStd skippable frame: %w", err)
		}
		return dr, decReaderZStd, nil

	default:
		// Use no decompression
		return io.NopCloser(c), decReaderNone, nil
	}
}

// decompressGZip decompresses a GZip stream from the given input reader r.
func decompressGZip(br *countingReader) (io.ReadCloser, error) {
	// Open GZip reader
	dr, err := newGzipReader(br)
	if err != nil {
		return nil, fmt.Errorf("read GZip stream: %w", err)
	}
	dr.Multistream(false) // prevent crossing into next member on prefetch
	return dr, nil
}

// decompressBZip2 decompresses a BZip2 stream from the given input reader r.
func decompressBzip2(br *countingReader) (io.ReadCloser, error) {
	dr := bzip2.NewReader(br)
	return io.NopCloser(dr), nil
}

// decompressXZ decompresses an XZ stream from the given input reader r.
func decompressXZ(br *countingReader) (io.ReadCloser, error) {
	dr, err := xz.NewReader(br)
	if err != nil {
		return nil, fmt.Errorf("read XZ stream: %w", err)
	}
	return io.NopCloser(dr), nil
}

// decompressZStd decompresses a ZStd stream from the given input reader r.
func decompressZStd(br *countingReader) (io.ReadCloser, error) {
	// Keep previous behavior (single-thread) unless overridden by env.
	concurrency := defaultZstdDecoderConc
	if s := os.Getenv(envZstdDecoderConc); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			// v==0 lets zstd pick an automatic value; v>=1 sets exact goroutines.
			concurrency = v
		}
	}
	opts := []zstd.DOption{zstd.WithDecoderConcurrency(concurrency)}
	dr, err := zstd.NewReader(br, opts...)
	if err != nil {
		return nil, fmt.Errorf("read ZStd stream: %w", err)
	}
	return dr.IOReadCloser(), nil
}

// decompressZStdCustomDict decompresses a ZStd stream with a prefixed custom dictionary from the given input reader r.
func decompressZStdCustomDict(br *countingReader) (io.ReadCloser, error) {
	// Read header
	var header [8]byte

	var nn int
	for nn < len(header) {
		n, err := br.Read(header[nn:])
		if err != nil {
			return nil, fmt.Errorf("read ZStd skippable frame header: %w", err)
		}
		nn += n
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
	concurrency := defaultZstdDecoderConc
	if s := os.Getenv(envZstdDecoderConc); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			concurrency = v
		}
	}
	dr, err := zstd.NewReader(br, zstd.WithDecoderDicts(dict), zstd.WithDecoderConcurrency(concurrency))
	if err != nil {
		return nil, fmt.Errorf("create ZStd reader: %w", err)
	}
	return dr.IOReadCloser(), nil
}

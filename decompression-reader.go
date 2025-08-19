// This file is copied from https://github.com/crissyfield/troll-a/blob/main/pkg/fetch/decompression-reader.go , under Apache-2.0 License
// Author: [Crissy Field](https://github.com/crissyfield)

package warc

import (
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

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

// newDecompressionReader will return a new reader transparently doing decompression of GZip, BZip2, XZ, and
// ZStd.
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

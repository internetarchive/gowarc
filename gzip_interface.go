package warc

import (
	"io"
)

// GzipWriterInterface defines the interface for gzip writers
// This allows us to switch between standard gzip and klauspost gzip
// based on build tags
type GzipWriterInterface interface {
	io.WriteCloser
	Flush() error
}

// GzipReaderInterface defines the interface for gzip readers
// This allows us to switch between standard gzip and klauspost gzip
// based on build tags
type GzipReaderInterface interface {
	io.ReadCloser
	Multistream(enable bool)
	Reset(r io.Reader) error
}

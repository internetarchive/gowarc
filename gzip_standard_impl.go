//go:build standard_gzip
// +build standard_gzip

package warc

import (
	"compress/gzip"
	"io"
)

func newGzipWriter(w io.Writer) GzipWriterInterface {
	return gzip.NewWriter(w)
}

// standardGzipReader wraps the standard library gzip.Reader
type standardGzipReader struct {
	*gzip.Reader
}

func (r *standardGzipReader) Multistream(enable bool) {
	r.Reader.Multistream(enable)
}

func newGzipReader(reader io.Reader) (GzipReaderInterface, error) {
	r, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	return &standardGzipReader{r}, nil
}

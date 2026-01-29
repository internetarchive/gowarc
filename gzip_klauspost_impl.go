//go:build !standard_gzip || klauspost_gzip
// +build !standard_gzip klauspost_gzip

package warc

import (
	"io"

	gzip "github.com/klauspost/compress/gzip"
)

func newGzipWriter(w io.Writer) GzipWriterInterface {
	return gzip.NewWriter(w)
}

// klauspostGzipReader wraps the klauspost gzip.Reader
type klauspostGzipReader struct {
	*gzip.Reader
}

func (r *klauspostGzipReader) Multistream(enable bool) {
	r.Reader.Multistream(enable)
}

func newGzipReader(reader io.Reader) (GzipReaderInterface, error) {
	r, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	return &klauspostGzipReader{r}, nil
}

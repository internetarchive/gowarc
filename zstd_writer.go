package warc

import (
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// sizedZstdWriter wraps a zstd encoder and remembers its current output writer
// so callers can set Frame_Content_Size before writing a record.
type sizedZstdWriter struct {
	*zstd.Encoder
	output io.Writer
}

func newSizedZstdWriter(w io.Writer, dictionary []byte) (*sizedZstdWriter, error) {
	opts := []zstd.EOption{
		zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
	}
	if len(dictionary) > 0 {
		opts = append(opts, zstd.WithEncoderDict(dictionary))
	}

	enc, err := zstd.NewWriter(w, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating zstd writer: %w", err)
	}
	return &sizedZstdWriter{Encoder: enc, output: w}, nil
}

func (w *sizedZstdWriter) Reset(output io.Writer) {
	w.output = output
	w.Encoder.Reset(output)
}

func (w *sizedZstdWriter) SetContentSize(size int64) {
	if w.output == nil {
		return
	}
	w.Encoder.ResetContentSize(w.output, size)
}

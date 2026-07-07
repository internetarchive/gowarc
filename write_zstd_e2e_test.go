package warc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestZSTDWriterProducesSpecRequiredFrames(t *testing.T) {
	var warcBytes bytes.Buffer

	writer, err := NewWriter(&warcBytes, "test.warc.zst", SHA1, CompressionZstd, false, nil)
	if err != nil {
		t.Fatalf("NewWriter() failed: %v", err)
	}

	if _, err := writer.WriteInfoRecord(map[string]string{
		"format": "WARC file version 1.1",
	}); err != nil {
		t.Fatalf("WriteInfoRecord() failed: %v", err)
	}

	payload := makeZSTDTestBytes(1 << 20)
	writer.Reset(&warcBytes)
	writeZSTDTestRecord(t, writer, "resource", "https://example.com/large", "application/octet-stream", payload)

	writer.Reset(&warcBytes)
	writeZSTDTestRecord(t, writer, "metadata", "https://example.com/small", "text/plain", []byte("x"))

	frames := readZSTDFrameInfos(t, warcBytes.Bytes())
	if len(frames) != 3 {
		t.Fatalf("frame count = %d, want 3 (warcinfo + large resource + small metadata)", len(frames))
	}

	var largeResourceFrames int
	for i, frame := range frames {
		if !zstdFrameHasContentSize(frame.compressed) {
			t.Fatalf("frame %d is missing Frame_Content_Size", i)
		}
		if !zstdFrameHasChecksum(frame.compressed) {
			t.Fatalf("frame %d is missing Content_Checksum", i)
		}
		assertZSTDFrameChecksumVerified(t, frame.compressed)

		record := readOnlyWARCRecordFromFrame(t, frame.decoded)
		if record.Header.Get("WARC-Type") == "resource" && record.Header.Get("WARC-Target-URI") == "https://example.com/large" {
			largeResourceFrames++
			if got := getContentLength(record.Content); got != int64(len(payload)) {
				t.Fatalf("resource frame %d content length = %d, want %d", i, got, len(payload))
			}
		}
		if err := record.Content.Close(); err != nil {
			t.Fatalf("close record content: %v", err)
		}
	}

	if largeResourceFrames != 1 {
		t.Fatalf("large resource record used %d ZSTD frames, want 1", largeResourceFrames)
	}
}

func writeZSTDTestRecord(t *testing.T, writer *Writer, recordType, targetURI, contentType string, payload []byte) {
	t.Helper()

	record := NewRecord(t.TempDir(), false)
	record.Header.Set("WARC-Type", recordType)
	record.Header.Set("WARC-Target-URI", targetURI)
	record.Header.Set("Content-Type", contentType)
	if _, err := record.Content.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	if _, err := writer.WriteRecord(record); err != nil {
		t.Fatalf("WriteRecord(%s): %v", targetURI, err)
	}
}

type zstdFrameInfo struct {
	compressed []byte
	decoded    []byte
}

func readZSTDFrameInfos(t *testing.T, data []byte) []zstdFrameInfo {
	t.Helper()

	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer dec.Close()

	var frames []zstdFrameInfo
	reader := bytes.NewReader(data)
	for {
		frame, err := readZSTDFrameBytes(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read ZSTD frame %d: %v", len(frames), err)
		}

		decoded, err := dec.DecodeAll(frame, nil)
		if err != nil {
			t.Fatalf("decode ZSTD frame %d: %v", len(frames), err)
		}
		frames = append(frames, zstdFrameInfo{
			compressed: frame,
			decoded:    append([]byte(nil), decoded...),
		})
	}

	return frames
}

func readZSTDFrameBytes(r io.Reader) ([]byte, error) {
	var magic [4]byte
	n, err := io.ReadFull(r, magic[:])
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		if n == 0 {
			return nil, io.EOF
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(magic[:]) != 0xfd2fb528 {
		return nil, errors.New("invalid ZSTD magic")
	}

	frame := append([]byte(nil), magic[:]...)
	var fhd [1]byte
	if _, err := io.ReadFull(r, fhd[:]); err != nil {
		return nil, err
	}
	frame = append(frame, fhd[0])

	fcsFlag := (fhd[0] >> 6) & 0x03
	singleSegmentFlag := (fhd[0] >> 5) & 0x01
	contentChecksumFlag := (fhd[0] >> 2) & 0x01
	dictIDFlag := fhd[0] & 0x03

	headerRestSize := 0
	if singleSegmentFlag == 0 {
		headerRestSize++
	}
	switch dictIDFlag {
	case 1:
		headerRestSize++
	case 2:
		headerRestSize += 2
	case 3:
		headerRestSize += 4
	}
	if singleSegmentFlag == 1 && fcsFlag == 0 {
		headerRestSize++
	} else {
		switch fcsFlag {
		case 1:
			headerRestSize += 2
		case 2:
			headerRestSize += 4
		case 3:
			headerRestSize += 8
		}
	}
	if headerRestSize > 0 {
		headerRest := make([]byte, headerRestSize)
		if _, err := io.ReadFull(r, headerRest); err != nil {
			return nil, err
		}
		frame = append(frame, headerRest...)
	}

	for {
		var blockHeader [3]byte
		if _, err := io.ReadFull(r, blockHeader[:]); err != nil {
			return nil, err
		}
		frame = append(frame, blockHeader[:]...)

		blockHeaderVal := uint32(blockHeader[0]) | uint32(blockHeader[1])<<8 | uint32(blockHeader[2])<<16
		lastBlock := (blockHeaderVal & 0x01) != 0
		blockType := (blockHeaderVal >> 1) & 0x03
		blockSize := blockHeaderVal >> 3
		if blockType == 3 {
			return nil, errors.New("invalid ZSTD block type")
		}

		dataSize := blockSize
		if blockType == 1 {
			dataSize = 1
		}
		if dataSize > 0 {
			blockData := make([]byte, dataSize)
			if _, err := io.ReadFull(r, blockData); err != nil {
				return nil, err
			}
			frame = append(frame, blockData...)
		}
		if lastBlock {
			break
		}
	}

	if contentChecksumFlag == 1 {
		var checksum [4]byte
		if _, err := io.ReadFull(r, checksum[:]); err != nil {
			return nil, err
		}
		frame = append(frame, checksum[:]...)
	}

	return frame, nil
}

func zstdFrameHasContentSize(frame []byte) bool {
	if len(frame) < 5 || binary.LittleEndian.Uint32(frame[:4]) != 0xfd2fb528 {
		return false
	}

	fhd := frame[4]
	frameContentSizeFlag := (fhd >> 6) & 0x03
	singleSegmentFlag := (fhd >> 5) & 0x01
	return frameContentSizeFlag != 0 || singleSegmentFlag != 0
}

func zstdFrameHasChecksum(frame []byte) bool {
	if len(frame) < 5 || binary.LittleEndian.Uint32(frame[:4]) != 0xfd2fb528 {
		return false
	}

	return frame[4]&(1<<2) != 0
}

func assertZSTDFrameChecksumVerified(t *testing.T, frame []byte) {
	t.Helper()

	corrupt := append([]byte(nil), frame...)
	corrupt[len(corrupt)-1] ^= 0xff

	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer dec.Close()

	if _, err := dec.DecodeAll(corrupt, nil); err == nil {
		t.Fatal("corrupting the ZSTD frame checksum did not fail decoding")
	}
}

func readOnlyWARCRecordFromFrame(t *testing.T, frame []byte) *Record {
	t.Helper()

	reader, err := NewReader(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("NewReader(frame): %v", err)
	}
	defer reader.Close()

	record, err := reader.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord(frame): %v", err)
	}

	if extra, err := reader.ReadRecord(); !errors.Is(err, io.EOF) {
		if extra != nil {
			_ = extra.Content.Close()
		}
		t.Fatalf("frame decoded to more than one WARC record; second ReadRecord err = %v", err)
	}

	return record
}

func makeZSTDTestBytes(n int) []byte {
	b := make([]byte, n)
	pattern := []byte("WARC test testtesttesttesttesttesttesttesttesttest")
	for i := range b {
		b[i] = pattern[i%len(pattern)]
	}
	return b
}

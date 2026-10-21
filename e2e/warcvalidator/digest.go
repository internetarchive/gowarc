package warcvalidator

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"

	warc "github.com/internetarchive/gowarc"
)

// validateRecordDigests checks WARC-Block-Digest and WARC-Payload-Digest where applicable.
// Logic follows cmd/warc/verify (block always when present; payload for full HTTP payloads only).
// Revisit records keep a truncated block — block digest still must match stored bytes;
// payload digest in the header must match the referred response and is checked in ValidateRevisitAgainstOriginal.
func validateRecordDigests(rec *warc.Record, filePath string) error {
	id := rec.Header.Get("WARC-Record-ID")
	if err := validateWARCVersion(rec, filePath, id); err != nil {
		return err
	}
	if err := validateBlockDigest(rec, filePath, id); err != nil {
		return err
	}
	if rec.Header.Get("WARC-Type") == "revisit" {
		// we cannot validate the payload digest of a revisit record
		// because there is no payload to digest
		return nil
	}
	return validatePayloadDigest(rec, filePath, id)
}

func validateWARCVersion(rec *warc.Record, filePath, recordID string) error {
	v := rec.Version
	if strings.ContainsAny(v, "\r\n") {
		return fmt.Errorf("%s: record %s: WARC version contains invalid characters", filePath, recordID)
	}
	if v != "WARC/1.0" && v != "WARC/1.1" {
		return fmt.Errorf("%s: record %s: invalid WARC version %q (want WARC/1.0 or WARC/1.1)", filePath, recordID, v)
	}
	return nil
}

func validateBlockDigest(rec *warc.Record, filePath, recordID string) error {
	blockDigest := rec.Header.Get("WARC-Block-Digest")
	if blockDigest == "" {
		return fmt.Errorf("%s: record %s: WARC-Block-Digest is missing", filePath, recordID)
	}
	prefix := strings.SplitN(blockDigest, ":", 2)[0]
	if !warc.IsDigestSupported(prefix) {
		return fmt.Errorf("%s: record %s: unsupported WARC-Block-Digest algorithm %q", filePath, recordID, prefix)
	}
	if _, err := rec.Content.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%s: record %s: seek content: %w", filePath, recordID, err)
	}
	defer rec.Content.Seek(0, io.SeekStart)

	algo := warc.GetDigestFromPrefix(prefix)
	got, err := warc.GetDigest(rec.Content, algo)
	if err != nil {
		return fmt.Errorf("%s: record %s: block digest compute: %w", filePath, recordID, err)
	}
	if got != blockDigest {
		return fmt.Errorf("%s: record %s: WARC-Block-Digest mismatch: header %q recomputed %q", filePath, recordID, blockDigest, got)
	}
	return nil
}

func validatePayloadDigest(rec *warc.Record, filePath, recordID string) error {
	want := rec.Header.Get("WARC-Payload-Digest")
	if want == "" {
		return nil
	}
	ct := rec.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "msgtype=response"):
		return validateHTTPResponsePayloadDigest(rec, filePath, recordID, want)
	case strings.Contains(ct, "msgtype=request"):
		return validateHTTPRequestPayloadDigest(rec, filePath, recordID, want)
	default:
		return nil
	}
}

func validateHTTPResponsePayloadDigest(rec *warc.Record, filePath, recordID, want string) error {
	if _, err := rec.Content.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%s: record %s: seek content: %w", filePath, recordID, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(rec.Content), nil)
	if err != nil {
		return fmt.Errorf("%s: record %s: read HTTP response: %w", filePath, recordID, err)
	}
	defer resp.Body.Close()
	defer rec.Content.Seek(0, io.SeekStart)

	if resp.Header.Get("X-Crawler-Transfer-Encoding") != "" || resp.Header.Get("X-Crawler-Content-Encoding") != "" {
		return fmt.Errorf("%s: record %s: crawler transport/content-encoding headers prevent payload digest verification", filePath, recordID)
	}
	prefix := strings.SplitN(want, ":", 2)[0]
	if !warc.IsDigestSupported(prefix) {
		return fmt.Errorf("%s: record %s: unsupported WARC-Payload-Digest algorithm %q", filePath, recordID, prefix)
	}
	got, err := warc.GetDigest(resp.Body, warc.GetDigestFromPrefix(prefix))
	if err != nil {
		return fmt.Errorf("%s: record %s: payload digest compute: %w", filePath, recordID, err)
	}
	if got != want {
		return fmt.Errorf("%s: record %s: WARC-Payload-Digest mismatch: header %q recomputed %q", filePath, recordID, want, got)
	}
	return nil
}

func validateHTTPRequestPayloadDigest(rec *warc.Record, filePath, recordID, want string) error {
	if _, err := rec.Content.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%s: record %s: seek content: %w", filePath, recordID, err)
	}
	req, err := http.ReadRequest(bufio.NewReader(rec.Content))
	if err != nil {
		return fmt.Errorf("%s: record %s: read HTTP request: %w", filePath, recordID, err)
	}
	defer req.Body.Close()
	defer rec.Content.Seek(0, io.SeekStart)

	prefix := strings.SplitN(want, ":", 2)[0]
	if !warc.IsDigestSupported(prefix) {
		return fmt.Errorf("%s: record %s: unsupported WARC-Payload-Digest algorithm %q", filePath, recordID, prefix)
	}
	got, err := warc.GetDigest(req.Body, warc.GetDigestFromPrefix(prefix))
	if err != nil {
		return fmt.Errorf("%s: record %s: request payload digest compute: %w", filePath, recordID, err)
	}
	if got != want {
		return fmt.Errorf("%s: record %s: WARC-Payload-Digest mismatch: header %q recomputed %q", filePath, recordID, want, got)
	}
	return nil
}

package warcvalidator

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	warc "github.com/internetarchive/gowarc"
	"github.com/internetarchive/gowarc/e2e/echoserver/imperative"
)

func (e ValidationError) Error() string { return e.Message }

type ValidationError struct {
	Message           string
	RelatedImperative *imperative.Imperative // is nil if not related to an imperative
	Record            *warc.Record
}

func newValidationError(record *warc.Record, imp *imperative.Imperative, format string, args ...any) ValidationError {
	return ValidationError{
		Message:           fmt.Sprintf(format, args...),
		RelatedImperative: imp,
		Record:            record,
	}
}

type Counts struct {
	Request  int
	Response int
	Revisit  int
}

func (c Counts) Increment(recordType string) Counts {
	switch recordType {
	case "request":
		c.Request++
	case "response":
		c.Response++
	case "revisit":
		c.Revisit++
	default:
		panic(fmt.Sprintf("invalid record type: %s", recordType))
	}
	return c
}

// Check validates the WARC files in the given directory against the given imperatives.
// On success it returns (nil or empty slice, nil). Validation findings are returned as
// a slice; I/O or read failures return (_, err).
func Check(outDir string) ([]ValidationError, map[string]Counts) {
	var validationErrs []ValidationError
	tracker := make(map[string]Counts)

	fmt.Printf("Checking %s\n", outDir)
	records, err := readWARCs(outDir)
	if err != nil {
		validationErrs = append(validationErrs, newValidationError(nil, nil, "Failed to read WARC files: %s", err))
		return validationErrs, nil
	}
	fmt.Printf("Found %d records in %s\n", len(records), outDir)
	for _, record := range records {
		if isRequestRecord(record) {
			request, err := parseHTTPRequest(record.Content)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, nil, "Failed to parse request: %s", err))
				continue
			}
			imp, err := imperativeFromRequest(request)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, nil, "Failed to parse imperative from request: %s", err))
				continue
			}
			tracker[imp.Hash()] = tracker[imp.Hash()].Increment("request")

			body, err := io.ReadAll(request.Body)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, &imp, "Failed to read request body: %s", err))
				continue
			}
			err = imperative.AssertRequest(request, body)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, &imp, "Failed to assert request: %s", err))
				continue
			}
		}
		if isResponseRecord(record) {
			response, err := parseHTTPResponse(record.Content)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, nil, "Failed to parse response: %s", err))
				continue
			}
			imp, err := imperativeFromResponse(response)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, nil, "Failed to parse imperative from response: %s", err))
				continue
			}
			tracker[imp.Hash()] = tracker[imp.Hash()].Increment("response")

			body, err := io.ReadAll(response.Body)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, &imp, "Failed to read response body: %s", err))
				continue
			}
			err = imperative.AssertResponse(response, body)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, &imp, "Failed to assert response: %s", err))
				continue
			}
		}
		if isRevisitRecord(record) {
			// only validates if the referred-to record exists, and payload digests match
			referredToRecord, err := validateRevisit(records, record.Header.Get("WARC-Record-ID"))
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(record, nil, "Failed to validate revisit: %s", err))
				continue
			}

			// calculate the body from the imperative, digest it, and compare it to the revisit record's payload digest

			response, err := parseHTTPResponse(record.Content)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(referredToRecord, nil, "Failed to parse revisit: %s", err))
				continue
			}
			// get the imperative from the response
			imp, err := imperativeFromResponse(response)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(referredToRecord, nil, "Failed to parse imperative from revisit: %s", err))
				continue
			}
			tracker[imp.Hash()] = tracker[imp.Hash()].Increment("revisit")

			// calculate the digest of the expected response body
			calculatedBody, err := imp.Response.Body.Build()
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(referredToRecord, &imp, "Failed to build response body: %s", err))
				continue
			}
			// calculate digest
			// digest contains the correct prefix
			digestAlgo := warc.GetDigestFromPrefix(strings.SplitN(record.Header.Get("WARC-Payload-Digest"), ":", 2)[0])
			digest, err := warc.GetDigest(bytes.NewReader(calculatedBody), digestAlgo)
			if err != nil {
				validationErrs = append(validationErrs, newValidationError(referredToRecord, &imp, "Failed to calculate digest of response body: %s", err))
				continue
			}
			// compare the prefix + digest of the calculated response body with the prefix + digest of the response body
			if digest != record.Header.Get("WARC-Payload-Digest") {
				validationErrs = append(validationErrs, newValidationError(referredToRecord, &imp, "WARC-Payload-Digest mismatch: %s != %s", digest, response.Header.Get("WARC-Payload-Digest")))
				continue
			}
		}
	}

	return validationErrs, tracker
}

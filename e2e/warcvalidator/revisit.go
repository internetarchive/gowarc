package warcvalidator

import (
	"fmt"

	warc "github.com/internetarchive/gowarc"
)

func validateRevisit(recordMap map[string]*warc.Record, recordID string) (*warc.Record, error) {
	// lookup revisit in recordMap
	revisit := recordMap[recordID]
	if revisit == nil {
		// we panic because this should never happen
		panic(fmt.Sprintf("revisit record not found for record: %s", recordID))
	}
	// read WARC-Refers-To header
	refersToID := revisit.Header.Get("WARC-Refers-To")
	if refersToID == "" {
		return nil, ValidationError{
			Message:           fmt.Sprintf("No WARC-Refers-To header found in revisit record: %s", recordID),
			RelatedImperative: nil,
			Record:            revisit,
		}
	}
	// lookup referred-to record in recordMap
	refersToRecord := recordMap[refersToID]
	if refersToRecord == nil {
		return nil, ValidationError{
			Message:           fmt.Sprintf("refers to record not found for revisit: %s -> %s", recordID, refersToID),
			RelatedImperative: nil,
			Record:            revisit,
		}
	}
	// digests were already validated when reading the records from file
	// we check if the payload digests match
	if revisit.Header.Get("WARC-Payload-Digest") != refersToRecord.Header.Get("WARC-Payload-Digest") {
		return nil, ValidationError{
			Message:           fmt.Sprintf("WARC-Payload-Digest mismatch for revisit: %s -> %s", recordID, refersToID),
			RelatedImperative: nil,
			Record:            revisit,
		}
	}

	// return the referred-to record
	return refersToRecord, nil
}

package warc

import (
	"io"
)

func (c *CustomHTTPClient) WriteRecord(WARCTargetURI, WARCType, contentType, payloadString string, payloadReader io.Reader) {
	// Initialize the record
	metadataRecord := NewRecord("", false)

	// Set the headers
	metadataRecord.Header.Set("WARC-Type", WARCType)
	metadataRecord.Header.Set("WARC-Target-URI", WARCTargetURI)

	if contentType != "" {
		metadataRecord.Header.Set("Content-Type", contentType)
	}

	// Write the payload
	if payloadString != "" {
		metadataRecord.Content.Write([]byte(payloadString))
	} else if payloadReader != nil {
		_, err := io.Copy(metadataRecord.Content, payloadReader)
		if err != nil {
			panic(err)
		}
	} else {
		panic("no payload provided")
	}

	// Add it to the batch
	batch := NewRecordBatch(make(chan struct{}, 1))
	batch.Records = append(batch.Records, metadataRecord)

	c.WARCWriter <- batch

	// Wait for the record to be written
	<-batch.FeedbackChan
}

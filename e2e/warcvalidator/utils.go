package warcvalidator

import (
	warc "github.com/internetarchive/gowarc"
)

// true if WARC-Type is "request"
func isRequestRecord(rec *warc.Record) bool {
	return rec.Header.Get("WARC-Type") == "request"
}

// true if WARC-Type is "response"
func isResponseRecord(rec *warc.Record) bool {
	return rec.Header.Get("WARC-Type") == "response"
}

// true if WARC-Type is "revisit"
func isRevisitRecord(rec *warc.Record) bool {
	return rec.Header.Get("WARC-Type") == "revisit"
}

// true if WARC-Type is not "request", "response" or "revisit"
func isInfoRecord(rec *warc.Record) bool {
	return !(isResponseRecord(rec) || isRequestRecord(rec) || isRevisitRecord(rec))
}

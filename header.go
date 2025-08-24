package warc

import "strings"

// Header provides information about the WARC record. It stores WARC record
// field names and their values. Since WARC field names are case-insensitive,
// the Header methods are case-insensitive as well.
type Header map[string]string

// Set sets the header field associated with key to value.
// The key is stored as-is, preserving its original case.
func (h Header) Set(key, value string) {
	h[key] = value
}

// Get returns the value associated with the given key.
// The key is compared in a case-insensitive manner.
func (h Header) Get(key string) string {
	lowerKey := strings.ToLower(key)
	for k, v := range h {
		if strings.ToLower(k) == lowerKey {
			return v
		}
	}
	return ""
}

// Del deletes the value associated with key.
// The key is compared in a case-insensitive manner.
func (h Header) Del(key string) {
	lowerKey := strings.ToLower(key)
	for k := range h {
		if strings.ToLower(k) == lowerKey {
			delete(h, k)
			return
		}
	}
}

// NewHeader creates a new WARC header.
func NewHeader() Header {
	return make(map[string]string)
}

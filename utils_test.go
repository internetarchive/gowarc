package warc

import (
	"testing"
)

// Tests for the NewRotatorSettings function
func TestNewRotatorSettings(t *testing.T) {
	rotatorSettings := NewRotatorSettings()

	if rotatorSettings.Prefix != "WARC" {
		t.Error("Failed to set WARC rotator's filename prefix")
	}

	if rotatorSettings.WARCSize != 1000 {
		t.Error("Failed to set WARC rotator's WARC size")
	}

	if rotatorSettings.OutputDirectory != "./" {
		t.Error("Failed to set WARC rotator's output directory")
	}

	if rotatorSettings.Compression != "GZIP" {
		t.Error("Failed to set WARC rotator's compression algorithm")
	}

	if rotatorSettings.CompressionDictionary != "" {
		t.Error("Failed to set WARC rotator's compression dictionary")
	}
}

// Tests for the isLineStartingWithHTTPMethod function
func TestIsHTTPRequest(t *testing.T) {
	goodHTTPRequestHeaders := []string{
		"GET /index.html HTTP/1.1",
		"POST /api/login HTTP/1.1",
		"DELETE /api/products/456 HTTP/1.1",
		"HEAD /about HTTP/1.0",
		"OPTIONS / HTTP/1.1",
		"PATCH /api/item/789 HTTP/1.1",
		"GET /images/logo.png HTTP/1.1",
	}

	for _, header := range goodHTTPRequestHeaders {
		if !isHTTPRequest(header) {
			t.Error("Invalid HTTP Method parsing:", header)
		}
	}
}

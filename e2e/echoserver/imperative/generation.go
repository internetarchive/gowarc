package imperative

import (
	"fmt"
	"net/http"
	"os"

	"bytes"
)

func (bdy Body) Validate() error {
	if bdy.Length != 0 && bdy.Filepath != "" {
		return fmt.Errorf("imperative: body length and filepath cannot be set at the same time. Pick one or the other.")
	}
	if bdy.Encoding != "" && bdy.Encoding != EncodingUTF8 && bdy.Encoding != EncodingBinary {
		return fmt.Errorf("imperative: invalid value for encoding, must be either empty, %s or %s", EncodingUTF8, EncodingBinary)
	}
	if bdy.Length > 0 && bdy.Seed == 0 {
		return fmt.Errorf("imperative: set the seed explicitly to prevent duplicate payload digests")
	}
	return nil
}

// Builds a body from specification
// If neither Filepath nor Length is set, returns empty []byte, nil
func (bdy Body) Build() ([]byte, error) {
	var body []byte
	var err error

	if bdy.Filepath != "" {
		// should be page cached by kernel
		body, err = os.ReadFile(bdy.Filepath)
		if err != nil {
			return nil, err
		}
	} else if bdy.Length > 0 {
		if bdy.Encoding == EncodingBinary {
			body = binaryBody(bdy.Length, bdy.Seed)
		} else {
			body = stringBody(bdy.Length, bdy.Seed)
		}
	}

	if bdy.Compress != "" && len(body) > 0 {
		compressed, err := Compress(body, bdy.Compress)
		if err != nil {
			return nil, fmt.Errorf("compress: %w", err)
		}
		body = compressed
	}

	return body, nil
}

// Returns deterministic binary slice
func binaryBody(n int, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i + seed) % 256)
	}
	return b
}

// Returns deterministic string as []byte
func stringBody(n int, seed int) []byte {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[(i+seed)%len(alphabet)]
	}
	return b
}

// Returns a http.Request from an Imperative
func (imp Imperative) BuildRequest(baseURL string) (*http.Request, error) {
	if err := imp.Validate(); err != nil {
		return nil, err
	}
	body, err := imp.Request.Body.Build()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(imp.Request.Method, fmt.Sprintf("%s%s", baseURL, imp.Request.Path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// set headers
	for key, value := range imp.Request.Headers {
		req.Header.Set(key, value)
	}
	// set imperative header
	req.Header.Set(ImperativeHeader, imp.Encode())

	// imp validated, body built, headers set
	return req, nil
}

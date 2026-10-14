package imperative

import (
	"bytes"
	"fmt"
	"net/http"
)

func AssertRequest(req *http.Request, body []byte) error {
	// parse imperative from request header
	imp, err := ParseImperative(req.Header.Get(ImperativeHeader))
	if err != nil {
		return fmt.Errorf("assertRequest: parse imperative: %v", err)
	}
	if err := imp.Validate(); err != nil {
		return fmt.Errorf("assertRequest: validate imperative: %v", err)
	}

	// check method
	if req.Method != imp.Request.Method {
		return fmt.Errorf("request method mismatch: expected %s, got %s", imp.Request.Method, req.Method)
	}
	// check path
	if req.URL.Path != imp.Request.Path {
		return fmt.Errorf("request path mismatch: expected %s, got %s", imp.Request.Path, req.URL.Path)
	}
	// check headers
	for key, value := range imp.Request.Headers {
		if req.Header.Get(key) != value {
			return fmt.Errorf("request header mismatch: expected %s, got %s", value, req.Header.Get(key))
		}
	}
	// calculate specified body
	specifiedBody, err := imp.Request.Body.Build()
	if err != nil {
		return fmt.Errorf("assertRequest: build body: %v", err)
	}
	// compare against passed body
	if !bytes.Equal(specifiedBody, body) {
		return fmt.Errorf("request body mismatch: expected %d bytes, got %d bytes", len(specifiedBody), len(body))
	}
	// imperative ok, method ok, path ok, headers ok, body ok
	return nil
}

func AssertResponse(resp *http.Response, body []byte) error {
	// read imperative from response header
	imp, err := ParseImperative(resp.Header.Get(ImperativeHeader))
	if err != nil {
		return fmt.Errorf("assertRequest: parse imperative: %v", err)
	}
	if err := imp.Validate(); err != nil {
		return fmt.Errorf("assertResponse: validate imperative: %v", err)
	}

	// check status code
	if resp.StatusCode != imp.Response.StatusCode {
		return fmt.Errorf("response status code mismatch: expected %d, got %d", imp.Response.StatusCode, resp.StatusCode)
	}
	// check headers
	for key, value := range imp.Response.Headers {
		if resp.Header.Get(key) != value {
			return fmt.Errorf("response header mismatch: expected %s, got %s", value, resp.Header.Get(key))
		}
	}
	// return early if the imperative aborts the connection
	if imp.Transport.Abort {
		return nil
	}
	// calculate specified body
	specifiedBody, err := imp.Response.Body.Build()
	if err != nil {
		return fmt.Errorf("assertResponse: build body: %v", err)
	}
	// compare against passed body
	if !bytes.Equal(specifiedBody, body) {
		return fmt.Errorf("response body mismatch: expected %d bytes, got %d bytes", len(specifiedBody), len(body))
	}
	// imperative ok, status code ok, headers ok, body ok
	return nil
}

package imperative

import (
	"io"
	"testing"
)

func TestAssertRequest(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200}, Request: Request{Method: "GET", Path: "/", Headers: map[string]string{"Content-Type": "application/json"}, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	req, err := imp.BuildRequest("")
	if err != nil {
		t.Errorf("BuildRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Errorf("ReadAll: %v", err)
	}

	if err := AssertRequest(req, body); err != nil {
		t.Errorf("AssertRequest: %v", err)
	}
}

func TestAssertRequest_InvalidBody(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200}, Request: Request{Method: "GET", Path: "/", Headers: map[string]string{"Content-Type": "application/json"}, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	req, err := imp.BuildRequest("")
	if err != nil {
		t.Errorf("BuildRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Errorf("ReadAll: %v", err)
	}
	// flip a single bit in the body
	body[512] ^= 1

	if err := AssertRequest(req, body); err == nil {
		t.Errorf("AssertRequest should have failed")
	}
}

func TestAssertRequest_InvalidHeaders(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200}, Request: Request{Method: "GET", Path: "/", Headers: map[string]string{"Content-Type": "application/json"}, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	req, err := imp.BuildRequest("")
	if err != nil {
		t.Errorf("BuildRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Errorf("ReadAll: %v", err)
	}
	// overwrite the content type
	req.Header.Set("Content-Type", "application/octet-stream")

	if err := AssertRequest(req, body); err == nil {
		t.Errorf("AssertRequest should have failed")
	}
}

func TestAssertRequest_InvalidPath(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200}, Request: Request{Method: "GET", Path: "/", Headers: map[string]string{"Content-Type": "application/json"}, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	req, err := imp.BuildRequest("")
	if err != nil {
		t.Errorf("BuildRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Errorf("ReadAll: %v", err)
	}
	// overwrite the path
	req.URL.Path = "/invalid"

	if err := AssertRequest(req, body); err == nil {
		t.Errorf("AssertRequest should have failed")
	}
}

func TestAssertRequest_InvalidMethod(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200}, Request: Request{Method: "POST", Path: "/", Headers: map[string]string{"Content-Type": "application/json"}, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	req, err := imp.BuildRequest("")
	if err != nil {
		t.Errorf("BuildRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Errorf("ReadAll: %v", err)
	}
	// overwrite the method
	req.Method = "GET"

	if err := AssertRequest(req, body); err == nil {
		t.Errorf("AssertRequest should have failed")
	}
}

// where are the tests for the response assertions?
// since it doesn't make sense to generate a http.Response from an Imperative
// because the http.Handler uses the http.ResponseWriter to write the response
// we test the response building logic in the ../server_test.go file :)

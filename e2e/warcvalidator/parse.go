package warcvalidator

import (
	"bufio"
	"io"
	"net/http"

	"github.com/internetarchive/gowarc/e2e/echoserver/imperative"
	"github.com/internetarchive/gowarc/pkg/spooledtempfile"
)

// parseHTTPResponse parses HTTP response from WARC content.
func parseHTTPResponse(content spooledtempfile.ReadSeekCloser) (*http.Response, error) {
	content.Seek(0, io.SeekStart)
	return http.ReadResponse(bufio.NewReader(content), nil)
}

// parseHTTPRequest parses HTTP request from WARC content.
// When passing a revisit record, reading the body will cause a panic!
func parseHTTPRequest(content spooledtempfile.ReadSeekCloser) (*http.Request, error) {
	content.Seek(0, io.SeekStart)
	return http.ReadRequest(bufio.NewReader(content))
}

func imperativeFromRequest(request *http.Request) (imperative.Imperative, error) {
	imp, err := imperative.ParseImperative(request.Header.Get(imperative.ImperativeHeader))
	if err != nil {
		return imperative.Imperative{}, err
	}
	return imp, nil
}

func imperativeFromResponse(response *http.Response) (imperative.Imperative, error) {
	imp, err := imperative.ParseImperative(response.Header.Get(imperative.ImperativeHeader))
	if err != nil {
		return imperative.Imperative{}, err
	}
	return imp, nil
}

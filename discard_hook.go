package warc

import (
	"fmt"
	"net/http"
)

type DiscardHookError struct {
	URL    string
	Reason string // reason for discarding
	Err    error  // nil: discarded successfully
}

func (e *DiscardHookError) Error() string {
	return fmt.Sprintf("response was blocked by DiscardHook. url: '%s', reason: '%s', err: %v", e.URL, e.Reason, e.Err)
}

func (e *DiscardHookError) Unwrap() error {
	return e.Err
}

// DiscardHook is a hook function that is called for each response. (if set)
// It can be used to determine if the response should be discarded.
// Returns:
//   - bool: should the response be discarded
//   - string: (optional) why the response was discarded or not
type DiscardHook func(resp *http.Response) (bool, string)

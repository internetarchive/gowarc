package imperative

type Imperative struct {
	// Describes the HTTP response the echo server shall produce
	Response Response `json:"response,omitempty"`

	// Opt. describes the request the echo server shall receive and assert
	Request Request `json:"request,omitempty"`

	// Opt. describes transport channel behaviour the echo server shall produce (e.g. close after bytes)
	Transport Transport `json:"transport,omitempty"`
}

type Response struct {
	// StatusCode is the HTTP status code.
	StatusCode int `json:"statusCode,omitempty"`

	// Headers are additional response headers set verbatim, allowing tests
	// to verify that gowarc preserves arbitrary response headers faithfully.
	Headers map[string]string `json:"headers,omitempty"`

	// Body configures the response body content and encoding.
	Body Body `json:"body,omitempty"`
}

type Request struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"` // a list of expected headers
	Method  string            `json:"method,omitempty"`
	// Body configures the HTTP request entity body (see [Imperative.RequestBody]).
	Body Body `json:"body,omitempty"`
}

type Transport struct {
	// Abort instructs the HTTP server to forcibly close the connection
	Abort bool `json:"abort,omitempty"`
	// Delay instructs the HTTP server to delay the response by n milliseconds
	Delay int `json:"delay,omitempty"`
}

const (
	EncodingUTF8   = ""       // printable characters (a–z, 0–9 cycling) — default
	EncodingBinary = "binary" // raw bytes (0x00–0xFF cycling)
)

type Body struct {
	// Length generates a deterministic body of exactly N bytes.
	Length int `json:"length,omitempty"`

	// Encoding controls the byte pattern when Length > 0:
	//   "" or "utf-8": printable characters (a–z, 0–9 cycling)
	//   "binary":      raw byte values (0x00–0xFF cycling)
	Encoding string `json:"encoding,omitempty"`

	// Compress applies HTTP content-encoding before sending the body.
	// Supported values: "gzip", "deflate", "zstd". Empty means no compression.
	Compress string `json:"compress,omitempty"`

	// Filepath serves a static file as the response body.
	// Path is relative to the echo server's configured test data directory.
	Filepath string `json:"filepath,omitempty"`

	// A random seed to generate a deterministic body
	Seed int `json:"seed,omitempty"`
}

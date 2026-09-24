package client

// Default timeout and retry constants (all in seconds)
const (
	// DefaultTimeoutSeconds is the default timeout for HTTP requests (in seconds)
	DefaultTimeoutSeconds = 3

	// DefaultMaxRetries is the default maximum number of retry attempts
	DefaultMaxRetries = 5

	// DefaultRetryDelaySeconds is the default initial delay between retries (in seconds)
	DefaultRetryDelaySeconds = 1

	// DefaultExponentialBackoff enables exponential backoff for retries by default
	DefaultExponentialBackoff = true
)

// MaxRetryDelaySeconds is the maximum delay between retry attempts (in seconds)
// Retry delays grow exponentially with exponential backoff but are capped at this value
const MaxRetryDelaySeconds = 60

// AuthType defines the authentication method
type AuthType int

const (
	AuthTypeToken AuthType = iota
	AuthTypeKey
)

// String returns the label used in debug logs
func (a AuthType) String() string {
	switch a {
	case AuthTypeToken:
		return "token"
	case AuthTypeKey:
		return "key"
	}
	return "unknown"
}

// Business-layer codes carried by the API envelope.
//
// Every one of them arrives on an HTTP 200 response, so the envelope is the
// only failure signal the API gives. 429 and 500 are the HTTP numbers reused as
// business codes by the server; the 1xxx/2xxx/4xxx/7xxx set is its own.
// Codes not listed here (1002, 1003, 2001-2003, 4001, 400, 404) are permanent
// rejections the caller has to fix something for.
const (
	codeSuccess             = 200
	codeThrottled           = 429
	codeServerError         = 500
	codePermissionDenied    = 1001
	codeUpstreamUnavailable = 7001
)

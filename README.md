# openapi-sdk-go

Go SDK for the Ghink OpenAPI (`api.gh.ink`).

This SDK provides a typed, retry-aware client for calling Ghink Open API services,
with built-in authentication, automatic token renewal, exponential-backoff retries,
request-ID tracing, and structured error handling.

## Features

- **Two authentication modes** — token-based (default, auto-renewing) or key-based (HTTP Basic).
- **Automatic retries** — network errors, non-2xx responses, and parse failures are retried.
- **Exponential backoff** — retry delays grow (1s → 2s → 4s → …) and are capped at 60s.
- **Request-ID tracing** — `x-request-id` is captured and attached to every error.
- **Structured errors** — sentinel errors compatible with `errors.Is` / `errors.As`, carrying API code, message, request ID, and the raw response.
- **Pluggable components** — custom logger, JSON marshaler, and unmarshaler.

## Requirements

- Go `1.26.3` or later

## Installation

```bash
go get go.gh.ink/openapi/sdk/20260512/v3
```

The module path is:

```
go.gh.ink/openapi/sdk/20260512/v3
```

## Quick Start

```go
package main

import (
	"fmt"
	"log"

	"go.gh.ink/openapi/sdk/20260512/v3/client"
	realname "go.gh.ink/openapi/sdk/20260512/v3/private/real_name"
)

func main() {
	// Create a client. By default, this acquires an auth token immediately.
	c, err := client.NewClient("your-secret-id", "your-secret-key")
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	// Verify a Chinese Mainland ID against a name.
	ok, err := realname.VerifyCNID(c, "11010119900307721X", "Zhang San")
	if err != nil {
		log.Fatalf("verify failed: %v", err)
	}

	fmt.Printf("verification result: %v\n", ok)
}
```

## Client Configuration

The client is created with `client.NewClient(secretID, secretKey, options...)` using the
functional-options pattern.

```go
c, err := client.NewClient(
	"your-secret-id",
	"your-secret-key",
	client.WithEndpoint("https://api.gh.ink/v3.1"),
	client.WithTimeout(5),                 // per-request HTTP timeout (seconds)
	client.WithMaxRetries(3),              // max retry attempts
	client.WithRetryDelay(2),              // initial retry delay (seconds)
	client.WithExponentialBackoff(true),   // grow delay between retries
	client.WithLogger(myLogger),           // custom Logger implementation
	client.WithMarshal(json.Marshal),      // custom JSON marshaler
	client.WithUnmarshal(json.Unmarshal),  // custom JSON unmarshaler
	client.EnableToken(true),              // use token auth (default true)
)
```

### Available options

| Option | Description | Default |
| --- | --- | --- |
| `WithEndpoint(string)` | API base endpoint | `https://api.gh.ink/v3.1` |
| `WithTimeout(int)` | Per-request HTTP timeout in seconds | `3` |
| `WithMaxRetries(int)` | Maximum retry attempts (≤ 0 resets to default) | `5` |
| `WithRetryDelay(int)` | Initial retry delay in seconds (≤ 0 resets to default) | `1` |
| `WithExponentialBackoff(bool)` | Grow retry delay on each attempt (capped at 60s) | `true` |
| `WithLogger(Logger)` | Custom logger implementation | stdout logger |
| `WithMarshal(func(any) ([]byte, error))` | Custom JSON marshaler | `json.Marshal` |
| `WithUnmarshal(func([]byte, any) error)` | Custom JSON unmarshaler | `json.Unmarshal` |
| `EnableToken(bool)` | Use token auth (`true`) or key auth (`false`) | `true` |

### Authentication

- **Token auth (default):** `NewClient` calls `GET /openapi/token` with HTTP Basic
  credentials to obtain a bearer token. Requests then send `Authorization: Bearer <token>`.
  If the API returns business code `1001` (permission denied / token expired), the SDK
  automatically renews the token and retries.
- **Key auth:** When `EnableToken(false)` is passed, requests use HTTP Basic auth
  (`Authorization: Basic base64(secretID:secretKey)`) and no token is acquired.

## Services

### Real-Name Verification — `private/real_name`

Verify a Chinese Mainland ID number (CNID) against a name. The SDK validates the ID
format locally (length, date, and checksum) before sending the request — invalid IDs
return `(false, nil)` without a network call.

```go
import realname "go.gh.ink/openapi/sdk/20260512/v3/private/real_name"

// Verify against the API (token auth).
ok, err := realname.VerifyCNID(c, "11010119900307721X", "Zhang San")

// Local-only helpers (no network call).
valid := realname.IsValidID("11010119900307721X") // format + checksum
ok2   := realname.IsValidDate(1990, 3, 7)          // date validity
```

The last character of a CNID may be `X` and is handled case-insensitively.

### Short Link — `public/short_link`

Create a short link, optionally with an expiry time.

```go
import (
	"time"

	shortlink "go.gh.ink/openapi/sdk/20260512/v3/public/short_link"
)

// No expiry.
linkID, err := shortlink.Add(c, "https://example.com/a/very/long/url", nil)

// With expiry.
expiry := time.Now().Add(24 * time.Hour)
linkID, err = shortlink.Add(c, "https://example.com/a/very/long/url", &expiry)
```

## Error Handling

All service errors are `*errors.SdkError` values built from predefined sentinels, so they
work with both `errors.Is` and `errors.As`.

Predefined sentinel errors:

| Sentinel | Meaning |
| --- | --- |
| `ErrTokenAcquisitionFailed` | Failed to acquire an authentication token |
| `ErrTokenUnmarshalFailed` | Failed to unmarshal the token response |
| `ErrRequestSendFailed` | Failed to send the request |
| `ErrResponseUnmarshalFailed` | Failed to unmarshal the response body |
| `ErrStatusError` | API returned a non-success business status code |

```go
import (
	stderrors "errors"

	sdkerrors "go.gh.ink/openapi/sdk/20260512/v3/errors"
)

ok, err := realname.VerifyCNID(c, "11010119900307721X", "Zhang San")
if err != nil {
	// Match against a sentinel.
	if stderrors.Is(err, sdkerrors.ErrStatusError) {
		// handle non-success API response
	}

	// Extract detailed fields.
	var sdkErr *sdkerrors.SdkError
	if stderrors.As(err, &sdkErr) {
		log.Printf(
			"api code: %d, api message: %s, request id: %s",
			sdkErr.ApiCode(),     // business error code (not HTTP status)
			sdkErr.ApiMessage(),  // business error message
			sdkErr.RequestID(),   // request ID for tracing
		)
		_ = sdkErr.Response()     // full raw response for debugging
	}
}
```

> Note: `ApiCode()` returns the business-layer code from the API payload (e.g. `200` = success,
> `1001` = permission denied), which is distinct from the HTTP status code.

## Logging

Provide any implementation of the `client.Logger` interface via `WithLogger`:

```go
type Logger interface {
	Debug(context.Context, ...any)
	Info(context.Context, ...any)
	Warn(context.Context, ...any)
	Error(context.Context, ...any)
}
```

The default logger writes to stdout with standard timestamps.

## Project Layout

```
v3/
├── meta.go                 # SDK version, endpoint, and User-Agent
├── client/                 # Core client, request/retry logic, logger, constants
├── errors/                 # SdkError type and predefined sentinel errors
├── private/
│   └── real_name/          # Real-name (CNID) verification service
└── public/
    └── short_link/         # Short link creation service
```

## Versioning

- SDK version: **v3.1.0**
- Default API endpoint: `https://api.gh.ink/v3.1`
- User-Agent: `GhinkOpenAPISDK-20260512-Go/3.1.0 (<os>; <arch>)`

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.

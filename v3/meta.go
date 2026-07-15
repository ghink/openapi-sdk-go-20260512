package openapi

import (
	"runtime"
)

const Endpoint = "https://api.gh.ink/v3.1"

const Version = "3.1.2"
const UserAgent = "GhinkOpenAPISDK-20260512-Go/" +
	Version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"

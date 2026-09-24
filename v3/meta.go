package openapi

import (
	"runtime"
)

const Endpoint = "https://api.gh.ink/v3.1"

const Version = "3.2.1"
const UserAgent = "GhinkOpenAPISDK-20260512-Go/" +
	Version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"

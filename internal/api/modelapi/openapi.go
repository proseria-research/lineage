package modelapi

import _ "embed"

// openapiSpec is the hand-authored OpenAPI 3.1 contract served at /v1/openapi.json. It is
// the source of truth from which the Python SDK and CLI are generated (§00.11.6, §10).
//
//go:embed openapi.json
var openapiSpec []byte

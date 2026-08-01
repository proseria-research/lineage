package modelapi

import _ "embed"

// openapiSpec is the hand-authored OpenAPI 3.1 contract served at /v1/openapi.json. It is
// the source of truth from which the Python SDK and CLI are generated (§00.11.6, §10).
//
//go:embed openapi.json
var openapiSpec []byte

// insightSchema is the versioned JSON Schema for insight submissions, served at
// /v1/insight-schema.json. It is published separately from the OpenAPI document because
// producers version against it independently of the API surface (§11.6.1, §11d).
//
//go:embed insight_schema.json
var insightSchema []byte

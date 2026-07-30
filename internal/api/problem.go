// Package api wires HTTP surfaces over the core and renders RFC 9457 errors (§03.9).
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/proseria-research/lineage/internal/domain"
)

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError renders any error as application/problem+json (§03.9). Domain errors carry
// their own code/status/details; everything else is a 500 internal.
func WriteError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		de = domain.Internal(err.Error())
	}
	body := map[string]any{
		"type":   "https://lineage.dev/errors/" + de.Code,
		"title":  de.Code,
		"status": de.Status(),
		"code":   de.Code,
		"detail": de.Message,
	}
	if len(de.Details) > 0 {
		body["details"] = de.Details
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(de.Status())
	_ = json.NewEncoder(w).Encode(body)
}

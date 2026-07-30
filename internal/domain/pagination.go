package domain

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// Cursor pagination (§03.3). The opaque pageToken encodes the (createdAt, id) of the last
// returned item; the next page is everything strictly after it in (createdAt DESC, id DESC)
// order. This is stable across concurrent inserts, unlike offset paging.

const (
	defaultPageSize = 50
	maxPageSize     = 500
)

// EncodeCursor builds an opaque page token from a sort key.
func EncodeCursor(createdAt int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(createdAt, 10) + "|" + id))
}

func decodeCursor(tok string) (createdAt int64, id string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return 0, "", false
	}
	ts, rest, found := strings.Cut(string(b), "|")
	if !found {
		return 0, "", false
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return n, rest, true
}

// ClampPageSize applies the default/limit from §03.3.
func ClampPageSize(size int) int {
	if size <= 0 {
		return defaultPageSize
	}
	if size > maxPageSize {
		return maxPageSize
	}
	return size
}

// Page slices items — which MUST already be sorted (createdAt DESC, id DESC) — starting
// after pageToken, returning up to pageSize items plus the next token (empty on the last
// page). key extracts the (createdAt, id) sort key from an item.
func Page[T any](items []T, key func(T) (int64, string), pageToken string, pageSize int) ([]T, string) {
	pageSize = ClampPageSize(pageSize)
	start := 0
	if pageToken != "" {
		if ts, id, ok := decodeCursor(pageToken); ok {
			start = len(items)
			for i, it := range items {
				cts, cid := key(it)
				if cts < ts || (cts == ts && cid < id) {
					start = i
					break
				}
			}
		}
	}
	if start >= len(items) {
		return []T{}, ""
	}
	end := start + pageSize
	if end >= len(items) {
		return items[start:], ""
	}
	page := items[start:end]
	lts, lid := key(page[len(page)-1])
	return page, EncodeCursor(lts, lid)
}

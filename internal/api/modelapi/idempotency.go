package modelapi

import (
	"bytes"
	"net/http"
	"sync"
	"time"
)

// Idempotency-Key support for POST creates (§03.1): a retry with the same key replays the
// original response instead of creating a second resource. Keyed in-process (single binary,
// v1); entries expire after a day. A future Redis-backed store fits behind the same shape.

type cachedResponse struct {
	status   int
	header   http.Header
	body     []byte
	storedAt time.Time
}

type idempotencyStore struct {
	mu  sync.Mutex
	m   map[string]cachedResponse
	ttl time.Duration
}

func newIdempotencyStore() *idempotencyStore {
	return &idempotencyStore{m: map[string]cachedResponse{}, ttl: 24 * time.Hour}
}

func (s *idempotencyStore) get(key string) (cachedResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cr, ok := s.m[key]
	if !ok {
		return cachedResponse{}, false
	}
	if time.Since(cr.storedAt) > s.ttl {
		delete(s.m, key)
		return cachedResponse{}, false
	}
	return cr, true
}

func (s *idempotencyStore) put(key string, cr cachedResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = cr
}

// idempotent wraps a create handler: with an Idempotency-Key header, a first call is captured
// and replayed on retries; a successful (2xx) response is what gets cached.
func (r *Router) idempotent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		key := req.Header.Get("Idempotency-Key")
		if key == "" {
			next(w, req)
			return
		}
		if cr, ok := r.idem.get(key); ok {
			replay(w, cr)
			return
		}
		rec := &captureWriter{header: http.Header{}, status: http.StatusOK}
		next(rec, req)
		if rec.status >= 200 && rec.status < 300 {
			r.idem.put(key, cachedResponse{status: rec.status, header: rec.header.Clone(), body: rec.body.Bytes(), storedAt: time.Now()})
		}
		replay(w, cachedResponse{status: rec.status, header: rec.header, body: rec.body.Bytes()})
	}
}

func replay(w http.ResponseWriter, cr cachedResponse) {
	for k, vs := range cr.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if cr.status != 0 {
		w.WriteHeader(cr.status)
	}
	_, _ = w.Write(cr.body)
}

// captureWriter buffers a handler's response so it can be cached and replayed.
type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) WriteHeader(s int)   { c.status = s }
func (c *captureWriter) Write(b []byte) (int, error) {
	return c.body.Write(b)
}

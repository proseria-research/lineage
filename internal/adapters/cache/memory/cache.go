// Package memcache is an in-process ResolutionCache (dev). Prod uses Redis behind the
// same port (§04.4). Keys are namespaced by model so InvalidateModel is a prefix sweep.
package memcache

import (
	"strings"
	"sync"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

type entry struct {
	val    []byte
	expiry time.Time
}

type Cache struct {
	mu sync.Mutex
	m  map[string]entry
}

func New() *Cache { return &Cache{m: map[string]entry{}} }

var _ domain.ResolutionCache = (*Cache)(nil)

func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expiry) {
		delete(c.m, key)
		return nil, false
	}
	return e.val, true
}

func (c *Cache) Set(key string, val []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = entry{val: val, expiry: time.Now().Add(ttl)}
}

// InvalidateModel drops every entry for a model (keys are "model|selector"), the
// event-driven invalidation of §04.4.
func (c *Cache) InvalidateModel(model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := model + "|"
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			delete(c.m, k)
		}
	}
}

// Package rediscache is a Redis-backed ResolutionCache for prod (§04.4), behind the same
// port as the in-memory dev cache. Per-model invalidation is O(1): each model has a
// generation counter, and cache keys embed the current generation — bumping it on
// publish/stage-change orphans the old keys (which then expire via TTL), so there is no
// SCAN/DEL sweep.
package rediscache

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/proseria-research/lineage/internal/domain"
)

type Cache struct {
	rdb *redis.Client
	ctx context.Context
}

var _ domain.ResolutionCache = (*Cache)(nil)

// New connects to Redis and pings it so a bad address fails fast at startup.
func New(addr, password string, db int) (*Cache, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, domain.Internal("redis: " + err.Error())
	}
	return &Cache{rdb: rdb, ctx: ctx}, nil
}

// NewClient wraps an existing client (used in tests against miniredis).
func NewClient(rdb *redis.Client) *Cache { return &Cache{rdb: rdb, ctx: context.Background()} }

func (c *Cache) Close() error { return c.rdb.Close() }

// modelOf extracts the model from a "model|selector" cache key (§04.4). Model names never
// contain '|', so the split is unambiguous.
func modelOf(key string) string {
	m, _, _ := strings.Cut(key, "|")
	return m
}

func (c *Cache) genKey(model string) string { return "lineage:gen:" + model }

func (c *Cache) realKey(key string) string {
	model := modelOf(key)
	gen, _ := c.rdb.Get(c.ctx, c.genKey(model)).Int64()
	return "lineage:res:" + model + ":" + strconv.FormatInt(gen, 10) + ":" + key
}

func (c *Cache) Get(key string) ([]byte, bool) {
	v, err := c.rdb.Get(c.ctx, c.realKey(key)).Bytes()
	if err != nil {
		return nil, false
	}
	return v, true
}

func (c *Cache) Set(key string, val []byte, ttl time.Duration) {
	_ = c.rdb.Set(c.ctx, c.realKey(key), val, ttl).Err()
}

// InvalidateModel bumps the model's generation, orphaning every cached entry for it (§04.4).
func (c *Cache) InvalidateModel(model string) {
	_ = c.rdb.Incr(c.ctx, c.genKey(model)).Err()
}

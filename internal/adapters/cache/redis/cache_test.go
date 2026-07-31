package rediscache_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/proseria-research/lineage/internal/adapters/cache/cachetest"
	rediscache "github.com/proseria-research/lineage/internal/adapters/cache/redis"
)

// The Redis adapter is verified against miniredis (an in-memory Redis server), so the full
// generation-based invalidation + TTL behavior runs with no external service.
func TestRedisCacheConformance(t *testing.T) {
	mr := miniredis.RunT(t)
	c := rediscache.NewClient(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	cachetest.Run(t, c)
}

func TestRedisCacheTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	c := rediscache.NewClient(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	// miniredis has a controllable clock — advance it instead of sleeping.
	cachetest.RunTTL(t, c, func(d time.Duration) { mr.FastForward(d) })
}

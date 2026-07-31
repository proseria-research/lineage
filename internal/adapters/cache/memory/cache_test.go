package memcache_test

import (
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/adapters/cache/cachetest"
	memcache "github.com/proseria-research/lineage/internal/adapters/cache/memory"
)

func TestMemoryCacheConformance(t *testing.T) {
	cachetest.Run(t, memcache.New())
}

func TestMemoryCacheTTL(t *testing.T) {
	c := memcache.New()
	// The memory cache uses the real clock; sleep past the short TTL.
	cachetest.RunTTL(t, c, func(d time.Duration) { time.Sleep(d) })
}

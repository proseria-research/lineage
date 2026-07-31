// Package cachetest is a shared conformance suite for any domain.ResolutionCache, so the
// memory and Redis adapters are held to identical behavior (§04.4).
package cachetest

import (
	"testing"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// Run exercises get/set, per-model invalidation isolation, and TTL expiry.
func Run(t *testing.T, c domain.ResolutionCache) {
	t.Helper()

	// Miss, then set + hit.
	if _, ok := c.Get("fraud|s=production"); ok {
		t.Fatal("expected miss on empty cache")
	}
	c.Set("fraud|s=production", []byte("v1"), time.Minute)
	c.Set("fraud|v=1.4.0", []byte("v2"), time.Minute)
	c.Set("churn|s=production", []byte("v3"), time.Minute)
	if v, ok := c.Get("fraud|s=production"); !ok || string(v) != "v1" {
		t.Fatalf("get after set = %q %v", v, ok)
	}

	// InvalidateModel drops only that model's entries.
	c.InvalidateModel("fraud")
	if _, ok := c.Get("fraud|s=production"); ok {
		t.Fatal("fraud entry should be invalidated")
	}
	if _, ok := c.Get("fraud|v=1.4.0"); ok {
		t.Fatal("all fraud entries should be invalidated")
	}
	if v, ok := c.Get("churn|s=production"); !ok || string(v) != "v3" {
		t.Fatalf("other model must survive invalidation: %q %v", v, ok)
	}

	// A fresh set after invalidation is retrievable (new generation).
	c.Set("fraud|s=production", []byte("v4"), time.Minute)
	if v, ok := c.Get("fraud|s=production"); !ok || string(v) != "v4" {
		t.Fatalf("re-set after invalidation = %q %v", v, ok)
	}
}

// RunTTL verifies entries expire; separated so callers can advance a fake clock (miniredis).
func RunTTL(t *testing.T, c domain.ResolutionCache, advance func(time.Duration)) {
	t.Helper()
	c.Set("m|s=production", []byte("x"), 50*time.Millisecond)
	if _, ok := c.Get("m|s=production"); !ok {
		t.Fatal("entry should exist before TTL")
	}
	advance(100 * time.Millisecond)
	if _, ok := c.Get("m|s=production"); ok {
		t.Fatal("entry should have expired after TTL")
	}
}

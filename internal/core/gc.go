package core

import (
	"context"
	"log"
	"time"

	"github.com/proseria-research/lineage/internal/domain"
)

// Garbage collection (§05.8). Default is **retain**: deleting an artifact drops its metadata
// row, never the bytes (they may be shared or referenced). Optional **sweep** reference-counts
// backend objects by uri and deletes those with no referencing artifact row, once older than a
// grace period — and only under a configured prefix, so Lineage never deletes objects it did
// not write.

// GCConfig configures the optional sweeper.
type GCConfig struct {
	Enabled  bool          // sweep vs retain
	Prefix   string        // storage prefix Lineage owns (path-scoped safety, §05.8)
	Grace    time.Duration // minimum object age before it is eligible
	Interval time.Duration // sweep period
}

// GCResult reports one sweep pass.
type GCResult struct {
	Scanned int
	Deleted int
}

// SweepGarbage runs one reference-counted GC pass over the default backend. An object is
// deleted only when (a) no artifact row references its uri and (b) it is older than grace.
func (s *Service) SweepGarbage(ctx context.Context, prefix string, grace time.Duration) (GCResult, error) {
	var res GCResult
	b := s.backend("")
	if b == nil {
		return res, domain.Internal("gc: no default storage backend")
	}
	objs, err := b.ListObjects(ctx, prefix)
	if err != nil {
		return res, err
	}
	cutoff := domain.NowMillis() - grace.Milliseconds()
	for _, o := range objs {
		res.Scanned++
		if o.ModifiedAt > cutoff {
			continue // too new — an in-flight upload may not be finalized yet
		}
		referenced, err := s.store.ArtifactRefsURI(ctx, o.URI)
		if err != nil {
			return res, err
		}
		if referenced {
			continue
		}
		if err := b.Delete(ctx, o.URI); err != nil {
			return res, err
		}
		res.Deleted++
	}
	return res, nil
}

// RunGC starts a background sweeper that runs until ctx is cancelled. It is a no-op when
// disabled (retain). Errors are logged, not fatal — GC is best-effort.
func (s *Service) RunGC(ctx context.Context, cfg GCConfig) {
	if !cfg.Enabled {
		return
	}
	go func() {
		t := time.NewTicker(cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				res, err := s.SweepGarbage(ctx, cfg.Prefix, cfg.Grace)
				if err != nil {
					log.Printf("gc: sweep error: %v", err)
					continue
				}
				if res.Deleted > 0 {
					log.Printf("gc: swept %d/%d unreferenced objects under %q", res.Deleted, res.Scanned, cfg.Prefix)
				}
			}
		}
	}()
}

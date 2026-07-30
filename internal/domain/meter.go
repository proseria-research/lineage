package domain

// Meter records domain-level telemetry at the points only the core knows about (§09.2): the
// resolve cache path, publishes, stage transitions, and finalize outcomes. The real backend
// is a Prometheus registry adapter; a no-op is used when metrics are off — so the core keeps
// depending only on this port, never on the metrics package (§01 dependency rule).
type Meter interface {
	ResolveServed(cacheHit bool)
	VersionPublished()
	StageTransitioned(to Stage, demotedIncumbent bool)
	UploadFinalized(seconds float64, digestMismatch bool)
	SignedURLMinted()
}

// NopMeter is the default do-nothing Meter.
type NopMeter struct{}

func (NopMeter) ResolveServed(bool)            {}
func (NopMeter) VersionPublished()             {}
func (NopMeter) StageTransitioned(Stage, bool) {}
func (NopMeter) UploadFinalized(float64, bool) {}
func (NopMeter) SignedURLMinted()              {}

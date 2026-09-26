package config

import "testing"

// LINEAGE_ACTOR_HEADER (§03.1): the default is X-Lineage-Actor, any valid header name can
// replace it, and a name no client could send refuses to start rather than attributing every
// write to nobody.
func TestActorHeader(t *testing.T) {
	t.Setenv("LINEAGE_ACTOR_HEADER", "")
	if c, err := Load(); err != nil || c.ActorHeader != "X-Lineage-Actor" {
		t.Fatalf("default: %q %v", c.ActorHeader, err)
	}
	t.Setenv("LINEAGE_ACTOR_HEADER", "X-Forwarded-User")
	if c, err := Load(); err != nil || c.ActorHeader != "X-Forwarded-User" {
		t.Fatalf("custom: %q %v", c.ActorHeader, err)
	}
	for _, bad := range []string{"X Forwarded User", "X-User:", "X-Üser"} {
		t.Setenv("LINEAGE_ACTOR_HEADER", bad)
		if _, err := Load(); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

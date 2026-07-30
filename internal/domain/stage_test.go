package domain

import "testing"

func TestStageTransitions(t *testing.T) {
	legal := [][2]Stage{
		{StageDraft, StageStaging},
		{StageStaging, StageProduction},
		{StageProduction, StageStaging},
		{StageProduction, StageArchived},
		{StageArchived, StageDraft},
	}
	for _, tc := range legal {
		if !CanTransition(tc[0], tc[1]) {
			t.Errorf("expected %s→%s to be legal", tc[0], tc[1])
		}
	}
	illegal := [][2]Stage{
		{StageDraft, StageProduction}, // must pass through staging
		{StageArchived, StageProduction},
		{StageDraft, StageDraft},
	}
	for _, tc := range illegal {
		if CanTransition(tc[0], tc[1]) {
			t.Errorf("expected %s→%s to be illegal", tc[0], tc[1])
		}
	}
}

func TestSingletonProduction(t *testing.T) {
	if !IsSingleton(StageProduction) {
		t.Error("production must be a singleton stage")
	}
	if IsSingleton(StageStaging) {
		t.Error("staging must not be a singleton stage by default")
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"fraud-detector", "bert.base", "m1", "a_b-c.d"} {
		if !ValidName(ok) {
			t.Errorf("expected %q to be valid", ok)
		}
	}
	for _, bad := range []string{"", "Upper", "-lead", "trail-", "has space", "sym$"} {
		if ValidName(bad) {
			t.Errorf("expected %q to be invalid", bad)
		}
	}
}

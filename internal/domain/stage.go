package domain

// stageGraph is the allowed stage-transition set (§02.4). Keyed by source stage.
var stageGraph = map[Stage]map[Stage]bool{
	StageDraft:      {StageStaging: true, StageArchived: true},
	StageStaging:    {StageProduction: true, StageDraft: true, StageArchived: true},
	StageProduction: {StageStaging: true, StageArchived: true},
	StageArchived:   {StageDraft: true},
}

// singletonStages may hold at most one version per model; promoting into one auto-demotes
// the incumbent to archived (§02.4). Configurable in a real build; production by default.
var singletonStages = map[Stage]bool{StageProduction: true}

// ValidStage reports whether s is a known stage.
func ValidStage(s Stage) bool { _, ok := stageGraph[s]; return ok }

// CanTransition reports whether from→to is a legal move.
func CanTransition(from, to Stage) bool { return stageGraph[from][to] }

// AllowedTargets lists the legal next stages from `from` (used in error details).
func AllowedTargets(from Stage) []string {
	out := make([]string, 0, len(stageGraph[from]))
	for s := range stageGraph[from] {
		out = append(out, string(s))
	}
	return out
}

// IsSingleton reports whether stage s permits only one version per model.
func IsSingleton(s Stage) bool { return singletonStages[s] }

// LocksArtifacts reports whether entering stage s freezes a version's artifact set
// (§00.11.19): what was tested is what ships, so the lock falls on the first entry into
// staging (or production, should the graph ever allow draft→production directly).
func LocksArtifacts(s Stage) bool { return s == StageStaging || s == StageProduction }

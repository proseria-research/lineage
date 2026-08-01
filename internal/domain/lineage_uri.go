package domain

import "strings"

// LineageRef is a parsed lineage:// reference (§04.5): a stable, stage-aware pointer that
// resolves at pull time, so a promotion takes effect with no manifest change.
//
//	lineage://<model>[/<stage>][@<version>][#<artifact>]
type LineageRef struct {
	Model    string
	Stage    Stage  // optional; empty ⇒ default serving stage (production)
	Version  string // optional; exact version name (overrides stage)
	Artifact string // optional; a named artifact within the version
}

// Selector maps a LineageRef to a resolution selector (§04.2). Version wins over stage;
// an empty ref resolves the default serving stage.
func (r LineageRef) Selector() Selector {
	switch {
	case r.Version != "":
		return Selector{Version: r.Version}
	case r.Stage != "":
		return Selector{Stage: r.Stage}
	default:
		return Selector{}
	}
}

// ParseLineageURI parses a lineage:// URI (§04.5). Order of the optional parts is fixed:
// the artifact fragment (#) is stripped first, then the version (@), leaving <model>[/<stage>].
func ParseLineageURI(s string) (LineageRef, error) {
	rest, ok := strings.CutPrefix(s, "lineage://")
	if !ok {
		return LineageRef{}, Invalid("not a lineage:// uri: '" + s + "'")
	}
	var ref LineageRef
	if before, frag, ok := strings.Cut(rest, "#"); ok {
		if frag == "" {
			return LineageRef{}, Invalid("empty artifact fragment in '" + s + "'")
		}
		rest, ref.Artifact = before, frag
	}
	if before, ver, ok := strings.Cut(rest, "@"); ok {
		if ver == "" {
			return LineageRef{}, Invalid("empty version in '" + s + "'")
		}
		rest, ref.Version = before, ver
	}
	model, stage, hasStage := strings.Cut(rest, "/")
	ref.Model = model
	if !ValidName(ref.Model) {
		return LineageRef{}, Invalid("invalid model in lineage uri: '" + s + "'")
	}
	if hasStage {
		if stage == "" || !ValidStage(Stage(stage)) {
			return LineageRef{}, Invalid("invalid stage in lineage uri: '" + s + "'")
		}
		ref.Stage = Stage(stage)
	}
	if ref.Artifact != "" && !ValidArtifactName(ref.Artifact) {
		return LineageRef{}, Invalid("invalid artifact in lineage uri: '" + s + "'")
	}
	return ref, nil
}

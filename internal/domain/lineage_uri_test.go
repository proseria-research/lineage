package domain

import "testing"

func TestParseLineageURI(t *testing.T) {
	cases := []struct {
		in       string
		model    string
		stage    Stage
		version  string
		artifact string
	}{
		{"lineage://fraud-detector", "fraud-detector", "", "", ""},
		{"lineage://fraud-detector/staging", "fraud-detector", StageStaging, "", ""},
		{"lineage://fraud-detector@1.4.0", "fraud-detector", "", "1.4.0", ""},
		{"lineage://fraud-detector/production#model.onnx", "fraud-detector", StageProduction, "", "model.onnx"},
		{"lineage://m/staging@2.0.0#weights.bin", "m", StageStaging, "2.0.0", "weights.bin"},
	}
	for _, c := range cases {
		ref, err := ParseLineageURI(c.in)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", c.in, err)
		}
		if ref.Model != c.model || ref.Stage != c.stage || ref.Version != c.version || ref.Artifact != c.artifact {
			t.Errorf("%s → %+v, want model=%s stage=%s version=%s artifact=%s",
				c.in, ref, c.model, c.stage, c.version, c.artifact)
		}
	}
}

func TestParseLineageURISelector(t *testing.T) {
	// version wins over stage; bare ref → default serving stage (empty selector).
	if s := mustRef(t, "lineage://m/staging@1.0.0").Selector(); s.Version != "1.0.0" {
		t.Fatalf("version should win: %+v", s)
	}
	if s := mustRef(t, "lineage://m/staging").Selector(); s.Stage != StageStaging {
		t.Fatalf("stage selector expected: %+v", s)
	}
	if s := mustRef(t, "lineage://m").Selector(); s.Stage != "" || s.Version != "" {
		t.Fatalf("bare ref should be the default (empty) selector: %+v", s)
	}
}

func TestParseLineageURIErrors(t *testing.T) {
	bad := []string{
		"s3://not-lineage",
		"lineage://",           // empty model
		"lineage://Bad_Name!",  // invalid model name
		"lineage://m/nonsense", // invalid stage
		"lineage://m@",         // empty version
		"lineage://m#",         // empty artifact
	}
	for _, in := range bad {
		if _, err := ParseLineageURI(in); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func mustRef(t *testing.T, s string) LineageRef {
	t.Helper()
	ref, err := ParseLineageURI(s)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"
)

func TestLineageDeploymentInsightAndAuditSurviveRestart(t *testing.T) {
	stateDir := t.TempDir()
	s := startServer(t, stateDir)
	const model = "cross-surface"
	createModelVersion(t, s, model, "1.0.0")
	s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions", map[string]any{
		"name": "2.0.0", "author": actor,
	}, http.StatusCreated)

	s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions/2.0.0/lineage", map[string]any{
		"relation": "derived_from", "to": map[string]string{"version": "1.0.0"},
	}, http.StatusCreated)
	s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions/2.0.0/lineage", map[string]any{
		"relation": "trained_on", "to": map[string]string{"uri": "s3://datasets/training-v2"},
	}, http.StatusCreated)
	s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions/2.0.0/deployments", map[string]any{
		"environment": "production", "endpointUri": "https://model.internal/v2", "externalRef": "kserve/prod/model",
	}, http.StatusCreated)
	s.json(t, http.MethodPatch, s.modelURL+"/v1/models/"+model+"/versions/2.0.0/insight", map[string]any{
		"schemaVersion": "1", "source": "derived", "reporter": "e2e-inspector", "reporterVersion": "1.0",
		"facts": map[string]any{"paramCountTotal": 123456, "dtypeDominant": "fp16"},
	}, http.StatusOK)
	s.stop(t)

	restarted := startServer(t, stateDir)
	graph := restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/models/"+model+"/versions/2.0.0/lineage?direction=both", nil, http.StatusOK)
	if len(graph["edges"].([]any)) != 2 || len(graph["nodes"].([]any)) != 3 {
		t.Fatalf("persisted lineage graph=%+v", graph)
	}
	deployments := restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/models/"+model+"/versions/2.0.0/deployments", nil, http.StatusOK)
	if len(deployments["items"].([]any)) != 1 {
		t.Fatalf("persisted deployments=%+v", deployments)
	}
	insight := restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/models/"+model+"/versions/2.0.0/insight", nil, http.StatusOK)
	if insight["paramCountTotal"] != float64(123456) || insight["dtypeDominant"] != "fp16" {
		t.Fatalf("persisted insight=%+v", insight)
	}
	admin := restarted.json(t, http.MethodGet, restarted.adminURL+"/api/models/"+model+"/versions/2.0.0", nil, http.StatusOK)
	if admin["insight"] == nil || len(admin["deployments"].([]any)) != 1 {
		t.Fatalf("admin BFF does not compose persisted state=%+v", admin)
	}

	audit := restarted.json(t, http.MethodGet, restarted.modelURL+"/v1/audit?pageSize=100", nil, http.StatusOK)
	wantActions := map[string]int{"lineage.add": 2, "deployment.create": 1, "insight.update": 1}
	seenActions := map[string]int{}
	for _, raw := range audit["items"].([]any) {
		event := raw.(map[string]any)
		if event["actor"] != actor {
			continue
		}
		if action := stringValue(event["action"]); action != "" {
			if _, tracked := wantActions[action]; tracked {
				seenActions[action]++
				if stringValue(event["subjectType"]) == "" || stringValue(event["subjectId"]) == "" || stringValue(event["summary"]) == "" || event["at"] == nil {
					t.Fatalf("audit event is incomplete: %+v", event)
				}
			}
		}
	}
	for action, want := range wantActions {
		if seenActions[action] != want {
			t.Fatalf("audit action %q count=%d want=%d for actor %q: %+v", action, seenActions[action], want, actor, audit)
		}
	}
}

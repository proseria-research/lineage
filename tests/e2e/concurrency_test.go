//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

func TestConcurrentProductionPromotionKeepsSingleton(t *testing.T) {
	s := startServer(t, t.TempDir())

	// Several independent races make this useful under both the regular suite and -race
	// without making the assertion depend on which request wins.
	for round := 0; round < 5; round++ {
		model := fmt.Sprintf("promotion-race-%d", round)
		createModelVersion(t, s, model, "1.0.0")
		s.json(t, http.MethodPost, s.modelURL+"/v1/models/"+model+"/versions", map[string]any{
			"name": "2.0.0", "author": actor,
		}, http.StatusCreated)
		transition(t, s, model, "1.0.0", "staging")
		transition(t, s, model, "2.0.0", "staging")

		payload, _ := json.Marshal(map[string]string{"to": "production", "reason": "concurrent E2E promotion"})
		start := make(chan struct{})
		results := make(chan response, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, version := range []string{"1.0.0", "2.0.0"} {
			version := version
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				result, err := s.doRequest(http.MethodPost,
					s.modelURL+"/v1/models/"+model+"/versions/"+version+":transition",
					payload, map[string]string{"X-Lineage-Actor": actor})
				if err != nil {
					errs <- err
					return
				}
				results <- result
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent promotion request: %v", err)
		}
		for result := range results {
			if result.status != http.StatusOK {
				t.Fatalf("concurrent promotion = %d, want 200; body=%s", result.status, result.body)
			}
		}

		listed := s.json(t, http.MethodGet, s.modelURL+"/v1/models/"+model+"/versions", nil, http.StatusOK)
		items, _ := listed["items"].([]any)
		production, archived := 0, 0
		for _, raw := range items {
			version, _ := raw.(map[string]any)
			switch version["stage"] {
			case "production":
				production++
			case "archived":
				archived++
			}
		}
		if production != 1 || archived != 1 {
			t.Fatalf("round %d stages: production=%d archived=%d; versions=%+v", round, production, archived, items)
		}
	}
}

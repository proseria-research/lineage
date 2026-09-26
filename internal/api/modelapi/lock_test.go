package modelapi_test

import (
	"net/http"
	"testing"

	"github.com/proseria-research/lineage/internal/domain"
)

// A locked version (§00.11.19) refuses artifact writes with 409 + details.reason, carries
// lockedAt on its JSON, and still takes metadata edits.
func TestLockedVersionOverHTTP(t *testing.T) {
	srv := holdableServer(t, apiServer(t))
	const v = "/v1/models/m/versions/1.0.0"

	if code, body := do(t, srv, "POST", v+"/artifacts", `{"name":"w","uri":"s3://b/w"}`, nil); code != http.StatusCreated {
		t.Fatalf("register on a draft: %d %v", code, body)
	}
	if _, body := do(t, srv, "GET", v, "", nil); body["lockedAt"] != nil {
		t.Fatalf("a draft carries lockedAt: %v", body)
	}
	if code, body := do(t, srv, "POST", v+":transition", `{"to":"staging"}`, nil); code != http.StatusOK {
		t.Fatalf("transition: %d %v", code, body)
	}
	if _, body := do(t, srv, "GET", v, "", nil); body["lockedAt"] == nil {
		t.Fatalf("a staged version has no lockedAt: %v", body)
	}

	for _, r := range []struct{ method, path, body string }{
		{"POST", v + "/artifacts", `{"name":"x","uri":"s3://b/x"}`},
		{"POST", v + "/artifacts:initiateUpload", `{"name":"y.bin"}`},
		{"DELETE", v + "/artifacts/w", ""},
	} {
		code, body := do(t, srv, r.method, r.path, r.body, nil)
		if code != http.StatusConflict || body["code"] != "failed_precondition" || details(t, body)["reason"] != domain.RefusedVersionLocked {
			t.Fatalf("%s %s: %d %v, want 409 version_locked", r.method, r.path, code, body)
		}
	}

	if code, body := do(t, srv, "PATCH", v+"/artifacts/w", `{"mediaType":"application/x-onnx"}`, nil); code != http.StatusOK {
		t.Fatalf("metadata patch on a locked artifact: %d %v", code, body)
	}
	if code, body := do(t, srv, "PATCH", v, `{"description":"still editable"}`, nil); code != http.StatusOK {
		t.Fatalf("version patch on a locked version: %d %v", code, body)
	}
}

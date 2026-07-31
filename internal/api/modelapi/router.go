// Package modelapi serves the machine-facing Model API on /v1 (§03/§04). No auth —
// infra handles it; the actor comes from a trusted header for audit (§00 axiom 4).
package modelapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/core"
	"github.com/proseria-research/lineage/internal/domain"
)

type Router struct {
	svc         *core.Service
	actorHeader string
	idem        *idempotencyStore
}

func New(svc *core.Service, actorHeader string) *Router {
	return &Router{svc: svc, actorHeader: actorHeader, idem: newIdempotencyStore()}
}

// Handler registers routes on a Go 1.22+ ServeMux (method + path patterns).
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()

	// Models
	mux.HandleFunc("POST /v1/models", r.idempotent(r.createModel))
	mux.HandleFunc("GET /v1/models", r.listModels)
	mux.HandleFunc("GET /v1/models/{model}", r.getModel)
	mux.HandleFunc("PATCH /v1/models/{model}", r.patchModel)
	mux.HandleFunc("DELETE /v1/models/{model}", r.deleteModel)
	// {model}:archive shares a segment with a wildcard, so it's parsed from a catch route.
	mux.HandleFunc("POST /v1/models/{modelAction}", r.modelAction)
	mux.HandleFunc("GET /v1/models/{model}/resolve", r.resolve)
	mux.HandleFunc("GET /v1/models/{model}/audit", r.modelAudit)

	// Versions
	mux.HandleFunc("POST /v1/models/{model}/versions", r.idempotent(r.publishVersion))
	mux.HandleFunc("GET /v1/models/{model}/versions", r.listVersions)
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}", r.getVersion)
	mux.HandleFunc("PATCH /v1/models/{model}/versions/{version}", r.patchVersion)
	mux.HandleFunc("DELETE /v1/models/{model}/versions/{version}", r.deleteVersion)
	// {version}:transition lives in the trailing segment, parsed below.
	mux.HandleFunc("POST /v1/models/{model}/versions/{action}", r.versionAction)

	// Artifacts: register-by-reference + the signed upload flow (§05.6). ':' is a literal
	// path char to ServeMux, so the colon-actions register as distinct routes.
	mux.HandleFunc("POST /v1/models/{model}/versions/{version}/artifacts", r.registerArtifact)
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}/artifacts", r.listArtifacts)
	mux.HandleFunc("POST /v1/models/{model}/versions/{version}/artifacts:initiateUpload", r.initiateUpload)
	mux.HandleFunc("PUT /v1/models/{model}/versions/{version}/artifacts:uploadContent", r.uploadContent)
	mux.HandleFunc("POST /v1/models/{model}/versions/{version}/artifacts:finalizeUpload", r.finalizeUpload)
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}/artifacts/{artifact}", r.getArtifact)
	mux.HandleFunc("PATCH /v1/models/{model}/versions/{version}/artifacts/{artifact}", r.patchArtifact)
	mux.HandleFunc("DELETE /v1/models/{model}/versions/{version}/artifacts/{artifact}", r.deleteArtifact)
	// Broker fetch: 302 to a fresh signed URL, or stream-through where the backend can't sign (§04.3).
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}/artifacts/{artifact}/content", r.fetchContent)

	// Lineage
	mux.HandleFunc("POST /v1/models/{model}/versions/{version}/lineage", r.addLineage)
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}/lineage", r.listLineage)
	mux.HandleFunc("DELETE /v1/models/{model}/versions/{version}/lineage/{edgeId}", r.deleteLineage)

	// Deployments
	mux.HandleFunc("POST /v1/models/{model}/versions/{version}/deployments", r.createDeployment)
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}/deployments", r.listDeployments)
	mux.HandleFunc("GET /v1/models/{model}/versions/{version}/deployments/{id}", r.getDeployment)
	mux.HandleFunc("PATCH /v1/models/{model}/versions/{version}/deployments/{id}", r.patchDeployment)
	mux.HandleFunc("DELETE /v1/models/{model}/versions/{version}/deployments/{id}", r.deleteDeployment)

	// Global audit feed + OpenAPI contract
	mux.HandleFunc("GET /v1/audit", r.auditFeed)
	mux.HandleFunc("GET /v1/openapi.json", r.openapi)
	return mux
}

func (r *Router) actor(req *http.Request) string { return req.Header.Get(r.actorHeader) }

func (r *Router) createModel(w http.ResponseWriter, req *http.Request) {
	var in core.CreateModelInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	m, err := r.svc.CreateModel(req.Context(), r.actor(req), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, m)
}

func (r *Router) listModels(w http.ResponseWriter, req *http.Request) {
	items, next, err := r.svc.ListModels(req.Context(), listOpts(req))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}

func (r *Router) getModel(w http.ResponseWriter, req *http.Request) {
	m, err := r.svc.GetModel(req.Context(), req.PathValue("model"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, m)
}

func (r *Router) publishVersion(w http.ResponseWriter, req *http.Request) {
	var in core.PublishVersionInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	v, arts, err := r.svc.PublishVersion(req.Context(), r.actor(req), req.PathValue("model"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, map[string]any{"version": v, "artifacts": arts})
}

func (r *Router) listVersions(w http.ResponseWriter, req *http.Request) {
	items, next, err := r.svc.ListVersions(req.Context(), req.PathValue("model"), listOpts(req))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextPageToken": next})
}

func (r *Router) getVersion(w http.ResponseWriter, req *http.Request) {
	v, err := r.svc.GetVersion(req.Context(), req.PathValue("model"), req.PathValue("version"))
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, v)
}

// versionAction handles the `{version}:transition` colon-action (§03.7).
func (r *Router) versionAction(w http.ResponseWriter, req *http.Request) {
	seg := req.PathValue("action")
	version, action, ok := strings.Cut(seg, ":")
	if !ok {
		api.WriteError(w, domain.Invalid("unknown version action '"+seg+"'"))
		return
	}
	switch action {
	case "transition":
		var body struct {
			To     domain.Stage `json:"to"`
			Reason string       `json:"reason"`
		}
		if err := decode(req, &body); err != nil {
			api.WriteError(w, err)
			return
		}
		v, err := r.svc.Transition(req.Context(), r.actor(req), req.PathValue("model"), version, body.To, body.Reason)
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, v)
	default:
		api.WriteError(w, domain.Invalid("unknown action ':"+action+"'"))
	}
}

// ---- Artifacts & upload (§05.6) ----

func (r *Router) registerArtifact(w http.ResponseWriter, req *http.Request) {
	var in core.ArtifactInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	a, err := r.svc.RegisterArtifact(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, a)
}

func (r *Router) initiateUpload(w http.ResponseWriter, req *http.Request) {
	var in core.InitiateUploadInput
	if err := decode(req, &in); err != nil {
		api.WriteError(w, err)
		return
	}
	t, err := r.svc.InitiateUpload(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), in)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusAccepted, t)
}

// uploadContent is the stream-through sink for non-signing backends (§05.6). Bytes are the
// raw request body; the uploadId comes from the query string returned by initiate.
func (r *Router) uploadContent(w http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("uploadId")
	if id == "" {
		api.WriteError(w, domain.Invalid("missing uploadId"))
		return
	}
	defer req.Body.Close()
	if err := r.svc.UploadContent(req.Context(), id, req.Body, req.ContentLength); err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) finalizeUpload(w http.ResponseWriter, req *http.Request) {
	var body struct {
		UploadID string                 `json:"uploadId"`
		Digest   string                 `json:"digest"`
		Parts    []domain.MultipartPart `json:"parts"`
	}
	if err := decode(req, &body); err != nil {
		api.WriteError(w, err)
		return
	}
	a, err := r.svc.FinalizeUpload(req.Context(), r.actor(req), req.PathValue("model"), req.PathValue("version"), body.UploadID, body.Digest, body.Parts)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, a)
}

func (r *Router) resolve(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	sel := domain.Selector{Stage: domain.Stage(q.Get("stage")), Version: q.Get("version")}
	for k, vs := range q {
		if strings.HasPrefix(k, "label.") && len(vs) > 0 {
			sel.LabelKey, sel.LabelValue = strings.TrimPrefix(k, "label."), vs[0]
		}
	}
	res, err := r.svc.Resolve(req.Context(), req.PathValue("model"), sel)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	// HTTP caching (§04.2): ETag = resolution digest. `no-cache` = clients may store but must
	// revalidate, so a fresh signed URL is minted whenever the selection actually changed.
	if res.Digest != "" {
		etag := `"` + res.Digest + `"`
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, no-cache")
		if matchesETag(req.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	api.WriteJSON(w, http.StatusOK, res)
}

// fetchContent brokers an artifact's bytes (§04.3): 302 to a fresh signed URL by default, or
// stream the bytes through (with Range + conditional support) when the backend can't sign or
// the caller passes ?mode=stream.
func (r *Router) fetchContent(w http.ResponseWriter, req *http.Request) {
	forceStream := req.URL.Query().Get("mode") == "stream"
	f, err := r.svc.FetchArtifact(req.Context(), req.PathValue("model"), req.PathValue("version"), req.PathValue("artifact"), forceStream)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	if f.SignedURL != "" {
		http.Redirect(w, req, f.SignedURL, http.StatusFound)
		return
	}
	defer f.Stream.Close()

	if f.Artifact.Digest != "" {
		etag := `"` + f.Artifact.Digest + `"`
		w.Header().Set("ETag", etag)
		if matchesETag(req.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if f.Artifact.MediaType != "" {
		w.Header().Set("Content-Type", f.Artifact.MediaType)
	}
	// A seekable stream (fs files) gets full Range/conditional handling via ServeContent;
	// otherwise stream straight through with a known length.
	if rs, ok := f.Stream.(io.ReadSeeker); ok {
		http.ServeContent(w, req, f.Artifact.Name, time.Time{}, rs)
		return
	}
	if f.Artifact.SizeBytes > 0 {
		w.Header().Set("Content-Length", strconvI(f.Artifact.SizeBytes))
	}
	_, _ = io.Copy(w, f.Stream)
}

// openapi serves the hand-authored OpenAPI contract (§00.11.6, §10) — the source of truth
// for the generated SDK/CLI.
func (r *Router) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(openapiSpec)
}

// ---- helpers ----

// matchesETag reports whether an If-None-Match header value matches etag (or is "*"). It
// tolerates comma-separated lists and a weak-validator prefix (§04.2).
func matchesETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	for tok := range strings.SplitSeq(ifNoneMatch, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "*" || tok == etag || strings.TrimPrefix(tok, "W/") == etag {
			return true
		}
	}
	return false
}

func strconvI(n int64) string {
	return strconv.FormatInt(n, 10)
}

func decode(req *http.Request, v any) error {
	if req.Body == nil {
		return domain.Invalid("missing request body")
	}
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.Invalid("invalid JSON: " + err.Error())
	}
	return nil
}

func listOpts(req *http.Request) domain.ListOptions {
	q := req.URL.Query()
	o := domain.ListOptions{
		OrderBy: q.Get("orderBy"), Q: q.Get("q"), PageToken: q.Get("pageToken"),
		Filters: map[string]string{}, Labels: map[string]string{},
	}
	if n, err := strconv.Atoi(q.Get("pageSize")); err == nil {
		o.PageSize = n
	}
	if s := q.Get("state"); s != "" {
		o.Filters["state"] = s
	}
	if s := q.Get("stage"); s != "" {
		o.Filters["stage"] = s
	}
	for k, vs := range q {
		if len(vs) == 0 {
			continue
		}
		if strings.HasPrefix(k, "label.") {
			o.Labels[strings.TrimPrefix(k, "label.")] = vs[0]
		}
		if strings.HasPrefix(k, "cp.") { // custom_properties filter (Postgres-only, §02.7)
			if o.CustomProps == nil {
				o.CustomProps = map[string]string{}
			}
			o.CustomProps[strings.TrimPrefix(k, "cp.")] = vs[0]
		}
	}
	return o
}

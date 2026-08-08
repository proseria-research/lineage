package modelapi

import (
	"net/http"

	"github.com/proseria-research/lineage/internal/api"
	"github.com/proseria-research/lineage/internal/domain"
)

// Legal hold and the retention floor over HTTP (§19.7).
//
//	POST /v1/models/{m}:hold                       ·  …:release
//	POST /v1/models/{m}/versions/{v}:hold          ·  …:release
//	GET  /v1/retention
//
// The colon-actions arrive through the existing catch routes in modelAction/versionAction —
// ':' is a literal path character to ServeMux, so a trailing `{model}` wildcard swallows
// `fraud-detector:hold` and the segment is split by the dispatcher (§03.1).

// holdBody is shared by both directions. `reason` is required either way: it is recorded on
// the audit event, never on the row (§19.7.1), so it is the only durable record of why the
// hold was placed — or why it was lifted, which §19.3.1 calls the event an auditor cares
// about.
type holdBody struct {
	Reason string `json:"reason"`
}

// holdAction serves :hold and :release for either subject. subject is the name (models) or
// the version name (versions); for a version the model comes from the path.
func (r *Router) holdAction(w http.ResponseWriter, req *http.Request, subjectType, action, subject string) {
	var body holdBody
	if err := decode(req, &body); err != nil {
		api.WriteError(w, err)
		return
	}
	actor := r.actor(req)
	ctx := req.Context()
	hold := action == "hold"

	if subjectType == domain.SubjectModel {
		var m *domain.Model
		var err error
		if hold {
			m, err = r.svc.SetModelHold(ctx, actor, subject, body.Reason)
		} else {
			m, err = r.svc.ReleaseModelHold(ctx, actor, subject, body.Reason)
		}
		writeOr(w, http.StatusOK, m, err)
		return
	}

	model := req.PathValue("model")
	var v *domain.ModelVersion
	var err error
	if hold {
		v, err = r.svc.SetVersionHold(ctx, actor, model, subject, body.Reason)
	} else {
		v, err = r.svc.ReleaseVersionHold(ctx, actor, model, subject, body.Reason)
	}
	writeOr(w, http.StatusOK, v, err)
}

// retention reports the floor in force (§19.4).
//
// It reports what the process is actually running under, not what a chart intended. The
// distinction is the point: a filing that cites a retention period is citing this number, and
// "what someone believes was configured" is exactly the failure mode §19.4 calls out.
func (r *Router) retention(w http.ResponseWriter, _ *http.Request) {
	api.WriteJSON(w, http.StatusOK, r.svc.Retention())
}

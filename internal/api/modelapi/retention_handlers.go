package modelapi

import (
	"net/http"
	"strconv"
	"strings"

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

// ---- Attestation (§19.7.2, §19.7.3) ----

//	GET /v1/audit:verify[?fromEpoch=&toEpoch=]
//	GET /v1/audit/{id}:proof

func (r *Router) verifyAudit(w http.ResponseWriter, req *http.Request) {
	from := intQuery(req, "fromEpoch")
	to := intQuery(req, "toEpoch")
	res, err := r.svc.VerifyAudit(req.Context(), from, to)
	if err != nil {
		api.WriteError(w, err)
		return
	}
	// A detected break is a **200 with ok:false**, not an error status. The request
	// succeeded; the answer is bad news. A 4xx/5xx here would be indistinguishable from the
	// endpoint being broken, which is the one confusion a tamper-evidence report cannot
	// afford.
	api.WriteJSON(w, http.StatusOK, res)
}

// auditAction handles `{id}:proof`, parsed from the trailing segment the way model and
// version colon-actions are (§03.1).
func (r *Router) auditAction(w http.ResponseWriter, req *http.Request) {
	seg := req.PathValue("idAction")
	id, action, ok := strings.Cut(seg, ":")
	if !ok {
		api.WriteError(w, domain.Invalid("unknown audit action '"+seg+"'"))
		return
	}
	switch action {
	case "proof":
		p, err := r.svc.ProveAudit(req.Context(), id)
		writeOr(w, http.StatusOK, p, err)
	default:
		api.WriteError(w, domain.Invalid("unknown action ':"+action+"'"))
	}
}

// intQuery reads a non-negative integer query parameter; absent or unparseable is 0, which
// every caller treats as "unbounded".
func intQuery(req *http.Request, key string) int64 {
	v := req.URL.Query().Get(key)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

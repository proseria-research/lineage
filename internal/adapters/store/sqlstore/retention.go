package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/proseria-research/lineage/internal/domain"
)

// RetentionStore (§19.3) — legal hold, and the one question the delete path asks.

// SetHold writes both hold columns in one statement, so they are never observed apart. A nil
// h releases: the release NULLs held_since and blanks held_by rather than keeping them, since
// the row holds current state and the trail holds history (`hold.set` / `hold.release`).
func (s *Store) SetHold(ctx context.Context, subjectType, subjectID string, h *domain.Hold) error {
	var table string
	switch subjectType {
	case domain.SubjectModel:
		table = "model"
	case domain.SubjectVersion:
		table = "model_version"
	default:
		return domain.Invalid("cannot hold subject type '" + subjectType + "'")
	}
	var since any
	var by string
	if h != nil {
		since, by = h.HeldSince, h.HeldBy
	}
	res, err := s.q.ExecContext(ctx, s.rb(
		`UPDATE `+table+` SET held_since=?,held_by=? WHERE id=?`), since, by, subjectID)
	return affected(res, err, subjectType)
}

// DeleteGuardFor answers in one round trip per subject kind.
func (s *Store) DeleteGuardFor(ctx context.Context, subjectType, subjectID string) (domain.DeleteGuard, error) {
	switch subjectType {
	case domain.SubjectModel:
		return s.modelDeleteGuard(ctx, subjectID)
	case domain.SubjectVersion:
		return s.versionDeleteGuard(ctx, subjectID)
	}
	return domain.DeleteGuard{}, domain.Invalid("cannot hold subject type '" + subjectType + "'")
}

// modelDeleteGuard accounts for the cascade. Deleting a model destroys every version under
// it, so a hold on any one of them refuses (inheritance upward — see the port doc), and the
// floor measures the youngest row the cascade would take, not the model's own age.
//
// The held version is picked by `MIN(id)`, i.e. the earliest ULID, so a model with several
// held versions names the same one on every call rather than whichever the planner reached
// first.
func (s *Store) modelDeleteGuard(ctx context.Context, id string) (domain.DeleteGuard, error) {
	const q = `
		SELECT m.held_since, m.held_by, m.created_at, m.name,
		       (SELECT MAX(v.created_at) FROM model_version v WHERE v.model_id = m.id),
		       (SELECT MIN(v.id) FROM model_version v WHERE v.model_id = m.id AND v.held_since IS NOT NULL)
		FROM model m WHERE m.id = ?`
	var since, newest sql.NullInt64
	var by, name string
	var created int64
	var heldVersionID sql.NullString
	err := s.q.QueryRowContext(ctx, s.rb(q), id).Scan(&since, &by, &created, &name, &newest, &heldVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DeleteGuard{}, domain.NotFound("model not found")
	}
	if err != nil {
		return domain.DeleteGuard{}, err
	}

	g := domain.DeleteGuard{Hold: scanHold(since, sql.NullString{String: by, Valid: true}), NewestCreatedAt: created}
	if newest.Valid && newest.Int64 > g.NewestCreatedAt {
		g.NewestCreatedAt = newest.Int64
	}
	// The model's own hold wins: it is the subject the caller named, and it is the one they
	// can release directly.
	if g.Hold == nil && heldVersionID.Valid {
		const vq = `SELECT held_since, held_by, name FROM model_version WHERE id = ?`
		var vSince sql.NullInt64
		var vBy, vName string
		if err := s.q.QueryRowContext(ctx, s.rb(vq), heldVersionID.String).Scan(&vSince, &vBy, &vName); err != nil {
			return domain.DeleteGuard{}, err
		}
		g.Hold = scanHold(vSince, sql.NullString{String: vBy, Valid: true})
		g.HeldSubject = "version/" + name + "@" + vName
	}
	return g, nil
}

// versionDeleteGuard resolves §19.3.1's downward inheritance: a hold on the model covers its
// versions. The version's own hold wins when both exist — it is the more specific statement,
// and it names a subject the caller can act on directly.
func (s *Store) versionDeleteGuard(ctx context.Context, id string) (domain.DeleteGuard, error) {
	const q = `
		SELECT v.held_since, v.held_by, v.created_at, m.held_since, m.held_by, m.name
		FROM model_version v JOIN model m ON m.id = v.model_id WHERE v.id = ?`
	var vSince, mSince sql.NullInt64
	var vBy, mBy, mName string
	var created int64
	err := s.q.QueryRowContext(ctx, s.rb(q), id).Scan(&vSince, &vBy, &created, &mSince, &mBy, &mName)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DeleteGuard{}, domain.NotFound("version not found")
	}
	if err != nil {
		return domain.DeleteGuard{}, err
	}

	g := domain.DeleteGuard{Hold: scanHold(vSince, sql.NullString{String: vBy, Valid: true}), NewestCreatedAt: created}
	if g.Hold == nil {
		if h := scanHold(mSince, sql.NullString{String: mBy, Valid: true}); h != nil {
			g.Hold = h
			g.HeldSubject = "model/" + mName
		}
	}
	return g, nil
}

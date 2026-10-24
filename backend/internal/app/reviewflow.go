package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/domain/assignment"
	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
	"github.com/fatmakaplan/kongrehub/internal/domain/review"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// actor converts a principal into audit actor fields.
func actor(p auth.Principal) (*int64, string) {
	if p.UserID == 0 {
		return nil, "system"
	}
	return audit.Ptr(p.UserID), p.Role
}

// loadPaper builds the domain view of a submission for assignment decisions.
// Existing = bu bildiride daha önce herhangi bir atamada bulunmuş tüm hakemler
// (süresi geçen veya reddeden hakem tekrar seçilmez).
func loadPaper(ctx context.Context, q db.Querier, subID int64, needed int) (assignment.Paper, error) {
	p := assignment.Paper{ID: subID, Needed: needed}
	var kws []string
	if err := q.QueryRow(ctx, `SELECT keywords FROM submissions WHERE id=$1`, subID).Scan(&kws); err != nil {
		return p, err
	}
	p.Keywords = keywords.NewSet(kws)
	rows, err := q.Query(ctx, `SELECT coalesce(user_id,0), coalesce(institution_id,0) FROM submission_authors WHERE submission_id=$1`, subID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var uid, inst int64
		if err := rows.Scan(&uid, &inst); err != nil {
			rows.Close()
			return p, err
		}
		if uid != 0 {
			p.AuthorUserIDs = append(p.AuthorUserIDs, uid)
		}
		if inst != 0 {
			p.AuthorInstitutionIDs = append(p.AuthorInstitutionIDs, inst)
		}
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT DISTINCT reviewer_id FROM review_assignments WHERE submission_id=$1`, subID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return p, err
		}
		p.Existing = append(p.Existing, id)
	}
	return p, rows.Err()
}

// loadReviewers returns the active reviewer pool. forUpdate=true locks the
// profiles (aynı anda çalışan elle atama ile otomatik atamanın çakışmaması için).
func loadReviewers(ctx context.Context, q db.Querier, forUpdate bool) ([]assignment.Reviewer, error) {
	sql := `SELECT rp.user_id, coalesce(u.institution_id,0), rp.keywords, rp.max_load, rp.current_load
		FROM reviewer_profiles rp JOIN users u ON u.id = rp.user_id WHERE rp.is_active ORDER BY rp.user_id`
	if forUpdate {
		sql += ` FOR UPDATE OF rp`
	}
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assignment.Reviewer
	for rows.Next() {
		var r assignment.Reviewer
		var kws []string
		if err := rows.Scan(&r.ID, &r.InstitutionID, &kws, &r.MaxLoad, &r.CurrentLoad); err != nil {
			return nil, err
		}
		r.Keywords = keywords.NewSet(kws)
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadConflicts reads co-authorship and advisor relations.
func loadConflicts(ctx context.Context, q db.Querier) (*assignment.Conflicts, error) {
	cf := assignment.NewConflicts()
	rows, err := q.Query(ctx, `SELECT user_a, user_b FROM coauthorships`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, b int64
		if err := rows.Scan(&a, &b); err != nil {
			rows.Close()
			return nil, err
		}
		cf.AddCoauthorship(a, b)
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT advisor_id, student_id FROM advisor_relations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b int64
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		cf.AddAdvisor(a, b)
	}
	return cf, rows.Err()
}

var errNoCapacity = errors.New("reviewer at capacity")

// assignTx creates one assignment. Yük artırımı koşullu UPDATE ile yapılır:
// kapasite doluysa 0 satır etkilenir ve errNoCapacity döner; böylece eşzamanlı
// iki atama aynı son boş yeri alamaz (CHECK kısıtı da son savunma hattıdır).
func assignTx(ctx context.Context, tx pgx.Tx, subID, reviewerID int64, round int, reason string, score float64, now time.Time, p auth.Principal) (int64, error) {
	tag, err := tx.Exec(ctx, `UPDATE reviewer_profiles SET current_load = current_load + 1
		WHERE user_id=$1 AND is_active AND current_load < max_load`, reviewerID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, errNoCapacity
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO review_assignments (submission_id, reviewer_id, round, reason, match_score, assigned_at, due_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, subID, reviewerID, round, reason, score, now, review.DueAt(now)).Scan(&id)
	if err != nil {
		return 0, err
	}
	aid, role := actor(p)
	err = audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "assignment.created", Entity: "submission", EntityID: itoa(subID),
		New: map[string]any{"assignment_id": id, "reviewer_id": reviewerID, "round": round, "reason": reason, "score": score}})
	return id, err
}

// releaseTx frees an assignment's load slot and sets its final status.
func releaseTx(ctx context.Context, tx pgx.Tx, assignmentID int64, newStatus string) (bool, error) {
	var reviewerID int64
	err := tx.QueryRow(ctx, `UPDATE review_assignments SET status=$2, completed_at=now()
		WHERE id=$1 AND status='assigned' RETURNING reviewer_id`, assignmentID, newStatus).Scan(&reviewerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // zaten kapanmış: idempotent
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE reviewer_profiles SET current_load = current_load - 1 WHERE user_id=$1 AND current_load > 0`, reviewerID)
	return true, err
}

// pickAndAssign chooses the best available reviewer for one more slot.
func pickAndAssign(ctx context.Context, tx pgx.Tx, subID int64, round int, reason string, now time.Time, p auth.Principal) (int64, int64, error) {
	paper, err := loadPaper(ctx, tx, subID, 1)
	if err != nil {
		return 0, 0, err
	}
	cf, err := loadConflicts(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		pool, err := loadReviewers(ctx, tx, false)
		if err != nil {
			return 0, 0, err
		}
		pick, ok := assignment.PickOne(&paper, pool, cf, assignment.DefaultOptions())
		if !ok {
			return 0, 0, nil
		}
		id, err := assignTx(ctx, tx, subID, pick.ReviewerID, round, reason, pick.Score, now, p)
		if errors.Is(err, errNoCapacity) {
			paper.Existing = append(paper.Existing, pick.ReviewerID) // yarışı kaybettik, başkasını dene
			continue
		}
		return id, pick.ReviewerID, err
	}
	return 0, 0, nil
}

// transitionTx applies a state-machine event with a row lock.
func transitionTx(ctx context.Context, tx pgx.Tx, subID int64, ev review.Event, p auth.Principal, extra map[string]any) (review.Status, error) {
	var cur string
	if err := tx.QueryRow(ctx, `SELECT status FROM submissions WHERE id=$1 FOR UPDATE`, subID).Scan(&cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", httpx.ErrNotFound
		}
		return "", err
	}
	next, err := review.Next(review.Status(cur), ev)
	if err != nil {
		return "", httpx.Conflict("invalid_transition", err.Error())
	}
	if _, err := tx.Exec(ctx, `UPDATE submissions SET status=$2, updated_at=now() WHERE id=$1`, subID, string(next)); err != nil {
		return "", err
	}
	aid, role := actor(p)
	nv := map[string]any{"status": string(next), "event": string(ev)}
	for k, v := range extra {
		nv[k] = v
	}
	err = audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "submission.status_changed", Entity: "submission", EntityID: itoa(subID),
		Old: map[string]any{"status": cur}, New: nv})
	return next, err
}

// evaluateRoundTx checks whether the current round is complete; if so it
// either requests a 4th reviewer (uyuşmazlık) or moves to reviews_complete.
// Döner: "waiting" | "extra_reviewer" | "complete" | "complete_no_extra".
func evaluateRoundTx(ctx context.Context, tx pgx.Tx, subID int64, now time.Time, p auth.Principal) (string, error) {
	var status string
	var round int
	if err := tx.QueryRow(ctx, `SELECT status, round FROM submissions WHERE id=$1 FOR UPDATE`, subID).Scan(&status, &round); err != nil {
		return "", err
	}
	if status != string(review.UnderReview) {
		return "waiting", nil
	}
	var open, done int
	var extraRequested bool
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='assigned'), count(*) FILTER (WHERE status='submitted'),
			bool_or(reason='disagreement')
		FROM review_assignments WHERE submission_id=$1 AND round=$2`, subID, round).Scan(&open, &done, &extraRequested); err != nil {
		return "", err
	}
	required := review.RequiredReviewers
	if round > 1 {
		required = 1 // düzeltme turunda yalnızca yeniden bakması istenen hakemler
	}
	if open > 0 || done < required {
		return "waiting", nil
	}
	rows, err := tx.Query(ctx, `SELECT r.weighted_score::float8 FROM reviews r JOIN review_assignments ra ON ra.id=r.assignment_id
		WHERE ra.submission_id=$1 AND ra.round=$2 AND ra.status='submitted'`, subID, round)
	if err != nil {
		return "", err
	}
	var scores []float64
	for rows.Next() {
		var s float64
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return "", err
		}
		scores = append(scores, s)
	}
	rows.Close()
	agg := review.Summarize(scores)
	if round == 1 && review.NeedsExtraReviewer(agg, extraRequested) {
		id, _, err := pickAndAssign(ctx, tx, subID, round, "disagreement", now, auth.Principal{})
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE submissions SET disagreement_flag=true, updated_at=now() WHERE id=$1`, subID); err != nil {
			return "", err
		}
		if id != 0 {
			return "extra_reviewer", audit.Log(ctx, tx, audit.Entry{ActorRole: "system", Action: "review.disagreement_detected", Entity: "submission", EntityID: itoa(subID),
				New: map[string]any{"spread": agg.Spread, "threshold": review.DisagreementThreshold, "extra_assignment_id": id}})
		}
		// uygun 4. hakem yok: editör karar verir, bayrak işaretli kalır
		if _, err := transitionTx(ctx, tx, subID, review.EvReviewsDone, auth.Principal{}, map[string]any{"mean": agg.Mean, "note": "no_extra_reviewer_available"}); err != nil {
			return "", err
		}
		_, err = tx.Exec(ctx, `UPDATE submissions SET final_score=$2 WHERE id=$1`, subID, fmt.Sprintf("%.2f", agg.Mean))
		return "complete_no_extra", err
	}
	if _, err := transitionTx(ctx, tx, subID, review.EvReviewsDone, auth.Principal{}, map[string]any{"mean": agg.Mean, "spread": agg.Spread}); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE submissions SET final_score=$2, disagreement_flag = disagreement_flag OR $3 WHERE id=$1`, subID, fmt.Sprintf("%.2f", agg.Mean), agg.Disagreement)
	return "complete", err
}

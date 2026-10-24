package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/domain/review"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

func (a *App) registerReviews(r chi.Router) {
	a.handle(r, "GET", "/api/reviewer/assignments", a.myAssignments, roles(RReviewer))
	a.handle(r, "GET", "/api/reviewer/assignments/{id}", a.myAssignment, roles(RReviewer))
	a.handle(r, "POST", "/api/reviewer/assignments/{id}/review", a.submitReview, roles(RReviewer), idempotent())
	a.handle(r, "POST", "/api/reviewer/assignments/{id}/decline", a.declineAssignment, roles(RReviewer))
	a.handle(r, "POST", "/api/submissions/{id}/decision", a.decide, roles(REditor), idempotent())
	a.handle(r, "POST", "/api/jobs/reminders/run", a.runRemindersHandler, roles(REditor, RAdmin))
	a.handle(r, "GET", "/api/jobs/runs", a.jobRuns, roles(REditor, RAdmin))
}

// Hakem görünümü: yalnızca bildirinin içeriği; yazar, kurum, dosya adı yok.
type reviewerAssignmentDTO struct {
	ID             int64      `json:"id"`
	SubmissionID   int64      `json:"submission_id"`
	Title          string     `json:"title"`
	Abstract       string     `json:"abstract"`
	Keywords       []string   `json:"keywords"`
	Round          int        `json:"round"`
	Version        int        `json:"version"`
	Status         string     `json:"status"`
	AssignedAt     time.Time  `json:"assigned_at"`
	DueAt          time.Time  `json:"due_at"`
	DaysLeft       int        `json:"days_left"`
	ResponseLetter string     `json:"response_letter,omitempty"`
	MyReview       *reviewDTO `json:"my_review,omitempty"`
}

func (a *App) myAssignments(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	status := r.URL.Query().Get("status")
	rows, err := a.Pool.Query(r.Context(), `SELECT ra.id, s.id, s.title, s.abstract, s.keywords, ra.round, s.current_version, ra.status, ra.assigned_at, ra.due_at
		FROM review_assignments ra JOIN submissions s ON s.id=ra.submission_id
		WHERE ra.reviewer_id=$1 AND ra.status IN ('assigned','submitted') AND ($2='' OR ra.status=$2)
		ORDER BY (ra.status='assigned') DESC, ra.due_at, ra.id LIMIT 200`, p.UserID, status)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := a.Clock.Now()
	items := []reviewerAssignmentDTO{}
	for rows.Next() {
		var d reviewerAssignmentDTO
		if err := rows.Scan(&d.ID, &d.SubmissionID, &d.Title, &d.Abstract, &d.Keywords, &d.Round, &d.Version, &d.Status, &d.AssignedAt, &d.DueAt); err != nil {
			return err
		}
		d.DaysLeft = int(d.DueAt.Sub(now).Hours() / 24)
		items = append(items, d)
	}
	httpx.WriteJSON(w, 200, httpx.Page[reviewerAssignmentDTO]{Items: items})
	return rows.Err()
}

func (a *App) loadMyAssignment(ctx context.Context, p auth.Principal, id int64) (*reviewerAssignmentDTO, error) {
	var d reviewerAssignmentDTO
	err := a.Pool.QueryRow(ctx, `SELECT ra.id, s.id, s.title, s.abstract, s.keywords, ra.round, s.current_version, ra.status, ra.assigned_at, ra.due_at,
			coalesce((SELECT response_letter FROM submission_files f WHERE f.submission_id=s.id AND f.version=s.current_version),'')
		FROM review_assignments ra JOIN submissions s ON s.id=ra.submission_id
		WHERE ra.id=$1 AND ra.reviewer_id=$2 AND ra.status IN ('assigned','submitted')`, id, p.UserID).
		Scan(&d.ID, &d.SubmissionID, &d.Title, &d.Abstract, &d.Keywords, &d.Round, &d.Version, &d.Status, &d.AssignedAt, &d.DueAt, &d.ResponseLetter)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.DaysLeft = int(d.DueAt.Sub(a.Clock.Now()).Hours() / 24)
	var rv reviewDTO
	err = a.Pool.QueryRow(ctx, `SELECT originality, significance, methodology, clarity, weighted_score::float8, recommendation, comments_to_author, comments_to_editor, submitted_at
		FROM reviews WHERE assignment_id=$1`, id).Scan(&rv.Originality, &rv.Significance, &rv.Methodology, &rv.Clarity, &rv.Weighted,
		&rv.Recommendation, &rv.CommentsToAuthor, &rv.CommentsToEditor, &rv.SubmittedAt)
	if err == nil {
		rv.Round = d.Round
		rv.Reviewer = "Siz"
		d.MyReview = &rv
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return &d, nil
}

func (a *App) myAssignment(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	d, err := a.loadMyAssignment(r.Context(), principal(r), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, d)
	return nil
}

// ReviewInput is the evaluation form.
type ReviewInput struct {
	review.Scores
	Recommendation   string `json:"recommendation"`
	CommentsToAuthor string `json:"comments_to_author"`
	CommentsToEditor string `json:"comments_to_editor"`
}

func (in *ReviewInput) validate() error {
	if err := in.Scores.Validate(); err != nil {
		return httpx.Validation(err.Error())
	}
	switch in.Recommendation {
	case "accept", "minor_revision", "major_revision", "reject":
	default:
		return httpx.Validation("öneri accept, minor_revision, major_revision veya reject olmalı")
	}
	in.CommentsToAuthor = strings.TrimSpace(in.CommentsToAuthor)
	if len(in.CommentsToAuthor) < 10 || len(in.CommentsToAuthor) > 20000 {
		return httpx.Validation("yazara yorum 10-20000 karakter olmalı")
	}
	if len(in.CommentsToEditor) > 20000 {
		return httpx.Validation("editöre yorum en fazla 20000 karakter")
	}
	return nil
}

func (a *App) submitReview(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in ReviewInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	p := principal(r)
	now := a.Clock.Now()
	var outcome string
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var subID int64
		var status string
		err := tx.QueryRow(ctx, `SELECT submission_id, status FROM review_assignments WHERE id=$1 AND reviewer_id=$2 FOR UPDATE`, id, p.UserID).Scan(&subID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "assigned" {
			return httpx.Conflict("assignment_closed", "bu atama artık açık değil (değerlendirme gönderilmiş ya da süresi dolmuş)")
		}
		weighted := in.Scores.Weighted()
		if _, err := tx.Exec(ctx, `INSERT INTO reviews (assignment_id, originality, significance, methodology, clarity, weighted_score, recommendation, comments_to_author, comments_to_editor, submitted_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, in.Originality, in.Significance, in.Methodology, in.Clarity, weighted,
			in.Recommendation, in.CommentsToAuthor, in.CommentsToEditor, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE review_assignments SET status='submitted', completed_at=$2 WHERE id=$1`, id, now); err != nil {
			return err
		}
		// denetim kaydı: puanlar ve atama numarası; ad/e-posta yok
		if err := audit.Log(ctx, tx, audit.Entry{ActorID: audit.Ptr(p.UserID), ActorRole: p.Role, Action: "review.submitted", Entity: "submission", EntityID: itoa(subID),
			New: map[string]any{"assignment_id": id, "weighted_score": weighted, "recommendation": in.Recommendation}}); err != nil {
			return err
		}
		outcome, err = evaluateRoundTx(ctx, tx, subID, now, p)
		return err
	})
	if err != nil {
		return err
	}
	d, err := a.loadMyAssignment(r.Context(), p, id)
	if err != nil {
		return err
	}
	// hakeme yalnızca kendi değerlendirmesi döner; uyuşmazlık sonucu editör içindir
	_ = outcome
	httpx.WriteJSON(w, http.StatusCreated, d)
	return nil
}

func (a *App) declineAssignment(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	now := a.Clock.Now()
	var replacement int64
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var subID int64
		var round int
		err := tx.QueryRow(ctx, `SELECT submission_id, round FROM review_assignments WHERE id=$1 AND reviewer_id=$2 AND status='assigned' FOR UPDATE`, id, p.UserID).Scan(&subID, &round)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := releaseTx(ctx, tx, id, "declined"); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, audit.Entry{ActorID: audit.Ptr(p.UserID), ActorRole: p.Role, Action: "assignment.declined", Entity: "submission", EntityID: itoa(subID),
			New: map[string]any{"assignment_id": id}}); err != nil {
			return err
		}
		replacement, _, err = pickAndAssign(ctx, tx, subID, round, "replacement", now, auth.Principal{})
		if err != nil {
			return err
		}
		_, err = evaluateRoundTx(ctx, tx, subID, now, auth.Principal{})
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"assignment_id": id, "status": "declined", "replacement_assigned": replacement != 0})
	return nil
}

// decide records the editor's decision for the current round.
func (a *App) decide(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in struct {
		Decision string `json:"decision"`
		Note     string `json:"note_to_author"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	ev, ok := review.DecisionEvent(in.Decision)
	if !ok {
		return httpx.Validation("karar accept, minor_revision, major_revision veya reject olmalı")
	}
	p := principal(r)
	var newStatus review.Status
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var round int
		var cur string
		if err := tx.QueryRow(ctx, `SELECT round, status FROM submissions WHERE id=$1 FOR UPDATE`, id).Scan(&round, &cur); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrNotFound
			}
			return err
		}
		if (ev == review.EvMinor || ev == review.EvMajor) && round >= review.MaxRounds {
			return httpx.Conflict("max_rounds", "son turda yalnızca kabul veya ret verilebilir")
		}
		newStatus, err = transitionTx(ctx, tx, id, ev, p, map[string]any{"decision": in.Decision, "round": round})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO decisions (submission_id, round, decision, note_to_author, decided_by) VALUES ($1,$2,$3,$4,$5)`,
			id, round, in.Decision, in.Note, p.UserID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"id": id, "status": newStatus, "decision": in.Decision})
	return nil
}

func (a *App) runRemindersHandler(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		DryRun bool `json:"dry_run"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	res, err := a.RunReminderJob(context.WithoutCancel(r.Context()))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, res)
	return nil
}

func (a *App) jobRuns(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT id, job, started_at, finished_at, stats FROM job_runs WHERE job <> 'assignment' ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return err
	}
	type run struct {
		ID       int64          `json:"id"`
		Job      string         `json:"job"`
		Started  time.Time      `json:"started_at"`
		Finished *time.Time     `json:"finished_at"`
		Stats    map[string]any `json:"stats"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[run])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": out})
	return nil
}

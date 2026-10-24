package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/domain/assignment"
	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
	"github.com/fatmakaplan/kongrehub/internal/domain/review"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

// Otomatik atama için PostgreSQL danışma kilidi anahtarı.
const assignmentLockKey = 7_100_001

func (a *App) registerAssignments(r chi.Router) {
	a.handle(r, "GET", "/api/reviewers", a.listReviewers, roles(REditor, RAdmin))
	a.handle(r, "POST", "/api/assignments/auto", a.autoAssign, roles(REditor, RAdmin), idempotent())
	a.handle(r, "GET", "/api/assignments/report", a.assignmentReport, roles(REditor, RAdmin))
	a.handle(r, "POST", "/api/submissions/{id}/assignments", a.manualAssign, roles(REditor, RAdmin))
	a.handle(r, "POST", "/api/assignments/{id}/release", a.releaseAssignment, roles(REditor, RAdmin))
}

type reviewerDTO struct {
	ID          int64    `json:"id"`
	FullName    string   `json:"full_name"`
	Email       string   `json:"email"`
	Institution string   `json:"institution"`
	Keywords    []string `json:"keywords"`
	MaxLoad     int      `json:"max_load"`
	CurrentLoad int      `json:"current_load"`
	Completed   int      `json:"completed_reviews"`
	Expired     int      `json:"expired_assignments"`
	Active      bool     `json:"is_active"`
}

func (a *App) listReviewers(w http.ResponseWriter, r *http.Request) error {
	limit, after, err := httpx.PageParams(r)
	if err != nil {
		return err
	}
	kw := r.URL.Query().Get("keyword")
	args := []any{after, limit + 1}
	filter := ""
	if kw != "" {
		args = append(args, []string{kw})
		filter = " AND rp.keywords @> $3"
	}
	rows, err := a.Pool.Query(r.Context(), `SELECT u.id, u.full_name, u.email, coalesce(i.name,''), rp.keywords, rp.max_load, rp.current_load,
			(SELECT count(*) FROM review_assignments ra WHERE ra.reviewer_id=u.id AND ra.status='submitted')::int,
			(SELECT count(*) FROM review_assignments ra WHERE ra.reviewer_id=u.id AND ra.status='expired')::int,
			rp.is_active
		FROM reviewer_profiles rp JOIN users u ON u.id=rp.user_id LEFT JOIN institutions i ON i.id=u.institution_id
		WHERE ($1=0 OR u.id > $1)`+filter+` ORDER BY u.id LIMIT $2`, args...)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reviewerDTO])
	if err != nil {
		return err
	}
	page := httpx.Page[reviewerDTO]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = httpx.Cursor(items[limit-1].ID)
	}
	httpx.WriteJSON(w, 200, page)
	return nil
}

// AutoAssignRequest controls a run.
type AutoAssignRequest struct {
	DryRun bool   `json:"dry_run"`
	Scope  string `json:"scope"` // "pending" (varsayılan): eksik hakemli bildiriler; "all": tüm bildiriler sıfırdan (yalnızca dry_run)
}

// AutoAssignResponse is the run report.
type AutoAssignResponse struct {
	DryRun     bool                    `json:"dry_run"`
	Scope      string                  `json:"scope"`
	Stats      assignment.Stats        `json:"stats"`
	Unassigned []assignment.Unassigned `json:"unassigned"`
	Violations []string                `json:"violations"`
	Created    int                     `json:"created"`
	Pairs      []assignment.Pair       `json:"assignments,omitempty"`
	RanAt      time.Time               `json:"ran_at"`
}

func (a *App) autoAssign(w http.ResponseWriter, r *http.Request) error {
	var in AutoAssignRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Scope == "" {
		in.Scope = "pending"
	}
	if in.Scope != "pending" && in.Scope != "all" {
		return httpx.Validation("scope 'pending' veya 'all' olmalı")
	}
	if in.Scope == "all" && !in.DryRun {
		return httpx.Validation("scope=all yalnızca dry_run ile kullanılabilir")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancel()
	resp, err := a.RunAutoAssign(ctx, principal(r), in)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("include") != "assignments" {
		resp.Pairs = nil
	}
	httpx.WriteJSON(w, 200, resp)
	return nil
}

// RunAutoAssign executes the min-cost-flow assignment.
//
// Eşzamanlılık: tüm çalışma tek transaction içindedir ve
// pg_try_advisory_xact_lock ile korunur; ikinci eşzamanlı çalıştırma 409 alır.
// Hakem profilleri FOR UPDATE ile kilitlenir, böylece aynı anda yapılan elle
// atamalar bu çalışmanın okuduğu yük bilgisini geçersiz kılamaz.
func (a *App) RunAutoAssign(ctx context.Context, p auth.Principal, in AutoAssignRequest) (*AutoAssignResponse, error) {
	resp := &AutoAssignResponse{DryRun: in.DryRun, Scope: in.Scope, RanAt: a.Clock.Now()}
	now := a.Clock.Now()
	err := db.InTx(ctx, a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if !in.DryRun {
			var got bool
			if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, assignmentLockKey).Scan(&got); err != nil {
				return err
			}
			if !got {
				return httpx.Conflict("assignment_in_progress", "başka bir otomatik atama şu anda çalışıyor")
			}
		}
		reviewers, err := loadReviewers(ctx, tx, !in.DryRun)
		if err != nil {
			return err
		}
		cf, err := loadConflicts(ctx, tx)
		if err != nil {
			return err
		}
		papers, err := loadPapersForRun(ctx, tx, in.Scope)
		if err != nil {
			return err
		}
		if in.Scope == "all" {
			for i := range reviewers {
				reviewers[i].CurrentLoad = 0
			}
		}
		opt := assignment.DefaultOptions()
		res := assignment.Solve(papers, reviewers, cf, opt)
		a.Metrics.AssignDuration.Observe(res.Stats.Duration.Seconds())
		resp.Stats = res.Stats
		resp.Unassigned = res.Unassigned
		if resp.Unassigned == nil {
			resp.Unassigned = []assignment.Unassigned{}
		}
		resp.Violations = assignment.Validate(papers, reviewers, cf, opt, res.Pairs)
		resp.Pairs = res.Pairs
		if len(resp.Violations) > 0 {
			return fmt.Errorf("atama doğrulaması başarısız: %v", resp.Violations[0])
		}
		if in.DryRun {
			return nil
		}
		// toplu ekleme
		subRound := map[int64]int{}
		for _, pp := range papers {
			subRound[pp.ID] = 0
		}
		rows, err := tx.Query(ctx, `SELECT id, round FROM submissions WHERE id = ANY($1)`, keysOf(subRound))
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var rd int
			if err := rows.Scan(&id, &rd); err != nil {
				rows.Close()
				return err
			}
			subRound[id] = rd
		}
		rows.Close()
		batch := &pgx.Batch{}
		loadDelta := map[int64]int{}
		touched := map[int64]bool{}
		for _, pr := range res.Pairs {
			batch.Queue(`INSERT INTO review_assignments (submission_id, reviewer_id, round, reason, match_score, assigned_at, due_at)
				VALUES ($1,$2,$3,'auto',$4,$5,$6)`, pr.PaperID, pr.ReviewerID, subRound[pr.PaperID], pr.Score, now, review.DueAt(now))
			loadDelta[pr.ReviewerID]++
			touched[pr.PaperID] = true
		}
		for rid, d := range loadDelta {
			batch.Queue(`UPDATE reviewer_profiles SET current_load = current_load + $2 WHERE user_id=$1`, rid, d)
		}
		for sid := range touched {
			batch.Queue(`UPDATE submissions SET status='under_review', updated_at=now() WHERE id=$1 AND status='submitted'`, sid)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
		resp.Created = len(res.Pairs)
		aid, role := actor(p)
		summary := map[string]any{"created": resp.Created, "unassigned": len(resp.Unassigned), "duration_ms": res.Stats.DurationMS,
			"mean_score": res.Stats.MeanScore, "load_min": res.Stats.LoadMin, "load_max": res.Stats.LoadMax}
		if err := audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "assignment.auto_run", Entity: "assignment_run", EntityID: now.Format(time.RFC3339), New: summary}); err != nil {
			return err
		}
		stats, _ := json.Marshal(resp)
		_, err = tx.Exec(ctx, `INSERT INTO job_runs (job, finished_at, stats) VALUES ('assignment', now(), $1)`, stats)
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func keysOf(m map[int64]int) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// loadPapersForRun returns papers needing reviewers.
//
//	pending: 'submitted' veya 'under_review' (tur 1) olup aktif hakem sayısı 3'ün altında olanlar
//	all:     geri çekilmemiş tüm bildiriler, mevcut atamalar yok sayılarak (ölçüm/dry-run)
func loadPapersForRun(ctx context.Context, q db.Querier, scope string) ([]assignment.Paper, error) {
	var sql string
	if scope == "all" {
		sql = `SELECT s.id, s.keywords, 3 AS needed, '{}'::bigint[] AS existing FROM submissions s WHERE s.status <> 'withdrawn'`
	} else {
		sql = `SELECT s.id, s.keywords,
				3 - count(ra.id) FILTER (WHERE ra.status IN ('assigned','submitted') AND ra.round = s.round) AS needed,
				coalesce(array_agg(DISTINCT ra.reviewer_id) FILTER (WHERE ra.id IS NOT NULL), '{}') AS existing
			FROM submissions s LEFT JOIN review_assignments ra ON ra.submission_id = s.id
			WHERE s.status IN ('submitted','under_review') AND s.round = 1
			GROUP BY s.id HAVING 3 - count(ra.id) FILTER (WHERE ra.status IN ('assigned','submitted') AND ra.round = s.round) > 0`
	}
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	var papers []assignment.Paper
	idx := map[int64]int{}
	for rows.Next() {
		var p assignment.Paper
		var kws []string
		if err := rows.Scan(&p.ID, &kws, &p.Needed, &p.Existing); err != nil {
			rows.Close()
			return nil, err
		}
		p.Keywords = keywords.NewSet(kws)
		idx[p.ID] = len(papers)
		papers = append(papers, p)
	}
	rows.Close()
	if len(papers) == 0 {
		return papers, nil
	}
	rows, err = q.Query(ctx, `SELECT submission_id, coalesce(user_id,0), coalesce(institution_id,0) FROM submission_authors WHERE submission_id = ANY($1)`, idsOf(papers))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, uid, inst int64
		if err := rows.Scan(&sid, &uid, &inst); err != nil {
			return nil, err
		}
		p := &papers[idx[sid]]
		if uid != 0 {
			p.AuthorUserIDs = append(p.AuthorUserIDs, uid)
		}
		if inst != 0 {
			p.AuthorInstitutionIDs = append(p.AuthorInstitutionIDs, inst)
		}
	}
	return papers, rows.Err()
}

func idsOf(ps []assignment.Paper) []int64 {
	out := make([]int64, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func (a *App) assignmentReport(w http.ResponseWriter, r *http.Request) error {
	var stats []byte
	var at time.Time
	err := a.Pool.QueryRow(r.Context(), `SELECT stats, started_at FROM job_runs WHERE job='assignment' ORDER BY id DESC LIMIT 1`).Scan(&stats, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteJSON(w, 200, map[string]any{"report": nil})
		return nil
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, err = fmt.Fprintf(w, `{"report":%s,"at":%q}`, stats, at.Format(time.RFC3339))
	return err
}

// manualAssign: editör tek bir hakemi atar. Hakemin profil satırı FOR UPDATE
// ile kilitlenir; kapasite ve çıkar çatışması kilit altında kontrol edilir.
func (a *App) manualAssign(w http.ResponseWriter, r *http.Request) error {
	subID, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in struct {
		ReviewerID int64 `json:"reviewer_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.ReviewerID <= 0 {
		return httpx.Validation("reviewer_id zorunlu")
	}
	p := principal(r)
	now := a.Clock.Now()
	var assignmentID int64
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var status string
		var round int
		if err := tx.QueryRow(ctx, `SELECT status, round FROM submissions WHERE id=$1 FOR UPDATE`, subID).Scan(&status, &round); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrNotFound
			}
			return err
		}
		if status != "submitted" && status != "under_review" {
			return httpx.Conflict("invalid_state", "bu durumdaki bildiriye hakem atanamaz")
		}
		var maxLoad, cur int
		var active bool
		err := tx.QueryRow(ctx, `SELECT max_load, current_load, is_active FROM reviewer_profiles WHERE user_id=$1 FOR UPDATE`, in.ReviewerID).Scan(&maxLoad, &cur, &active)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Validation("hakem bulunamadı")
		}
		if err != nil {
			return err
		}
		if !active || cur >= maxLoad {
			return httpx.Conflict("reviewer_at_capacity", fmt.Sprintf("hakemin yükü azami sınırda (%d/%d)", cur, maxLoad))
		}
		paper, err := loadPaper(ctx, tx, subID, 1)
		if err != nil {
			return err
		}
		for _, e := range paper.Existing {
			if e == in.ReviewerID {
				return httpx.Conflict("already_assigned", "bu hakem bu bildiride zaten yer aldı")
			}
		}
		cf, err := loadConflicts(ctx, tx)
		if err != nil {
			return err
		}
		pool, err := loadReviewers(ctx, tx, false)
		if err != nil {
			return err
		}
		for i := range pool {
			if pool[i].ID == in.ReviewerID {
				if reason := cf.Conflict(&paper, &pool[i]); reason != assignment.COINone {
					return httpx.Conflict("conflict_of_interest", "çıkar çatışması: "+coiLabel(reason))
				}
				score := keywords.Coverage(paper.Keywords, pool[i].Keywords)
				assignmentID, err = assignTx(ctx, tx, subID, in.ReviewerID, round, "manual", score, now, p)
				if errors.Is(err, errNoCapacity) {
					return httpx.Conflict("reviewer_at_capacity", "hakemin yükü azami sınırda")
				}
				if err != nil {
					if db.IsUniqueViolation(err, "") {
						return httpx.Conflict("already_assigned", "bu hakem bu bildiride zaten yer aldı")
					}
					return err
				}
				_, err = tx.Exec(ctx, `UPDATE submissions SET status='under_review', updated_at=now() WHERE id=$1 AND status='submitted'`, subID)
				return err
			}
		}
		return httpx.Validation("hakem aktif değil")
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"assignment_id": assignmentID, "submission_id": subID, "reviewer_id": in.ReviewerID})
	return nil
}

func coiLabel(code string) string {
	switch code {
	case assignment.COIIsAuthor:
		return "hakem bildirinin yazarı"
	case assignment.COISameInstitution:
		return "aynı kurum"
	case assignment.COICoauthor:
		return "ortak yayın"
	case assignment.COIAdvisor:
		return "danışman ilişkisi"
	}
	return code
}

func (a *App) releaseAssignment(w http.ResponseWriter, r *http.Request) error {
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
	var subID int64
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT submission_id FROM review_assignments WHERE id=$1`, id).Scan(&subID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrNotFound
			}
			return err
		}
		ok, err := releaseTx(r.Context(), tx, id, "released")
		if err != nil {
			return err
		}
		if !ok {
			return httpx.Conflict("not_open", "yalnızca açık atamalar geri alınabilir")
		}
		aid, role := actor(p)
		return audit.Log(r.Context(), tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "assignment.released", Entity: "submission", EntityID: itoa(subID),
			New: map[string]any{"assignment_id": id, "reason": in.Reason}})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"assignment_id": id, "status": "released"})
	return nil
}

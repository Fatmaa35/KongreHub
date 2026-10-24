package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/domain/blind"
	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
	"github.com/fatmakaplan/kongrehub/internal/domain/review"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
	"github.com/fatmakaplan/kongrehub/internal/pdfmeta"
)

func (a *App) registerSubmissions(r chi.Router) {
	a.handle(r, "GET", "/api/institutions", a.listInstitutions)
	a.handle(r, "GET", "/api/keywords", a.listKeywords)
	a.handle(r, "POST", "/api/submissions", a.createSubmission, roles(RAuthor))
	a.handle(r, "GET", "/api/submissions/mine", a.mySubmissions, roles(RAuthor))
	a.handle(r, "GET", "/api/submissions", a.listSubmissions, roles(REditor, RAdmin))
	a.handle(r, "GET", "/api/submissions/{id}", a.getSubmission, roles(RAuthor, RReviewer, REditor, RAdmin))
	a.handle(r, "GET", "/api/submissions/{id}/file", a.downloadFile, roles(RAuthor, RReviewer, REditor, RAdmin))
	a.handle(r, "GET", "/api/submissions/{id}/history", a.submissionHistory, roles(RAuthor, REditor, RAdmin))
	a.handle(r, "POST", "/api/submissions/{id}/revisions", a.uploadRevision, roles(RAuthor))
	a.handle(r, "POST", "/api/submissions/{id}/withdraw", a.withdraw, roles(RAuthor))
}

// ---- erişim düzeyi -------------------------------------------------------------

type accessLevel int

const (
	accessNone accessLevel = iota
	accessReviewer
	accessOwner
	accessEditor
)

// submissionAccess decides what the caller may see. Yetkisiz veya ilgisiz
// kullanıcıya 404 döner: bildirinin varlığı bile sızdırılmaz.
func (a *App) submissionAccess(ctx context.Context, p auth.Principal, id int64) (accessLevel, error) {
	var exists bool
	if err := a.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM submissions WHERE id=$1)`, id).Scan(&exists); err != nil {
		return accessNone, err
	}
	if !exists {
		return accessNone, httpx.ErrNotFound
	}
	switch p.Role {
	case REditor, RAdmin:
		return accessEditor, nil
	case RAuthor:
		var ok bool
		err := a.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM submissions WHERE id=$1 AND submitter_id=$2)
			OR EXISTS(SELECT 1 FROM submission_authors WHERE submission_id=$1 AND user_id=$2)`, id, p.UserID).Scan(&ok)
		if err != nil {
			return accessNone, err
		}
		if ok {
			return accessOwner, nil
		}
	case RReviewer:
		var ok bool
		err := a.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM review_assignments WHERE submission_id=$1 AND reviewer_id=$2
			AND status IN ('assigned','submitted'))`, id, p.UserID).Scan(&ok)
		if err != nil {
			return accessNone, err
		}
		if ok {
			return accessReviewer, nil
		}
	}
	return accessNone, httpx.ErrNotFound
}

// ---- yardımcı listeler ---------------------------------------------------------

func (a *App) listInstitutions(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT id, name, country FROM institutions ORDER BY name`)
	if err != nil {
		return err
	}
	type inst struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Country string `json:"country"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[inst])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": out})
	return nil
}

func (a *App) listKeywords(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT k, count(*) FROM (
		SELECT unnest(keywords) k FROM reviewer_profiles UNION ALL SELECT unnest(keywords) FROM submissions) x
		GROUP BY k ORDER BY count(*) DESC, k LIMIT 200`)
	if err != nil {
		return err
	}
	type kw struct {
		Keyword string `json:"keyword"`
		Count   int    `json:"count"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[kw])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": out})
	return nil
}

// ---- gönderim ------------------------------------------------------------------

type authorInput struct {
	FullName      string `json:"full_name"`
	Email         string `json:"email"`
	InstitutionID *int64 `json:"institution_id"`
	IsPresenter   bool   `json:"is_presenter"`
}

type submissionInput struct {
	Title           string        `json:"title"`
	Abstract        string        `json:"abstract"`
	Keywords        []string      `json:"keywords"`
	DurationMinutes int           `json:"duration_minutes"`
	Authors         []authorInput `json:"authors"`
}

func (in *submissionInput) validate() error {
	in.Title = strings.TrimSpace(in.Title)
	in.Abstract = strings.TrimSpace(in.Abstract)
	in.Keywords = keywords.Normalize(in.Keywords)
	switch {
	case len(in.Title) < 5 || len(in.Title) > 300:
		return httpx.Validation("başlık 5-300 karakter olmalı")
	case len(in.Abstract) < 20 || len(in.Abstract) > 5000:
		return httpx.Validation("özet 20-5000 karakter olmalı")
	case len(in.Keywords) < 1 || len(in.Keywords) > 8:
		return httpx.Validation("1-8 anahtar kelime gerekli")
	case len(in.Authors) < 1 || len(in.Authors) > 20:
		return httpx.Validation("1-20 yazar gerekli")
	}
	if in.DurationMinutes == 0 {
		in.DurationMinutes = 20
	}
	if in.DurationMinutes != 15 && in.DurationMinutes != 20 && in.DurationMinutes != 30 {
		return httpx.Validation("sunum süresi 15, 20 veya 30 dakika olmalı")
	}
	seen := map[string]bool{}
	presenters := 0
	for i := range in.Authors {
		au := &in.Authors[i]
		au.FullName = strings.TrimSpace(au.FullName)
		au.Email = strings.ToLower(strings.TrimSpace(au.Email))
		if len(au.FullName) < 2 {
			return httpx.Validation(fmt.Sprintf("%d. yazarın adı eksik", i+1))
		}
		if _, err := mail.ParseAddress(au.Email); err != nil {
			return httpx.Validation(fmt.Sprintf("%d. yazarın e-posta adresi geçersiz", i+1))
		}
		if seen[au.Email] {
			return httpx.Validation("aynı e-posta iki yazarda kullanılamaz")
		}
		seen[au.Email] = true
		if au.IsPresenter {
			presenters++
		}
	}
	if presenters > 1 {
		return httpx.Validation("yalnızca bir sunucu yazar seçilebilir")
	}
	if presenters == 0 {
		in.Authors[0].IsPresenter = true
	}
	return nil
}

// readPDF reads and sanitizes the uploaded file (yazar metadata'sı temizlenir).
func (a *App) readPDF(r *http.Request) ([]byte, string, error) {
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return nil, "", httpx.Validation("PDF dosyası ('file') zorunlu")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, a.Cfg.MaxUploadBytes+1))
	if err != nil {
		return nil, "", httpx.Validation("dosya okunamadı")
	}
	if int64(len(data)) > a.Cfg.MaxUploadBytes {
		return nil, "", httpx.E(http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("dosya en fazla %d MB olabilir", a.Cfg.MaxUploadBytes>>20))
	}
	clean, err := pdfmeta.Sanitize(data)
	if err != nil {
		return nil, "", httpx.Validation("dosya geçerli bir PDF değil")
	}
	name := hdr.Filename
	if len(name) > 200 {
		name = name[:200]
	}
	return clean, name, nil
}

func (a *App) parseMultipart(r *http.Request) error {
	if err := r.ParseMultipartForm(a.Cfg.MaxUploadBytes + (1 << 20)); err != nil {
		return httpx.Validation("multipart/form-data bekleniyor")
	}
	return nil
}

func (a *App) createSubmission(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	if err := a.parseMultipart(r); err != nil {
		return err
	}
	var in submissionInput
	dec := json.NewDecoder(strings.NewReader(r.FormValue("metadata")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return httpx.Validation("'metadata' alanı geçerli JSON olmalı")
	}
	if err := in.validate(); err != nil {
		return err
	}
	pdf, origName, err := a.readPDF(r)
	if err != nil {
		return err
	}
	key, sum, err := a.Store.Put(pdf)
	if err != nil {
		return err
	}
	var subID int64
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// gönderen, yazar listesinde olmalı
		var myEmail string
		if err := tx.QueryRow(r.Context(), `SELECT lower(email) FROM users WHERE id=$1`, p.UserID).Scan(&myEmail); err != nil {
			return err
		}
		found := false
		for _, au := range in.Authors {
			if au.Email == myEmail {
				found = true
			}
		}
		if !found {
			return httpx.Validation("gönderen kişi yazar listesinde yer almalı")
		}
		if err := tx.QueryRow(r.Context(), `INSERT INTO submissions (title, abstract, keywords, submitter_id, duration_minutes)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`, in.Title, in.Abstract, in.Keywords, p.UserID, in.DurationMinutes).Scan(&subID); err != nil {
			return err
		}
		for i, au := range in.Authors {
			if au.InstitutionID != nil {
				var ok bool
				if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM institutions WHERE id=$1)`, *au.InstitutionID).Scan(&ok); err != nil {
					return err
				}
				if !ok {
					return httpx.Validation(fmt.Sprintf("%d. yazarın kurumu bulunamadı", i+1))
				}
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO submission_authors (submission_id, position, user_id, full_name, email, institution_id, is_presenter)
				VALUES ($1,$2,(SELECT id FROM users WHERE lower(email)=$4),$3,$4,$5,$6)`,
				subID, i+1, au.FullName, au.Email, au.InstitutionID, au.IsPresenter); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO submission_files (submission_id, version, storage_key, sha256, size_bytes, original_name)
			VALUES ($1,1,$2,$3,$4,$5)`, subID, key, sum, len(pdf), origName); err != nil {
			return err
		}
		aid, role := actor(p)
		return audit.Log(r.Context(), tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "submission.created", Entity: "submission", EntityID: itoa(subID),
			New: map[string]any{"title": in.Title, "keywords": in.Keywords, "authors": len(in.Authors), "file_sha256": sum}})
	})
	if err != nil {
		return err
	}
	dto, err := a.buildSubmission(r.Context(), p, subID, accessOwner)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// ---- listeleme -----------------------------------------------------------------

type submissionSummary struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	Keywords   []string  `json:"keywords"`
	Status     string    `json:"status"`
	Round      int       `json:"round"`
	FinalScore *float64  `json:"final_score,omitempty"`
	Disagree   bool      `json:"disagreement_flag"`
	Reviewers  int       `json:"active_reviewers"`
	Reviews    int       `json:"submitted_reviews"`
	CreatedAt  time.Time `json:"created_at"`
}

func (a *App) mySubmissions(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	rows, err := a.Pool.Query(r.Context(), `SELECT s.id, s.title, s.keywords, s.status, s.round, s.final_score::float8, false, 0, 0, s.created_at
		FROM submissions s WHERE s.submitter_id=$1 OR EXISTS (SELECT 1 FROM submission_authors sa WHERE sa.submission_id=s.id AND sa.user_id=$1)
		ORDER BY s.id DESC LIMIT 200`, p.UserID)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[submissionSummary])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, httpx.Page[submissionSummary]{Items: items})
	return nil
}

// listSubmissions: editör listesi; duruma/anahtar kelimeye/başlığa göre filtre,
// keyset sayfalama (id DESC, dizin: submissions_status_id_idx).
func (a *App) listSubmissions(w http.ResponseWriter, r *http.Request) error {
	limit, after, err := httpx.PageParams(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	args := []any{}
	where := []string{"true"}
	if st := q.Get("status"); st != "" {
		args = append(args, st)
		where = append(where, fmt.Sprintf("s.status = $%d", len(args)))
	}
	if kw := q.Get("keyword"); kw != "" {
		args = append(args, []string{strings.ToLower(kw)})
		where = append(where, fmt.Sprintf("s.keywords @> $%d", len(args)))
	}
	if t := q.Get("q"); t != "" {
		args = append(args, "%"+strings.ToLower(t)+"%")
		where = append(where, fmt.Sprintf("lower(s.title) LIKE $%d", len(args)))
	}
	if q.Get("disagreement") == "true" {
		where = append(where, "s.disagreement_flag")
	}
	if after > 0 {
		args = append(args, after)
		where = append(where, fmt.Sprintf("s.id < $%d", len(args)))
	}
	args = append(args, limit+1)
	sql := fmt.Sprintf(`SELECT s.id, s.title, s.keywords, s.status, s.round, s.final_score::float8, s.disagreement_flag,
		(SELECT count(*) FROM review_assignments ra WHERE ra.submission_id=s.id AND ra.round=s.round AND ra.status IN ('assigned','submitted'))::int,
		(SELECT count(*) FROM review_assignments ra WHERE ra.submission_id=s.id AND ra.round=s.round AND ra.status='submitted')::int,
		s.created_at
		FROM submissions s WHERE %s ORDER BY s.id DESC LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := a.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[submissionSummary])
	if err != nil {
		return err
	}
	page := httpx.Page[submissionSummary]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = httpx.Cursor(items[limit-1].ID)
	}
	httpx.WriteJSON(w, 200, page)
	return nil
}

// ---- detay (çift kör projeksiyon) ---------------------------------------------

type authorDTO struct {
	Position    int    `json:"position"`
	FullName    string `json:"full_name"`
	Email       string `json:"email"`
	Institution string `json:"institution,omitempty"`
	IsPresenter bool   `json:"is_presenter"`
}

type fileDTO struct {
	Version        int       `json:"version"`
	SizeBytes      int64     `json:"size_bytes"`
	OriginalName   string    `json:"original_name,omitempty"`
	ResponseLetter string    `json:"response_letter,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type reviewDTO struct {
	Reviewer         string    `json:"reviewer"`                // yazar için "Hakem A"
	ReviewerID       int64     `json:"reviewer_id,omitempty"`   // yalnızca editör
	ReviewerName     string    `json:"reviewer_name,omitempty"` // yalnızca editör
	Round            int       `json:"round"`
	Originality      int       `json:"originality"`
	Significance     int       `json:"significance"`
	Methodology      int       `json:"methodology"`
	Clarity          int       `json:"clarity"`
	Weighted         float64   `json:"weighted_score"`
	Recommendation   string    `json:"recommendation"`
	CommentsToAuthor string    `json:"comments_to_author"`
	CommentsToEditor string    `json:"comments_to_editor,omitempty"` // yalnızca editör
	SubmittedAt      time.Time `json:"submitted_at"`
}

type assignmentDTO struct {
	ID           int64      `json:"id"`
	ReviewerID   int64      `json:"reviewer_id"`
	ReviewerName string     `json:"reviewer_name"`
	Round        int        `json:"round"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason"`
	MatchScore   float64    `json:"match_score"`
	AssignedAt   time.Time  `json:"assigned_at"`
	DueAt        time.Time  `json:"due_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type decisionDTO struct {
	Round    int       `json:"round"`
	Decision string    `json:"decision"`
	Note     string    `json:"note_to_author"`
	At       time.Time `json:"decided_at"`
}

type submissionDTO struct {
	ID              int64             `json:"id"`
	Title           string            `json:"title"`
	Abstract        string            `json:"abstract"`
	Keywords        []string          `json:"keywords"`
	Status          string            `json:"status"`
	Round           int               `json:"round"`
	CurrentVersion  int               `json:"current_version"`
	DurationMinutes int               `json:"duration_minutes"`
	FinalScore      *float64          `json:"final_score,omitempty"`
	Disagreement    *bool             `json:"disagreement_flag,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	Authors         []authorDTO       `json:"authors,omitempty"`
	Files           []fileDTO         `json:"files"`
	Reviews         []reviewDTO       `json:"reviews,omitempty"`
	Decisions       []decisionDTO     `json:"decisions,omitempty"`
	Assignments     []assignmentDTO   `json:"assignments,omitempty"`
	Aggregate       *review.Aggregate `json:"aggregate,omitempty"`
	MyAssignmentID  int64             `json:"my_assignment_id,omitempty"`
}

func (a *App) getSubmission(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	p := principal(r)
	lvl, err := a.submissionAccess(r.Context(), p, id)
	if err != nil {
		return err
	}
	dto, err := a.buildSubmission(r.Context(), p, id, lvl)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, dto)
	return nil
}

// buildSubmission projects a submission for the given access level. Her alan
// açıkça seçilir: hakem görünümünde yazar alanları hiç doldurulmaz, yazar
// görünümünde hakem kimlikleri takma adlarla değiştirilir.
func (a *App) buildSubmission(ctx context.Context, p auth.Principal, id int64, lvl accessLevel) (*submissionDTO, error) {
	d := &submissionDTO{}
	var finalScore *float64
	var disagree bool
	err := a.Pool.QueryRow(ctx, `SELECT id, title, abstract, keywords, status, round, current_version, duration_minutes, final_score::float8, disagreement_flag, created_at
		FROM submissions WHERE id=$1`, id).Scan(&d.ID, &d.Title, &d.Abstract, &d.Keywords, &d.Status, &d.Round, &d.CurrentVersion,
		&d.DurationMinutes, &finalScore, &disagree, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	if lvl == accessEditor || lvl == accessOwner {
		d.FinalScore = finalScore
	}
	if lvl == accessEditor {
		d.Disagreement = &disagree
	}
	// yazarlar: yalnızca yazarın kendisi ve editör/yönetim
	if lvl == accessEditor || lvl == accessOwner {
		rows, err := a.Pool.Query(ctx, `SELECT sa.position, sa.full_name, sa.email, coalesce(i.name,''), sa.is_presenter
			FROM submission_authors sa LEFT JOIN institutions i ON i.id=sa.institution_id WHERE sa.submission_id=$1 ORDER BY sa.position`, id)
		if err != nil {
			return nil, err
		}
		d.Authors, err = pgx.CollectRows(rows, pgx.RowToStructByPos[authorDTO])
		if err != nil {
			return nil, err
		}
	}
	// dosyalar
	rows, err := a.Pool.Query(ctx, `SELECT version, size_bytes, original_name, coalesce(response_letter,''), created_at
		FROM submission_files WHERE submission_id=$1 ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	d.Files, err = pgx.CollectRows(rows, pgx.RowToStructByPos[fileDTO])
	if err != nil {
		return nil, err
	}
	if lvl == accessReviewer {
		for i := range d.Files {
			d.Files[i].OriginalName = "" // dosya adı yazar adını içerebilir
		}
		var aid int64
		_ = a.Pool.QueryRow(ctx, `SELECT id FROM review_assignments WHERE submission_id=$1 AND reviewer_id=$2 AND status IN ('assigned','submitted')
			ORDER BY round DESC LIMIT 1`, id, p.UserID).Scan(&aid)
		d.MyAssignmentID = aid
		return d, nil // hakem başka hiçbir şey görmez
	}

	// kararlar
	rows, err = a.Pool.Query(ctx, `SELECT round, decision, note_to_author, decided_at FROM decisions WHERE submission_id=$1 ORDER BY round`, id)
	if err != nil {
		return nil, err
	}
	d.Decisions, err = pgx.CollectRows(rows, pgx.RowToStructByPos[decisionDTO])
	if err != nil {
		return nil, err
	}

	// değerlendirmeler
	type revRow struct {
		AssignmentID int64
		ReviewerID   int64
		ReviewerName string
		reviewDTO
	}
	rows, err = a.Pool.Query(ctx, `SELECT ra.id, ra.reviewer_id, u.full_name, ra.round, r.originality, r.significance, r.methodology, r.clarity,
			r.weighted_score::float8, r.recommendation, r.comments_to_author, r.comments_to_editor, r.submitted_at
		FROM reviews r JOIN review_assignments ra ON ra.id=r.assignment_id JOIN users u ON u.id=ra.reviewer_id
		WHERE ra.submission_id=$1 ORDER BY ra.round, ra.id`, id)
	if err != nil {
		return nil, err
	}
	var all []revRow
	for rows.Next() {
		var x revRow
		if err := rows.Scan(&x.AssignmentID, &x.ReviewerID, &x.ReviewerName, &x.Round, &x.Originality, &x.Significance, &x.Methodology, &x.Clarity,
			&x.Weighted, &x.Recommendation, &x.CommentsToAuthor, &x.CommentsToEditor, &x.SubmittedAt); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, x)
	}
	rows.Close()
	decidedRounds := map[int]bool{}
	for _, dc := range d.Decisions {
		decidedRounds[dc.Round] = true
	}
	// takma adlar hakem başına sabit: aynı hakem iki turda da aynı harfi alır
	firstAssign := map[int64]int64{}
	var firstIDs []int64
	for _, x := range all {
		if _, ok := firstAssign[x.ReviewerID]; !ok {
			firstAssign[x.ReviewerID] = x.AssignmentID
			firstIDs = append(firstIDs, x.AssignmentID)
		}
	}
	labels := blind.ReviewerLabels(firstIDs)
	scores := []float64{}
	for _, x := range all {
		rv := x.reviewDTO
		rv.Reviewer = labels[firstAssign[x.ReviewerID]]
		if lvl == accessEditor {
			rv.ReviewerID, rv.ReviewerName = x.ReviewerID, x.ReviewerName
		} else {
			rv.CommentsToEditor = ""
			if !decidedRounds[x.Round] { // yazar değerlendirmeleri karar sonrası görür
				continue
			}
		}
		if x.Round == d.Round {
			scores = append(scores, x.Weighted)
		}
		d.Reviews = append(d.Reviews, rv)
	}
	if lvl == accessEditor {
		agg := review.Summarize(scores)
		d.Aggregate = &agg
		rows, err := a.Pool.Query(ctx, `SELECT ra.id, ra.reviewer_id, u.full_name, ra.round, ra.status, ra.reason, ra.match_score::float8, ra.assigned_at, ra.due_at, ra.completed_at
			FROM review_assignments ra JOIN users u ON u.id=ra.reviewer_id WHERE ra.submission_id=$1 ORDER BY ra.round, ra.id`, id)
		if err != nil {
			return nil, err
		}
		d.Assignments, err = pgx.CollectRows(rows, pgx.RowToStructByPos[assignmentDTO])
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

// ---- dosya indirme -------------------------------------------------------------

func (a *App) downloadFile(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	if _, err := a.submissionAccess(r.Context(), principal(r), id); err != nil {
		return err
	}
	version := 0
	if v := r.URL.Query().Get("version"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &version); err != nil || version < 1 {
			return httpx.Validation("geçersiz sürüm")
		}
	}
	var key string
	err = a.Pool.QueryRow(r.Context(), `SELECT storage_key, version FROM submission_files WHERE submission_id=$1 AND ($2=0 OR version=$2)
		ORDER BY version DESC LIMIT 1`, id, version).Scan(&key, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	data, err := a.Store.Get(key)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/pdf")
	// dosya adı her rolde nötr: yazar adını taşıyabilecek orijinal ad kullanılmaz
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="submission-%d-v%d.pdf"`, id, version))
	_, err = w.Write(data)
	return err
}

// ---- geçmiş (yazar için takma adlı) ---------------------------------------------

func (a *App) submissionHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	p := principal(r)
	lvl, err := a.submissionAccess(r.Context(), p, id)
	if err != nil {
		return err
	}
	rows, err := a.Pool.Query(r.Context(), `SELECT at, actor_id, actor_role, action, coalesce(new_value->>'status','') FROM audit_log
		WHERE entity='submission' AND entity_id=$1 ORDER BY id`, itoa(id))
	if err != nil {
		return err
	}
	defer rows.Close()
	type ev struct {
		At     time.Time `json:"at"`
		Actor  string    `json:"actor"`
		Action string    `json:"action"`
		Status string    `json:"status,omitempty"`
	}
	var out []ev
	for rows.Next() {
		var e ev
		var aid *int64
		var role string
		if err := rows.Scan(&e.At, &aid, &role, &e.Action, &e.Status); err != nil {
			return err
		}
		switch {
		case lvl == accessEditor && aid != nil:
			e.Actor = fmt.Sprintf("%s #%d", role, *aid)
		case aid != nil && *aid == p.UserID:
			e.Actor = "Siz"
		case role == RReviewer:
			e.Actor = "Hakem" // yazar hangi hakem olduğunu göremez
		case role == REditor:
			e.Actor = "Editör"
		case role == RAuthor:
			e.Actor = "Yazar"
		default:
			e.Actor = "Sistem"
		}
		// hakem atama olayları yazara yalnızca sayı olarak yansır
		if lvl != accessEditor && strings.HasPrefix(e.Action, "assignment.") {
			e.Action = "review.assignment_updated"
		}
		out = append(out, e)
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": out})
	return rows.Err()
}

// ---- düzeltme turu ve geri çekme -------------------------------------------------

func (a *App) uploadRevision(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	p := principal(r)
	if err := a.parseMultipart(r); err != nil {
		return err
	}
	lvl, err := a.submissionAccess(r.Context(), p, id)
	if err != nil {
		return err
	}
	if lvl != accessOwner {
		return httpx.ErrNotFound
	}
	letter := strings.TrimSpace(r.FormValue("response_letter"))
	if len(letter) < 10 {
		return httpx.Validation("hakemlere yanıt mektubu (en az 10 karakter) zorunlu")
	}
	pdf, origName, err := a.readPDF(r)
	if err != nil {
		return err
	}
	key, sum, err := a.Store.Put(pdf)
	if err != nil {
		return err
	}
	now := a.Clock.Now()
	var assigned []int64
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var round, version int
		if err := tx.QueryRow(ctx, `SELECT round, current_version FROM submissions WHERE id=$1 FOR UPDATE`, id).Scan(&round, &version); err != nil {
			return err
		}
		if _, err := transitionTx(ctx, tx, id, review.EvResubmit, p, map[string]any{"version": version + 1}); err != nil {
			return err
		}
		if round >= review.MaxRounds {
			return httpx.Conflict("max_rounds", "azami düzeltme turu sayısına ulaşıldı")
		}
		var decision string
		if err := tx.QueryRow(ctx, `SELECT decision FROM decisions WHERE submission_id=$1 AND round=$2`, id, round).Scan(&decision); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO submission_files (submission_id, version, storage_key, sha256, size_bytes, original_name, response_letter)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, version+1, key, sum, len(pdf), origName, letter); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE submissions SET round=round+1, current_version=current_version+1, updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		// hangi hakemler tekrar bakacak?
		rows, err := tx.Query(ctx, `SELECT ra.reviewer_id, r.recommendation FROM reviews r JOIN review_assignments ra ON ra.id=r.assignment_id
			WHERE ra.submission_id=$1 AND ra.round=$2 ORDER BY ra.id`, id, round)
		if err != nil {
			return err
		}
		var prior []review.PriorReview
		for rows.Next() {
			var pr review.PriorReview
			if err := rows.Scan(&pr.ReviewerID, &pr.Recommendation); err != nil {
				rows.Close()
				return err
			}
			prior = append(prior, pr)
		}
		rows.Close()
		sysP := auth.Principal{}
		for _, rid := range review.RevisionReviewers(decision, prior) {
			aid, err := assignTx(ctx, tx, id, rid, round+1, "revision", 0, now, sysP)
			if errors.Is(err, errNoCapacity) {
				// önceki hakemin kapasitesi doluysa yerine uygun başka hakem
				aid, _, err = pickAndAssign(ctx, tx, id, round+1, "replacement", now, sysP)
			}
			if err != nil {
				return err
			}
			if aid != 0 {
				assigned = append(assigned, aid)
			}
		}
		if len(assigned) == 0 {
			return httpx.Conflict("no_reviewer", "düzeltilmiş sürüm için uygun hakem bulunamadı; editör elle atamalı")
		}
		return nil
	})
	if err != nil {
		return err
	}
	dto, err := a.buildSubmission(r.Context(), p, id, accessOwner)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

func (a *App) withdraw(w http.ResponseWriter, r *http.Request) error {
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
	lvl, err := a.submissionAccess(r.Context(), p, id)
	if err != nil {
		return err
	}
	if lvl != accessOwner {
		return httpx.ErrNotFound
	}
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := transitionTx(r.Context(), tx, id, review.EvWithdraw, p, map[string]any{"reason": in.Reason}); err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `SELECT id FROM review_assignments WHERE submission_id=$1 AND status='assigned'`, id)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		for _, aid := range ids {
			if _, err := releaseTx(r.Context(), tx, aid, "released"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"id": id, "status": "withdrawn"})
	return nil
}

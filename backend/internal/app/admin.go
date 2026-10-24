package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

func (a *App) registerAdmin(r chi.Router) {
	a.handle(r, "GET", "/api/admin/users", a.listUsers, roles(RAdmin))
	a.handle(r, "POST", "/api/admin/users", a.createUser, roles(RAdmin))
	a.handle(r, "GET", "/api/admin/audit", a.auditLog, roles(RAdmin))
	a.handle(r, "GET", "/api/admin/routes", a.listRoutes, roles(RAdmin))
	a.handle(r, "GET", "/api/admin/clock", a.getClock, roles(RAdmin, REditor))
	a.handle(r, "POST", "/api/admin/clock", a.setClock, roles(RAdmin))
	a.handle(r, "POST", "/api/admin/demo/backdate-assignment", a.backdateAssignment, roles(RAdmin))
}

func (a *App) listUsers(w http.ResponseWriter, r *http.Request) error {
	limit, after, err := httpx.PageParams(r)
	if err != nil {
		return err
	}
	role := r.URL.Query().Get("role")
	rows, err := a.Pool.Query(r.Context(), `SELECT u.id, u.email, u.full_name, u.role, coalesce(i.name,''), u.timezone, u.is_student, u.is_member
		FROM users u LEFT JOIN institutions i ON i.id=u.institution_id WHERE ($1='' OR u.role=$1) AND ($2=0 OR u.id > $2) ORDER BY u.id LIMIT $3`, role, after, limit+1)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[userDTO])
	if err != nil {
		return err
	}
	page := httpx.Page[userDTO]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = httpx.Cursor(items[limit-1].ID)
	}
	httpx.WriteJSON(w, 200, page)
	return nil
}

func (a *App) createUser(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email         string   `json:"email"`
		Password      string   `json:"password"`
		FullName      string   `json:"full_name"`
		Role          string   `json:"role"`
		InstitutionID *int64   `json:"institution_id"`
		Timezone      string   `json:"timezone"`
		IsStudent     bool     `json:"is_student"`
		IsMember      bool     `json:"is_member"`
		Keywords      []string `json:"keywords"`
		MaxLoad       int      `json:"max_load"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if _, err := mail.ParseAddress(in.Email); err != nil {
		return httpx.Validation("geçersiz e-posta")
	}
	if len(in.Password) < 8 {
		return httpx.Validation("parola en az 8 karakter olmalı")
	}
	valid := false
	for _, ro := range AllRoles {
		if ro == in.Role {
			valid = true
		}
	}
	if !valid {
		return httpx.Validation("geçersiz rol")
	}
	if in.Timezone == "" {
		in.Timezone = "Europe/Istanbul"
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return httpx.Validation("geçersiz saat dilimi")
	}
	if in.Role == RReviewer && (len(in.Keywords) == 0 || in.MaxLoad < 1) {
		return httpx.Validation("hakem için anahtar kelime ve azami yük gerekli")
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	p := principal(r)
	var id int64
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `INSERT INTO users (email, password_hash, full_name, role, institution_id, timezone, is_student, is_member)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, in.Email, hash, in.FullName, in.Role, in.InstitutionID, in.Timezone, in.IsStudent, in.IsMember).Scan(&id)
		if db.IsUniqueViolation(err, "") {
			return httpx.Conflict("email_taken", "bu e-posta zaten kayıtlı")
		}
		if db.IsCheckViolation(err, "") {
			return httpx.Validation("geçersiz kullanıcı bilgisi")
		}
		if err != nil {
			return err
		}
		if in.Role == RReviewer {
			if _, err := tx.Exec(r.Context(), `INSERT INTO reviewer_profiles (user_id, keywords, max_load) VALUES ($1,$2,$3)`, id, in.Keywords, in.MaxLoad); err != nil {
				return err
			}
		}
		aid, role := actor(p)
		return audit.Log(r.Context(), tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "user.created", Entity: "user", EntityID: itoa(id), New: map[string]any{"role": in.Role}})
	})
	if err != nil {
		return err
	}
	u, err := a.loadUser(r, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 201, u)
	return nil
}

func (a *App) auditLog(w http.ResponseWriter, r *http.Request) error {
	limit, after, err := httpx.PageParams(r)
	if err != nil {
		return err
	}
	entity := r.URL.Query().Get("entity")
	action := r.URL.Query().Get("action")
	rows, err := a.Pool.Query(r.Context(), `SELECT id, at, actor_id, actor_role, action, entity, entity_id, old_value, new_value FROM audit_log
		WHERE ($1='' OR entity=$1) AND ($2='' OR action LIKE $2 || '%') AND ($3=0 OR id < $3) ORDER BY id DESC LIMIT $4`, entity, action, after, limit+1)
	if err != nil {
		return err
	}
	type row struct {
		ID        int64           `json:"id"`
		At        time.Time       `json:"at"`
		ActorID   *int64          `json:"actor_id"`
		ActorRole string          `json:"actor_role"`
		Action    string          `json:"action"`
		Entity    string          `json:"entity"`
		EntityID  string          `json:"entity_id"`
		Old       json.RawMessage `json:"old_value"`
		New       json.RawMessage `json:"new_value"`
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return err
	}
	page := httpx.Page[row]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = httpx.Cursor(items[limit-1].ID)
	}
	httpx.WriteJSON(w, 200, page)
	return nil
}

func (a *App) listRoutes(w http.ResponseWriter, _ *http.Request) error {
	httpx.WriteJSON(w, 200, map[string]any{"items": a.Routes, "roles": AllRoles})
	return nil
}

func (a *App) getClock(w http.ResponseWriter, _ *http.Request) error {
	httpx.WriteJSON(w, 200, map[string]any{"now": a.Clock.Now().UTC(), "offset_hours": a.Clock.Offset().Hours(), "demo_mode": a.Cfg.DemoMode})
	return nil
}

// setClock (yalnızca DEMO_MODE): 14 günlük süre aşımını beklemeden göstermek için.
func (a *App) setClock(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		OffsetHours float64 `json:"offset_hours"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if !a.Cfg.DemoMode {
		return httpx.E(http.StatusForbidden, "demo_mode_disabled", "saat kaydırma yalnızca DEMO_MODE=true iken kullanılabilir")
	}
	if in.OffsetHours < 0 || in.OffsetHours > 24*365 {
		return httpx.Validation("offset_hours 0 ile 8760 arasında olmalı")
	}
	d := time.Duration(in.OffsetHours * float64(time.Hour))
	a.Clock.SetOffset(d)
	if _, err := a.Pool.Exec(r.Context(), `INSERT INTO app_settings (key, value) VALUES ('clock_offset_hours', $1)
		ON CONFLICT (key) DO UPDATE SET value=excluded.value`, strconvF(in.OffsetHours)); err != nil {
		return err
	}
	p := principal(r)
	aid, role := actor(p)
	_ = audit.Log(r.Context(), a.Pool, audit.Entry{ActorID: aid, ActorRole: role, Action: "clock.offset_changed", Entity: "clock", EntityID: "app", New: map[string]any{"offset_hours": in.OffsetHours}})
	return a.getClock(w, r)
}

// LoadClock restores a persisted demo offset at startup.
func (a *App) LoadClock(ctx context.Context) {
	if !a.Cfg.DemoMode {
		return
	}
	var v string
	if err := a.Pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key='clock_offset_hours'`).Scan(&v); err == nil {
		if f, err := parseF(v); err == nil {
			a.Clock.SetOffset(time.Duration(f * float64(time.Hour)))
		}
	}
}

// backdateAssignment (yalnızca DEMO_MODE): tek bir atamanın atanma tarihini
// geriye alır; 14 günlük süre aşımı ve hatırlatmalar saati global olarak
// kaydırmadan, tek bir kayıt üzerinde gösterilebilir.
func (a *App) backdateAssignment(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		AssignmentID int64 `json:"assignment_id"`
		Days         int   `json:"days"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if !a.Cfg.DemoMode {
		return httpx.E(http.StatusForbidden, "demo_mode_disabled", "yalnızca DEMO_MODE=true iken kullanılabilir")
	}
	if in.Days < 1 || in.Days > 60 {
		return httpx.Validation("days 1-60 arasında olmalı")
	}
	tag, err := a.Pool.Exec(r.Context(), `UPDATE review_assignments SET assigned_at = assigned_at - make_interval(days => $2),
		due_at = due_at - make_interval(days => $2) WHERE id=$1 AND status='assigned'`, in.AssignmentID, in.Days)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	p := principal(r)
	aid, role := actor(p)
	_ = audit.Log(r.Context(), a.Pool, audit.Entry{ActorID: aid, ActorRole: role, Action: "demo.assignment_backdated", Entity: "assignment", EntityID: itoa(in.AssignmentID), New: map[string]any{"days": in.Days}})
	httpx.WriteJSON(w, 200, map[string]any{"assignment_id": in.AssignmentID, "backdated_days": in.Days})
	return nil
}

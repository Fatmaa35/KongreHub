package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

func (a *App) registerAuth(r chi.Router) {
	a.handle(r, "POST", "/api/auth/login", a.login, public())
	a.handle(r, "POST", "/api/auth/refresh", a.refresh, public())
	a.handle(r, "POST", "/api/auth/logout", a.logout, public())
	a.handle(r, "GET", "/api/me", a.me)
}

type tokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         userDTO   `json:"user"`
}

type userDTO struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	FullName    string `json:"full_name"`
	Role        string `json:"role"`
	Institution string `json:"institution,omitempty"`
	Timezone    string `json:"timezone"`
	IsStudent   bool   `json:"is_student"`
	IsMember    bool   `json:"is_member"`
}

var errBadCredentials = httpx.E(http.StatusUnauthorized, "invalid_credentials", "e-posta veya parola hatalı")

func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.Password == "" {
		return httpx.Validation("e-posta ve parola zorunlu")
	}
	var u userDTO
	var hash string
	err := a.Pool.QueryRow(r.Context(), `SELECT u.id, u.email, u.full_name, u.role, coalesce(i.name,''), u.timezone, u.is_student, u.is_member, u.password_hash
		FROM users u LEFT JOIN institutions i ON i.id = u.institution_id WHERE lower(u.email) = $1`, in.Email).
		Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.Institution, &u.Timezone, &u.IsStudent, &u.IsMember, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		auth.DummyVerify(in.Password) // kullanıcı var/yok bilgisini süreyle sızdırma
		return errBadCredentials
	}
	if err != nil {
		return err
	}
	ok, err := auth.VerifyPassword(in.Password, hash)
	if err != nil || !ok {
		_ = audit.Log(r.Context(), a.Pool, audit.Entry{ActorID: audit.Ptr(u.ID), ActorRole: u.Role, Action: "auth.login_failed", Entity: "user", EntityID: itoa(u.ID)})
		return errBadCredentials
	}
	return a.issueTokens(w, r, u)
}

func (a *App) issueTokens(w http.ResponseWriter, r *http.Request, u userDTO) error {
	access, exp, err := a.Tokens.Access(auth.Principal{UserID: u.ID, Role: u.Role, Name: u.FullName})
	if err != nil {
		return err
	}
	refresh, hash, err := auth.NewRefreshToken()
	if err != nil {
		return err
	}
	if _, err := a.Pool.Exec(r.Context(), `INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1,$2,$3)`,
		u.ID, hash, time.Now().Add(a.Cfg.RefreshTTL)); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, tokenResponse{AccessToken: access, RefreshToken: refresh, ExpiresAt: exp, User: u})
	return nil
}

// refresh rotates the refresh token: eskisi iptal edilir, yenisi verilir.
// İptal edilmiş bir token tekrar kullanılırsa (çalınma belirtisi) kullanıcının
// tüm refresh tokenları iptal edilir.
func (a *App) refresh(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.RefreshToken == "" {
		return httpx.Validation("refresh_token zorunlu")
	}
	hash := auth.HashRefresh(in.RefreshToken)
	var userID int64
	var revoked *time.Time
	var expires time.Time
	err := a.Pool.QueryRow(r.Context(), `SELECT user_id, revoked_at, expires_at FROM refresh_tokens WHERE token_hash=$1`, hash).
		Scan(&userID, &revoked, &expires)
	invalid := httpx.E(http.StatusUnauthorized, "invalid_refresh_token", "oturum yenilenemedi, lütfen tekrar giriş yapın")
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid
	}
	if err != nil {
		return err
	}
	if revoked != nil {
		_, _ = a.Pool.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		return invalid
	}
	if time.Now().After(expires) {
		return invalid
	}
	tag, err := a.Pool.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 { // eşzamanlı yenileme yarışını kaybetti
		return invalid
	}
	u, err := a.loadUser(r, userID)
	if err != nil {
		return err
	}
	return a.issueTokens(w, r, u)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.RefreshToken != "" {
		_, _ = a.Pool.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, auth.HashRefresh(in.RefreshToken))
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *App) loadUser(r *http.Request, id int64) (userDTO, error) {
	var u userDTO
	err := a.Pool.QueryRow(r.Context(), `SELECT u.id, u.email, u.full_name, u.role, coalesce(i.name,''), u.timezone, u.is_student, u.is_member
		FROM users u LEFT JOIN institutions i ON i.id = u.institution_id WHERE u.id=$1`, id).
		Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.Institution, &u.Timezone, &u.IsStudent, &u.IsMember)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, httpx.ErrUnauthorized
	}
	return u, err
}

func (a *App) me(w http.ResponseWriter, r *http.Request) error {
	u, err := a.loadUser(r, principal(r).UserID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, u)
	return nil
}

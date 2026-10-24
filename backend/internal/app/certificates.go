package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-pdf/fpdf"
	"github.com/jackc/pgx/v5"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/domain/certsign"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

func (a *App) registerCertificates(r chi.Router) {
	a.handle(r, "POST", "/api/certificates", a.issueCertificate, roles(selfRegisterRoles...), idempotent())
	a.handle(r, "GET", "/api/certificates/mine", a.myCertificates, roles(selfRegisterRoles...))
	a.handle(r, "GET", "/api/certificates/{id}/pdf", a.certificatePDF, roles(append(selfRegisterRoles, RRegistrar, RAdmin)...))
	a.handle(r, "GET", "/api/certificates/verify", a.verifyCertificate, public())
	a.handle(r, "GET", "/api/certificates/public-key", a.certificateKey, public())
}

type certDTO struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Holder    string    `json:"holder_name"`
	Detail    string    `json:"detail"`
	IssuedAt  time.Time `json:"issued_at"`
	Signature string    `json:"signature"`
	VerifyURL string    `json:"verify_url"`
}

func (a *App) issueCertificate(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Kind string `json:"kind"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Kind != "attendance" && in.Kind != "presenter" {
		return httpx.Validation("kind attendance veya presenter olmalı")
	}
	p := principal(r)
	ctx := r.Context()
	var paid bool
	if err := a.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM registrations WHERE user_id=$1 AND status='paid')`, p.UserID).Scan(&paid); err != nil {
		return err
	}
	if !paid {
		return httpx.E(http.StatusUnprocessableEntity, "not_eligible", "sertifika için ödemesi tamamlanmış kayıt gerekli")
	}
	detail := "KongreHub 2027 — Katılım"
	if in.Kind == "presenter" {
		var title string
		err := a.Pool.QueryRow(ctx, `SELECT s.title FROM session_papers sp JOIN submissions s ON s.id=sp.submission_id
			JOIN submission_authors sa ON sa.submission_id=s.id AND sa.is_presenter AND sa.user_id=$1 ORDER BY s.id LIMIT 1`, p.UserID).Scan(&title)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.E(http.StatusUnprocessableEntity, "not_eligible", "sunum sertifikası için programda yer alan bir bildirinin sunucusu olmalısınız")
		}
		if err != nil {
			return err
		}
		detail = "Sunum: " + title
	}
	var name string
	if err := a.Pool.QueryRow(ctx, `SELECT full_name FROM users WHERE id=$1`, p.UserID).Scan(&name); err != nil {
		return err
	}
	// mevcutsa aynısını döndür (sertifika tekil: kullanıcı+tür)
	if c, err := a.loadCert(ctx, `user_id=$1 AND kind=$2`, p.UserID, in.Kind); err == nil {
		httpx.WriteJSON(w, 200, c)
		return nil
	}
	id := newUUID()
	issued := a.Clock.Now().UTC().Truncate(time.Second)
	sig := a.Signer.Sign(certsign.Payload{ID: id, UserID: p.UserID, Kind: in.Kind, Holder: name, Detail: detail, IssuedAt: issued})
	_, err := a.Pool.Exec(ctx, `INSERT INTO certificates (id, user_id, kind, holder_name, detail, signature, issued_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id, kind) DO NOTHING`, id, p.UserID, in.Kind, name, detail, sig, issued)
	if err != nil {
		return err
	}
	c, err := a.loadCert(ctx, `user_id=$1 AND kind=$2`, p.UserID, in.Kind)
	if err != nil {
		return err
	}
	aid, role := actor(p)
	_ = audit.Log(ctx, a.Pool, audit.Entry{ActorID: aid, ActorRole: role, Action: "certificate.issued", Entity: "certificate", EntityID: c.ID, New: map[string]any{"kind": in.Kind}})
	httpx.WriteJSON(w, http.StatusCreated, c)
	return nil
}

func newUUID() string {
	b := []byte(randHex(16))
	return fmt.Sprintf("%s-%s-4%s-a%s-%s", b[0:8], b[8:12], b[13:16], b[17:20], b[20:32])
}

func (a *App) loadCert(ctx context.Context, where string, args ...any) (*certDTO, error) {
	var c certDTO
	err := a.Pool.QueryRow(ctx, `SELECT id::text, kind, holder_name, detail, issued_at, signature FROM certificates WHERE `+where, args...).
		Scan(&c.ID, &c.Kind, &c.Holder, &c.Detail, &c.IssuedAt, &c.Signature)
	if err != nil {
		return nil, err
	}
	c.VerifyURL = certsign.VerifyURL(a.Cfg.PublicURL, c.ID, c.Signature)
	return &c, nil
}

func (a *App) myCertificates(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT id::text FROM certificates WHERE user_id=$1 ORDER BY issued_at`, principal(r).UserID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	out := []certDTO{}
	for _, id := range ids {
		c, err := a.loadCert(r.Context(), `id=$1::uuid`, id)
		if err != nil {
			return err
		}
		out = append(out, *c)
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": out})
	return nil
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (a *App) certificatePDF(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		return httpx.ErrNotFound
	}
	p := principal(r)
	var owner int64
	if err := a.Pool.QueryRow(r.Context(), `SELECT user_id FROM certificates WHERE id=$1::uuid`, id).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		return err
	}
	if owner != p.UserID && p.Role != RRegistrar && p.Role != RAdmin {
		return httpx.ErrNotFound
	}
	c, err := a.loadCert(r.Context(), `id=$1::uuid`, id)
	if err != nil {
		return err
	}
	pdf, err := RenderCertificatePDF(c)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="sertifika-%s.pdf"`, c.ID[:8]))
	_, err = w.Write(pdf)
	return err
}

// RenderCertificatePDF draws the certificate with an embedded signed QR code.
func RenderCertificatePDF(c *certDTO) ([]byte, error) {
	png, err := qrcode.Encode(c.VerifyURL, qrcode.Medium, 512)
	if err != nil {
		return nil, err
	}
	p := fpdf.New("L", "mm", "A4", "")
	tr := p.UnicodeTranslatorFromDescriptor("cp1254") // Türkçe karakterler
	p.SetTitle("KongreHub Sertifika", true)
	p.SetAuthor("KongreHub", true)
	p.SetCreator("KongreHub", true)
	p.AddPage()
	p.SetDrawColor(30, 64, 120)
	p.SetLineWidth(1.5)
	p.Rect(10, 10, 277, 190, "D")
	p.SetLineWidth(0.4)
	p.Rect(14, 14, 269, 182, "D")
	p.SetFont("Helvetica", "B", 30)
	p.SetTextColor(30, 64, 120)
	p.SetXY(20, 35)
	title := "KATILIM SERTİFİKASI"
	if c.Kind == "presenter" {
		title = "SUNUM SERTİFİKASI"
	}
	p.CellFormat(257, 14, tr(title), "", 1, "C", false, 0, "")
	p.SetFont("Helvetica", "", 14)
	p.SetTextColor(60, 60, 60)
	p.SetX(20)
	p.CellFormat(257, 10, tr("Bu belge aşağıda adı geçen kişinin"), "", 1, "C", false, 0, "")
	p.SetFont("Helvetica", "B", 26)
	p.SetTextColor(20, 20, 20)
	p.SetX(20)
	p.CellFormat(257, 18, tr(c.Holder), "", 1, "C", false, 0, "")
	p.SetFont("Helvetica", "", 14)
	p.SetTextColor(60, 60, 60)
	p.SetX(40)
	p.MultiCell(217, 8, tr(fmt.Sprintf("Uluslararası KongreHub Bilimsel Kongresi'ne (10–12 Mayıs 2027, İstanbul) katıldığını onaylar.\n%s", c.Detail)), "", "C", false)
	p.RegisterImageOptionsReader("qr", fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(png))
	p.ImageOptions("qr", 235, 140, 40, 40, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	p.SetFont("Helvetica", "", 8)
	p.SetXY(20, 165)
	p.MultiCell(200, 4.5, tr(fmt.Sprintf("Sertifika No: %s\nDüzenlenme: %s\nDoğrulama: QR kodu okutun veya %s\nEd25519 imzası: %s",
		c.ID, c.IssuedAt.Format("2006-01-02 15:04 UTC"), c.VerifyURL[:min(len(c.VerifyURL), 60)]+"…", c.Signature[:24]+"…")), "", "L", false)
	var buf bytes.Buffer
	if err := p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// verifyCertificate is public: QR kodundaki id+imza ile doğrulama.
func (a *App) verifyCertificate(w http.ResponseWriter, r *http.Request) error {
	id := r.URL.Query().Get("id")
	sig := r.URL.Query().Get("sig")
	invalid := map[string]any{"valid": false}
	if !validUUID(id) || sig == "" {
		httpx.WriteJSON(w, 200, invalid)
		return nil
	}
	var c certDTO
	var userID int64
	err := a.Pool.QueryRow(r.Context(), `SELECT id::text, user_id, kind, holder_name, detail, issued_at FROM certificates WHERE id=$1::uuid`, id).
		Scan(&c.ID, &userID, &c.Kind, &c.Holder, &c.Detail, &c.IssuedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteJSON(w, 200, invalid)
		return nil
	}
	if err != nil {
		return err
	}
	ok := a.Signer.Verify(certsign.Payload{ID: c.ID, UserID: userID, Kind: c.Kind, Holder: c.Holder, Detail: c.Detail, IssuedAt: c.IssuedAt}, sig)
	if !ok {
		httpx.WriteJSON(w, 200, invalid)
		return nil
	}
	httpx.WriteJSON(w, 200, map[string]any{"valid": true, "id": c.ID, "kind": c.Kind, "holder_name": c.Holder, "detail": c.Detail, "issued_at": c.IssuedAt})
	return nil
}

func (a *App) certificateKey(w http.ResponseWriter, _ *http.Request) error {
	httpx.WriteJSON(w, 200, map[string]any{"algorithm": "Ed25519", "public_key_hex": a.Signer.PublicKeyHex(),
		"canonical_format": "kongrehub-cert|v1|{id}|{user_id}|{kind}|{holder}|{detail}|{issued_unix}"})
	return nil
}

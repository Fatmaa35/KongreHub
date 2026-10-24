package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/fatmakaplan/kongrehub/internal/domain/pricing"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

// Kayıt yaptırabilen roller (kendi adına).
var selfRegisterRoles = []string{RAuthor, RReviewer, REditor, RAttendee}

func (a *App) registerRegistrations(r chi.Router) {
	a.handle(r, "GET", "/api/fx/current", a.currentFX)
	a.handle(r, "POST", "/api/registrations/quote", a.quote, roles(append(selfRegisterRoles, RRegistrar)...))
	a.handle(r, "POST", "/api/registrations", a.createRegistration, roles(selfRegisterRoles...), idempotent())
	a.handle(r, "GET", "/api/registrations/mine", a.myRegistrations, roles(selfRegisterRoles...))
	a.handle(r, "GET", "/api/registrations", a.listRegistrations, roles(RRegistrar, RAdmin))
	a.handle(r, "POST", "/api/registrations/group", a.groupRegistration, roles(RRegistrar), idempotent())
	a.handle(r, "POST", "/api/registrations/{id}/payments", a.pay, roles(append(selfRegisterRoles, RRegistrar)...), idempotent())
	a.handle(r, "POST", "/api/registrations/{id}/requote", a.requote, roles(append(selfRegisterRoles, RRegistrar)...))
	a.handle(r, "POST", "/api/registrations/{id}/cancel", a.cancelRegistration, roles(append(selfRegisterRoles, RRegistrar)...), idempotent())
	a.handle(r, "GET", "/api/registrations/{id}/refund-quote", a.refundQuote, roles(append(selfRegisterRoles, RRegistrar)...))
	a.handle(r, "POST", "/api/fx", a.recordFX, roles(RRegistrar, RAdmin))
	a.handle(r, "GET", "/api/invoices", a.listInvoices, roles(RRegistrar, RAdmin))
	a.handle(r, "POST", "/api/payments/webhook", a.paymentWebhook, public())
}

// latestRate returns the most recent EUR→TRY rate.
func (a *App) latestRate(ctx context.Context, q db.Querier) (pricing.Rate, time.Time, error) {
	var s string
	var at time.Time
	err := q.QueryRow(ctx, `SELECT rate::text, observed_at FROM fx_rates ORDER BY observed_at DESC, id DESC LIMIT 1`).Scan(&s, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, at, httpx.Conflict("no_fx_rate", "kur bilgisi henüz yok")
	}
	if err != nil {
		return 0, at, err
	}
	r, err := pricing.ParseRate(s)
	return r, at, err
}

func (a *App) currentFX(w http.ResponseWriter, r *http.Request) error {
	rate, at, err := a.latestRate(r.Context(), a.Pool)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"base": "EUR", "quote": "TRY", "rate": rate.String(), "observed_at": at})
	return nil
}

type quoteRequest struct {
	Currency  string `json:"currency"`
	Student   *bool  `json:"student"`    // yalnızca kayıt görevlisi senaryo için verebilir
	Member    *bool  `json:"member"`
	GroupSize int    `json:"group_size"`
	At        string `json:"at"` // RFC3339; boşsa şimdi (yalnızca hesap önizleme)
}

func (a *App) quote(w http.ResponseWriter, r *http.Request) error {
	var in quoteRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	u, err := a.loadUser(r, p.UserID)
	if err != nil {
		return err
	}
	qi := pricing.QuoteInput{At: a.Clock.Now(), Student: u.IsStudent, Member: u.IsMember, GroupSize: in.GroupSize, Currency: strings.ToUpper(in.Currency)}
	if in.Student != nil {
		qi.Student = *in.Student
	}
	if in.Member != nil {
		qi.Member = *in.Member
	}
	if in.At != "" {
		t, err := time.Parse(time.RFC3339, in.At)
		if err != nil {
			return httpx.Validation("at RFC3339 biçiminde olmalı")
		}
		qi.At = t
	}
	if qi.Currency == "TRY" {
		if qi.Rate, _, err = a.latestRate(r.Context(), a.Pool); err != nil {
			return err
		}
	}
	q, err := pricing.Price(a.Calendar, qi)
	if err != nil {
		return httpx.Validation(err.Error())
	}
	httpx.WriteJSON(w, 200, q)
	return nil
}

type registrationDTO struct {
	ID            int64           `json:"id"`
	UserID        int64           `json:"user_id"`
	UserName      string          `json:"user_name"`
	GroupID       *int64          `json:"group_id,omitempty"`
	Tier          string          `json:"tier"`
	Category      string          `json:"category"`
	IsMember      bool            `json:"is_member"`
	Currency      string          `json:"currency"`
	FXRate        string          `json:"fx_rate"`
	BaseCents     int64           `json:"base_cents"`
	DiscountCents int64           `json:"discount_cents"`
	TotalCents    int64           `json:"total_cents"`
	Breakdown     json.RawMessage `json:"breakdown"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	PaidAt        *time.Time      `json:"paid_at,omitempty"`
	QuoteValidTo  time.Time       `json:"quote_valid_until"`
	InvoiceNumber *string         `json:"invoice_number,omitempty"`
	RefundCents   *int64          `json:"refund_cents,omitempty"`
}

const registrationSelect = `SELECT rg.id, rg.user_id, u.full_name, rg.group_id, rg.tier, rg.category, rg.is_member, rg.currency, rg.fx_rate::text,
	rg.base_cents, rg.discount_cents, rg.total_cents, rg.breakdown, rg.status, rg.created_at, rg.paid_at, rg.created_at + interval '24 hours',
	(SELECT number FROM invoices i WHERE i.registration_id=rg.id), (SELECT amount_cents FROM refunds f WHERE f.registration_id=rg.id)
	FROM registrations rg JOIN users u ON u.id=rg.user_id`

func (a *App) getRegistration(ctx context.Context, q db.Querier, id int64) (*registrationDTO, error) {
	rows, err := q.Query(ctx, registrationSelect+` WHERE rg.id=$1`, id)
	if err != nil {
		return nil, err
	}
	d, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[registrationDTO])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	return &d, err
}

// insertRegistration prices and stores one registration with a frozen rate.
func (a *App) insertRegistration(ctx context.Context, tx pgx.Tx, userID int64, currency string, groupID *int64, groupSize int, rate pricing.Rate, now time.Time) (int64, error) {
	var student, member bool
	if err := tx.QueryRow(ctx, `SELECT is_student, is_member FROM users WHERE id=$1`, userID).Scan(&student, &member); err != nil {
		return 0, err
	}
	q, err := pricing.Price(a.Calendar, pricing.QuoteInput{At: now, Student: student, Member: member, GroupSize: groupSize, Currency: currency, Rate: rate})
	if err != nil {
		return 0, httpx.Validation(err.Error())
	}
	br, _ := json.Marshal(q.Lines)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO registrations (user_id, group_id, tier, category, is_member, currency, fx_rate, base_cents, discount_cents, total_cents, breakdown, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7::numeric,$8,$9,$10,$11,$12) RETURNING id`,
		userID, groupID, string(q.Tier), q.Category, member, currency, q.Rate, q.BaseCents, q.DiscountCents, q.TotalCents, br, now).Scan(&id)
	if db.IsUniqueViolation(err, "registrations_one_active") {
		return 0, httpx.Conflict("already_registered", "bu kullanıcının zaten aktif bir kaydı var")
	}
	return id, err
}

func (a *App) createRegistration(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Currency string `json:"currency"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Currency = strings.ToUpper(in.Currency)
	if in.Currency != "EUR" && in.Currency != "TRY" {
		return httpx.Validation("currency EUR veya TRY olmalı")
	}
	p := principal(r)
	now := a.Clock.Now()
	var id int64
	err := db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rate := pricing.Rate(1_000_000)
		if in.Currency == "TRY" {
			var err error
			if rate, _, err = a.latestRate(r.Context(), tx); err != nil {
				return err
			}
		}
		var err error
		id, err = a.insertRegistration(r.Context(), tx, p.UserID, in.Currency, nil, 1, rate, now)
		if err != nil {
			return err
		}
		aid, role := actor(p)
		return audit.Log(r.Context(), tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "registration.created", Entity: "registration", EntityID: itoa(id),
			New: map[string]any{"currency": in.Currency, "fx_rate": rate.String()}})
	})
	if err != nil {
		return err
	}
	d, err := a.getRegistration(r.Context(), a.Pool, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
	return nil
}

// groupRegistration: kayıt görevlisi en az 5 kişilik grubu tek seferde kaydeder;
// grup indirimi her üyeye uygulanır. Tüm grup tek transaction'dadır — bir üye
// başarısızsa hiçbiri kaydedilmez.
func (a *App) groupRegistration(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name     string   `json:"name"`
		Currency string   `json:"currency"`
		Emails   []string `json:"emails"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Currency = strings.ToUpper(in.Currency)
	if in.Currency != "EUR" && in.Currency != "TRY" {
		return httpx.Validation("currency EUR veya TRY olmalı")
	}
	if strings.TrimSpace(in.Name) == "" {
		return httpx.Validation("grup adı zorunlu")
	}
	seen := map[string]bool{}
	for _, e := range in.Emails {
		e = strings.ToLower(strings.TrimSpace(e))
		if _, err := mail.ParseAddress(e); err != nil {
			return httpx.Validation("geçersiz e-posta adresi")
		}
		if seen[e] {
			return httpx.Validation("e-posta listesinde tekrar var")
		}
		seen[e] = true
	}
	if len(seen) < 1 || len(seen) > 100 {
		return httpx.Validation("1-100 katılımcı gerekli")
	}
	p := principal(r)
	now := a.Clock.Now()
	var ids []int64
	var groupID int64
	err := db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		ids = ids[:0]
		rate := pricing.Rate(1_000_000)
		if in.Currency == "TRY" {
			var err error
			if rate, _, err = a.latestRate(ctx, tx); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO registration_groups (name, created_by, size) VALUES ($1,$2,$3) RETURNING id`, in.Name, p.UserID, len(seen)).Scan(&groupID); err != nil {
			return err
		}
		for e := range seen {
			var uid int64
			if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=$1`, e).Scan(&uid); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return httpx.Validation("sistemde kayıtlı olmayan e-posta: listedeki tüm katılımcıların hesabı olmalı")
				}
				return err
			}
			id, err := a.insertRegistration(ctx, tx, uid, in.Currency, &groupID, len(seen), rate, now)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		aid, role := actor(p)
		return audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "registration.group_created", Entity: "registration_group", EntityID: itoa(groupID),
			New: map[string]any{"size": len(seen), "registrations": ids, "group_discount": len(seen) >= pricing.GroupMinSize}})
	})
	if err != nil {
		return err
	}
	var out []registrationDTO
	for _, id := range ids {
		d, err := a.getRegistration(r.Context(), a.Pool, id)
		if err != nil {
			return err
		}
		out = append(out, *d)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"group_id": groupID, "registrations": out})
	return nil
}

func (a *App) myRegistrations(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), registrationSelect+` WHERE rg.user_id=$1 ORDER BY rg.id DESC`, principal(r).UserID)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[registrationDTO])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, httpx.Page[registrationDTO]{Items: items})
	return nil
}

func (a *App) listRegistrations(w http.ResponseWriter, r *http.Request) error {
	limit, after, err := httpx.PageParams(r)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	rows, err := a.Pool.Query(r.Context(), registrationSelect+` WHERE ($1='' OR rg.status=$1) AND ($2=0 OR rg.id < $2) ORDER BY rg.id DESC LIMIT $3`,
		status, after, limit+1)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[registrationDTO])
	if err != nil {
		return err
	}
	page := httpx.Page[registrationDTO]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = httpx.Cursor(items[limit-1].ID)
	}
	httpx.WriteJSON(w, 200, page)
	return nil
}

// ownRegistration: kayıt sahibi ya da kayıt görevlisi erişebilir.
func (a *App) ownRegistration(ctx context.Context, p auth.Principal, id int64) error {
	if p.Role == RRegistrar || p.Role == RAdmin {
		return nil
	}
	var ok bool
	if err := a.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM registrations WHERE id=$1 AND user_id=$2)`, id, p.UserID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.ErrNotFound
	}
	return nil
}

// requote: süresi geçmiş fiyat teklifini güncel kurla yeniden fiyatlar.
func (a *App) requote(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in struct{}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	if err := a.ownRegistration(r.Context(), p, id); err != nil {
		return err
	}
	now := a.Clock.Now()
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var status, currency string
		var userID int64
		var groupID *int64
		if err := tx.QueryRow(ctx, `SELECT status, currency, user_id, group_id FROM registrations WHERE id=$1 FOR UPDATE`, id).Scan(&status, &currency, &userID, &groupID); err != nil {
			return err
		}
		if status != "pending" {
			return httpx.Conflict("not_pending", "yalnızca ödeme bekleyen kayıtlar yeniden fiyatlanabilir")
		}
		var size int
		if groupID != nil {
			_ = tx.QueryRow(ctx, `SELECT size FROM registration_groups WHERE id=$1`, *groupID).Scan(&size)
		}
		var student, member bool
		if err := tx.QueryRow(ctx, `SELECT is_student, is_member FROM users WHERE id=$1`, userID).Scan(&student, &member); err != nil {
			return err
		}
		rate := pricing.Rate(1_000_000)
		if currency == "TRY" {
			var err error
			if rate, _, err = a.latestRate(ctx, tx); err != nil {
				return err
			}
		}
		q, err := pricing.Price(a.Calendar, pricing.QuoteInput{At: now, Student: student, Member: member, GroupSize: size, Currency: currency, Rate: rate})
		if err != nil {
			return err
		}
		br, _ := json.Marshal(q.Lines)
		_, err = tx.Exec(ctx, `UPDATE registrations SET tier=$2, fx_rate=$3::numeric, base_cents=$4, discount_cents=$5, total_cents=$6, breakdown=$7, created_at=$8 WHERE id=$1`,
			id, string(q.Tier), q.Rate, q.BaseCents, q.DiscountCents, q.TotalCents, br, now)
		if err != nil {
			return err
		}
		aid, role := actor(p)
		return audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "registration.requoted", Entity: "registration", EntityID: itoa(id),
			New: map[string]any{"fx_rate": q.Rate, "total_cents": q.TotalCents, "tier": q.Tier}})
	})
	if err != nil {
		return err
	}
	d, err := a.getRegistration(r.Context(), a.Pool, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, d)
	return nil
}

// ---- ödeme ----------------------------------------------------------------------

// PaymentEvent is what the (simulated) provider sends to the webhook.
type PaymentEvent struct {
	EventID     string `json:"event_id"`
	Type        string `json:"type"` // payment.succeeded | payment.failed
	ProviderRef string `json:"provider_ref"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

// pay creates a payment at the simulated provider and processes its result
// through the same idempotent path as the webhook. Idempotency-Key zorunludur:
// aynı anahtarla tekrar istek yeni ödeme oluşturmaz, ilk sonucu döner.
func (a *App) pay(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in struct {
		Outcome string `json:"outcome"` // simülatör: success | fail | async (sonuç webhook ile gelir)
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Outcome == "" {
		in.Outcome = "success"
	}
	if in.Outcome != "success" && in.Outcome != "fail" && in.Outcome != "async" {
		return httpx.Validation("outcome success, fail veya async olmalı")
	}
	p := principal(r)
	if err := a.ownRegistration(r.Context(), p, id); err != nil {
		return err
	}
	now := a.Clock.Now()
	var ev PaymentEvent
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var status, currency string
		var total int64
		var created time.Time
		if err := tx.QueryRow(ctx, `SELECT status, currency, total_cents, created_at FROM registrations WHERE id=$1 FOR UPDATE`, id).Scan(&status, &currency, &total, &created); err != nil {
			return err
		}
		if status != "pending" {
			return httpx.Conflict("not_pending", "bu kayıt için ödeme beklenmiyor (durum: "+status+")")
		}
		if currency == "TRY" && pricing.QuoteExpired(created, now) {
			return httpx.Conflict("quote_expired", "dondurulmuş kurun süresi doldu; yeniden fiyatlandırın")
		}
		if total == 0 {
			return httpx.Conflict("nothing_to_pay", "ödenecek tutar yok")
		}
		ref := "pay_" + randHex(12)
		if _, err := tx.Exec(ctx, `INSERT INTO payments (registration_id, provider_ref, amount_cents, currency) VALUES ($1,$2,$3,$4)`, id, ref, total, currency); err != nil {
			return err
		}
		typ := "payment.succeeded"
		if in.Outcome == "fail" {
			typ = "payment.failed"
		}
		ev = PaymentEvent{EventID: "evt_" + ref, Type: typ, ProviderRef: ref, AmountCents: total, Currency: currency}
		aid, role := actor(p)
		return audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "payment.initiated", Entity: "registration", EntityID: itoa(id),
			New: map[string]any{"provider_ref": ref, "amount_cents": total, "currency": currency}})
	})
	if err != nil {
		return err
	}
	outcome, status := "awaiting_webhook", "pending"
	if in.Outcome != "async" {
		// sağlayıcı simülatörü anında yanıt verir; olay webhook ile aynı idempotent yoldan işlenir
		if outcome, err = a.ProcessPaymentEvent(r.Context(), ev); err != nil {
			return err
		}
		status = strings.TrimPrefix(ev.Type, "payment.")
	}
	d, err := a.getRegistration(r.Context(), a.Pool, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"provider_ref": ev.ProviderRef, "amount_cents": ev.AmountCents, "currency": ev.Currency,
		"payment_status": status, "outcome": outcome, "registration": d})
	return nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ProcessPaymentEvent applies a provider event exactly once.
//
//	1. payment_events(event_id) satırı ON CONFLICT DO NOTHING ile eklenir;
//	   eklenemezse olay daha önce işlenmiştir → "duplicate".
//	2. Ödeme satırı FOR UPDATE kilitlenir; zaten sonuçlanmışsa olay yok sayılır
//	   (ör. başarılıdan sonra gelen gecikmiş "failed").
//	3. Başarılıysa kayıt "paid" olur ve aynı transaction'da boşluksuz fatura
//	   numarası alınır (sayaç satırı kilitlenir; geri alınırsa numara da geri alınır).
func (a *App) ProcessPaymentEvent(ctx context.Context, ev PaymentEvent) (string, error) {
	if ev.EventID == "" || ev.ProviderRef == "" || (ev.Type != "payment.succeeded" && ev.Type != "payment.failed") {
		return "", httpx.Validation("geçersiz ödeme olayı")
	}
	payload, _ := json.Marshal(ev)
	var outcome string
	err := db.InTx(ctx, a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO payment_events (event_id, provider_ref, type, payload, outcome) VALUES ($1,$2,$3,$4,'pending')
			ON CONFLICT (event_id) DO NOTHING`, ev.EventID, ev.ProviderRef, ev.Type, payload)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			outcome = "duplicate"
			return nil
		}
		var payID, regID, amount int64
		var pstatus, currency string
		err = tx.QueryRow(ctx, `SELECT id, registration_id, status, amount_cents, currency FROM payments WHERE provider_ref=$1 FOR UPDATE`, ev.ProviderRef).
			Scan(&payID, &regID, &pstatus, &amount, &currency)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = "unknown_payment"
		} else if err != nil {
			return err
		} else if pstatus != "pending" {
			outcome = "already_" + pstatus
		} else if ev.AmountCents != amount || ev.Currency != currency {
			outcome = "amount_mismatch"
			if _, err := tx.Exec(ctx, `UPDATE payments SET status='failed', updated_at=now() WHERE id=$1`, payID); err != nil {
				return err
			}
		} else if ev.Type == "payment.failed" {
			outcome = "failed"
			if _, err := tx.Exec(ctx, `UPDATE payments SET status='failed', updated_at=now() WHERE id=$1`, payID); err != nil {
				return err
			}
		} else {
			var rstatus string
			if err := tx.QueryRow(ctx, `SELECT status FROM registrations WHERE id=$1 FOR UPDATE`, regID).Scan(&rstatus); err != nil {
				return err
			}
			// kayıt satırı kilitli olduğundan bu kontrol ile güncelleme arasında yarış yoktur;
			// payments_one_success kısmi tekil dizini ayrıca güvence verir.
			var already bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payments WHERE registration_id=$1 AND status='succeeded')`, regID).Scan(&already); err != nil {
				return err
			}
			if already {
				outcome = "double_charge_blocked"
				if _, err := tx.Exec(ctx, `UPDATE payments SET status='failed', updated_at=now() WHERE id=$1`, payID); err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `UPDATE payment_events SET outcome=$2 WHERE event_id=$1`, ev.EventID, outcome)
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payments SET status='succeeded', updated_at=now() WHERE id=$1`, payID); err != nil {
				return err
			}
			if rstatus != "pending" {
				outcome = "paid_but_registration_" + rstatus // iade gerekir; raporlarda görünür
			} else {
				now := a.Clock.Now()
				if _, err := tx.Exec(ctx, `UPDATE registrations SET status='paid', paid_at=$2 WHERE id=$1`, regID, now); err != nil {
					return err
				}
				number, err := issueInvoiceTx(ctx, tx, regID, amount, currency, now)
				if err != nil {
					return err
				}
				outcome = "paid"
				if err := audit.Log(ctx, tx, audit.Entry{ActorRole: "system", Action: "registration.paid", Entity: "registration", EntityID: itoa(regID),
					Old: map[string]any{"status": "pending"}, New: map[string]any{"status": "paid", "invoice": number, "provider_ref": ev.ProviderRef}}); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(ctx, `UPDATE payment_events SET outcome=$2 WHERE event_id=$1`, ev.EventID, outcome)
		return err
	})
	if err != nil {
		return "", err
	}
	a.Metrics.PaymentEvents.Inc(outcome)
	return outcome, nil
}

// issueInvoiceTx allocates the next gap-free invoice number for the year.
func issueInvoiceTx(ctx context.Context, tx pgx.Tx, regID, amount int64, currency string, now time.Time) (string, error) {
	year := now.In(istanbul()).Year()
	var seq int64
	if err := tx.QueryRow(ctx, `INSERT INTO invoice_counters (year, last_value) VALUES ($1, 1)
		ON CONFLICT (year) DO UPDATE SET last_value = invoice_counters.last_value + 1 RETURNING last_value`, year).Scan(&seq); err != nil {
		return "", err
	}
	number := fmt.Sprintf("KH-%d-%06d", year, seq)
	_, err := tx.Exec(ctx, `INSERT INTO invoices (year, seq, number, registration_id, amount_cents, currency, issued_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		year, seq, number, regID, amount, currency, now)
	return number, err
}

// paymentWebhook receives provider callbacks signed with HMAC-SHA256.
func (a *App) paymentWebhook(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return httpx.Validation("gövde okunamadı")
	}
	mac := hmac.New(sha256.New, []byte(a.Cfg.WebhookSecret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Signature"))) {
		return httpx.E(http.StatusUnauthorized, "invalid_signature", "imza doğrulanamadı")
	}
	var ev PaymentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return httpx.Validation("geçersiz JSON")
	}
	outcome, err := a.ProcessPaymentEvent(r.Context(), ev)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"event_id": ev.EventID, "outcome": outcome})
	return nil
}

// SignWebhook is used by the simulator and tests.
func SignWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ---- iptal ve iade ----------------------------------------------------------------

func (a *App) cancelRegistration(w http.ResponseWriter, r *http.Request) error {
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
	if err := a.ownRegistration(r.Context(), p, id); err != nil {
		return err
	}
	now := a.Clock.Now()
	var rule pricing.RefundRule
	var refund int64
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var status, currency string
		var total int64
		if err := tx.QueryRow(ctx, `SELECT status, currency, total_cents FROM registrations WHERE id=$1 FOR UPDATE`, id).Scan(&status, &currency, &total); err != nil {
			return err
		}
		switch status {
		case "pending":
			_, err := tx.Exec(ctx, `UPDATE registrations SET status='cancelled', cancelled_at=$2 WHERE id=$1`, id, now)
			rule = pricing.RefundRule{Code: "unpaid", Percent: 0, Label: "Ödeme yapılmamıştı"}
			if err != nil {
				return err
			}
		case "paid":
			rule = a.Calendar.RefundFor(now)
			refund = pricing.RefundAmount(total, rule) // ödenen para biriminde, dondurulmuş kurla
			if _, err := tx.Exec(ctx, `UPDATE registrations SET status='refunded', cancelled_at=$2 WHERE id=$1`, id, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO refunds (registration_id, percent, amount_cents, currency, rule, created_by) VALUES ($1,$2,$3,$4,$5,$6)`,
				id, rule.Percent, refund, currency, rule.Code, p.UserID); err != nil {
				return err
			}
		default:
			return httpx.Conflict("not_cancellable", "kayıt zaten iptal edilmiş")
		}
		aid, role := actor(p)
		return audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "registration.cancelled", Entity: "registration", EntityID: itoa(id),
			Old: map[string]any{"status": status}, New: map[string]any{"refund_cents": refund, "rule": rule.Code, "percent": rule.Percent, "reason": in.Reason}})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"registration_id": id, "refund_cents": refund, "rule": rule})
	return nil
}

// refundQuote previews the refund rule for a date without changing anything.
func (a *App) refundQuote(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	p := principal(r)
	if err := a.ownRegistration(r.Context(), p, id); err != nil {
		return err
	}
	at := a.Clock.Now()
	if s := r.URL.Query().Get("at"); s != "" {
		if at, err = time.Parse(time.RFC3339, s); err != nil {
			return httpx.Validation("at RFC3339 biçiminde olmalı")
		}
	}
	d, err := a.getRegistration(r.Context(), a.Pool, id)
	if err != nil {
		return err
	}
	rule := a.Calendar.RefundFor(at)
	amount := int64(0)
	if d.Status == "paid" {
		amount = pricing.RefundAmount(d.TotalCents, rule)
	}
	httpx.WriteJSON(w, 200, map[string]any{"registration_id": id, "at": at, "rule": rule, "refund_cents": amount, "currency": d.Currency,
		"paid_cents": d.TotalCents, "conference_start": a.Calendar.ConferenceStart})
	return nil
}

// recordFX stores a new EUR→TRY observation (kur sağlayıcı / kayıt görevlisi).
// Mevcut kayıtların dondurulmuş kuru değişmez.
func (a *App) recordFX(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Rate string `json:"rate"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rate, err := pricing.ParseRate(in.Rate)
	if err != nil {
		return httpx.Validation(err.Error())
	}
	if _, err := a.Pool.Exec(r.Context(), `INSERT INTO fx_rates (rate, observed_at) VALUES ($1::numeric, $2)`, rate.String(), a.Clock.Now()); err != nil {
		return err
	}
	p := principal(r)
	aid, role := actor(p)
	_ = audit.Log(r.Context(), a.Pool, audit.Entry{ActorID: aid, ActorRole: role, Action: "fx.recorded", Entity: "fx_rate", EntityID: "EURTRY", New: map[string]any{"rate": rate.String()}})
	httpx.WriteJSON(w, 201, map[string]any{"rate": rate.String()})
	return nil
}

func (a *App) listInvoices(w http.ResponseWriter, r *http.Request) error {
	limit, after, err := httpx.PageParams(r)
	if err != nil {
		return err
	}
	rows, err := a.Pool.Query(r.Context(), `SELECT i.id, i.number, i.seq, i.year, i.registration_id, u.full_name, i.amount_cents, i.currency, i.issued_at
		FROM invoices i JOIN registrations rg ON rg.id=i.registration_id JOIN users u ON u.id=rg.user_id
		WHERE ($1=0 OR i.id < $1) ORDER BY i.id DESC LIMIT $2`, after, limit+1)
	if err != nil {
		return err
	}
	type inv struct {
		ID             int64     `json:"id"`
		Number         string    `json:"number"`
		Seq            int64     `json:"seq"`
		Year           int       `json:"year"`
		RegistrationID int64     `json:"registration_id"`
		Holder         string    `json:"holder"`
		AmountCents    int64     `json:"amount_cents"`
		Currency       string    `json:"currency"`
		IssuedAt       time.Time `json:"issued_at"`
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[inv])
	if err != nil {
		return err
	}
	page := httpx.Page[inv]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = httpx.Cursor(items[limit-1].ID)
	}
	var gaps int64
	_ = a.Pool.QueryRow(r.Context(), `SELECT coalesce(sum(cnt),0) FROM (SELECT max(seq) - count(*) AS cnt FROM invoices GROUP BY year) g`).Scan(&gaps)
	httpx.WriteJSON(w, 200, map[string]any{"items": page.Items, "next_cursor": page.NextCursor, "gap_count": gaps})
	return nil
}

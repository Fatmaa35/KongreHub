// Command simulator seeds the database and then generates live traffic.
//
//	simulator seed         → veritabanı boşsa SEED tohumuyla doldurur
//	simulator reset        → tüm veriyi silip aynı tohumla yeniden yükler (make reset-demo)
//	simulator run          → seed + canlı olaylar (compose servisi varsayılanı)
//	simulator hash [seed]  → veri kümesi özetini yazar (aynı tohum → aynı özet)
//	simulator healthcheck  → seed tamamlandı mı?
//
// Canlı modda simülatör gerçek bir istemci gibi API'yi kullanır: rastgele bir
// katılımcı kayıt olur, ödemeyi "async" başlatır ve ödeme sağlayıcısı rolünde
// imzalı webhook gönderir — bazen aynı olayı iki kez (idempotans), bazen önce
// başarısız ödeme. Ayrıca kur günceller ve zamanında yanıt veren hakemler
// değerlendirme gönderir; "geç" işaretli hakemler bilerek hiç yanıt vermez.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/fatmakaplan/kongrehub/internal/app"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/seed"
	"github.com/fatmakaplan/kongrehub/internal/storage"
)

const marker = "/tmp/kongrehub-seeded"

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "simulator")
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	seedVal, _ := strconv.ParseUint(env("SEED", "42"), 10, 64)
	switch cmd {
	case "healthcheck":
		if _, err := os.Stat(marker); err != nil {
			os.Exit(1)
		}
		return
	case "hash":
		if len(os.Args) > 2 {
			seedVal, _ = strconv.ParseUint(os.Args[2], 10, 64)
		}
		ref := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		fmt.Println(seed.Generate(seedVal, ref).Hash())
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, env("DATABASE_URL", ""), log)
	if err != nil {
		log.Error("veritabanı", "err", err.Error())
		os.Exit(1)
	}
	defer pool.Close()
	store, err := storage.New(env("STORAGE_DIR", "./data/uploads"))
	if err != nil {
		log.Error("depolama", "err", err.Error())
		os.Exit(1)
	}
	doSeed := func(force bool) error {
		if force {
			if err := seed.Reset(ctx, pool); err != nil {
				return err
			}
		} else if empty, err := seed.IsEmpty(ctx, pool); err != nil {
			return err
		} else if !empty {
			log.Info("veritabanı zaten dolu, seed atlandı")
			return nil
		}
		start := time.Now()
		ref := time.Now()
		d := seed.Generate(seedVal, ref)
		if err := seed.Load(ctx, pool, store, d, ref); err != nil {
			return err
		}
		log.Info("seed yüklendi", "seed", seedVal, "hash", d.Hash(), "submissions", len(d.Submissions), "users", len(d.Users), "ms", time.Since(start).Milliseconds())
		return nil
	}
	switch cmd {
	case "seed", "reset":
		if err := doSeed(cmd == "reset"); err != nil {
			log.Error("seed başarısız", "err", err.Error())
			os.Exit(1)
		}
	case "run":
		if err := doSeed(false); err != nil {
			log.Error("seed başarısız", "err", err.Error())
			os.Exit(1)
		}
		_ = os.WriteFile(marker, []byte("ok"), 0o644)
		if env("LIVE_ENABLED", "true") != "true" {
			<-ctx.Done()
			return
		}
		sim := &live{api: env("API_URL", "http://api:8080"), secret: env("PAYMENT_WEBHOOK_SECRET", ""), log: log,
			rng: rand.New(rand.NewPCG(seedVal, 7)), http: &http.Client{Timeout: 15 * time.Second}}
		every, _ := time.ParseDuration(env("LIVE_INTERVAL", "4s"))
		sim.loop(ctx, every)
	default:
		fmt.Fprintln(os.Stderr, "kullanım: simulator [run|seed|reset|hash|healthcheck]")
		os.Exit(2)
	}
}

type live struct {
	api, secret string
	log         *slog.Logger
	rng         *rand.Rand
	http        *http.Client
	tokens      map[string]string
}

func (l *live) loop(ctx context.Context, every time.Duration) {
	l.tokens = map[string]string{}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var err error
			switch n := l.rng.IntN(10); {
			case n < 5:
				err = l.registrationAndPayment(ctx)
			case n < 8:
				err = l.reviewerResponds(ctx)
			default:
				err = l.fxTick(ctx)
			}
			if err != nil {
				l.log.Warn("canlı olay başarısız", "err", err.Error())
			}
		}
	}
}

func (l *live) call(ctx context.Context, method, path, token string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, l.api+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", fmt.Sprintf("sim-%d-%d", time.Now().UnixNano(), l.rng.IntN(1e9)))
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

func (l *live) login(ctx context.Context, email string) (string, error) {
	if t, ok := l.tokens[email]; ok {
		return t, nil
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	code, err := l.call(ctx, "POST", "/api/auth/login", "", map[string]string{"email": email, "password": seed.DemoPassword}, &out)
	if err != nil || code != 200 {
		return "", fmt.Errorf("login %s: %d %v", email, code, err)
	}
	l.tokens[email] = out.AccessToken
	return out.AccessToken, nil
}

func (l *live) adminToken(ctx context.Context) (string, error) { return l.login(ctx, seed.DemoAdmin) }

// randomUser picks a user of the given role via the admin API.
func (l *live) randomUser(ctx context.Context, role string) (string, error) {
	tok, err := l.adminToken(ctx)
	if err != nil {
		return "", err
	}
	var page struct {
		Items []struct {
			Email string `json:"email"`
		} `json:"items"`
	}
	if _, err := l.call(ctx, "GET", "/api/admin/users?limit=200&role="+role, tok, nil, &page); err != nil {
		return "", err
	}
	if len(page.Items) == 0 {
		return "", fmt.Errorf("no %s", role)
	}
	return page.Items[l.rng.IntN(len(page.Items))].Email, nil
}

func (l *live) webhook(ctx context.Context, ev app.PaymentEvent) error {
	b, _ := json.Marshal(ev)
	req, _ := http.NewRequestWithContext(ctx, "POST", l.api+"/api/payments/webhook", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", app.SignWebhook(l.secret, b))
	resp, err := l.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// registrationAndPayment: kayıt → async ödeme → (bazen başarısız) → başarılı webhook (bazen iki kez).
func (l *live) registrationAndPayment(ctx context.Context) error {
	email, err := l.randomUser(ctx, "attendee")
	if err != nil {
		return err
	}
	tok, err := l.login(ctx, email)
	if err != nil {
		return err
	}
	cur := []string{"EUR", "TRY"}[l.rng.IntN(2)]
	var reg struct {
		ID int64 `json:"id"`
	}
	code, err := l.call(ctx, "POST", "/api/registrations", tok, map[string]string{"currency": cur}, &reg)
	if err != nil || code != 201 {
		return nil // zaten kayıtlı olabilir; sorun değil
	}
	payOnce := func() (app.PaymentEvent, error) {
		var p struct {
			ProviderRef string `json:"provider_ref"`
			Amount      int64  `json:"amount_cents"`
			Currency    string `json:"currency"`
		}
		code, err := l.call(ctx, "POST", fmt.Sprintf("/api/registrations/%d/payments", reg.ID), tok, map[string]string{"outcome": "async"}, &p)
		if err != nil || code != 201 {
			return app.PaymentEvent{}, fmt.Errorf("pay %d: %d %v", reg.ID, code, err)
		}
		return app.PaymentEvent{EventID: "evt_" + p.ProviderRef, ProviderRef: p.ProviderRef, AmountCents: p.Amount, Currency: p.Currency}, nil
	}
	ev, err := payOnce()
	if err != nil {
		return err
	}
	if l.rng.IntN(5) == 0 { // kart reddedildi, kullanıcı tekrar dener
		ev.Type = "payment.failed"
		if err := l.webhook(ctx, ev); err != nil {
			return err
		}
		if ev, err = payOnce(); err != nil {
			return err
		}
	}
	ev.Type = "payment.succeeded"
	if err := l.webhook(ctx, ev); err != nil {
		return err
	}
	if l.rng.IntN(3) == 0 { // sağlayıcı aynı olayı tekrar teslim eder
		_ = l.webhook(ctx, ev)
	}
	l.log.Info("canlı: kayıt ve ödeme", "registration_id", reg.ID, "currency", cur)
	return nil
}

// reviewerResponds: zamanında yanıt veren bir hakem bekleyen değerlendirmesini gönderir.
func (l *live) reviewerResponds(ctx context.Context) error {
	email, err := l.randomUser(ctx, "reviewer")
	if err != nil || email == seed.DemoReviewer {
		return err
	}
	tok, err := l.login(ctx, email)
	if err != nil {
		return err
	}
	var page struct {
		Items []struct {
			ID           int64 `json:"id"`
			SubmissionID int64 `json:"submission_id"`
			DaysLeft     int   `json:"days_left"`
		} `json:"items"`
	}
	if _, err := l.call(ctx, "GET", "/api/reviewer/assignments?status=assigned", tok, nil, &page); err != nil {
		return err
	}
	for _, a := range page.Items {
		// yalnızca seed bildirileri; geç kalan (süresi az kalan) atamalara dokunma — bilerek geç hakemler
		if a.SubmissionID > int64(seed.NumSubmissions) || a.DaysLeft < 4 {
			continue
		}
		q := 4 + l.rng.IntN(6)
		body := map[string]any{"originality": q, "significance": q, "methodology": max(1, q-1), "clarity": min(10, q+1),
			"recommendation":     []string{"accept", "minor_revision", "major_revision", "reject"}[l.rng.IntN(4)],
			"comments_to_author": "Simülatör: bildiri ilginç, deneyler genişletilebilir.", "comments_to_editor": ""}
		code, err := l.call(ctx, "POST", fmt.Sprintf("/api/reviewer/assignments/%d/review", a.ID), tok, body, nil)
		if err == nil && code == 201 {
			l.log.Info("canlı: değerlendirme gönderildi", "assignment_id", a.ID)
		}
		return err
	}
	return nil
}

func (l *live) fxTick(ctx context.Context) error {
	tok, err := l.login(ctx, seed.DemoRegistrar)
	if err != nil {
		return err
	}
	var cur struct {
		Rate string `json:"rate"`
	}
	if _, err := l.call(ctx, "GET", "/api/fx/current", tok, nil, &cur); err != nil {
		return err
	}
	r, _ := strconv.ParseFloat(cur.Rate, 64)
	r += (l.rng.Float64() - 0.5) * 0.08
	_, err = l.call(ctx, "POST", "/api/fx", tok, map[string]string{"rate": fmt.Sprintf("%.6f", r)}, nil)
	return err
}

package app_test

// Entegrasyon testleri gerçek PostgreSQL 16 gerektirir (TEST_DATABASE_URL).
// CI'da postgres servis konteyneri ile çalışır; değişken yoksa atlanır.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fatmakaplan/kongrehub/internal/app"
	"github.com/fatmakaplan/kongrehub/internal/config"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/e2e"
	"github.com/fatmakaplan/kongrehub/internal/seed"
	"github.com/fatmakaplan/kongrehub/internal/storage"
)

var (
	pool   *pgxpool.Pool
	srv    *httptest.Server
	theApp *app.App
)

const secret = "whsec_test"

func TestMain(m *testing.M) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		fmt.Println("TEST_DATABASE_URL yok: entegrasyon testleri atlandı")
		os.Exit(0)
	}
	ctx := context.Background()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sqlDB, err := db.SQLDB(url)
	if err != nil {
		panic(err)
	}
	if _, err := sqlDB.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		panic(err)
	}
	sqlDB.Close()
	if err := db.Migrate(ctx, url); err != nil {
		panic(err)
	}
	pool, err = db.Open(ctx, url, log)
	if err != nil {
		panic(err)
	}
	dir, _ := os.MkdirTemp("", "kh-uploads")
	store, _ := storage.New(dir)
	ref := time.Now()
	if err := seed.Load(ctx, pool, store, seed.Generate(42, ref), ref); err != nil {
		panic(err)
	}
	cfg := config.Config{DatabaseURL: url, JWTSecret: "test-secret-test-secret-test-secret-xx", AccessTTL: time.Hour, RefreshTTL: time.Hour,
		CertSigningSeed: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", WebhookSecret: secret,
		PublicURL: "http://localhost:3000", StorageDir: dir, DemoMode: true, JobInterval: time.Hour, RequestTimeout: time.Minute, MaxUploadBytes: 10 << 20}
	theApp, err = app.New(cfg, pool, log)
	if err != nil {
		panic(err)
	}
	srv = httptest.NewServer(theApp.Router())
	code := m.Run()
	srv.Close()
	pool.Close()
	os.Exit(code)
}

// TestVerifySuite runs the same end-to-end checks as `make verify`.
func TestVerifySuite(t *testing.T) {
	e2e.WebhookSecret = secret
	e2e.Signer = app.SignWebhook
	r := e2e.NewRunner(srv.URL, "")
	for _, c := range r.Checks() {
		c := c
		t.Run(c.ID, func(t *testing.T) {
			detail, err := c.Fn()
			if err != nil {
				t.Fatal(err)
			}
			t.Log(detail)
		})
	}
}

func login(t *testing.T, email string) string {
	t.Helper()
	tok, err := e2e.NewClient(srv.URL).Login(email, seed.DemoPassword)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// Yarış: kalan kapasitesi 1 olan hakeme 12 farklı bildiri aynı anda atanmaya çalışılır → tam 1 başarı.
func TestRaceManualAssignmentRespectsCapacity(t *testing.T) {
	ctx := context.Background()
	var reviewerID int64
	if err := pool.QueryRow(ctx, `SELECT user_id FROM reviewer_profiles WHERE is_active ORDER BY current_load, user_id LIMIT 1`).Scan(&reviewerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reviewer_profiles SET max_load = current_load + 1 WHERE user_id=$1`, reviewerID); err != nil {
		t.Fatal(err)
	}
	// hakemle çatışması olmayan, 'submitted' durumdaki bildiriler
	rows, err := pool.Query(ctx, `SELECT s.id FROM submissions s WHERE s.status='submitted'
		AND NOT EXISTS (SELECT 1 FROM submission_authors sa JOIN users u ON u.id=$1 WHERE sa.submission_id=s.id AND (sa.institution_id=u.institution_id OR sa.user_id=u.id))
		AND NOT EXISTS (SELECT 1 FROM submission_authors sa JOIN coauthorships c ON (c.user_a=sa.user_id AND c.user_b=$1) OR (c.user_b=sa.user_id AND c.user_a=$1) WHERE sa.submission_id=s.id)
		AND NOT EXISTS (SELECT 1 FROM submission_authors sa JOIN advisor_relations ar ON (ar.advisor_id=$1 AND ar.student_id=sa.user_id) OR (ar.student_id=$1 AND ar.advisor_id=sa.user_id) WHERE sa.submission_id=s.id)
		ORDER BY s.id LIMIT 12`, reviewerID)
	if err != nil {
		t.Fatal(err)
	}
	var subs []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		subs = append(subs, id)
	}
	rows.Close()
	if len(subs) < 5 {
		t.Skip("yeterli aday bildiri yok")
	}
	tok := login(t, seed.DemoEditor)
	c := e2e.NewClient(srv.URL)
	var wg sync.WaitGroup
	codes := make([]int, len(subs))
	for i, sid := range subs {
		wg.Add(1)
		go func(i int, sid int64) {
			defer wg.Done()
			res, err := c.Do("POST", fmt.Sprintf("/api/submissions/%d/assignments", sid), tok, map[string]int64{"reviewer_id": reviewerID})
			if err == nil {
				codes[i] = res.Status
			}
		}(i, sid)
	}
	wg.Wait()
	ok := 0
	for _, code := range codes {
		if code == 201 {
			ok++
		} else if code != 409 {
			t.Fatalf("unexpected status: %v", codes)
		}
	}
	if ok != 1 {
		t.Fatalf("expected exactly 1 successful assignment, got %d (%v)", ok, codes)
	}
	var load, max int
	_ = pool.QueryRow(ctx, `SELECT current_load, max_load FROM reviewer_profiles WHERE user_id=$1`, reviewerID).Scan(&load, &max)
	if load != max {
		t.Fatalf("load %d max %d", load, max)
	}
}

// Yarış: 25 farklı kaydın ödemesi aynı anda tamamlanır → fatura sıra numaraları boşluksuz ve tekil.
func TestRaceInvoicesGapFree(t *testing.T) {
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT id, total_cents, currency FROM registrations WHERE status='pending' AND total_cents > 0 ORDER BY id LIMIT 25`)
	if err != nil {
		t.Fatal(err)
	}
	type reg struct {
		id, total int64
		cur       string
	}
	var regs []reg
	for rows.Next() {
		var r reg
		_ = rows.Scan(&r.id, &r.total, &r.cur)
		regs = append(regs, r)
	}
	rows.Close()
	if len(regs) < 10 {
		t.Skipf("only %d pending registrations", len(regs))
	}
	var before int64
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM invoices`).Scan(&before)
	var wg sync.WaitGroup
	for i, r := range regs {
		ref := fmt.Sprintf("pay_race_%d_%d", r.id, i)
		if _, err := pool.Exec(ctx, `INSERT INTO payments (registration_id, provider_ref, amount_cents, currency) VALUES ($1,$2,$3,$4)`, r.id, ref, r.total, r.cur); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(r reg, ref string) {
			defer wg.Done()
			_, err := theApp.ProcessPaymentEvent(ctx, app.PaymentEvent{EventID: "evt_" + ref, Type: "payment.succeeded", ProviderRef: ref, AmountCents: r.total, Currency: r.cur})
			if err != nil {
				t.Error(err)
			}
		}(r, ref)
	}
	wg.Wait()
	var n, maxSeq, distinct int64
	_ = pool.QueryRow(ctx, `SELECT count(*), max(seq), count(DISTINCT seq) FROM invoices`).Scan(&n, &maxSeq, &distinct)
	if n != before+int64(len(regs)) || maxSeq != n || distinct != n {
		t.Fatalf("invoices=%d max_seq=%d distinct=%d (before %d, +%d)", n, maxSeq, distinct, before, len(regs))
	}
}

// Aynı webhook olayı 10 kez eşzamanlı gelir → tek "paid", geri kalanı "duplicate".
func TestRaceDuplicateWebhookProcessedOnce(t *testing.T) {
	ctx := context.Background()
	var regID, total int64
	var cur string
	if err := pool.QueryRow(ctx, `SELECT id, total_cents, currency FROM registrations WHERE status='pending' AND total_cents>0 ORDER BY id DESC LIMIT 1`).Scan(&regID, &total, &cur); err != nil {
		t.Skip("no pending registration")
	}
	ref := fmt.Sprintf("pay_dup_%d", regID)
	if _, err := pool.Exec(ctx, `INSERT INTO payments (registration_id, provider_ref, amount_cents, currency) VALUES ($1,$2,$3,$4)`, regID, ref, total, cur); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	outcomes := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := theApp.ProcessPaymentEvent(ctx, app.PaymentEvent{EventID: "evt_" + ref, Type: "payment.succeeded", ProviderRef: ref, AmountCents: total, Currency: cur})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			outcomes[o]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if outcomes["paid"] != 1 || outcomes["duplicate"] != 9 {
		t.Fatalf("%v", outcomes)
	}
}

// Aynı Idempotency-Key ile 10 eşzamanlı istek → tek işlem, tek ödeme satırı.
func TestRaceSameIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	c := e2e.NewClient(srv.URL)
	admin := login(t, seed.DemoAdmin)
	email := fmt.Sprintf("idem.%d@test.example", time.Now().UnixNano())
	if _, err := c.Expect(201, "POST", "/api/admin/users", admin, map[string]any{"email": email, "password": "Demo123!", "full_name": "Idem Test", "role": "attendee"}); err != nil {
		t.Fatal(err)
	}
	tok := login(t, email)
	reg, err := c.Expect(201, "POST", "/api/registrations", tok, map[string]string{"currency": "EUR"}, "Idempotency-Key", "reg-"+email)
	if err != nil {
		t.Fatal(err)
	}
	id := int64(reg.M()["id"].(float64))
	key := "same-key-" + email
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := c.Do("POST", fmt.Sprintf("/api/registrations/%d/payments", id), tok, map[string]string{"outcome": "success"}, "Idempotency-Key", key)
			if err == nil {
				codes[i] = res.Status
			}
		}(i)
	}
	wg.Wait()
	sort.Ints(codes)
	var payments int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE registration_id=$1`, id).Scan(&payments)
	if payments != 1 {
		t.Fatalf("expected 1 payment row, got %d (codes %v)", payments, codes)
	}
	for _, code := range codes {
		if code != 201 && code != 409 {
			t.Fatalf("codes %v", codes)
		}
	}
	// bitmiş istek artık aynen tekrar oynatılır
	res, _ := c.Do("POST", fmt.Sprintf("/api/registrations/%d/payments", id), tok, map[string]string{"outcome": "success"}, "Idempotency-Key", key)
	if res.Status != 201 || res.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %v", res.Status, res.Header)
	}
}

// İki hatırlatma job'ı aynı anda çalışır → hiçbir hatırlatma iki kez gönderilmez.
func TestReminderJobConcurrentRunsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE review_assignments SET assigned_at = assigned_at - interval '8 days', due_at = due_at - interval '8 days'
		WHERE id IN (SELECT id FROM review_assignments WHERE status='assigned' ORDER BY id LIMIT 20)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]*app.ReminderResult, 3)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := theApp.RunReminderJob(ctx)
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}(i)
	}
	wg.Wait()
	var dup int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT assignment_id, kind FROM reminders GROUP BY 1,2 HAVING count(*)>1) x`).Scan(&dup)
	if dup != 0 {
		t.Fatalf("%d duplicated reminders", dup)
	}
	again, err := theApp.RunReminderJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Reminders) != 0 || again.Expired != 0 {
		t.Fatalf("rerun changed state: %+v", again)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET action='tampered' WHERE id = (SELECT min(id) FROM audit_log)`); err == nil {
		t.Fatal("UPDATE on audit_log must fail")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Fatal("DELETE on audit_log must fail")
	}
}

func TestProgramGuardRejectsUnpaidPaper(t *testing.T) {
	ctx := context.Background()
	var subID, sessionID int64
	err := pool.QueryRow(ctx, `SELECT s.id FROM submissions s WHERE s.status='accepted' AND NOT EXISTS (
		SELECT 1 FROM submission_authors sa JOIN registrations r ON r.user_id=sa.user_id AND r.status='paid' WHERE sa.submission_id=s.id) LIMIT 1`).Scan(&subID)
	if err != nil {
		t.Skip("no unpaid accepted paper")
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM program_sessions LIMIT 1`).Scan(&sessionID); err != nil {
		t.Skip("no session")
	}
	_, err = pool.Exec(ctx, `INSERT INTO session_papers (session_id, submission_id, position, starts_at, ends_at) VALUES ($1,$2,99,now(),now()+interval '20 minutes')`, sessionID, subID)
	if err == nil || !db.IsCheckViolation(err, "") {
		t.Fatalf("trigger should reject unpaid paper, got %v", err)
	}
}

func TestMoneyColumnsAreNotFloat(t *testing.T) {
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_schema='public'
		AND data_type IN ('real','double precision')`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d float columns found", n)
	}
}

func TestHealthReportsDatabaseDown(t *testing.T) {
	// ayrı, kapalı bir havuzla /health 503 dönmeli
	cfg, _ := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	cfg.ConnConfig.Port = 1 // erişilemez port
	bad, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	a2, err := app.New(theApp.Cfg, bad, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(a2.Router())
	defer s.Close()
	resp, err := http.Get(s.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

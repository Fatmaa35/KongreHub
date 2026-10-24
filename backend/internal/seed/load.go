package seed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fatmakaplan/kongrehub/internal/app"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/domain/pricing"
	"github.com/fatmakaplan/kongrehub/internal/domain/review"
	"github.com/fatmakaplan/kongrehub/internal/domain/schedule"
	"github.com/fatmakaplan/kongrehub/internal/storage"
)

// Tables cleared by Reset (goose_db_version hariç).
var tables = []string{"personal_agenda", "session_papers", "program_sessions", "certificates", "refunds", "invoices", "invoice_counters",
	"payment_events", "payments", "registrations", "registration_groups", "fx_rates", "reminders", "decisions", "reviews", "review_assignments",
	"submission_files", "submission_authors", "submissions", "advisor_relations", "coauthorships", "reviewer_profiles", "refresh_tokens",
	"idempotency_keys", "job_runs", "audit_log", "app_settings", "time_slots", "rooms", "users", "institutions"}

// IsEmpty reports whether the database has no users yet.
func IsEmpty(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n == 0, err
}

// Reset wipes all data (denetim kaydı dahil; yalnızca demo sıfırlama bayrağıyla).
func Reset(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL kongrehub.allow_audit_reset = 'on'`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `TRUNCATE `+strings.Join(tables, ", ")+` RESTART IDENTITY CASCADE`)
		return err
	})
}

// asciiFold keeps seed manuscripts on the fast core font (seed metinleri İngilizce).
var asciiFold = strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "ö", "o", "ş", "s", "ü", "u", "İ", "I", "Ç", "C", "Ğ", "G", "Ö", "O", "Ş", "S", "Ü", "U")

// paperPDF renders an anonymous placeholder manuscript.
func paperPDF(s Submission) ([]byte, error) {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetCreationDate(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	p.SetTitle("Anonymous submission", false)
	p.AddPage()
	p.SetFont("Helvetica", "B", 15)
	p.MultiCell(0, 8, asciiFold.Replace(s.Title), "", "L", false)
	p.Ln(4)
	p.SetFont("Helvetica", "I", 10)
	p.MultiCell(0, 6, "Anonymous submission for double-blind review", "", "L", false)
	p.Ln(4)
	p.SetFont("Helvetica", "", 11)
	p.MultiCell(0, 6, asciiFold.Replace("Abstract. "+s.Abstract), "", "J", false)
	p.Ln(2)
	p.MultiCell(0, 6, asciiFold.Replace("Keywords: "+strings.Join(s.Keywords, ", ")), "", "L", false)
	var b bytes.Buffer
	err := p.Output(&b)
	return b.Bytes(), err
}

// Load inserts the dataset and generates the initial program.
func Load(ctx context.Context, pool *pgxpool.Pool, store *storage.Store, d *Dataset, ref time.Time) error {
	ref = ref.UTC().Truncate(time.Hour)
	shared, err := auth.HashPassword(DemoPassword)
	if err != nil {
		return err
	}
	files := map[int64][3]any{} // id → key, sha, size
	for _, s := range d.Submissions {
		b, err := paperPDF(s)
		if err != nil {
			return err
		}
		key, sum, err := store.Put(b)
		if err != nil {
			return err
		}
		files[s.ID] = [3]any{key, sum, len(b)}
	}
	var editorID, registrarID int64
	for _, u := range d.Users {
		if u.Email == DemoEditor {
			editorID = u.ID
		}
		if u.Email == DemoRegistrar {
			registrarID = u.ID
		}
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		cp := func(table string, cols []string, rows [][]any) error {
			_, err := tx.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows))
			if err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
			return nil
		}
		var rows [][]any
		for _, i := range d.Institutions {
			rows = append(rows, []any{i.ID, i.Name, i.Country})
		}
		if err := cp("institutions", []string{"id", "name", "country"}, rows); err != nil {
			return err
		}
		rows = nil
		for _, u := range d.Users {
			rows = append(rows, []any{u.ID, u.Email, shared, u.FullName, u.Role, u.InstitutionID, u.Timezone, int16(u.AvailStart), int16(u.AvailEnd), u.IsMember, u.IsStudent, ref.Add(-90 * 24 * time.Hour)})
		}
		if err := cp("users", []string{"id", "email", "password_hash", "full_name", "role", "institution_id", "timezone", "avail_start_hour", "avail_end_hour", "is_member", "is_student", "created_at"}, rows); err != nil {
			return err
		}
		load := map[int64]int{}
		for _, a := range d.Assignments {
			if a.Status == "assigned" || a.Status == "submitted" {
				load[a.ReviewerID]++
			}
		}
		rows = nil
		for _, r := range d.Reviewers {
			ml := r.MaxLoad
			if load[r.UserID] > ml {
				ml = load[r.UserID]
			}
			rows = append(rows, []any{r.UserID, r.Keywords, ml, load[r.UserID]})
		}
		if err := cp("reviewer_profiles", []string{"user_id", "keywords", "max_load", "current_load"}, rows); err != nil {
			return err
		}
		rows = nil
		for _, c := range d.Coauthorships {
			rows = append(rows, []any{c.A, c.B, c.Title, c.Year})
		}
		if err := cp("coauthorships", []string{"user_a", "user_b", "paper_title", "year"}, rows); err != nil {
			return err
		}
		rows = nil
		for _, a := range d.Advisors {
			rows = append(rows, []any{a[0], a[1]})
		}
		if err := cp("advisor_relations", []string{"advisor_id", "student_id"}, rows); err != nil {
			return err
		}
		rows = nil
		var authors, fileRows [][]any
		for _, s := range d.Submissions {
			var fs any
			if s.FinalScore != nil {
				fs = fmt.Sprintf("%.2f", *s.FinalScore)
			}
			rows = append(rows, []any{s.ID, s.Title, s.Abstract, s.Keywords, s.Status, s.Round, 1, s.SubmitterID, s.Audience, s.Duration, s.Disagree, fs, s.CreatedAt, s.CreatedAt})
			for k, a := range s.Authors {
				authors = append(authors, []any{s.ID, k + 1, a.UserID, a.FullName, a.Email, a.InstitutionID, a.IsPresenter})
			}
			f := files[s.ID]
			fileRows = append(fileRows, []any{s.ID, 1, f[0], f[1], int64(f[2].(int)), fmt.Sprintf("manuscript-%d.pdf", s.ID), s.CreatedAt})
		}
		if err := cp("submissions", []string{"id", "title", "abstract", "keywords", "status", "round", "current_version", "submitter_id", "expected_audience", "duration_minutes", "disagreement_flag", "final_score", "created_at", "updated_at"}, rows); err != nil {
			return err
		}
		if err := cp("submission_authors", []string{"submission_id", "position", "user_id", "full_name", "email", "institution_id", "is_presenter"}, authors); err != nil {
			return err
		}
		if err := cp("submission_files", []string{"submission_id", "version", "storage_key", "sha256", "size_bytes", "original_name", "created_at"}, fileRows); err != nil {
			return err
		}
		rows = nil
		reviewed := map[int64]Review{}
		for _, r := range d.Reviews {
			reviewed[r.AssignmentID] = r
		}
		for _, a := range d.Assignments {
			var done any
			if r, ok := reviewed[a.ID]; ok {
				done = r.SubmittedAt
			}
			rows = append(rows, []any{a.ID, a.SubmissionID, a.ReviewerID, a.Round, a.Status, a.Reason, fmt.Sprintf("%.4f", a.Score), a.AssignedAt, review.DueAt(a.AssignedAt), done})
		}
		if err := cp("review_assignments", []string{"id", "submission_id", "reviewer_id", "round", "status", "reason", "match_score", "assigned_at", "due_at", "completed_at"}, rows); err != nil {
			return err
		}
		rows = nil
		for _, r := range d.Reviews {
			s := r.Scores
			rows = append(rows, []any{r.AssignmentID, int16(s.Originality), int16(s.Significance), int16(s.Methodology), int16(s.Clarity), fmt.Sprintf("%.2f", s.Weighted()), r.Recommendation, r.Comment, "", r.SubmittedAt})
		}
		if err := cp("reviews", []string{"assignment_id", "originality", "significance", "methodology", "clarity", "weighted_score", "recommendation", "comments_to_author", "comments_to_editor", "submitted_at"}, rows); err != nil {
			return err
		}
		rows = nil
		for _, dc := range d.Decisions {
			rows = append(rows, []any{dc.SubmissionID, dc.Round, dc.Decision, dc.Note, editorID, dc.At})
		}
		if err := cp("decisions", []string{"submission_id", "round", "decision", "note_to_author", "decided_by", "decided_at"}, rows); err != nil {
			return err
		}
		rows = nil
		for _, r := range d.Rooms {
			rows = append(rows, []any{r.ID, r.Name, r.Capacity})
		}
		if err := cp("rooms", []string{"id", "name", "capacity"}, rows); err != nil {
			return err
		}
		rows = nil
		for _, s := range d.Slots {
			rows = append(rows, []any{s.ID, s.Start, s.End})
		}
		if err := cp("time_slots", []string{"id", "starts_at", "ends_at"}, rows); err != nil {
			return err
		}
		for _, f := range d.FXRates {
			if _, err := tx.Exec(ctx, `INSERT INTO fx_rates (rate, observed_at) VALUES ($1::numeric,$2)`, f.Rate, f.At); err != nil {
				return err
			}
		}
		for _, g := range d.Groups {
			if _, err := tx.Exec(ctx, `INSERT INTO registration_groups (id, name, created_by, size) VALUES ($1,$2,$3,$4)`, g.ID, g.Name, registrarID, g.Size); err != nil {
				return err
			}
		}
		userMember := map[int64]bool{}
		for _, u := range d.Users {
			userMember[u.ID] = u.IsMember
		}
		for _, r := range d.Registrations {
			var gid any
			if r.GroupID != 0 {
				gid = r.GroupID
			}
			br, _ := json.Marshal(r.Quote.Lines)
			var cancelled any
			if r.Status == "refunded" {
				cancelled = r.PaidAt.Add(24 * time.Hour)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO registrations (id, user_id, group_id, tier, category, is_member, currency, fx_rate, base_cents, discount_cents, total_cents, breakdown, status, created_at, paid_at, cancelled_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8::numeric,$9,$10,$11,$12,$13,$14,$15,$16)`,
				r.ID, r.UserID, gid, string(r.Quote.Tier), r.Quote.Category, userMember[r.UserID], r.Currency, r.Quote.Rate, r.Quote.BaseCents, r.Quote.DiscountCents, r.Quote.TotalCents,
				br, r.Status, r.CreatedAt, r.PaidAt, cancelled); err != nil {
				return err
			}
		}
		// ödemeler, olaylar ve boşluksuz faturalar (ödeme zamanına göre sıralı)
		paid := []Registration{}
		for _, r := range d.Registrations {
			if r.PaidAt != nil {
				paid = append(paid, r)
			}
		}
		sortByPaid(paid)
		year := 0
		var seq int64
		for _, r := range paid {
			ref := fmt.Sprintf("pay_seed_%06d", r.ID)
			if _, err := tx.Exec(ctx, `INSERT INTO payments (registration_id, provider_ref, amount_cents, currency, status, created_at, updated_at) VALUES ($1,$2,$3,$4,'succeeded',$5,$5)`,
				r.ID, ref, r.Quote.TotalCents, r.Currency, *r.PaidAt); err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{"event_id": "evt_" + ref, "type": "payment.succeeded", "provider_ref": ref, "amount_cents": r.Quote.TotalCents, "currency": r.Currency})
			if _, err := tx.Exec(ctx, `INSERT INTO payment_events (event_id, provider_ref, type, payload, received_at, outcome) VALUES ($1,$2,'payment.succeeded',$3,$4,'paid')`,
				"evt_"+ref, ref, payload, *r.PaidAt); err != nil {
				return err
			}
			y := r.PaidAt.In(pricing.Istanbul).Year()
			if y != year {
				year, seq = y, 0
			}
			seq++
			if _, err := tx.Exec(ctx, `INSERT INTO invoices (year, seq, number, registration_id, amount_cents, currency, issued_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				y, seq, fmt.Sprintf("KH-%d-%06d", y, seq), r.ID, r.Quote.TotalCents, r.Currency, *r.PaidAt); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO invoice_counters (year, last_value) VALUES ($1,$2) ON CONFLICT (year) DO UPDATE SET last_value=excluded.last_value`, y, seq); err != nil {
				return err
			}
			if r.Refund != nil {
				amt := r.Quote.TotalCents * int64(r.Refund.Percent) / 100
				if _, err := tx.Exec(ctx, `INSERT INTO refunds (registration_id, percent, amount_cents, currency, rule, created_by, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
					r.ID, r.Refund.Percent, amt, r.Currency, r.Refund.Code, registrarID, r.PaidAt.Add(24*time.Hour)); err != nil {
					return err
				}
			}
		}
		// dizileri açık ID'lerden sonra devam ettir
		for _, t := range []string{"institutions", "users", "submissions", "review_assignments", "rooms", "time_slots", "registrations", "registration_groups"} {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s','id'), (SELECT coalesce(max(id),1) FROM %s))`, t, t)); err != nil {
				return err
			}
		}
		// ilk program: uygulamanın kullandığı aynı çizelgeleyici ve doğrulayıcı
		in, _, err := app.LoadScheduleInput(ctx, tx)
		if err != nil {
			return err
		}
		prog := schedule.Build(in)
		if v := schedule.Validate(in, prog); len(v) > 0 {
			return fmt.Errorf("seed programı doğrulanamadı: %s", v[0])
		}
		if err := app.PersistProgram(ctx, tx, prog); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_log (actor_role, action, entity, entity_id, new_value) VALUES ('system','seed.loaded','dataset',$1,$2)`,
			fmt.Sprint(d.Seed), fmt.Sprintf(`{"hash":%q,"submissions":%d,"reviewers":%d,"sessions":%d}`, d.Hash(), len(d.Submissions), len(d.Reviewers), len(prog.Sessions)))
		return err
	})
}

func sortByPaid(rs []Registration) {
	sort.SliceStable(rs, func(i, j int) bool {
		if !rs[i].PaidAt.Equal(*rs[j].PaidAt) {
			return rs[i].PaidAt.Before(*rs[j].PaidAt)
		}
		return rs[i].ID < rs[j].ID
	})
}

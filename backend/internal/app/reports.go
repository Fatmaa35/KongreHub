package app

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

func (a *App) registerReports(r chi.Router) {
	a.handle(r, "GET", "/api/reports/overview", a.reportOverview, roles(REditor, RAdmin))
	a.handle(r, "GET", "/api/reports/reviewers", a.reportReviewers, roles(REditor, RAdmin))
	a.handle(r, "GET", "/api/reports/finance", a.reportFinance, roles(RRegistrar, RAdmin))
	a.handle(r, "GET", "/api/reports/submissions.csv", a.reportSubmissionsCSV, roles(REditor, RAdmin))
	a.handle(r, "GET", "/api/live", a.live)
}

func (a *App) reportOverview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	out := map[string]any{}
	byStatus := map[string]int{}
	rows, err := a.Pool.Query(ctx, `SELECT status, count(*) FROM submissions GROUP BY status`)
	if err != nil {
		return err
	}
	total := 0
	for rows.Next() {
		var s string
		var c int
		if err := rows.Scan(&s, &c); err != nil {
			rows.Close()
			return err
		}
		byStatus[s] = c
		total += c
	}
	rows.Close()
	out["submissions_by_status"] = byStatus
	out["total_submissions"] = total
	decided := byStatus["accepted"] + byStatus["rejected"]
	if decided > 0 {
		out["acceptance_rate"] = float64(byStatus["accepted"]) / float64(decided)
	}
	var avgScore *float64
	var disagreements, pendingReviews, doneReviews, expired int
	if err := a.Pool.QueryRow(ctx, `SELECT avg(final_score)::float8, count(*) FILTER (WHERE disagreement_flag)::int FROM submissions`).Scan(&avgScore, &disagreements); err != nil {
		return err
	}
	if err := a.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='assigned')::int, count(*) FILTER (WHERE status='submitted')::int,
		count(*) FILTER (WHERE status='expired')::int FROM review_assignments`).Scan(&pendingReviews, &doneReviews, &expired); err != nil {
		return err
	}
	out["average_final_score"] = avgScore
	out["disagreements"] = disagreements
	out["reviews"] = map[string]int{"pending": pendingReviews, "submitted": doneReviews, "expired": expired}
	rows, err = a.Pool.Query(ctx, `SELECT k, count(*)::int, count(*) FILTER (WHERE status='accepted')::int, count(*) FILTER (WHERE status IN ('accepted','rejected'))::int
		FROM (SELECT unnest(keywords) k, status FROM submissions) x GROUP BY k ORDER BY count(*) DESC LIMIT 15`)
	if err != nil {
		return err
	}
	type kwRow struct {
		Keyword  string `json:"keyword"`
		Total    int    `json:"submissions"`
		Accepted int    `json:"accepted"`
		Decided  int    `json:"decided"`
	}
	kws, err := pgx.CollectRows(rows, pgx.RowToStructByPos[kwRow])
	if err != nil {
		return err
	}
	out["top_keywords"] = kws
	var sessions, scheduled int
	_ = a.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM program_sessions)::int, (SELECT count(*) FROM session_papers)::int`).Scan(&sessions, &scheduled)
	out["program"] = map[string]int{"sessions": sessions, "scheduled_papers": scheduled}
	httpx.WriteJSON(w, 200, out)
	return nil
}

// reportReviewers (bonus): hakem performansı ve gecikme raporu.
func (a *App) reportReviewers(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT u.id, u.full_name, rp.current_load, rp.max_load,
			count(ra.id) FILTER (WHERE ra.status='submitted')::int,
			count(ra.id) FILTER (WHERE ra.status='assigned')::int,
			count(ra.id) FILTER (WHERE ra.status='expired')::int,
			count(ra.id) FILTER (WHERE ra.status='assigned' AND ra.due_at < $1)::int,
			coalesce(avg(extract(epoch FROM ra.completed_at - ra.assigned_at)/86400) FILTER (WHERE ra.status='submitted'), 0)::float8,
			(SELECT count(*) FROM reminders rm JOIN review_assignments x ON x.id=rm.assignment_id WHERE x.reviewer_id=u.id AND rm.kind<>'expired')::int
		FROM reviewer_profiles rp JOIN users u ON u.id=rp.user_id LEFT JOIN review_assignments ra ON ra.reviewer_id=u.id
		GROUP BY u.id, u.full_name, rp.current_load, rp.max_load
		ORDER BY count(ra.id) FILTER (WHERE ra.status='expired') DESC, u.id`, a.Clock.Now())
	if err != nil {
		return err
	}
	type row struct {
		ID          int64   `json:"id"`
		Name        string  `json:"full_name"`
		Load        int     `json:"current_load"`
		MaxLoad     int     `json:"max_load"`
		Completed   int     `json:"completed"`
		Open        int     `json:"open"`
		Expired     int     `json:"expired"`
		Overdue     int     `json:"overdue"`
		AvgDays     float64 `json:"avg_days_to_review"`
		Reminders   int     `json:"reminders_received"`
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (a *App) reportFinance(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	type money struct {
		Currency string `json:"currency"`
		Tier     string `json:"tier"`
		Count    int    `json:"count"`
		Cents    int64  `json:"total_cents"`
	}
	rows, err := a.Pool.Query(ctx, `SELECT currency, tier, count(*)::int, sum(total_cents)::bigint FROM registrations WHERE status IN ('paid','refunded')
		GROUP BY currency, tier ORDER BY currency, tier`)
	if err != nil {
		return err
	}
	revenue, err := pgx.CollectRows(rows, pgx.RowToStructByPos[money])
	if err != nil {
		return err
	}
	rows, err = a.Pool.Query(ctx, `SELECT currency, rule, count(*)::int, sum(amount_cents)::bigint FROM refunds GROUP BY currency, rule ORDER BY currency, rule`)
	if err != nil {
		return err
	}
	refunds, err := pgx.CollectRows(rows, pgx.RowToStructByPos[money])
	if err != nil {
		return err
	}
	byStatus := map[string]int{}
	rows, err = a.Pool.Query(ctx, `SELECT status, count(*)::int FROM registrations GROUP BY status`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s string
		var c int
		if err := rows.Scan(&s, &c); err != nil {
			rows.Close()
			return err
		}
		byStatus[s] = c
	}
	rows.Close()
	var invoices, maxSeq int64
	_ = a.Pool.QueryRow(ctx, `SELECT count(*), coalesce(max(seq),0) FROM invoices WHERE year=$1`, a.Clock.Now().In(istanbul()).Year()).Scan(&invoices, &maxSeq)
	var events map[string]int
	rows, err = a.Pool.Query(ctx, `SELECT outcome, count(*)::int FROM payment_events GROUP BY outcome`)
	if err != nil {
		return err
	}
	events = map[string]int{}
	for rows.Next() {
		var o string
		var c int
		if err := rows.Scan(&o, &c); err != nil {
			rows.Close()
			return err
		}
		events[o] = c
	}
	rows.Close()
	httpx.WriteJSON(w, 200, map[string]any{
		"revenue": revenue, "refunds": refunds, "registrations_by_status": byStatus,
		"invoices": map[string]any{"count": invoices, "max_seq": maxSeq, "gap_free": invoices == maxSeq},
		"payment_events": events,
	})
	return nil
}

func (a *App) reportSubmissionsCSV(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT s.id, s.title, array_to_string(s.keywords,';'), s.status, s.round, coalesce(s.final_score::text,''), s.disagreement_flag, s.created_at
		FROM submissions s ORDER BY s.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="bildiriler.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "baslik", "anahtar_kelimeler", "durum", "tur", "puan", "uyusmazlik", "gonderim"})
	for rows.Next() {
		var id int64
		var title, kw, st, score string
		var round int
		var dis bool
		var at time.Time
		if err := rows.Scan(&id, &title, &kw, &st, &round, &score, &dis, &at); err != nil {
			return err
		}
		_ = cw.Write([]string{strconv.FormatInt(id, 10), title, kw, st, strconv.Itoa(round), score, strconv.FormatBool(dis), at.Format(time.RFC3339)})
	}
	cw.Flush()
	return rows.Err()
}

// live: arayüzde sistemin canlı olduğunu gösteren özet. Kimlik içermez; yalnızca
// sayılar ve olay türleri döner, her rol görebilir.
func (a *App) live(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var subs, regs, paid, assignments, reviews int
	if err := a.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM submissions)::int, (SELECT count(*) FROM registrations)::int,
		(SELECT count(*) FROM registrations WHERE status='paid')::int, (SELECT count(*) FROM review_assignments WHERE status='assigned')::int,
		(SELECT count(*) FROM reviews)::int`).Scan(&subs, &regs, &paid, &assignments, &reviews); err != nil {
		return err
	}
	rows, err := a.Pool.Query(ctx, `SELECT id, at, action, entity FROM audit_log WHERE action NOT LIKE 'auth.%' ORDER BY id DESC LIMIT 15`)
	if err != nil {
		return err
	}
	type ev struct {
		ID     int64     `json:"id"`
		At     time.Time `json:"at"`
		Action string    `json:"action"`
		Entity string    `json:"entity"`
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ev])
	if err != nil {
		return err
	}
	var perMin int
	_ = a.Pool.QueryRow(ctx, `SELECT count(*)::int FROM audit_log WHERE at > now() - interval '1 minute'`).Scan(&perMin)
	rate, at, _ := a.latestRate(ctx, a.Pool)
	httpx.WriteJSON(w, 200, map[string]any{
		"counts":            map[string]int{"submissions": subs, "registrations": regs, "paid_registrations": paid, "open_assignments": assignments, "reviews": reviews},
		"events_per_minute": perMin,
		"recent_events":     events,
		"fx":                map[string]any{"rate": strings.TrimSpace(rate.String()), "observed_at": at},
		"server_time":       a.Clock.Now().UTC(),
		"clock_offset_hours": a.Clock.Offset().Hours(),
	})
	return nil
}

package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/domain/review"
)

// ReminderResult summarises one job run.
type ReminderResult struct {
	Scanned       int            `json:"scanned"`
	Reminders     map[string]int `json:"reminders_sent"`
	Expired       int            `json:"expired"`
	Reassigned    int            `json:"reassigned"`
	NoReplacement int            `json:"no_replacement"`
	At            time.Time      `json:"at"`
}

// RunReminderJob sends day-7/day-12 reminders and expires assignments older
// than 14 days, assigning a replacement reviewer.
//
// İdempotans: her hatırlatma (atama, tür) birincil anahtarlı reminders
// tablosuna ON CONFLICT DO NOTHING ile yazılır — yalnızca satırı ekleyen
// çalıştırma "gönderir". Süre aşımı koşullu UPDATE (status='assigned') ile
// yapılır; aynı atama iki kez düşürülemez ve iki kez yerine hakem atanmaz.
// Satırlar FOR UPDATE SKIP LOCKED ile alındığından aynı anda çalışan iki
// job örneği birbirini beklemez ve aynı atamayı işlemez.
func (a *App) RunReminderJob(ctx context.Context) (*ReminderResult, error) {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	now := a.Clock.Now()
	res := &ReminderResult{Reminders: map[string]int{}, At: now}
	threshold := now.Add(-time.Duration(review.ReminderDays["day7"]) * 24 * time.Hour)
	var lastID int64
	for {
		var batch []struct {
			id, sub  int64
			round    int
			assigned time.Time
		}
		err := db.InTx(ctx, a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			batch = batch[:0]
			rows, err := tx.Query(ctx, `SELECT id, submission_id, round, assigned_at FROM review_assignments
				WHERE status='assigned' AND assigned_at <= $1 AND id > $2 ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED`, threshold, lastID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var x struct {
					id, sub  int64
					round    int
					assigned time.Time
				}
				if err := rows.Scan(&x.id, &x.sub, &x.round, &x.assigned); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, x)
			}
			rows.Close()
			for _, x := range batch {
				res.Scanned++
				for _, kind := range review.DueReminders(x.assigned, now) {
					tag, err := tx.Exec(ctx, `INSERT INTO reminders (assignment_id, kind, sent_at) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, x.id, kind, now)
					if err != nil {
						return err
					}
					if tag.RowsAffected() == 1 {
						res.Reminders[kind]++
						a.Metrics.Reminders.Inc(kind)
						if err := audit.Log(ctx, tx, audit.Entry{ActorRole: "system", Action: "review.reminder_sent", Entity: "assignment", EntityID: itoa(x.id),
							New: map[string]any{"kind": kind}}); err != nil {
							return err
						}
					}
				}
				if !review.Expired(x.assigned, now) {
					continue
				}
				ok, err := releaseTx(ctx, tx, x.id, "expired")
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				res.Expired++
				if _, err := tx.Exec(ctx, `INSERT INTO reminders (assignment_id, kind, sent_at) VALUES ($1,'expired',$2) ON CONFLICT DO NOTHING`, x.id, now); err != nil {
					return err
				}
				newID, _, err := pickAndAssign(ctx, tx, x.sub, x.round, "replacement", now, auth.Principal{})
				if err != nil {
					return err
				}
				if newID != 0 {
					res.Reassigned++
				} else {
					res.NoReplacement++
				}
				if err := audit.Log(ctx, tx, audit.Entry{ActorRole: "system", Action: "assignment.expired", Entity: "submission", EntityID: itoa(x.sub),
					New: map[string]any{"assignment_id": x.id, "replacement_assignment_id": newID, "after_days": review.ResponseDays}}); err != nil {
					return err
				}
				if _, err := evaluateRoundTx(ctx, tx, x.sub, now, auth.Principal{}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			a.Metrics.JobRuns.Inc("reminders", "error")
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		lastID = batch[len(batch)-1].id
	}
	stats, _ := json.Marshal(res)
	_, _ = a.Pool.Exec(ctx, `INSERT INTO job_runs (job, started_at, finished_at, stats) VALUES ('reminders', $1, now(), $2)`, now, stats)
	a.Metrics.JobRuns.Inc("reminders", "ok")
	return res, nil
}

// StartJobs runs background jobs until ctx is cancelled.
func (a *App) StartJobs(ctx context.Context) {
	go func() {
		t := time.NewTicker(a.Cfg.JobInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				jctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				res, err := a.RunReminderJob(jctx)
				cancel()
				if err != nil {
					a.Log.Error("hatırlatma job'ı başarısız", "err", err.Error())
				} else if res.Expired > 0 || len(res.Reminders) > 0 {
					a.Log.Info("hatırlatma job'ı", "reminders", res.Reminders, "expired", res.Expired, "reassigned", res.Reassigned)
				}
			}
		}
	}()
}

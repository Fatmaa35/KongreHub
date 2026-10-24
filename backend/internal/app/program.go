package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/fatmakaplan/kongrehub/internal/audit"
	"github.com/fatmakaplan/kongrehub/internal/db"
	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
	"github.com/fatmakaplan/kongrehub/internal/domain/schedule"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

const programLockKey = 7_100_002

func (a *App) registerProgram(r chi.Router) {
	a.handle(r, "GET", "/api/rooms", a.listRooms)
	a.handle(r, "POST", "/api/rooms", a.createRoom, roles(REditor, RAdmin))
	a.handle(r, "GET", "/api/slots", a.listSlots)
	a.handle(r, "GET", "/api/program", a.getProgram)
	a.handle(r, "POST", "/api/program/generate", a.generateProgram, roles(REditor, RAdmin), idempotent())
	a.handle(r, "GET", "/api/program/validate", a.validateProgram, roles(REditor, RAdmin))
	a.handle(r, "PUT", "/api/program/sessions/{id}", a.moveSession, roles(REditor, RAdmin))
	a.handle(r, "GET", "/api/agenda", a.getAgenda)
	a.handle(r, "POST", "/api/agenda", a.addAgenda)
	a.handle(r, "DELETE", "/api/agenda/{sessionId}", a.removeAgenda)
	a.handle(r, "GET", "/api/agenda.ics", a.agendaICS)
}

type roomDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

func (a *App) listRooms(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT id, name, capacity FROM rooms ORDER BY capacity, id`)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[roomDTO])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (a *App) createRoom(w http.ResponseWriter, r *http.Request) error {
	var in roomDTO
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || in.Capacity < 1 || in.Capacity > 10000 {
		return httpx.Validation("salon adı ve 1-10000 arası kapasite gerekli")
	}
	err := a.Pool.QueryRow(r.Context(), `INSERT INTO rooms (name, capacity) VALUES ($1,$2) RETURNING id`, in.Name, in.Capacity).Scan(&in.ID)
	if db.IsUniqueViolation(err, "") {
		return httpx.Conflict("duplicate_room", "bu adla bir salon zaten var")
	}
	if err != nil {
		return err
	}
	p := principal(r)
	aid, role := actor(p)
	_ = audit.Log(r.Context(), a.Pool, audit.Entry{ActorID: aid, ActorRole: role, Action: "room.created", Entity: "room", EntityID: itoa(in.ID), New: in})
	httpx.WriteJSON(w, 201, in)
	return nil
}

type slotDTO struct {
	ID    int64     `json:"id"`
	Start time.Time `json:"starts_at"`
	End   time.Time `json:"ends_at"`
}

func (a *App) listSlots(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Pool.Query(r.Context(), `SELECT id, starts_at, ends_at FROM time_slots ORDER BY starts_at`)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[slotDTO])
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (a *App) scheduleInput(ctx context.Context, q db.Querier) (schedule.Input, map[int64]string, error) {
	return LoadScheduleInput(ctx, q)
}

// LoadScheduleInput loads everything the scheduler needs from the database
// (seed de programı aynı yoldan üretir).
func LoadScheduleInput(ctx context.Context, q db.Querier) (schedule.Input, map[int64]string, error) {
	in := schedule.Input{People: map[int64]schedule.Person{}}
	titles := map[int64]string{}
	rows, err := q.Query(ctx, `SELECT id, name, capacity FROM rooms`)
	if err != nil {
		return in, nil, err
	}
	for rows.Next() {
		var rm schedule.Room
		if err := rows.Scan(&rm.ID, &rm.Name, &rm.Capacity); err != nil {
			rows.Close()
			return in, nil, err
		}
		in.Rooms = append(in.Rooms, rm)
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT id, starts_at, ends_at FROM time_slots ORDER BY starts_at`)
	if err != nil {
		return in, nil, err
	}
	for rows.Next() {
		var s schedule.Slot
		if err := rows.Scan(&s.ID, &s.Start, &s.End); err != nil {
			rows.Close()
			return in, nil, err
		}
		in.Slots = append(in.Slots, s)
	}
	rows.Close()
	// kabul edilmiş bildiriler + sunucu + ödeme durumu
	rows, err = q.Query(ctx, `SELECT s.id, s.title, s.keywords, s.duration_minutes, s.expected_audience,
			coalesce((SELECT sa.user_id FROM submission_authors sa WHERE sa.submission_id=s.id AND sa.is_presenter), 0),
			coalesce((SELECT array_agg(sa.user_id) FROM submission_authors sa WHERE sa.submission_id=s.id AND sa.user_id IS NOT NULL), '{}'),
			EXISTS (SELECT 1 FROM submission_authors sa JOIN registrations rg ON rg.user_id=sa.user_id AND rg.status='paid' WHERE sa.submission_id=s.id)
		FROM submissions s WHERE s.status='accepted' ORDER BY s.id`)
	if err != nil {
		return in, nil, err
	}
	for rows.Next() {
		var p schedule.Paper
		var kws []string
		if err := rows.Scan(&p.ID, &p.Title, &kws, &p.Minutes, &p.Audience, &p.PresenterID, &p.AuthorIDs, &p.Paid); err != nil {
			rows.Close()
			return in, nil, err
		}
		if p.PresenterID == 0 { // hesabı olmayan sunucu: bildiriye özgü sanal kişi, İstanbul saati
			p.PresenterID = -p.ID
		}
		p.Keywords = keywords.NewSet(kws)
		titles[p.ID] = p.Title
		in.Papers = append(in.Papers, p)
	}
	rows.Close()
	// kişiler: sunucular ve başkan adayları (editörler + aktif hakemler)
	rows, err = q.Query(ctx, `SELECT u.id, u.timezone, u.avail_start_hour, u.avail_end_hour, u.role, coalesce(rp.keywords,'{}')
		FROM users u LEFT JOIN reviewer_profiles rp ON rp.user_id=u.id
		WHERE u.role IN ('editor') OR (u.role='reviewer' AND rp.is_active)
		   OR u.id IN (SELECT sa.user_id FROM submission_authors sa JOIN submissions s ON s.id=sa.submission_id WHERE s.status='accepted' AND sa.is_presenter)`)
	if err != nil {
		return in, nil, err
	}
	for rows.Next() {
		var id int64
		var tz, role string
		var sh, eh int
		var kws []string
		if err := rows.Scan(&id, &tz, &sh, &eh, &role, &kws); err != nil {
			rows.Close()
			return in, nil, err
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc, _ = time.LoadLocation("Europe/Istanbul")
		}
		in.People[id] = schedule.Person{ID: id, Location: loc, StartHour: sh, EndHour: eh}
		if role == REditor || role == RReviewer {
			in.Chairs = append(in.Chairs, schedule.Chair{ID: id, Keywords: keywords.NewSet(kws)})
		}
	}
	rows.Close()
	ist, _ := time.LoadLocation("Europe/Istanbul")
	for _, p := range in.Papers {
		if _, ok := in.People[p.PresenterID]; !ok {
			in.People[p.PresenterID] = schedule.Person{ID: p.PresenterID, Location: ist, StartHour: 8, EndHour: 22}
		}
	}
	return in, titles, nil
}

// currentProgram reads the stored program in solver format.
func currentProgram(ctx context.Context, q db.Querier) (schedule.Program, map[int64]int64, error) {
	var prog schedule.Program
	idx := map[int64]int{}
	sessionIDs := map[int64]int64{} // index → db id
	rows, err := q.Query(ctx, `SELECT ps.id, ps.topic, ps.room_id, ps.slot_id, coalesce(ps.chair_id,0), sp.submission_id, sp.starts_at, sp.ends_at
		FROM program_sessions ps JOIN session_papers sp ON sp.session_id=ps.id ORDER BY ps.id, sp.position`)
	if err != nil {
		return prog, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, room, slot, chair, sub int64
		var topic string
		var st, en time.Time
		if err := rows.Scan(&id, &topic, &room, &slot, &chair, &sub, &st, &en); err != nil {
			return prog, nil, err
		}
		i, ok := idx[id]
		if !ok {
			i = len(prog.Sessions)
			idx[id] = i
			sessionIDs[int64(i)] = id
			prog.Sessions = append(prog.Sessions, schedule.Session{Topic: topic, RoomID: room, SlotID: slot, ChairID: chair})
		}
		prog.Sessions[i].Talks = append(prog.Sessions[i].Talks, schedule.Talk{PaperID: sub, Start: st, End: en})
	}
	return prog, sessionIDs, rows.Err()
}

type unscheduledDTO struct {
	schedule.Unscheduled
	Title string `json:"title"`
}

type generateResponse struct {
	DryRun      bool                 `json:"dry_run"`
	Stats       schedule.Stats       `json:"stats"`
	Violations  []schedule.Violation `json:"violations"`
	Unscheduled []unscheduledDTO     `json:"unscheduled"`
	Sessions    int                  `json:"sessions"`
}

func (a *App) generateProgram(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		DryRun bool `json:"dry_run"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	var resp generateResponse
	err := db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		if !in.DryRun {
			var got bool
			if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, programLockKey).Scan(&got); err != nil {
				return err
			}
			if !got {
				return httpx.Conflict("program_in_progress", "program şu anda başka bir istekle oluşturuluyor")
			}
		}
		input, titles, err := a.scheduleInput(ctx, tx)
		if err != nil {
			return err
		}
		if len(input.Rooms) == 0 || len(input.Slots) == 0 {
			return httpx.Conflict("no_rooms", "program için salon ve zaman dilimi tanımlı olmalı")
		}
		prog := schedule.Build(input)
		resp = generateResponse{DryRun: in.DryRun, Stats: prog.Stats, Violations: schedule.Validate(input, prog), Sessions: len(prog.Sessions)}
		if resp.Violations == nil {
			resp.Violations = []schedule.Violation{}
		}
		resp.Unscheduled = []unscheduledDTO{}
		for _, u := range prog.Unscheduled {
			resp.Unscheduled = append(resp.Unscheduled, unscheduledDTO{u, titles[u.PaperID]})
		}
		if len(resp.Violations) > 0 {
			return fmt.Errorf("üretilen program doğrulanamadı: %s", resp.Violations[0])
		}
		if in.DryRun {
			return nil
		}
		if err := PersistProgram(ctx, tx, prog); err != nil {
			return err
		}
		aid, role := actor(p)
		return audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "program.generated", Entity: "program", EntityID: "current",
			New: map[string]any{"sessions": len(prog.Sessions), "scheduled": prog.Stats.Scheduled, "unscheduled": len(prog.Unscheduled)}})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, resp)
	return nil
}

// PersistProgram replaces the stored program (session_papers tetikleyicisi
// kabul + ödeme kuralını ayrıca uygular).
func PersistProgram(ctx context.Context, tx pgx.Tx, prog schedule.Program) error {
	if _, err := tx.Exec(ctx, `DELETE FROM program_sessions`); err != nil {
		return err
	}
	for _, s := range prog.Sessions {
		var sid int64
		var chair any
		if s.ChairID != 0 {
			chair = s.ChairID
		}
		title := s.Topic
		if title != "" {
			title = strings.ToUpper(title[:1]) + title[1:]
		}
		if err := tx.QueryRow(ctx, `INSERT INTO program_sessions (title, topic, room_id, slot_id, chair_id) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			title, s.Topic, s.RoomID, s.SlotID, chair).Scan(&sid); err != nil {
			return err
		}
		for i, t := range s.Talks {
			if _, err := tx.Exec(ctx, `INSERT INTO session_papers (session_id, submission_id, position, starts_at, ends_at) VALUES ($1,$2,$3,$4,$5)`,
				sid, t.PaperID, i+1, t.Start, t.End); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateProgram re-checks the stored program with the independent validator.
func (a *App) validateProgram(w http.ResponseWriter, r *http.Request) error {
	input, _, err := a.scheduleInput(r.Context(), a.Pool)
	if err != nil {
		return err
	}
	prog, _, err := currentProgram(r.Context(), a.Pool)
	if err != nil {
		return err
	}
	// programda olmayan kabul edilmiş bildiriler "dışarıda" sayılır; burada
	// yalnızca çakışmaları raporlarız.
	in := map[int64]bool{}
	for _, s := range prog.Sessions {
		for _, t := range s.Talks {
			in[t.PaperID] = true
		}
	}
	for _, p := range input.Papers {
		if !in[p.ID] {
			reason := schedule.ReasonNoRoom
			if !p.Paid {
				reason = schedule.ReasonUnpaid
			}
			prog.Unscheduled = append(prog.Unscheduled, schedule.Unscheduled{PaperID: p.ID, Reason: reason})
		}
	}
	v := schedule.Validate(input, prog)
	if v == nil {
		v = []schedule.Violation{}
	}
	scheduled := 0
	for _, s := range prog.Sessions {
		scheduled += len(s.Talks)
	}
	httpx.WriteJSON(w, 200, map[string]any{"valid": len(v) == 0, "violations": v, "sessions": len(prog.Sessions), "scheduled": scheduled})
	return nil
}

type talkView struct {
	SubmissionID int64     `json:"submission_id"`
	Title        string    `json:"title"`
	Presenter    string    `json:"presenter"`
	Start        time.Time `json:"starts_at"`
	End          time.Time `json:"ends_at"`
}

type sessionView struct {
	ID       int64      `json:"id"`
	Title    string     `json:"title"`
	Topic    string     `json:"topic"`
	RoomID   int64      `json:"room_id"`
	Room     string     `json:"room"`
	Capacity int        `json:"capacity"`
	SlotID   int64      `json:"slot_id"`
	Start    time.Time  `json:"starts_at"`
	End      time.Time  `json:"ends_at"`
	Chair    string     `json:"chair"`
	Talks    []talkView `json:"talks"`
	InAgenda bool       `json:"in_agenda"`
}

// loadSessions returns the published program. Kabul edilip programa alınan
// bildirilerde çift kör süreç bitmiştir; sunucu adı herkese açıktır.
func (a *App) loadSessions(ctx context.Context, userID int64, onlyAgenda bool) ([]sessionView, error) {
	rows, err := a.Pool.Query(ctx, `SELECT ps.id, ps.title, ps.topic, rm.id, rm.name, rm.capacity, ts.id, ts.starts_at, ts.ends_at, coalesce(cu.full_name,''),
			EXISTS(SELECT 1 FROM personal_agenda pa WHERE pa.session_id=ps.id AND pa.user_id=$1)
		FROM program_sessions ps JOIN rooms rm ON rm.id=ps.room_id JOIN time_slots ts ON ts.id=ps.slot_id LEFT JOIN users cu ON cu.id=ps.chair_id
		WHERE NOT $2 OR EXISTS(SELECT 1 FROM personal_agenda pa WHERE pa.session_id=ps.id AND pa.user_id=$1)
		ORDER BY ts.starts_at, rm.name`, userID, onlyAgenda)
	if err != nil {
		return nil, err
	}
	var out []sessionView
	idx := map[int64]int{}
	for rows.Next() {
		var s sessionView
		if err := rows.Scan(&s.ID, &s.Title, &s.Topic, &s.RoomID, &s.Room, &s.Capacity, &s.SlotID, &s.Start, &s.End, &s.Chair, &s.InAgenda); err != nil {
			rows.Close()
			return nil, err
		}
		s.Talks = []talkView{}
		idx[s.ID] = len(out)
		out = append(out, s)
	}
	rows.Close()
	rows, err = a.Pool.Query(ctx, `SELECT sp.session_id, s.id, s.title, coalesce((SELECT sa.full_name FROM submission_authors sa WHERE sa.submission_id=s.id AND sa.is_presenter),''),
			sp.starts_at, sp.ends_at
		FROM session_papers sp JOIN submissions s ON s.id=sp.submission_id ORDER BY sp.session_id, sp.position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid int64
		var t talkView
		if err := rows.Scan(&sid, &t.SubmissionID, &t.Title, &t.Presenter, &t.Start, &t.End); err != nil {
			return nil, err
		}
		if i, ok := idx[sid]; ok {
			out[i].Talks = append(out[i].Talks, t)
		}
	}
	if out == nil {
		out = []sessionView{}
	}
	return out, rows.Err()
}

func (a *App) getProgram(w http.ResponseWriter, r *http.Request) error {
	s, err := a.loadSessions(r.Context(), principal(r).UserID, false)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"sessions": s})
	return nil
}

// moveSession: sürükle-bırak düzenleme. Oturum yeni salon/dilime taşınır,
// konuşma saatleri yeniden dizilir ve tüm program bağımsız doğrulayıcıdan
// geçmezse değişiklik reddedilir (409 + ihlaller).
func (a *App) moveSession(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var in struct {
		RoomID int64 `json:"room_id"`
		SlotID int64 `json:"slot_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.RoomID <= 0 || in.SlotID <= 0 {
		return httpx.Validation("room_id ve slot_id zorunlu")
	}
	p := principal(r)
	err = db.InTx(r.Context(), a.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		var got bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, programLockKey).Scan(&got); err != nil {
			return err
		}
		if !got {
			return httpx.Conflict("program_in_progress", "program şu anda düzenleniyor")
		}
		input, _, err := a.scheduleInput(ctx, tx)
		if err != nil {
			return err
		}
		prog, ids, err := currentProgram(ctx, tx)
		if err != nil {
			return err
		}
		target := -1
		for i := range prog.Sessions {
			if ids[int64(i)] == id {
				target = i
			}
		}
		if target < 0 {
			return httpx.ErrNotFound
		}
		var slot *schedule.Slot
		for i := range input.Slots {
			if input.Slots[i].ID == in.SlotID {
				slot = &input.Slots[i]
			}
		}
		if slot == nil {
			return httpx.Validation("zaman dilimi bulunamadı")
		}
		papers := map[int64]schedule.Paper{}
		for _, pp := range input.Papers {
			papers[pp.ID] = pp
		}
		var sessPapers []schedule.Paper
		for _, t := range prog.Sessions[target].Talks {
			sessPapers = append(sessPapers, papers[t.PaperID])
		}
		talks, ok := schedule.OrderTalks(sessPapers, *slot, input.People)
		if !ok {
			return httpx.Conflict("schedule_conflict", "oturumdaki konuşmacılar bu zaman diliminde uygun değil veya süre yetmiyor").
				With("violations", []schedule.Violation{{Code: "availability", Message: "konuşmacı uygunluğu/süre"}})
		}
		old := prog.Sessions[target]
		prog.Sessions[target].RoomID, prog.Sessions[target].SlotID, prog.Sessions[target].Talks = in.RoomID, in.SlotID, talks
		// programda olmayanlar doğrulayıcı için "dışarıda" listesine
		inProg := map[int64]bool{}
		for _, s := range prog.Sessions {
			for _, t := range s.Talks {
				inProg[t.PaperID] = true
			}
		}
		for _, pp := range input.Papers {
			if !inProg[pp.ID] {
				prog.Unscheduled = append(prog.Unscheduled, schedule.Unscheduled{PaperID: pp.ID, Reason: "not_in_program"})
			}
		}
		if v := schedule.Validate(input, prog); len(v) > 0 {
			sort.Slice(v, func(i, j int) bool { return v[i].Code < v[j].Code })
			return httpx.Conflict("schedule_conflict", "taşıma programda çakışma yaratıyor").With("violations", v)
		}
		if _, err := tx.Exec(ctx, `UPDATE program_sessions SET room_id=$2, slot_id=$3 WHERE id=$1`, id, in.RoomID, in.SlotID); err != nil {
			if db.IsUniqueViolation(err, "") {
				return httpx.Conflict("schedule_conflict", "hedef hücre dolu")
			}
			return err
		}
		for _, t := range talks {
			if _, err := tx.Exec(ctx, `UPDATE session_papers SET starts_at=$3, ends_at=$4 WHERE session_id=$1 AND submission_id=$2`, id, t.PaperID, t.Start, t.End); err != nil {
				return err
			}
		}
		aid, role := actor(p)
		return audit.Log(ctx, tx, audit.Entry{ActorID: aid, ActorRole: role, Action: "program.session_moved", Entity: "program_session", EntityID: itoa(id),
			Old: map[string]any{"room_id": old.RoomID, "slot_id": old.SlotID}, New: map[string]any{"room_id": in.RoomID, "slot_id": in.SlotID}})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"id": id, "room_id": in.RoomID, "slot_id": in.SlotID})
	return nil
}

// ---- kişisel program (bonus) ------------------------------------------------------

func (a *App) getAgenda(w http.ResponseWriter, r *http.Request) error {
	s, err := a.loadSessions(r.Context(), principal(r).UserID, true)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, 200, map[string]any{"sessions": s})
	return nil
}

func (a *App) addAgenda(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		SessionID int64 `json:"session_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	tag, err := a.Pool.Exec(r.Context(), `INSERT INTO personal_agenda (user_id, session_id) SELECT $1, id FROM program_sessions WHERE id=$2 ON CONFLICT DO NOTHING`,
		principal(r).UserID, in.SessionID)
	if err != nil {
		return err
	}
	var exists bool
	_ = a.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM program_sessions WHERE id=$1)`, in.SessionID).Scan(&exists)
	if !exists {
		return httpx.ErrNotFound
	}
	httpx.WriteJSON(w, 201, map[string]any{"session_id": in.SessionID, "added": tag.RowsAffected() == 1})
	return nil
}

func (a *App) removeAgenda(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(chi.URLParam(r, "sessionId"))
	if err != nil {
		return err
	}
	if _, err := a.Pool.Exec(r.Context(), `DELETE FROM personal_agenda WHERE user_id=$1 AND session_id=$2`, principal(r).UserID, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func icsEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace(s)
}

// agendaICS exports the personal program as iCalendar (RFC 5545).
func (a *App) agendaICS(w http.ResponseWriter, r *http.Request) error {
	s, err := a.loadSessions(r.Context(), principal(r).UserID, true)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//KongreHub//Program//TR\r\nCALSCALE:GREGORIAN\r\n")
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, ss := range s {
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:session-%d@kongrehub\r\nDTSTAMP:%s\r\nDTSTART:%s\r\nDTEND:%s\r\nSUMMARY:%s\r\nLOCATION:%s\r\n",
			ss.ID, stamp, ss.Start.UTC().Format("20060102T150405Z"), ss.End.UTC().Format("20060102T150405Z"), icsEscape(ss.Title), icsEscape(ss.Room))
		var desc []string
		for _, t := range ss.Talks {
			desc = append(desc, fmt.Sprintf("%s %s — %s", t.Start.In(istanbul()).Format("15:04"), t.Title, t.Presenter))
		}
		fmt.Fprintf(&b, "DESCRIPTION:%s\r\nEND:VEVENT\r\n", icsEscape(strings.Join(desc, "\n")))
	}
	b.WriteString("END:VCALENDAR\r\n")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="kongrehub-program.ics"`)
	_, err = w.Write([]byte(b.String()))
	return err
}

func istanbul() *time.Location {
	l, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		return time.UTC
	}
	return l
}


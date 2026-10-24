// Package schedule builds a conflict-free conference program.
//
// İki aşama:
//  1. Gruplama: kabul edilmiş bildiriler anahtar kelime benzerliğine (Jaccard)
//     göre oturumlara toplanır. En "yalnız" bildiri tohum seçilir, oturum
//     süresi dolana kadar ortalama benzerliği en yüksek bildiriler eklenir.
//  2. Yerleştirme: oturumlar (salon, zaman dilimi) hücrelerine kısıt
//     yayılımlı geri izleme ile yerleştirilir (MRV: en az seçeneği kalan oturum
//     önce). Ardından oturum başkanları atanır.
//
// Sonuç, çözücüden bağımsız yazılmış Validate fonksiyonuyla doğrulanır.
package schedule

import (
	"fmt"
	"sort"
	"time"

	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
)

// Person is a speaker or chair with an availability window in local time.
type Person struct {
	ID        int64
	Location  *time.Location
	StartHour int // yerel saat, dahil
	EndHour   int // yerel saat, hariç (24 = gece yarısı)
}

// Paper is an accepted, schedulable paper.
type Paper struct {
	ID          int64
	Title       string
	Keywords    keywords.Set
	PresenterID int64
	AuthorIDs   []int64
	Minutes     int
	Audience    int
	Paid        bool // en az bir yazar ödemeyi tamamladı mı (T-5 kuralı)
}

type Room struct {
	ID       int64
	Name     string
	Capacity int
}

type Slot struct {
	ID    int64
	Start time.Time
	End   time.Time
}

func (s Slot) Minutes() int { return int(s.End.Sub(s.Start).Minutes()) }

// Chair is a session chair candidate.
type Chair struct {
	ID       int64
	Keywords keywords.Set
}

// Input bundles the problem.
type Input struct {
	Papers []Paper
	Rooms  []Room
	Slots  []Slot
	Chairs []Chair
	People map[int64]Person // presenter ve başkanların uygunlukları
}

// Talk is a placed paper.
type Talk struct {
	PaperID int64     `json:"paper_id"`
	Start   time.Time `json:"starts_at"`
	End     time.Time `json:"ends_at"`
}

// Session is a placed session.
type Session struct {
	Topic   string `json:"topic"`
	RoomID  int64  `json:"room_id"`
	SlotID  int64  `json:"slot_id"`
	ChairID int64  `json:"chair_id"`
	Talks   []Talk `json:"talks"`
}

// Unscheduled explains a paper left out.
type Unscheduled struct {
	PaperID int64  `json:"paper_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

const (
	ReasonUnpaid       = "no_paid_author"
	ReasonAvailability = "speaker_unavailable"
	ReasonNoRoom       = "no_room_or_slot"
)

// Program is the solver output.
type Program struct {
	Sessions    []Session     `json:"sessions"`
	Unscheduled []Unscheduled `json:"unscheduled"`
	Stats       Stats         `json:"stats"`
}

type Stats struct {
	Papers        int     `json:"papers"`
	Scheduled     int     `json:"scheduled"`
	Sessions      int     `json:"sessions"`
	AvgSimilarity float64 `json:"avg_intra_session_similarity"`
	SearchNodes   int     `json:"search_nodes"`
	DurationMS    int64   `json:"duration_ms"`
}

// ---- yardımcılar -----------------------------------------------------------

// Available reports whether person p can present during [s,e).
func Available(p Person, s, e time.Time) bool {
	loc := p.Location
	if loc == nil {
		loc = time.UTC
	}
	ls, le := s.In(loc), e.In(loc)
	startMin := ls.Hour()*60 + ls.Minute()
	endMin := le.Hour()*60 + le.Minute()
	if le.YearDay() != ls.YearDay() || le.Year() != ls.Year() {
		if endMin == 0 { // tam gece yarısında biten konuşma
			endMin = 24 * 60
		} else {
			return false
		}
	}
	return startMin >= p.StartHour*60 && endMin <= p.EndHour*60
}

// orderTalks finds a sequence of the given papers inside the slot so that each
// presenter is available for their own talk. n ≤ 6 olduğundan permütasyonlar
// denenir (en fazla 720); önce son uygunluk saatine göre sıralı deneme.
func orderTalks(papers []*Paper, slot Slot, people map[int64]Person) ([]Talk, bool) {
	n := len(papers)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var best []Talk
	var try func(k int) bool
	try = func(k int) bool {
		if k == n {
			t := slot.Start
			out := make([]Talk, n)
			for i, pi := range idx {
				p := papers[pi]
				e := t.Add(time.Duration(p.Minutes) * time.Minute)
				if e.After(slot.End) {
					return false
				}
				per, ok := people[p.PresenterID]
				if !ok {
					per = Person{ID: p.PresenterID, Location: time.UTC, StartHour: 0, EndHour: 24}
				}
				if !Available(per, t, e) {
					return false
				}
				out[i] = Talk{PaperID: p.ID, Start: t, End: e}
				t = e
			}
			best = out
			return true
		}
		for i := k; i < n; i++ {
			idx[k], idx[i] = idx[i], idx[k]
			if try(k + 1) {
				return true
			}
			idx[k], idx[i] = idx[i], idx[k]
		}
		return false
	}
	ok := try(0)
	return best, ok
}

// OrderTalks is the exported form used when an editor moves a session to a
// new slot (sürükle-bırak): konuşma saatleri yeni dilime göre yeniden dizilir.
func OrderTalks(papers []Paper, slot Slot, people map[int64]Person) ([]Talk, bool) {
	ps := make([]*Paper, len(papers))
	for i := range papers {
		ps[i] = &papers[i]
	}
	return orderTalks(ps, slot, people)
}

// ---- 1. gruplama ------------------------------------------------------------

type group struct {
	papers   []*Paper
	minutes  int
	audience int
	topic    string
}

func groupPapers(papers []*Paper, slotMinutes int, feasible func([]*Paper) bool) []*group {
	n := len(papers)
	sim := make([][]float64, n)
	for i := range sim {
		sim[i] = make([]float64, n)
		for j := range sim[i] {
			if i != j {
				sim[i][j] = keywords.Jaccard(papers[i].Keywords, papers[j].Keywords)
			}
		}
	}
	used := make([]bool, n)
	var groups []*group
	for {
		// tohum: kalanlar içinde benzerlik toplamı en düşük (yerleştirmesi en zor) bildiri
		seed, seedMass := -1, 1e18
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			m := 0.0
			for j := 0; j < n; j++ {
				if !used[j] {
					m += sim[i][j]
				}
			}
			if m < seedMass || (m == seedMass && papers[i].ID < papers[seed].ID) {
				seed, seedMass = i, m
			}
		}
		if seed < 0 {
			break
		}
		g := &group{}
		members := []int{seed}
		used[seed] = true
		g.papers = append(g.papers, papers[seed])
		g.minutes = papers[seed].Minutes
		for {
			bestJ, bestS := -1, -1.0
			for j := 0; j < n; j++ {
				if used[j] || g.minutes+papers[j].Minutes > slotMinutes {
					continue
				}
				if !feasible(append(append([]*Paper(nil), g.papers...), papers[j])) {
					continue
				}
				s := 0.0
				for _, m := range members {
					s += sim[m][j]
				}
				s /= float64(len(members))
				if s > bestS || (s == bestS && papers[j].ID < papers[bestJ].ID) {
					bestJ, bestS = j, s
				}
			}
			// benzerliği sıfır olanları yalnızca oturum çok boşsa ekle
			if bestJ < 0 || (bestS == 0 && g.minutes >= slotMinutes/2) {
				break
			}
			used[bestJ] = true
			members = append(members, bestJ)
			g.papers = append(g.papers, papers[bestJ])
			g.minutes += papers[bestJ].Minutes
		}
		groups = append(groups, g)
	}
	for _, g := range groups {
		g.topic = topicOf(g.papers)
		for _, p := range g.papers {
			if p.Audience > g.audience {
				g.audience = p.Audience
			}
		}
	}
	return groups
}

func topicOf(ps []*Paper) string {
	cnt := map[string]int{}
	for _, p := range ps {
		for k := range p.Keywords {
			cnt[k]++
		}
	}
	best, bc := "", 0
	for k, c := range cnt {
		if c > bc || (c == bc && k < best) {
			best, bc = k, c
		}
	}
	return best
}

// ---- 2. yerleştirme ---------------------------------------------------------

type option struct {
	room, slot int
	talks      []Talk
}

// Build schedules the program.
func Build(in Input) Program {
	start := time.Now()
	prog := Program{}
	rooms := append([]Room(nil), in.Rooms...)
	slots := append([]Slot(nil), in.Slots...)
	sort.Slice(rooms, func(i, j int) bool {
		return rooms[i].Capacity < rooms[j].Capacity || (rooms[i].Capacity == rooms[j].Capacity && rooms[i].ID < rooms[j].ID)
	})
	sort.Slice(slots, func(i, j int) bool { return slots[i].Start.Before(slots[j].Start) })

	var eligible []*Paper
	papers := append([]Paper(nil), in.Papers...)
	sort.Slice(papers, func(i, j int) bool { return papers[i].ID < papers[j].ID })
	for i := range papers {
		p := &papers[i]
		if !p.Paid {
			prog.Unscheduled = append(prog.Unscheduled, Unscheduled{p.ID, ReasonUnpaid, "hiçbir yazar kayıt ödemesini tamamlamadı"})
			continue
		}
		// hiçbir dilimde uygun olmayan konuşmacıyı baştan ayıkla
		ok := false
		for _, s := range slots {
			if _, fit := orderTalks([]*Paper{p}, s, in.People); fit {
				ok = true
				break
			}
		}
		if !ok {
			prog.Unscheduled = append(prog.Unscheduled, Unscheduled{p.ID, ReasonAvailability, "konuşmacının saat dilimindeki uygunluk penceresi hiçbir oturum dilimiyle örtüşmüyor"})
			continue
		}
		eligible = append(eligible, p)
	}
	slotMinutes := 0
	for _, s := range slots {
		if s.Minutes() > slotMinutes {
			slotMinutes = s.Minutes()
		}
	}
	maxCap := 0
	for _, r := range rooms {
		if r.Capacity > maxCap {
			maxCap = r.Capacity
		}
	}
	// Bir grup ancak bir dilime sığıyor ve en büyük salona sığıyorsa oluşturulur.
	// Ayrıca aynı konuşmacının iki bildirisi aynı oturumda olabilir (aynı yer).
	feasible := func(ps []*Paper) bool {
		aud := 0
		for _, p := range ps {
			if p.Audience > aud {
				aud = p.Audience
			}
		}
		if aud > maxCap {
			return false
		}
		for _, s := range slots {
			if _, ok := orderTalks(ps, s, in.People); ok {
				return true
			}
		}
		return false
	}
	groups := groupPapers(eligible, slotMinutes, feasible)

	// her grup için geçerli (salon, dilim) seçenekleri
	opts := make([][]option, len(groups))
	for gi, g := range groups {
		for si, s := range slots {
			if g.minutes > s.Minutes() {
				continue
			}
			talks, ok := orderTalks(g.papers, s, in.People)
			if !ok {
				continue
			}
			for ri, r := range rooms {
				if r.Capacity >= g.audience {
					opts[gi] = append(opts[gi], option{ri, si, talks})
				}
			}
		}
	}

	presenters := make([]map[int64]bool, len(groups))
	for gi, g := range groups {
		presenters[gi] = map[int64]bool{}
		for _, p := range g.papers {
			presenters[gi][p.PresenterID] = true
		}
	}

	placed := make([]int, len(groups)) // seçenek indeksi, -1 = yerleşmedi
	for i := range placed {
		placed[i] = -1
	}
	roomBusy := map[[2]int]bool{}
	slotPeople := make([]map[int64]int, len(slots))
	for i := range slotPeople {
		slotPeople[i] = map[int64]int{}
	}
	valid := func(gi int, o option) bool {
		if roomBusy[[2]int{o.room, o.slot}] {
			return false
		}
		for pid := range presenters[gi] {
			if slotPeople[o.slot][pid] > 0 {
				return false
			}
		}
		return true
	}
	apply := func(gi, oi int, on bool) {
		o := opts[gi][oi]
		roomBusy[[2]int{o.room, o.slot}] = on
		d := 1
		if !on {
			d = -1
		}
		for pid := range presenters[gi] {
			slotPeople[o.slot][pid] += d
		}
		if on {
			placed[gi] = oi
		} else {
			placed[gi] = -1
		}
	}

	nodes := 0
	const nodeLimit = 300000
	var search func(remaining int) bool
	search = func(remaining int) bool {
		if remaining == 0 {
			return true
		}
		nodes++
		if nodes > nodeLimit {
			return false
		}
		// MRV
		pick, pickCount := -1, 1<<30
		for gi := range groups {
			if placed[gi] >= 0 {
				continue
			}
			c := 0
			for _, o := range opts[gi] {
				if valid(gi, o) {
					c++
				}
			}
			if c < pickCount || (c == pickCount && groups[gi].audience > groups[pick].audience) {
				pick, pickCount = gi, c
			}
		}
		if pickCount == 0 {
			return false
		}
		for oi, o := range opts[pick] {
			if !valid(pick, o) {
				continue
			}
			apply(pick, oi, true)
			if search(remaining - 1) {
				return true
			}
			apply(pick, oi, false)
			if nodes > nodeLimit {
				return false
			}
		}
		return false
	}

	if !search(len(groups)) {
		// Tam çözüm bulunamadı: açgözlü yerleştir, kalan oturumları dağıt ve
		// bildirileri tek tek mevcut oturumlardaki boşluklara ya da boş
		// hücrelere yerleştirmeyi dene (onarım).
		for gi := range placed {
			if placed[gi] >= 0 {
				apply(gi, placed[gi], false)
			}
		}
		order := make([]int, len(groups))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool { return len(opts[order[a]]) < len(opts[order[b]]) })
		var leftovers []*Paper
		for _, gi := range order {
			ok := false
			for oi, o := range opts[gi] {
				if valid(gi, o) {
					apply(gi, oi, true)
					ok = true
					break
				}
			}
			if !ok {
				leftovers = append(leftovers, groups[gi].papers...)
			}
		}
		for _, p := range leftovers {
			if !repairInsert(p, &groups, &opts, &placed, &presenters, rooms, slots, slotPeople, roomBusy, in.People) {
				prog.Unscheduled = append(prog.Unscheduled, Unscheduled{p.ID, ReasonNoRoom, "kapasitesi yeterli, konuşmacı çakışması olmayan salon/zaman bulunamadı"})
			}
		}
	}

	// oturum başkanları: en az seçeneği olan oturumdan başla
	type placedSess struct {
		gi  int
		opt option
	}
	var ps []placedSess
	for gi := range groups {
		if placed[gi] >= 0 {
			ps = append(ps, placedSess{gi, opts[gi][placed[gi]]})
		}
	}
	chairLoad := map[int64]int{}
	chairSlot := map[[2]int64]bool{} // (chair, slotIdx)
	authorsOf := func(gi int) map[int64]bool {
		m := map[int64]bool{}
		for _, p := range groups[gi].papers {
			m[p.PresenterID] = true
			for _, a := range p.AuthorIDs {
				m[a] = true
			}
		}
		return m
	}
	chairOK := func(c Chair, s placedSess, authors map[int64]bool) bool {
		if authors[c.ID] || chairSlot[[2]int64{c.ID, int64(s.opt.slot)}] || slotPeople[s.opt.slot][c.ID] > 0 {
			return false
		}
		if per, ok := in.People[c.ID]; ok && !Available(per, slots[s.opt.slot].Start, slots[s.opt.slot].End) {
			return false
		}
		return true
	}
	sort.SliceStable(ps, func(a, b int) bool {
		ca, cb := 0, 0
		aa, ab := authorsOf(ps[a].gi), authorsOf(ps[b].gi)
		for _, c := range in.Chairs {
			if chairOK(c, ps[a], aa) {
				ca++
			}
			if chairOK(c, ps[b], ab) {
				cb++
			}
		}
		return ca < cb
	})
	simSum, simCnt := 0.0, 0
	for _, s := range ps {
		g := groups[s.gi]
		authors := authorsOf(s.gi)
		var best *Chair
		bestVal := -1e18
		for ci := range in.Chairs {
			c := &in.Chairs[ci]
			if !chairOK(*c, s, authors) {
				continue
			}
			exp := 0.0
			for _, p := range g.papers {
				exp += keywords.Coverage(p.Keywords, c.Keywords)
			}
			val := exp/float64(len(g.papers)) - 0.15*float64(chairLoad[c.ID])
			if val > bestVal || (val == bestVal && c.ID < best.ID) {
				best, bestVal = c, val
			}
		}
		sess := Session{Topic: g.topic, RoomID: rooms[s.opt.room].ID, SlotID: slots[s.opt.slot].ID, Talks: s.opt.talks}
		if best != nil {
			sess.ChairID = best.ID
			chairLoad[best.ID]++
			chairSlot[[2]int64{best.ID, int64(s.opt.slot)}] = true
		}
		prog.Sessions = append(prog.Sessions, sess)
		for i := 0; i < len(g.papers); i++ {
			for j := i + 1; j < len(g.papers); j++ {
				simSum += keywords.Jaccard(g.papers[i].Keywords, g.papers[j].Keywords)
				simCnt++
			}
		}
	}
	sort.Slice(prog.Sessions, func(i, j int) bool {
		a, b := prog.Sessions[i], prog.Sessions[j]
		if !a.Talks[0].Start.Equal(b.Talks[0].Start) {
			return a.Talks[0].Start.Before(b.Talks[0].Start)
		}
		return a.RoomID < b.RoomID
	})
	sort.Slice(prog.Unscheduled, func(i, j int) bool { return prog.Unscheduled[i].PaperID < prog.Unscheduled[j].PaperID })

	prog.Stats = Stats{Papers: len(in.Papers), Sessions: len(prog.Sessions), SearchNodes: nodes, DurationMS: time.Since(start).Milliseconds()}
	for _, s := range prog.Sessions {
		prog.Stats.Scheduled += len(s.Talks)
	}
	if simCnt > 0 {
		prog.Stats.AvgSimilarity = float64(int(simSum/float64(simCnt)*1000)) / 1000
	}
	return prog
}

// repairInsert places a single paper either into an already placed session
// with spare time (re-ordering talks) or as a new one-paper session in a free
// cell. Kısıtlar: süre, salon kapasitesi, konuşmacı uygunluğu ve konuşmacının
// aynı dilimde başka oturumda olmaması.
func repairInsert(p *Paper, groups *[]*group, opts *[][]option, placed *[]int, presenters *[]map[int64]bool,
	rooms []Room, slots []Slot, slotPeople []map[int64]int, roomBusy map[[2]int]bool, people map[int64]Person) bool {
	// 1) mevcut oturuma ekle
	for gi, g := range *groups {
		if (*placed)[gi] < 0 {
			continue
		}
		o := (*opts)[gi][(*placed)[gi]]
		if g.minutes+p.Minutes > slots[o.slot].Minutes() || p.Audience > rooms[o.room].Capacity {
			continue
		}
		if !(*presenters)[gi][p.PresenterID] && slotPeople[o.slot][p.PresenterID] > 0 {
			continue
		}
		cand := append(append([]*Paper(nil), g.papers...), p)
		talks, ok := orderTalks(cand, slots[o.slot], people)
		if !ok {
			continue
		}
		g.papers = cand
		g.minutes += p.Minutes
		if p.Audience > g.audience {
			g.audience = p.Audience
		}
		(*opts)[gi][(*placed)[gi]].talks = talks
		if !(*presenters)[gi][p.PresenterID] {
			(*presenters)[gi][p.PresenterID] = true
			slotPeople[o.slot][p.PresenterID]++
		}
		return true
	}
	// 2) boş hücrede yeni oturum
	for si, s := range slots {
		if slotPeople[si][p.PresenterID] > 0 {
			continue
		}
		talks, ok := orderTalks([]*Paper{p}, s, people)
		if !ok {
			continue
		}
		for ri, r := range rooms {
			if roomBusy[[2]int{ri, si}] || r.Capacity < p.Audience {
				continue
			}
			g := &group{papers: []*Paper{p}, minutes: p.Minutes, audience: p.Audience, topic: topicOf([]*Paper{p})}
			*groups = append(*groups, g)
			*opts = append(*opts, []option{{ri, si, talks}})
			*placed = append(*placed, 0)
			*presenters = append(*presenters, map[int64]bool{p.PresenterID: true})
			roomBusy[[2]int{ri, si}] = true
			slotPeople[si][p.PresenterID]++
			return true
		}
	}
	return false
}

// ---- bağımsız doğrulayıcı -------------------------------------------------

// Violation is one broken constraint.
type Violation struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (v Violation) String() string { return v.Code + ": " + v.Message }

// Validate checks a program against every hard constraint without reusing
// solver internals. Boş dönüş = çakışmasız program.
func Validate(in Input, prog Program) []Violation {
	var out []Violation
	add := func(code, f string, a ...any) { out = append(out, Violation{code, fmt.Sprintf(f, a...)}) }

	papers := map[int64]Paper{}
	for _, p := range in.Papers {
		papers[p.ID] = p
	}
	rooms := map[int64]Room{}
	for _, r := range in.Rooms {
		rooms[r.ID] = r
	}
	slots := map[int64]Slot{}
	for _, s := range in.Slots {
		slots[s.ID] = s
	}
	chairs := map[int64]bool{}
	for _, c := range in.Chairs {
		chairs[c.ID] = true
	}

	type busy struct {
		start, end time.Time
		session    int
		what       string
	}
	personBusy := map[int64][]busy{}
	seen := map[int64]bool{}

	for si, s := range prog.Sessions {
		room, ok := rooms[s.RoomID]
		if !ok {
			add("unknown_room", "oturum %d: bilinmeyen salon %d", si, s.RoomID)
			continue
		}
		slot, ok := slots[s.SlotID]
		if !ok {
			add("unknown_slot", "oturum %d: bilinmeyen zaman dilimi %d", si, s.SlotID)
			continue
		}
		if len(s.Talks) == 0 {
			add("empty_session", "oturum %d boş", si)
			continue
		}
		talks := append([]Talk(nil), s.Talks...)
		sort.Slice(talks, func(i, j int) bool { return talks[i].Start.Before(talks[j].Start) })
		authors := map[int64]bool{}
		for ti, t := range talks {
			p, ok := papers[t.PaperID]
			if !ok {
				add("unknown_paper", "oturum %d: bilinmeyen bildiri %d", si, t.PaperID)
				continue
			}
			if seen[p.ID] {
				add("duplicate_paper", "bildiri %d birden fazla kez programda", p.ID)
			}
			seen[p.ID] = true
			if !p.Paid {
				add("unpaid", "bildiri %d: ödemesi tamamlanmış yazarı yok", p.ID)
			}
			if t.Start.Before(slot.Start) || t.End.After(slot.End) {
				add("outside_slot", "bildiri %d zaman dilimi dışına taşıyor", p.ID)
			}
			if int(t.End.Sub(t.Start).Minutes()) != p.Minutes {
				add("wrong_duration", "bildiri %d süresi %d dk olmalı", p.ID, p.Minutes)
			}
			if ti > 0 && t.Start.Before(talks[ti-1].End) {
				add("talk_overlap", "oturum %d: bildiri %d önceki konuşmayla çakışıyor", si, p.ID)
			}
			if p.Audience > room.Capacity {
				add("capacity", "bildiri %d: beklenen %d kişi > salon '%s' kapasitesi %d", p.ID, p.Audience, room.Name, room.Capacity)
			}
			if per, ok := in.People[p.PresenterID]; ok && !Available(per, t.Start, t.End) {
				add("availability", "bildiri %d: konuşmacı %d yerel saatinde uygun değil (%s)", p.ID, p.PresenterID, t.Start.In(per.Location).Format("15:04 MST"))
			}
			personBusy[p.PresenterID] = append(personBusy[p.PresenterID], busy{slot.Start, slot.End, si, "konuşmacı"})
			authors[p.PresenterID] = true
			for _, a := range p.AuthorIDs {
				authors[a] = true
			}
		}
		if s.ChairID == 0 {
			add("no_chair", "oturum %d (%s) başkansız", si, s.Topic)
		} else {
			if !chairs[s.ChairID] {
				add("chair_not_candidate", "oturum %d: %d başkan adayı değil", si, s.ChairID)
			}
			if authors[s.ChairID] {
				add("chair_conflict", "oturum %d: başkan %d oturumdaki bir bildirinin yazarı", si, s.ChairID)
			}
			personBusy[s.ChairID] = append(personBusy[s.ChairID], busy{slot.Start, slot.End, si, "başkan"})
		}
	}
	// salon çift rezervasyon
	for i := 0; i < len(prog.Sessions); i++ {
		for j := i + 1; j < len(prog.Sessions); j++ {
			a, b := prog.Sessions[i], prog.Sessions[j]
			sa, sb := slots[a.SlotID], slots[b.SlotID]
			if a.RoomID == b.RoomID && sa.Start.Before(sb.End) && sb.Start.Before(sa.End) {
				add("room_double_booked", "salon %d aynı anda iki oturumda (%d, %d)", a.RoomID, i, j)
			}
		}
	}
	// kişi aynı anda iki farklı oturumda olamaz
	for pid, bs := range personBusy {
		for i := 0; i < len(bs); i++ {
			for j := i + 1; j < len(bs); j++ {
				if bs[i].session != bs[j].session && bs[i].start.Before(bs[j].end) && bs[j].start.Before(bs[i].end) {
					add("person_double_booked", "kişi %d aynı anda iki oturumda (%s/%s)", pid, bs[i].what, bs[j].what)
				}
			}
		}
	}
	// her bildiri ya programda ya da gerekçeli listede
	listed := map[int64]bool{}
	for _, u := range prog.Unscheduled {
		listed[u.PaperID] = true
		if u.Reason == "" {
			add("unexplained", "bildiri %d gerekçesiz dışarıda", u.PaperID)
		}
	}
	for id := range papers {
		if !seen[id] && !listed[id] {
			add("missing_paper", "bildiri %d ne programda ne de dışarıda bırakılanlar listesinde", id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

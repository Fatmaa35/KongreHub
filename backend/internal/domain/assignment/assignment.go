// Package assignment solves the reviewer–paper assignment problem.
//
// Model (min-cost flow):
//
//		kaynak ──(kap=ihtiyaç, 0)──► bildiri ──(kap=1, uzmanlık maliyeti)──► hakem ──(kap=1, artan yük maliyeti)×kalan──► hedef
//
//	  - Bildiri→hakem kenarı yalnızca çıkar çatışması olmayan ve uzmanlık skoru
//	    MinScore eşiğini geçen çiftler için eklenir (sert kısıtlar graf yapısıyla).
//	  - Hakem→hedef arasında kalan kapasite kadar birim kenar vardır; k. ek
//	    incelemenin maliyeti FairnessStep·(mevcutYük+k) olduğundan konveks bir
//	    yük cezası oluşur ve akış yükü kendiliğinden dengeler.
//	  - Tüm maliyetler negatif olmayan tam sayıdır → Dijkstra + potansiyeller.
//
// Ardışık en kısa yol (SSP) her artırmada bir birim akış iter; toplam akış
// ≤ 3·bildiri sayısı olduğundan 500 bildiri için ≤1500 Dijkstra çalışır.
package assignment

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
)

// Reviewer is a candidate in the pool.
type Reviewer struct {
	ID            int64
	InstitutionID int64
	Keywords      keywords.Set
	MaxLoad       int
	CurrentLoad   int
}

// Paper needs Needed more reviewers. Existing lists reviewers already on it
// (never re-picked).
type Paper struct {
	ID                   int64
	Keywords             keywords.Set
	AuthorUserIDs        []int64
	AuthorInstitutionIDs []int64
	Needed               int
	Existing             []int64
}

// Conflicts holds relationship data used for conflict-of-interest checks.
type Conflicts struct {
	coauthor map[[2]int64]struct{}
	advisor  map[[2]int64]struct{}
}

func NewConflicts() *Conflicts {
	return &Conflicts{coauthor: map[[2]int64]struct{}{}, advisor: map[[2]int64]struct{}{}}
}

func pair(a, b int64) [2]int64 {
	if a > b {
		a, b = b, a
	}
	return [2]int64{a, b}
}

// AddCoauthorship records a joint publication between two users.
func (c *Conflicts) AddCoauthorship(a, b int64) { c.coauthor[pair(a, b)] = struct{}{} }

// AddAdvisor records an advisor/student relation (direction irrelevant for COI).
func (c *Conflicts) AddAdvisor(a, b int64) { c.advisor[pair(a, b)] = struct{}{} }

// COI reason codes.
const (
	COINone            = ""
	COIIsAuthor        = "is_author"
	COISameInstitution = "same_institution"
	COICoauthor        = "coauthor"
	COIAdvisor         = "advisor"
)

// Conflict returns why reviewer r must not review paper p, or COINone.
func (c *Conflicts) Conflict(p *Paper, r *Reviewer) string {
	for _, a := range p.AuthorUserIDs {
		if a == r.ID {
			return COIIsAuthor
		}
	}
	if r.InstitutionID != 0 {
		for _, inst := range p.AuthorInstitutionIDs {
			if inst == r.InstitutionID {
				return COISameInstitution
			}
		}
	}
	for _, a := range p.AuthorUserIDs {
		if a == 0 {
			continue
		}
		if _, ok := c.coauthor[pair(a, r.ID)]; ok {
			return COICoauthor
		}
		if _, ok := c.advisor[pair(a, r.ID)]; ok {
			return COIAdvisor
		}
	}
	return COINone
}

// Options tune the solver.
type Options struct {
	MinScore     float64 // uzmanlık eşiği (0,1]; altındaki çiftler aday değildir
	FairnessStep int64   // yük dengesi ağırlığı (birim inceleme başına artan maliyet)
	ScoreScale   int64   // skor→maliyet ölçeği
}

func DefaultOptions() Options {
	return Options{MinScore: 0.2, FairnessStep: 15, ScoreScale: 1000}
}

// Pair is one decided assignment.
type Pair struct {
	PaperID    int64   `json:"paper_id"`
	ReviewerID int64   `json:"reviewer_id"`
	Score      float64 `json:"score"`
}

// Unassigned explains why a paper did not get all reviewers it needed.
type Unassigned struct {
	PaperID       int64          `json:"paper_id"`
	Needed        int            `json:"needed"`
	Assigned      int            `json:"assigned"`
	Reason        string         `json:"reason"`
	Message       string         `json:"message"`
	Qualified     int            `json:"qualified_reviewers"`
	ExcludedByCOI map[string]int `json:"excluded_by_coi"`
	LowExpertise  int            `json:"low_expertise"`
}

// Reason codes for unassigned papers.
const (
	ReasonNoExpertise = "insufficient_expertise"
	ReasonCOI         = "conflict_of_interest"
	ReasonCapacity    = "reviewer_capacity_exhausted"
)

// Stats summarises a run.
type Stats struct {
	Papers         int           `json:"papers"`
	Reviewers      int           `json:"reviewers"`
	CandidateEdges int           `json:"candidate_edges"`
	Assigned       int           `json:"assigned"`
	Requested      int           `json:"requested"`
	MeanScore      float64       `json:"mean_score"`
	LoadMin        int           `json:"load_min"`
	LoadMax        int           `json:"load_max"`
	LoadMean       float64       `json:"load_mean"`
	LoadStdDev     float64       `json:"load_stddev"`
	Duration       time.Duration `json:"-"`
	DurationMS     int64         `json:"duration_ms"`
}

// Result is the solver output.
type Result struct {
	Pairs      []Pair        `json:"assignments"`
	Unassigned []Unassigned  `json:"unassigned"`
	Stats      Stats         `json:"stats"`
	FinalLoad  map[int64]int `json:"-"`
}

// ---- min-cost flow --------------------------------------------------------

type edge struct {
	to, rev int
	cap     int
	cost    int64
}

type graph struct{ adj [][]edge }

func newGraph(n int) *graph { return &graph{adj: make([][]edge, n)} }

func (g *graph) add(u, v, cap int, cost int64) {
	g.adj[u] = append(g.adj[u], edge{to: v, rev: len(g.adj[v]), cap: cap, cost: cost})
	g.adj[v] = append(g.adj[v], edge{to: u, rev: len(g.adj[u]) - 1, cap: 0, cost: -cost})
}

type pqItem struct {
	node int
	dist int64
}
type pq []pqItem

func (q pq) Len() int { return len(q) }
func (q pq) Less(i, j int) bool {
	if q[i].dist != q[j].dist {
		return q[i].dist < q[j].dist
	}
	return q[i].node < q[j].node // deterministik eşitlik bozma
}
func (q pq) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *pq) Push(x any)   { *q = append(*q, x.(pqItem)) }
func (q *pq) Pop() any     { old := *q; it := old[len(old)-1]; *q = old[:len(old)-1]; return it }

// minCostFlow pushes up to maxFlow units from s to t; returns flow pushed.
func (g *graph) minCostFlow(s, t, maxFlow int) int {
	n := len(g.adj)
	pot := make([]int64, n) // tüm maliyetler ≥0 olduğu için başlangıç potansiyeli 0
	dist := make([]int64, n)
	prevNode := make([]int, n)
	prevEdge := make([]int, n)
	const inf = math.MaxInt64 / 4
	flow := 0
	for flow < maxFlow {
		for i := range dist {
			dist[i] = inf
		}
		dist[s] = 0
		q := &pq{{s, 0}}
		for q.Len() > 0 {
			it := heap.Pop(q).(pqItem)
			if it.dist > dist[it.node] {
				continue
			}
			u := it.node
			for i, e := range g.adj[u] {
				if e.cap <= 0 {
					continue
				}
				nd := dist[u] + e.cost + pot[u] - pot[e.to]
				if nd < dist[e.to] {
					dist[e.to] = nd
					prevNode[e.to] = u
					prevEdge[e.to] = i
					heap.Push(q, pqItem{e.to, nd})
				}
			}
		}
		if dist[t] == inf {
			break
		}
		for i := 0; i < n; i++ {
			if dist[i] < inf {
				pot[i] += dist[i]
			}
		}
		// her yol tam olarak 1 birim taşır (bildiri→hakem kapasitesi 1)
		for v := t; v != s; v = prevNode[v] {
			e := &g.adj[prevNode[v]][prevEdge[v]]
			e.cap--
			g.adj[v][e.rev].cap++
		}
		flow++
	}
	return flow
}

// ---- solver ---------------------------------------------------------------

// Solve computes assignments for all papers.
func Solve(papers []Paper, reviewers []Reviewer, cf *Conflicts, opt Options) Result {
	start := time.Now()
	if cf == nil {
		cf = NewConflicts()
	}
	if opt.ScoreScale == 0 {
		opt = DefaultOptions()
	}
	// deterministik sıra
	papers = append([]Paper(nil), papers...)
	reviewers = append([]Reviewer(nil), reviewers...)
	sort.Slice(papers, func(i, j int) bool { return papers[i].ID < papers[j].ID })
	sort.Slice(reviewers, func(i, j int) bool { return reviewers[i].ID < reviewers[j].ID })

	P, R := len(papers), len(reviewers)
	src, sink := 0, 1+P+R
	g := newGraph(P + R + 2)
	paperNode := func(i int) int { return 1 + i }
	revNode := func(j int) int { return 1 + P + j }

	type cand struct {
		j     int
		score float64
	}
	cands := make([][]cand, P)
	requested := 0
	edges := 0
	for i := range papers {
		p := &papers[i]
		if p.Needed <= 0 {
			continue
		}
		requested += p.Needed
		existing := map[int64]bool{}
		for _, id := range p.Existing {
			existing[id] = true
		}
		g.add(src, paperNode(i), p.Needed, 0)
		for j := range reviewers {
			r := &reviewers[j]
			if existing[r.ID] || r.MaxLoad-r.CurrentLoad <= 0 {
				continue
			}
			if cf.Conflict(p, r) != COINone {
				continue
			}
			sc := keywords.Coverage(p.Keywords, r.Keywords)
			if sc < opt.MinScore {
				continue
			}
			cost := int64(math.Round((1 - sc) * float64(opt.ScoreScale)))
			g.add(paperNode(i), revNode(j), 1, cost)
			cands[i] = append(cands[i], cand{j, sc})
			edges++
		}
	}
	for j := range reviewers {
		r := &reviewers[j]
		for k := 1; k <= r.MaxLoad-r.CurrentLoad; k++ {
			g.add(revNode(j), sink, 1, opt.FairnessStep*int64(r.CurrentLoad+k))
		}
	}

	g.minCostFlow(src, sink, requested)

	res := Result{FinalLoad: map[int64]int{}}
	assignedCount := make([]int, P)
	scoreSum := 0.0
	for i := range papers {
		for _, e := range g.adj[paperNode(i)] {
			if e.to >= 1+P && e.to < 1+P+R && e.cap == 0 && e.cost >= 0 {
				// ileri kenar ve kapasite tükenmiş → atanmış
				j := e.to - 1 - P
				sc := 0.0
				for _, c := range cands[i] {
					if c.j == j {
						sc = c.score
						break
					}
				}
				res.Pairs = append(res.Pairs, Pair{PaperID: papers[i].ID, ReviewerID: reviewers[j].ID, Score: round4(sc)})
				assignedCount[i]++
				scoreSum += sc
			}
		}
	}
	for j := range reviewers {
		res.FinalLoad[reviewers[j].ID] = reviewers[j].CurrentLoad
	}
	for _, pr := range res.Pairs {
		res.FinalLoad[pr.ReviewerID]++
	}

	for i := range papers {
		p := &papers[i]
		if p.Needed <= 0 || assignedCount[i] >= p.Needed {
			continue
		}
		res.Unassigned = append(res.Unassigned, explain(p, reviewers, cf, opt, assignedCount[i]))
	}

	// istatistikler
	st := Stats{Papers: P, Reviewers: R, CandidateEdges: edges, Assigned: len(res.Pairs), Requested: requested}
	if len(res.Pairs) > 0 {
		st.MeanScore = round4(scoreSum / float64(len(res.Pairs)))
	}
	if R > 0 {
		st.LoadMin = math.MaxInt
		sum := 0
		for _, r := range reviewers {
			l := res.FinalLoad[r.ID]
			sum += l
			if l < st.LoadMin {
				st.LoadMin = l
			}
			if l > st.LoadMax {
				st.LoadMax = l
			}
		}
		st.LoadMean = round4(float64(sum) / float64(R))
		v := 0.0
		for _, r := range reviewers {
			d := float64(res.FinalLoad[r.ID]) - st.LoadMean
			v += d * d
		}
		st.LoadStdDev = round4(math.Sqrt(v / float64(R)))
	}
	st.Duration = time.Since(start)
	st.DurationMS = st.Duration.Milliseconds()
	res.Stats = st
	return res
}

func explain(p *Paper, reviewers []Reviewer, cf *Conflicts, opt Options, got int) Unassigned {
	u := Unassigned{PaperID: p.ID, Needed: p.Needed, Assigned: got, ExcludedByCOI: map[string]int{}}
	existing := map[int64]bool{}
	for _, id := range p.Existing {
		existing[id] = true
	}
	expertsBlocked := 0
	for j := range reviewers {
		r := &reviewers[j]
		if existing[r.ID] {
			continue
		}
		sc := keywords.Coverage(p.Keywords, r.Keywords)
		reason := cf.Conflict(p, r)
		switch {
		case reason != COINone:
			u.ExcludedByCOI[reason]++
			if sc >= opt.MinScore {
				expertsBlocked++
			}
		case sc < opt.MinScore:
			u.LowExpertise++
		default:
			u.Qualified++
		}
	}
	missing := p.Needed - got
	switch {
	case u.Qualified >= p.Needed:
		// yeterli uzman var ama hepsinin yükü dolu
		u.Reason = ReasonCapacity
		u.Message = fmt.Sprintf("%d uygun hakem var ancak yükleri azami sınırda; %d hakem eksik", u.Qualified, missing)
	case u.Qualified+expertsBlocked >= p.Needed:
		u.Reason = ReasonCOI
		u.Message = fmt.Sprintf("uzman hakemlerden %d tanesi çıkar çatışması nedeniyle elendi; yalnızca %d uygun hakem kaldı (%d gerekli)", expertsBlocked, u.Qualified, p.Needed)
	default:
		u.Reason = ReasonNoExpertise
		u.Message = fmt.Sprintf("anahtar kelimelerle eşleşen yalnızca %d uygun hakem var (%d gerekli)", u.Qualified, p.Needed)
	}
	return u
}

// PickOne selects the single best extra reviewer for a paper (4. hakem veya
// süre aşımı yerine atama). Skor − yük cezası en yüksek, çatışmasız ve
// kapasitesi olan hakemi döner; yoksa ok=false.
func PickOne(p *Paper, reviewers []Reviewer, cf *Conflicts, opt Options) (Pair, bool) {
	if cf == nil {
		cf = NewConflicts()
	}
	existing := map[int64]bool{}
	for _, id := range p.Existing {
		existing[id] = true
	}
	best := -1
	bestVal := math.Inf(-1)
	bestScore := 0.0
	for j := range reviewers {
		r := &reviewers[j]
		if existing[r.ID] || r.CurrentLoad >= r.MaxLoad || cf.Conflict(p, r) != COINone {
			continue
		}
		sc := keywords.Coverage(p.Keywords, r.Keywords)
		if sc < opt.MinScore {
			continue
		}
		// aynı maliyet modeli: uzmanlık maliyeti + bir sonraki birimin yük maliyeti
		val := -((1-sc)*float64(opt.ScoreScale) + float64(opt.FairnessStep*int64(r.CurrentLoad+1)))
		if val > bestVal || (val == bestVal && best >= 0 && r.ID < reviewers[best].ID) {
			best, bestVal, bestScore = j, val, sc
		}
	}
	if best < 0 {
		return Pair{}, false
	}
	return Pair{PaperID: p.ID, ReviewerID: reviewers[best].ID, Score: round4(bestScore)}, true
}

// Validate independently checks a result against all hard constraints and
// returns human-readable violations (empty = valid). Testler ve make verify
// çözücüye güvenmeden bu fonksiyonu kullanır.
func Validate(papers []Paper, reviewers []Reviewer, cf *Conflicts, opt Options, pairs []Pair) []string {
	var v []string
	pm := map[int64]*Paper{}
	for i := range papers {
		pm[papers[i].ID] = &papers[i]
	}
	rm := map[int64]*Reviewer{}
	for i := range reviewers {
		rm[reviewers[i].ID] = &reviewers[i]
	}
	perPaper := map[int64]int{}
	load := map[int64]int{}
	seen := map[[2]int64]bool{}
	for _, pr := range pairs {
		p, r := pm[pr.PaperID], rm[pr.ReviewerID]
		if p == nil || r == nil {
			v = append(v, fmt.Sprintf("bilinmeyen çift %d/%d", pr.PaperID, pr.ReviewerID))
			continue
		}
		k := [2]int64{pr.PaperID, pr.ReviewerID}
		if seen[k] {
			v = append(v, fmt.Sprintf("bildiri %d hakem %d iki kez atanmış", pr.PaperID, pr.ReviewerID))
		}
		seen[k] = true
		for _, e := range p.Existing {
			if e == r.ID {
				v = append(v, fmt.Sprintf("bildiri %d: hakem %d zaten atanmıştı", p.ID, r.ID))
			}
		}
		if c := cf.Conflict(p, r); c != COINone {
			v = append(v, fmt.Sprintf("bildiri %d hakem %d: çıkar çatışması (%s)", p.ID, r.ID, c))
		}
		if sc := keywords.Coverage(p.Keywords, r.Keywords); sc < opt.MinScore {
			v = append(v, fmt.Sprintf("bildiri %d hakem %d: uzmanlık skoru %.2f < %.2f", p.ID, r.ID, sc, opt.MinScore))
		}
		perPaper[p.ID]++
		load[r.ID]++
	}
	for id, n := range perPaper {
		if n > pm[id].Needed {
			v = append(v, fmt.Sprintf("bildiri %d: %d hakem atanmış, ihtiyaç %d", id, n, pm[id].Needed))
		}
	}
	for id, n := range load {
		r := rm[id]
		if r.CurrentLoad+n > r.MaxLoad {
			v = append(v, fmt.Sprintf("hakem %d: yük %d > azami %d", id, r.CurrentLoad+n, r.MaxLoad))
		}
	}
	sort.Strings(v)
	return v
}

func round4(x float64) float64 { return math.Round(x*10000) / 10000 }

package assignment

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
)

func kw(s ...string) keywords.Set { return keywords.NewSet(s) }

// instance builds a realistic random instance: topics of keywords, reviewers
// and papers clustered on topics, institutions shared and some co-authorships.
func instance(seed uint64, nPapers, nReviewers int) ([]Paper, []Reviewer, *Conflicts) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	const topics, perTopic = 10, 6
	vocab := make([][]string, topics)
	for t := range vocab {
		for k := 0; k < perTopic; k++ {
			vocab[t] = append(vocab[t], fmt.Sprintf("t%d-k%d", t, k))
		}
	}
	pick := func(t, n int) []string {
		idx := rng.Perm(perTopic)[:n]
		out := []string{}
		for _, i := range idx {
			out = append(out, vocab[t][i])
		}
		return out
	}
	reviewers := make([]Reviewer, nReviewers)
	for j := range reviewers {
		t1, t2 := rng.IntN(topics), rng.IntN(topics)
		k := append(pick(t1, 4), pick(t2, 2)...)
		reviewers[j] = Reviewer{ID: int64(1000 + j), InstitutionID: int64(1 + rng.IntN(40)), Keywords: kw(k...), MaxLoad: 10 + rng.IntN(5)}
	}
	cf := NewConflicts()
	papers := make([]Paper, nPapers)
	for i := range papers {
		t := rng.IntN(topics)
		n := 3 + rng.IntN(2)
		authors := []int64{int64(5000 + 3*i), int64(5001 + 3*i)}
		insts := []int64{int64(1 + rng.IntN(40)), int64(1 + rng.IntN(40))}
		papers[i] = Paper{ID: int64(1 + i), Keywords: kw(pick(t, n)...), AuthorUserIDs: authors, AuthorInstitutionIDs: insts, Needed: 3}
		if rng.IntN(5) == 0 {
			cf.AddCoauthorship(authors[0], reviewers[rng.IntN(nReviewers)].ID)
		}
		if rng.IntN(20) == 0 {
			cf.AddAdvisor(reviewers[rng.IntN(nReviewers)].ID, authors[1])
		}
	}
	return papers, reviewers, cf
}

func TestConflictDetection(t *testing.T) {
	cf := NewConflicts()
	cf.AddCoauthorship(10, 20)
	cf.AddAdvisor(30, 11)
	p := &Paper{ID: 1, AuthorUserIDs: []int64{10, 11, 99}, AuthorInstitutionIDs: []int64{7}}
	cases := []struct {
		r    Reviewer
		want string
	}{
		{Reviewer{ID: 99, InstitutionID: 1}, COIIsAuthor},
		{Reviewer{ID: 50, InstitutionID: 7}, COISameInstitution},
		{Reviewer{ID: 20, InstitutionID: 2}, COICoauthor},
		{Reviewer{ID: 30, InstitutionID: 3}, COIAdvisor},
		{Reviewer{ID: 40, InstitutionID: 4}, COINone},
	}
	for _, c := range cases {
		if got := cf.Conflict(p, &c.r); got != c.want {
			t.Errorf("reviewer %d: got %q want %q", c.r.ID, got, c.want)
		}
	}
}

func TestSolveSmallRespectsAllConstraints(t *testing.T) {
	cf := NewConflicts()
	cf.AddCoauthorship(100, 4)
	papers := []Paper{
		{ID: 1, Keywords: kw("ml", "vision"), AuthorUserIDs: []int64{100}, AuthorInstitutionIDs: []int64{1}, Needed: 3},
		{ID: 2, Keywords: kw("ml", "nlp"), AuthorUserIDs: []int64{101}, AuthorInstitutionIDs: []int64{2}, Needed: 3},
	}
	reviewers := []Reviewer{
		{ID: 1, InstitutionID: 1, Keywords: kw("ml", "vision"), MaxLoad: 2}, // aynı kurum → bildiri 1 hariç
		{ID: 2, InstitutionID: 3, Keywords: kw("ml", "vision", "nlp"), MaxLoad: 2},
		{ID: 3, InstitutionID: 4, Keywords: kw("ml", "nlp"), MaxLoad: 2},
		{ID: 4, InstitutionID: 5, Keywords: kw("vision"), MaxLoad: 2}, // ortak yayın → bildiri 1 hariç
		{ID: 5, InstitutionID: 6, Keywords: kw("ml"), MaxLoad: 2},
		{ID: 6, InstitutionID: 7, Keywords: kw("nlp"), MaxLoad: 1},
	}
	opt := DefaultOptions()
	res := Solve(papers, reviewers, cf, opt)
	if v := Validate(papers, reviewers, cf, opt, res.Pairs); len(v) > 0 {
		t.Fatalf("violations: %v", v)
	}
	if len(res.Pairs) != 6 || len(res.Unassigned) != 0 {
		t.Fatalf("expected 6 pairs, got %d (unassigned %v)", len(res.Pairs), res.Unassigned)
	}
	for _, p := range res.Pairs {
		if p.PaperID == 1 && (p.ReviewerID == 1 || p.ReviewerID == 4) {
			t.Fatalf("COI reviewer %d assigned to paper 1", p.ReviewerID)
		}
	}
}

func TestUnassignedReasons(t *testing.T) {
	cf := NewConflicts()
	cf.AddCoauthorship(200, 3)
	papers := []Paper{
		{ID: 1, Keywords: kw("quantum biology"), AuthorUserIDs: []int64{100}, Needed: 3},                      // kimse uzman değil
		{ID: 2, Keywords: kw("ml"), AuthorUserIDs: []int64{200}, AuthorInstitutionIDs: []int64{9}, Needed: 3}, // uzmanlar çatışmalı
	}
	reviewers := []Reviewer{
		{ID: 1, InstitutionID: 9, Keywords: kw("ml"), MaxLoad: 5},
		{ID: 2, InstitutionID: 9, Keywords: kw("ml"), MaxLoad: 5},
		{ID: 3, InstitutionID: 1, Keywords: kw("ml"), MaxLoad: 5},
		{ID: 4, InstitutionID: 2, Keywords: kw("ml"), MaxLoad: 5},
	}
	res := Solve(papers, reviewers, cf, DefaultOptions())
	if len(res.Unassigned) != 2 {
		t.Fatalf("want 2 unassigned, got %+v", res.Unassigned)
	}
	byID := map[int64]Unassigned{}
	for _, u := range res.Unassigned {
		byID[u.PaperID] = u
	}
	if byID[1].Reason != ReasonNoExpertise || byID[1].Assigned != 0 {
		t.Errorf("paper1: %+v", byID[1])
	}
	if byID[2].Reason != ReasonCOI || byID[2].Assigned != 1 || byID[2].ExcludedByCOI[COISameInstitution] != 2 || byID[2].ExcludedByCOI[COICoauthor] != 1 {
		t.Errorf("paper2: %+v", byID[2])
	}
	if byID[2].Message == "" {
		t.Error("message must explain reason")
	}
}

func TestCapacityReason(t *testing.T) {
	papers := []Paper{
		{ID: 1, Keywords: kw("ml"), Needed: 3},
		{ID: 2, Keywords: kw("ml"), Needed: 3},
	}
	reviewers := []Reviewer{
		{ID: 1, Keywords: kw("ml"), MaxLoad: 1},
		{ID: 2, Keywords: kw("ml"), MaxLoad: 1},
		{ID: 3, Keywords: kw("ml"), MaxLoad: 2},
	}
	res := Solve(papers, reviewers, nil, DefaultOptions())
	if res.Stats.Assigned != 4 {
		t.Fatalf("total capacity is 4, got %d", res.Stats.Assigned)
	}
	if len(res.Unassigned) == 0 || res.Unassigned[0].Reason != ReasonCapacity {
		t.Fatalf("expected capacity reason, got %+v", res.Unassigned)
	}
}

func TestExistingReviewersNotRepicked(t *testing.T) {
	papers := []Paper{{ID: 1, Keywords: kw("ml"), Needed: 1, Existing: []int64{1, 2}}}
	reviewers := []Reviewer{
		{ID: 1, Keywords: kw("ml"), MaxLoad: 5, CurrentLoad: 1},
		{ID: 2, Keywords: kw("ml"), MaxLoad: 5, CurrentLoad: 1},
		{ID: 3, Keywords: kw("ml"), MaxLoad: 5},
	}
	res := Solve(papers, reviewers, nil, DefaultOptions())
	if len(res.Pairs) != 1 || res.Pairs[0].ReviewerID != 3 {
		t.Fatalf("got %+v", res.Pairs)
	}
}

func TestFairnessBalancesLoad(t *testing.T) {
	// 12 bildiri × 3 = 36 inceleme, 6 eşit uzman hakem → ideal yük 6
	var papers []Paper
	for i := 1; i <= 12; i++ {
		papers = append(papers, Paper{ID: int64(i), Keywords: kw("ml"), Needed: 3})
	}
	var reviewers []Reviewer
	for j := 1; j <= 6; j++ {
		reviewers = append(reviewers, Reviewer{ID: int64(j), Keywords: kw("ml"), MaxLoad: 20})
	}
	res := Solve(papers, reviewers, nil, DefaultOptions())
	if res.Stats.LoadMin != 6 || res.Stats.LoadMax != 6 {
		t.Fatalf("expected perfectly balanced load 6, got min=%d max=%d", res.Stats.LoadMin, res.Stats.LoadMax)
	}
}

func TestPreferHigherExpertise(t *testing.T) {
	papers := []Paper{{ID: 1, Keywords: kw("a", "b", "c", "d"), Needed: 1}}
	reviewers := []Reviewer{
		{ID: 1, Keywords: kw("a"), MaxLoad: 5},
		{ID: 2, Keywords: kw("a", "b", "c", "d"), MaxLoad: 5},
	}
	res := Solve(papers, reviewers, nil, DefaultOptions())
	if len(res.Pairs) != 1 || res.Pairs[0].ReviewerID != 2 || res.Pairs[0].Score != 1 {
		t.Fatalf("expected best expert, got %+v", res.Pairs)
	}
}

func TestDeterministic(t *testing.T) {
	p, r, cf := instance(7, 120, 40)
	a := Solve(p, r, cf, DefaultOptions())
	b := Solve(p, r, cf, DefaultOptions())
	if fmt.Sprint(a.Pairs) != fmt.Sprint(b.Pairs) {
		t.Fatal("same input must give same output")
	}
}

// greedy baseline: her bildiriye sırayla en iyi skorlu müsait hakem.
func greedy(papers []Paper, reviewers []Reviewer, cf *Conflicts, opt Options) []Pair {
	load := map[int64]int{}
	var out []Pair
	for i := range papers {
		p := &papers[i]
		ex := append([]int64(nil), p.Existing...)
		for n := 0; n < p.Needed; n++ {
			tmp := *p
			tmp.Existing = ex
			rs := make([]Reviewer, len(reviewers))
			copy(rs, reviewers)
			for k := range rs {
				rs[k].CurrentLoad += load[rs[k].ID]
			}
			pr, ok := PickOne(&tmp, rs, cf, Options{MinScore: opt.MinScore, ScoreScale: opt.ScoreScale, FairnessStep: 0})
			if !ok {
				break
			}
			out = append(out, pr)
			load[pr.ReviewerID]++
			ex = append(ex, pr.ReviewerID)
		}
	}
	return out
}

// T-1 kanıtı: 500 bildiri / 150 hakem, 20 sn sınırı, tüm kısıtlar bağımsız doğrulayıcıyla.
func TestScale500x150(t *testing.T) {
	papers, reviewers, cf := instance(42, 500, 150)
	opt := DefaultOptions()
	start := time.Now()
	res := Solve(papers, reviewers, cf, opt)
	el := time.Since(start)
	if el > 20*time.Second {
		t.Fatalf("too slow: %v", el)
	}
	if v := Validate(papers, reviewers, cf, opt, res.Pairs); len(v) > 0 {
		t.Fatalf("%d violations, first: %s", len(v), v[0])
	}
	covered := 0
	for _, u := range res.Unassigned {
		covered += u.Assigned
		if u.Reason == "" || u.Message == "" {
			t.Fatalf("unassigned paper %d lacks reason", u.PaperID)
		}
	}
	g := greedy(papers, reviewers, cf, opt)
	gs := 0.0
	for _, p := range g {
		gs += p.Score
	}
	t.Logf("min-cost flow: %d/%d assigned in %v, mean score %.3f, load %d..%d σ=%.2f, unassigned papers=%d; greedy: %d pairs mean %.3f",
		res.Stats.Assigned, res.Stats.Requested, el, res.Stats.MeanScore, res.Stats.LoadMin, res.Stats.LoadMax, res.Stats.LoadStdDev,
		len(res.Unassigned), len(g), gs/float64(len(g)))
	if res.Stats.Assigned < len(g) {
		t.Fatalf("flow (%d) should assign at least as many as greedy (%d)", res.Stats.Assigned, len(g))
	}
}

func TestValidateCatchesViolations(t *testing.T) {
	cf := NewConflicts()
	papers := []Paper{{ID: 1, Keywords: kw("ml"), AuthorInstitutionIDs: []int64{1}, Needed: 1}}
	reviewers := []Reviewer{
		{ID: 1, InstitutionID: 1, Keywords: kw("ml"), MaxLoad: 1},
		{ID: 2, InstitutionID: 2, Keywords: kw("art"), MaxLoad: 1},
	}
	bad := []Pair{{PaperID: 1, ReviewerID: 1}, {PaperID: 1, ReviewerID: 2}, {PaperID: 1, ReviewerID: 2}, {PaperID: 9, ReviewerID: 1}}
	v := Validate(papers, reviewers, cf, DefaultOptions(), bad)
	if len(v) < 5 {
		t.Fatalf("expected COI, low-score, duplicate, over-need, over-load and unknown violations, got %v", v)
	}
}

func TestPickOne(t *testing.T) {
	p := &Paper{ID: 1, Keywords: kw("ml", "nlp"), Existing: []int64{1}}
	rs := []Reviewer{
		{ID: 1, Keywords: kw("ml", "nlp"), MaxLoad: 5},
		{ID: 2, Keywords: kw("ml", "nlp"), MaxLoad: 5, CurrentLoad: 5},
		{ID: 3, Keywords: kw("ml"), MaxLoad: 5},
		{ID: 4, Keywords: kw("ml", "nlp"), MaxLoad: 5, CurrentLoad: 2},
	}
	got, ok := PickOne(p, rs, nil, DefaultOptions())
	if !ok || got.ReviewerID != 4 {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if _, ok := PickOne(&Paper{ID: 2, Keywords: kw("zzz")}, rs, nil, DefaultOptions()); ok {
		t.Fatal("no expert should be found")
	}
}

func BenchmarkSolve500x150(b *testing.B) {
	papers, reviewers, cf := instance(42, 500, 150)
	for i := 0; i < b.N; i++ {
		Solve(papers, reviewers, cf, DefaultOptions())
	}
}

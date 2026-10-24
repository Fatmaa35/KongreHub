// Package seed generates the deterministic demo dataset.
//
// Generate(tohum, referans zamanı) saf bir fonksiyondur: aynı tohum ve
// referansla bit bit aynı veri kümesini üretir (Hash ile kanıtlanır).
// Zaman damgaları referans zamana görelidir; böylece veri her kurulumda
// "güncel" görünür, içerik ise tohuma bağlı kalır.
package seed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/fatmakaplan/kongrehub/internal/domain/assignment"
	"github.com/fatmakaplan/kongrehub/internal/domain/keywords"
	"github.com/fatmakaplan/kongrehub/internal/domain/pricing"
	"github.com/fatmakaplan/kongrehub/internal/domain/review"
)

// DemoPassword is shared by every seeded account (README'de listelenir).
const DemoPassword = "Demo123!"

// Demo account e-mails (her rol için bir hesap).
const (
	DemoAdmin     = "admin@kongrehub.test"
	DemoEditor    = "editor@kongrehub.test"
	DemoRegistrar = "registrar@kongrehub.test"
	DemoAuthor    = "author@kongrehub.test"
	DemoReviewer  = "reviewer@kongrehub.test"
	DemoAttendee  = "attendee@kongrehub.test"
)

// Counts of the generated dataset.
const (
	NumSubmissions = 500
	NumReviewers   = 150
	NumAuthors     = 700
	NumAttendees   = 220
	NumInstitution = 40
)

type Institution struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
}

type User struct {
	ID            int64  `json:"id"`
	Email         string `json:"email"`
	FullName      string `json:"full_name"`
	Role          string `json:"role"`
	InstitutionID int64  `json:"institution_id"`
	Timezone      string `json:"timezone"`
	AvailStart    int    `json:"avail_start"`
	AvailEnd      int    `json:"avail_end"`
	IsStudent     bool   `json:"is_student"`
	IsMember      bool   `json:"is_member"`
	Demo          bool   `json:"demo"`
}

type ReviewerProfile struct {
	UserID   int64    `json:"user_id"`
	Keywords []string `json:"keywords"`
	MaxLoad  int      `json:"max_load"`
	Late     bool     `json:"late"` // bilerek geç yanıt veren hakem
}

type Pair struct {
	A, B  int64
	Title string
	Year  int
}

type Author struct {
	UserID        int64  `json:"user_id"`
	FullName      string `json:"full_name"`
	Email         string `json:"email"`
	InstitutionID int64  `json:"institution_id"`
	IsPresenter   bool   `json:"is_presenter"`
}

type Submission struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Abstract    string    `json:"abstract"`
	Keywords    []string  `json:"keywords"`
	SubmitterID int64     `json:"submitter_id"`
	Authors     []Author  `json:"authors"`
	Duration    int       `json:"duration"`
	Audience    int       `json:"audience"`
	Status      string    `json:"status"`
	Round       int       `json:"round"`
	CreatedAt   time.Time `json:"created_at"`
	Disagree    bool      `json:"disagree"`
	FinalScore  *float64  `json:"final_score"`
}

type Assignment struct {
	ID           int64     `json:"id"`
	SubmissionID int64     `json:"submission_id"`
	ReviewerID   int64     `json:"reviewer_id"`
	Round        int       `json:"round"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	Score        float64   `json:"score"`
	AssignedAt   time.Time `json:"assigned_at"`
}

type Review struct {
	AssignmentID   int64     `json:"assignment_id"`
	Scores         review.Scores `json:"scores"`
	Recommendation string    `json:"recommendation"`
	Comment        string    `json:"comment"`
	SubmittedAt    time.Time `json:"submitted_at"`
}

type Decision struct {
	SubmissionID int64     `json:"submission_id"`
	Round        int       `json:"round"`
	Decision     string    `json:"decision"`
	Note         string    `json:"note"`
	At           time.Time `json:"at"`
}

type Room struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

type Slot struct {
	ID    int64     `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type FX struct {
	Rate string    `json:"rate"`
	At   time.Time `json:"at"`
}

type Registration struct {
	ID        int64          `json:"id"`
	UserID    int64          `json:"user_id"`
	GroupID   int64          `json:"group_id"`
	Currency  string         `json:"currency"`
	Quote     pricing.Quote  `json:"quote"`
	Status    string         `json:"status"` // pending | paid | refunded
	CreatedAt time.Time      `json:"created_at"`
	PaidAt    *time.Time     `json:"paid_at"`
	Refund    *pricing.RefundRule `json:"refund"`
}

type Group struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int    `json:"size"`
}

// Dataset is the full generated content.
type Dataset struct {
	Seed          uint64            `json:"seed"`
	Institutions  []Institution     `json:"institutions"`
	Users         []User            `json:"users"`
	Reviewers     []ReviewerProfile `json:"reviewers"`
	Coauthorships []Pair            `json:"coauthorships"`
	Advisors      [][2]int64        `json:"advisors"`
	Submissions   []Submission      `json:"submissions"`
	Assignments   []Assignment      `json:"assignments"`
	Reviews       []Review          `json:"reviews"`
	Decisions     []Decision        `json:"decisions"`
	Rooms         []Room            `json:"rooms"`
	Slots         []Slot            `json:"slots"`
	FXRates       []FX              `json:"fx_rates"`
	Groups        []Group           `json:"groups"`
	Registrations []Registration    `json:"registrations"`
}

// Hash fingerprints the dataset (aynı tohum → aynı özet).
func (d *Dataset) Hash() string {
	b, _ := json.Marshal(d)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ---- sözlükler -------------------------------------------------------------------

var topics = []struct {
	Name     string
	Keywords []string
}{
	{"machine learning", []string{"deep learning", "reinforcement learning", "graph neural networks", "federated learning", "explainable ai", "transfer learning"}},
	{"computer vision", []string{"image segmentation", "object detection", "medical imaging", "3d reconstruction", "video understanding", "remote sensing"}},
	{"natural language processing", []string{"large language models", "machine translation", "turkish nlp", "speech recognition", "information extraction", "sentiment analysis"}},
	{"distributed systems", []string{"consensus protocols", "cloud computing", "edge computing", "stream processing", "microservices", "fault tolerance"}},
	{"databases", []string{"query optimization", "transaction processing", "time series databases", "data lakes", "indexing", "graph databases"}},
	{"security", []string{"cryptography", "intrusion detection", "privacy", "blockchain", "malware analysis", "access control"}},
	{"bioinformatics", []string{"genomics", "protein folding", "drug discovery", "single-cell analysis", "epidemiology", "phylogenetics"}},
	{"robotics", []string{"motion planning", "slam", "swarm robotics", "human-robot interaction", "autonomous vehicles", "manipulation"}},
	{"human-computer interaction", []string{"accessibility", "user studies", "visualization", "augmented reality", "education technology", "usability"}},
	{"energy systems", []string{"smart grids", "renewable forecasting", "battery management", "demand response", "power electronics", "energy markets"}},
}

var firstNames = []string{"Ayşe", "Mehmet", "Zeynep", "Mustafa", "Elif", "Ahmet", "Merve", "Emre", "Selin", "Burak", "Deniz", "Can", "Ece", "Kerem",
	"Defne", "Onur", "İrem", "Tolga", "Gizem", "Barış", "Nazlı", "Serkan", "Pınar", "Cem", "Hande", "Oğuz", "Büşra", "Kaan", "Derya", "Umut",
	"Anna", "Lukas", "Sofia", "Mateo", "Hiro", "Yuki", "Wei", "Mei", "Liam", "Olivia", "Noah", "Emma", "Ravi", "Priya", "Omar", "Layla",
	"Jonas", "Clara", "Diego", "Lucía", "Kenji", "Aiko", "Ivan", "Nadia", "Pierre", "Camille", "Erik", "Ingrid", "Marco", "Giulia"}

var lastNames = []string{"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir", "Arslan", "Doğan",
	"Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek", "Polat", "Korkmaz", "Erdem", "Güneş", "Aksoy", "Tekin", "Ünal", "Bulut",
	"Müller", "Schmidt", "Rossi", "García", "Tanaka", "Suzuki", "Chen", "Wang", "Smith", "Johnson", "Patel", "Singh", "Haddad", "Novak",
	"Andersen", "Dubois", "Kowalski", "Silva", "Nakamura", "Petrov", "Larsen", "Moreau", "Bianchi", "Fischer", "Lopez", "Kim", "Park", "Ali"}

var instPrefix = []string{"Marmara", "Anadolu", "Ege", "Karadeniz", "Toros", "Kapadokya", "Fırat", "Uludağ", "Akdeniz", "Trakya",
	"Nordic", "Alpine", "Baltic", "Pacific", "Atlantic", "Danube", "Andes", "Kyoto Bay", "Rhine", "Iberian"}
var instSuffix = []string{"Teknik Üniversitesi", "Bilim Enstitüsü", "Institute of Technology", "University"}

var countries = map[string]string{"Teknik Üniversitesi": "TR", "Bilim Enstitüsü": "TR", "Institute of Technology": "INT", "University": "INT"}

var remoteZones = []string{"Asia/Tokyo", "America/New_York", "America/Los_Angeles", "Europe/London", "Europe/Berlin", "Australia/Sydney", "Asia/Shanghai"}

var methods = []string{"A Scalable Approach to", "Rethinking", "Towards Robust", "Efficient", "Learning-Based", "A Benchmark for",
	"Privacy-Preserving", "Adaptive", "Interpretable", "Hybrid Methods for", "An Empirical Study of", "Lightweight"}

func titleCase(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		if len(p) > 3 || i == 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func slug(s string) string {
	r := strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "ö", "o", "ş", "s", "ü", "u", "İ", "i", "Ç", "c", "Ğ", "g", "Ö", "o", "Ş", "s", "Ü", "u", "í", "i", "á", "a", "é", "e", " ", "-")
	return strings.ToLower(r.Replace(s))
}

// ---- üretim --------------------------------------------------------------------------

// Generate builds the dataset for the given seed and reference time.
func Generate(seedVal uint64, ref time.Time) *Dataset {
	ref = ref.UTC().Truncate(time.Hour)
	rng := rand.New(rand.NewPCG(seedVal, seedVal*0x9E3779B97F4A7C15+1))
	d := &Dataset{Seed: seedVal}

	// kurumlar
	used := map[string]bool{}
	for len(d.Institutions) < NumInstitution {
		pre := instPrefix[rng.IntN(len(instPrefix))]
		suf := instSuffix[rng.IntN(len(instSuffix))]
		name := pre + " " + suf
		if used[name] {
			continue
		}
		used[name] = true
		d.Institutions = append(d.Institutions, Institution{ID: int64(len(d.Institutions) + 1), Name: name, Country: countries[suf]})
	}
	instDomain := func(id int64) string { return slug(d.Institutions[id-1].Name) + ".example" }

	var nextUser int64 = 1
	usedEmail := map[string]bool{}
	newUser := func(role string, inst int64, tz string) *User {
		var fn, ln, email string
		for {
			fn = firstNames[rng.IntN(len(firstNames))]
			ln = lastNames[rng.IntN(len(lastNames))]
			email = fmt.Sprintf("%s.%s@%s", slug(fn), slug(ln), instDomain(inst))
			if !usedEmail[email] {
				break
			}
			email = fmt.Sprintf("%s.%s%d@%s", slug(fn), slug(ln), nextUser, instDomain(inst))
			if !usedEmail[email] {
				break
			}
		}
		usedEmail[email] = true
		u := User{ID: nextUser, Email: email, FullName: fn + " " + ln, Role: role, InstitutionID: inst, Timezone: tz, AvailStart: 8, AvailEnd: 22}
		nextUser++
		d.Users = append(d.Users, u)
		return &d.Users[len(d.Users)-1]
	}
	demo := func(email, name, role string, inst int64) int64 {
		u := User{ID: nextUser, Email: email, FullName: name, Role: role, InstitutionID: inst, Timezone: "Europe/Istanbul", AvailStart: 8, AvailEnd: 22, Demo: true}
		usedEmail[email] = true
		nextUser++
		d.Users = append(d.Users, u)
		return u.ID
	}
	adminID := demo(DemoAdmin, "Yönetim Hesabı", "admin", 1)
	editorID := demo(DemoEditor, "Prof. Dr. Leyla Editör", "editor", 2)
	_ = demo(DemoRegistrar, "Kayıt Görevlisi Ali", "registrar", 1)
	demoAuthorID := demo(DemoAuthor, "Dr. Fatma Yazar", "author", 3)
	demoReviewerID := demo(DemoReviewer, "Doç. Dr. Hakan Hakem", "reviewer", 4)
	demoAttendeeID := demo(DemoAttendee, "Katılımcı Selin", "attendee", 5)
	_, _ = adminID, editorID
	// ek editörler (oturum başkanı adayları)
	for i := 0; i < 5; i++ {
		newUser("editor", int64(1+rng.IntN(NumInstitution)), "Europe/Istanbul")
	}

	// hakemler (demo hakem dahil 150)
	reviewerIDs := []int64{demoReviewerID}
	for len(reviewerIDs) < NumReviewers {
		u := newUser("reviewer", int64(1+rng.IntN(NumInstitution)), "Europe/Istanbul")
		reviewerIDs = append(reviewerIDs, u.ID)
	}
	for i, id := range reviewerIDs {
		t1 := rng.IntN(len(topics))
		t2 := rng.IntN(len(topics))
		kw := []string{}
		for _, k := range rng.Perm(6)[:4] {
			kw = append(kw, topics[t1].Keywords[k])
		}
		for _, k := range rng.Perm(6)[:2] {
			kw = append(kw, topics[t2].Keywords[k])
		}
		if i == 0 { // demo hakem: makine öğrenmesi + bilgisayarlı görü
			kw = []string{"deep learning", "transfer learning", "explainable ai", "graph neural networks", "medical imaging", "object detection"}
		}
		d.Reviewers = append(d.Reviewers, ReviewerProfile{UserID: id, Keywords: keywords.Normalize(kw), MaxLoad: 10 + rng.IntN(5), Late: i > 0 && i%11 == 0})
	}
	// niş konu: yalnızca 3 hakem "underwater acoustics" biliyor (çıkar çatışması senaryosu)
	nicheReviewers := []int{20, 21, 22}
	for _, idx := range nicheReviewers {
		d.Reviewers[idx].Keywords = keywords.Normalize(append(d.Reviewers[idx].Keywords, "underwater acoustics"))
	}

	// yazarlar
	authorIDs := []int64{demoAuthorID}
	for len(authorIDs) < NumAuthors {
		tz := "Europe/Istanbul"
		if rng.IntN(100) < 18 {
			tz = remoteZones[rng.IntN(len(remoteZones))]
		}
		u := newUser("author", int64(1+rng.IntN(NumInstitution)), tz)
		if rng.IntN(5) == 0 {
			u.IsStudent = true
		}
		if rng.IntN(4) == 0 {
			u.IsMember = true
		}
		authorIDs = append(authorIDs, u.ID)
	}
	// katılımcılar
	attendeeIDs := []int64{}
	for len(attendeeIDs) < NumAttendees {
		u := newUser("attendee", int64(1+rng.IntN(NumInstitution)), "Europe/Istanbul")
		u.IsStudent = rng.IntN(3) == 0
		u.IsMember = rng.IntN(3) == 0
		attendeeIDs = append(attendeeIDs, u.ID)
	}
	userByID := map[int64]*User{}
	for i := range d.Users {
		userByID[d.Users[i].ID] = &d.Users[i]
	}

	// ortak yayınlar ve danışmanlık (çıkar çatışması verisi)
	seenPair := map[[2]int64]bool{}
	for _, rid := range reviewerIDs {
		n := 3 + rng.IntN(6)
		for k := 0; k < n; k++ {
			aid := authorIDs[rng.IntN(len(authorIDs))]
			a, b := min(rid, aid), max(rid, aid)
			if seenPair[[2]int64{a, b}] {
				continue
			}
			seenPair[[2]int64{a, b}] = true
			t := topics[rng.IntN(len(topics))]
			d.Coauthorships = append(d.Coauthorships, Pair{A: a, B: b, Title: titleCase(methods[rng.IntN(len(methods))] + " " + t.Keywords[rng.IntN(6)]), Year: 2015 + rng.IntN(11)})
		}
	}
	for k := 0; k < 40; k++ {
		adv := reviewerIDs[rng.IntN(len(reviewerIDs))]
		st := authorIDs[1+rng.IntN(len(authorIDs)-1)]
		d.Advisors = append(d.Advisors, [2]int64{adv, st})
	}
	sort.Slice(d.Advisors, func(i, j int) bool {
		if d.Advisors[i][0] != d.Advisors[j][0] {
			return d.Advisors[i][0] < d.Advisors[j][0]
		}
		return d.Advisors[i][1] < d.Advisors[j][1]
	})
	d.Advisors = dedupe(d.Advisors)

	// bildiriler
	for i := 0; i < NumSubmissions; i++ {
		t := rng.IntN(len(topics))
		nk := 3 + rng.IntN(2)
		kw := []string{}
		for _, k := range rng.Perm(6)[:nk] {
			kw = append(kw, topics[t].Keywords[k])
		}
		if rng.IntN(4) == 0 {
			t2 := rng.IntN(len(topics))
			kw = append(kw, topics[t2].Keywords[rng.IntN(6)])
		}
		na := 2 + rng.IntN(3)
		authorSet := map[int64]bool{}
		var authors []Author
		for len(authors) < na {
			aid := authorIDs[1+rng.IntN(len(authorIDs)-1)]
			if authorSet[aid] {
				continue
			}
			authorSet[aid] = true
			u := userByID[aid]
			authors = append(authors, Author{UserID: aid, FullName: u.FullName, Email: u.Email, InstitutionID: u.InstitutionID})
		}
		authors[rng.IntN(len(authors))].IsPresenter = true
		title := titleCase(fmt.Sprintf("%s %s in %s", methods[rng.IntN(len(methods))], kw[0], kw[len(kw)-1]))
		abstract := fmt.Sprintf("We study %s with a focus on %s. Our method combines ideas from %s and is evaluated on public benchmarks, "+
			"showing consistent improvements over strong baselines while remaining reproducible. Code and data are released.", kw[0], strings.Join(kw[1:], ", "), topics[t].Name)
		s := Submission{ID: int64(i + 1), Title: title, Abstract: abstract, Keywords: keywords.Normalize(kw), SubmitterID: authors[0].UserID, Authors: authors,
			Duration: []int{15, 20, 20, 20, 30}[rng.IntN(5)], Audience: 20 + rng.IntN(160) + int(30*math.Sin(float64(t))), Status: "submitted", Round: 1,
			CreatedAt: ref.Add(-time.Duration(40+rng.IntN(30)) * 24 * time.Hour).Add(time.Duration(rng.IntN(1440)) * time.Minute)}
		if s.Audience < 15 {
			s.Audience = 15
		}
		d.Submissions = append(d.Submissions, s)
	}
	// özel senaryolar: atanamayan bildiriler
	niche := func(i int, kw []string, title string) {
		s := &d.Submissions[i]
		s.Keywords = keywords.Normalize(kw)
		s.Title = title
	}
	niche(497, []string{"quantum gravity", "string cosmology"}, "Holographic Entanglement in String Cosmology")
	niche(498, []string{"medieval paleography"}, "Automated Dating of Medieval Manuscripts by Script Style")
	niche(499, []string{"underwater acoustics"}, "Marine Mammal Call Classification from Underwater Acoustics")
	// 499: uzman 3 hakemden ikisi çatışmalı → yalnızca 1 uygun hakem
	s499 := &d.Submissions[499]
	nr0, nr1 := d.Reviewers[nicheReviewers[0]].UserID, d.Reviewers[nicheReviewers[1]].UserID
	s499.Authors[0].InstitutionID = userByID[nr0].InstitutionID // aynı kurum
	d.Coauthorships = append(d.Coauthorships, Pair{A: min(nr1, s499.Authors[1].UserID), B: max(nr1, s499.Authors[1].UserID), Title: "Passive Acoustic Monitoring Revisited", Year: 2023})
	// demo yazarın bildirileri: kabul (0), düzeltme (100), incelemede (150), gönderildi (200)
	for _, idx := range []int{0, 100, 150, 200} {
		s := &d.Submissions[idx]
		du := userByID[demoAuthorID]
		s.Authors[0] = Author{UserID: demoAuthorID, FullName: du.FullName, Email: du.Email, InstitutionID: du.InstitutionID, IsPresenter: s.Authors[0].IsPresenter}
		s.SubmitterID = demoAuthorID
		if idx == 0 {
			for k := range s.Authors {
				s.Authors[k].IsPresenter = k == 0
			}
		}
	}
	sort.Slice(d.Coauthorships, func(i, j int) bool {
		a, b := d.Coauthorships[i], d.Coauthorships[j]
		if a.A != b.A {
			return a.A < b.A
		}
		if a.B != b.B {
			return a.B < b.B
		}
		return a.Title < b.Title
	})

	// ---- tarihsel değerlendirme süreci ----
	// 0..89 kabul, 90..109 ret, 100..109 düzeltme (demo 100 dahil), 110..159 incelemede, geri kalanı yeni
	state := func(i int) string {
		switch {
		case i < 90:
			return "accepted"
		case i < 100:
			return "rejected"
		case i < 110:
			return "revision_requested"
		case i < 160:
			return "under_review"
		}
		return "submitted"
	}
	cf := assignment.NewConflicts()
	for _, p := range d.Coauthorships {
		cf.AddCoauthorship(p.A, p.B)
	}
	for _, p := range d.Advisors {
		cf.AddAdvisor(p[0], p[1])
	}
	var hist []assignment.Paper
	for i := 0; i < 160; i++ {
		s := d.Submissions[i]
		p := assignment.Paper{ID: s.ID, Keywords: keywords.NewSet(s.Keywords), Needed: 3}
		for _, a := range s.Authors {
			p.AuthorUserIDs = append(p.AuthorUserIDs, a.UserID)
			p.AuthorInstitutionIDs = append(p.AuthorInstitutionIDs, a.InstitutionID)
		}
		hist = append(hist, p)
	}
	pool := make([]assignment.Reviewer, len(d.Reviewers))
	for i, r := range d.Reviewers {
		pool[i] = assignment.Reviewer{ID: r.UserID, InstitutionID: userByID[r.UserID].InstitutionID, Keywords: keywords.NewSet(r.Keywords), MaxLoad: 4}
	}
	res := assignment.Solve(hist, pool, cf, assignment.DefaultOptions())
	byPaper := map[int64][]assignment.Pair{}
	for _, pr := range res.Pairs {
		byPaper[pr.PaperID] = append(byPaper[pr.PaperID], pr)
	}
	// demo hakemi incelemedeki 3 bildiriye yerleştir
	demoSlots := 0
	for i := 110; i < 160 && demoSlots < 3; i++ {
		p := hist[i]
		rv := pool[0]
		if cf.Conflict(&p, &rv) != assignment.COINone || keywords.Coverage(p.Keywords, rv.Keywords) < 0.2 {
			continue
		}
		prs := byPaper[p.ID]
		if len(prs) == 0 {
			continue
		}
		dup := false
		for _, x := range prs {
			if x.ReviewerID == demoReviewerID {
				dup = true
			}
		}
		if dup {
			continue
		}
		prs[0] = assignment.Pair{PaperID: p.ID, ReviewerID: demoReviewerID, Score: keywords.Coverage(p.Keywords, rv.Keywords)}
		demoSlots++
	}
	lateByID := map[int64]bool{}
	for _, r := range d.Reviewers {
		lateByID[r.UserID] = r.Late
	}
	var nextAssign int64 = 1
	for i := 0; i < 160; i++ {
		s := &d.Submissions[i]
		st := state(i)
		prs := byPaper[s.ID]
		if len(prs) < 3 {
			st = "submitted" // tarihsel atama yapılamadıysa yeni bildiri olarak kalsın
			s.Status = st
			continue
		}
		s.Status = st
		assignedAt := s.CreatedAt.Add(time.Duration(5+rng.IntN(5)) * 24 * time.Hour)
		quality := 3.0 + rng.Float64()*6.5
		switch st {
		case "accepted":
			quality = 7 + rng.Float64()*2.5
		case "rejected":
			quality = 2 + rng.Float64()*3
		case "revision_requested":
			quality = 5 + rng.Float64()*2
		}
		disagree := st == "accepted" && i%9 == 4 // bir kısmında 4. hakem gerekmiş
		var weighted []float64
		for k, pr := range prs {
			a := Assignment{ID: nextAssign, SubmissionID: s.ID, ReviewerID: pr.ReviewerID, Round: 1, Status: "submitted", Reason: "auto", Score: pr.Score, AssignedAt: assignedAt}
			nextAssign++
			if st == "under_review" {
				a.AssignedAt = ref.Add(-time.Duration(1+rng.IntN(5)) * 24 * time.Hour)
				switch {
				case pr.ReviewerID == demoReviewerID:
					a.AssignedAt = ref.Add(-time.Duration(3+demoSlots) * 24 * time.Hour)
					a.Status = "assigned"
				case lateByID[pr.ReviewerID] && i%3 == 0:
					a.AssignedAt = ref.Add(-time.Duration(16+rng.IntN(4)) * 24 * time.Hour) // süresi geçmiş → job yeniden atar
					a.Status = "assigned"
				case lateByID[pr.ReviewerID] || k == 2:
					a.AssignedAt = ref.Add(-time.Duration(8+rng.IntN(5)) * 24 * time.Hour) // hatırlatma zamanı gelmiş
					a.Status = "assigned"
				}
			}
			d.Assignments = append(d.Assignments, a)
			if a.Status == "submitted" {
				q := quality + rng.NormFloat64()*0.6
				if disagree && k == 2 {
					q = quality - 4.2
				}
				sc := scoresAround(rng, q)
				rv := Review{AssignmentID: a.ID, Scores: sc, Recommendation: recommend(sc.Weighted()), SubmittedAt: a.AssignedAt.Add(time.Duration(2+rng.IntN(10)) * 24 * time.Hour),
					Comment: reviewComment(rng, sc.Weighted())}
				d.Reviews = append(d.Reviews, rv)
				weighted = append(weighted, sc.Weighted())
			}
		}
		if disagree {
			// 4. hakem
			extra := pickExtra(hist[i], pool, cf, prs)
			if extra != 0 {
				a := Assignment{ID: nextAssign, SubmissionID: s.ID, ReviewerID: extra, Round: 1, Status: "submitted", Reason: "disagreement", AssignedAt: assignedAt.Add(12 * 24 * time.Hour)}
				nextAssign++
				d.Assignments = append(d.Assignments, a)
				sc := scoresAround(rng, quality)
				d.Reviews = append(d.Reviews, Review{AssignmentID: a.ID, Scores: sc, Recommendation: recommend(sc.Weighted()), SubmittedAt: a.AssignedAt.Add(4 * 24 * time.Hour), Comment: reviewComment(rng, sc.Weighted())})
				weighted = append(weighted, sc.Weighted())
				s.Disagree = true
			}
		}
		if st == "accepted" || st == "rejected" || st == "revision_requested" {
			agg := review.Summarize(weighted)
			m := agg.Mean
			s.FinalScore = &m
			dec := map[string]string{"accepted": "accept", "rejected": "reject", "revision_requested": "major_revision"}[st]
			if st == "revision_requested" && i%2 == 0 {
				dec = "minor_revision"
			}
			d.Decisions = append(d.Decisions, Decision{SubmissionID: s.ID, Round: 1, Decision: dec, Note: "Hakem yorumları doğrultusunda karar verilmiştir.", At: assignedAt.Add(20 * 24 * time.Hour)})
		}
	}

	// salonlar ve zaman dilimleri (10–12 Mayıs 2027, İstanbul)
	for i, rm := range []Room{{Name: "Salon A — Boğaziçi", Capacity: 40}, {Name: "Salon B — Galata", Capacity: 60}, {Name: "Salon C — Kadıköy", Capacity: 80},
		{Name: "Salon D — Üsküdar", Capacity: 120}, {Name: "Salon E — Haliç", Capacity: 200}, {Name: "Ana Salon — Marmara", Capacity: 300}} {
		rm.ID = int64(i + 1)
		d.Rooms = append(d.Rooms, rm)
	}
	ist := pricing.Istanbul
	var slotID int64 = 1
	for day := 0; day < 3; day++ {
		for _, h := range []int{9, 11, 14, 16, 18} {
			st := time.Date(2027, 5, 10+day, h, 0, 0, 0, ist)
			d.Slots = append(d.Slots, Slot{ID: slotID, Start: st.UTC(), End: st.Add(90 * time.Minute).UTC()})
			slotID++
		}
	}

	// döviz kurları (kurgusal): son 30 gün
	rate := 45.0 + rng.Float64()
	for k := 30; k >= 0; k-- {
		rate += (rng.Float64() - 0.45) * 0.25
		d.FXRates = append(d.FXRates, FX{Rate: fmt.Sprintf("%.6f", rate), At: ref.Add(-time.Duration(k) * 24 * time.Hour)})
	}
	lastRate, _ := pricing.ParseRate(d.FXRates[len(d.FXRates)-1].Rate)

	// kayıtlar
	cal := pricing.DefaultCalendar()
	var nextReg int64 = 1
	register := func(uid int64, currency string, groupID int64, groupSize int, status string, created time.Time) *Registration {
		u := userByID[uid]
		r := lastRate
		if currency == "EUR" {
			r = 1_000_000
		}
		q, _ := pricing.Price(cal, pricing.QuoteInput{At: created, Student: u.IsStudent, Member: u.IsMember, GroupSize: groupSize, Currency: currency, Rate: r})
		reg := Registration{ID: nextReg, UserID: uid, GroupID: groupID, Currency: currency, Quote: q, Status: status, CreatedAt: created}
		nextReg++
		if status == "paid" || status == "refunded" {
			pa := created.Add(time.Duration(10+rng.IntN(600)) * time.Minute)
			reg.PaidAt = &pa
		}
		if status == "refunded" {
			rr := cal.RefundFor(created.Add(48 * time.Hour))
			reg.Refund = &rr
		}
		d.Registrations = append(d.Registrations, reg)
		return &d.Registrations[len(d.Registrations)-1]
	}
	registered := map[int64]bool{}
	// kabul edilen bildirilerin sunucuları: çoğu ödemiş, bir kısmı ödememiş (programa alınamaz)
	unpaidAccepted := 0
	for i := 0; i < 90; i++ {
		s := d.Submissions[i]
		if s.Status != "accepted" {
			continue
		}
		var presenter int64
		for _, a := range s.Authors {
			if a.IsPresenter {
				presenter = a.UserID
			}
		}
		if registered[presenter] {
			continue
		}
		status := "paid"
		if i%9 == 7 && i != 0 {
			status = "pending" // hiçbir yazarı ödememiş bildiri
			unpaidAccepted++
		}
		cur := "EUR"
		if rng.IntN(2) == 0 {
			cur = "TRY"
		}
		created := ref.Add(-time.Duration(1+rng.IntN(12)) * time.Hour)
		if status == "paid" {
			created = ref.Add(-time.Duration(2+rng.IntN(20)) * 24 * time.Hour)
		}
		register(presenter, cur, 0, 1, status, created)
		registered[presenter] = true
	}
	// grup kaydı (6 kişi, grup indirimi)
	g := Group{ID: 1, Name: "Marmara Lab Ekibi", Size: 6}
	d.Groups = append(d.Groups, g)
	for k := 0; k < 6; k++ {
		uid := attendeeIDs[k]
		register(uid, "TRY", g.ID, g.Size, "paid", ref.Add(-6*24*time.Hour))
		registered[uid] = true
	}
	// bireysel katılımcılar
	for k := 6; k < 160; k++ {
		uid := attendeeIDs[k]
		status := "paid"
		switch {
		case k%13 == 0:
			status = "refunded"
		case k%5 == 0:
			status = "pending"
		}
		cur := "EUR"
		if rng.IntN(3) > 0 {
			cur = "TRY"
		}
		created := ref.Add(-time.Duration(1+rng.IntN(25)) * 24 * time.Hour)
		if status == "pending" {
			created = ref.Add(-time.Duration(1+rng.IntN(10)) * time.Hour)
		}
		register(uid, cur, 0, 1, status, created)
		registered[uid] = true
	}
	_ = demoAttendeeID // demo katılımcı kayıtsız başlar: arayüzden kayıt/ödeme senaryosu
	return d
}

func dedupe(in [][2]int64) [][2]int64 {
	var out [][2]int64
	for i, p := range in {
		if i == 0 || p != in[i-1] {
			out = append(out, p)
		}
	}
	return out
}

func clamp(v float64) int {
	x := int(math.Round(v))
	if x < 1 {
		return 1
	}
	if x > 10 {
		return 10
	}
	return x
}

func scoresAround(rng *rand.Rand, q float64) review.Scores {
	return review.Scores{
		Originality:  clamp(q + rng.NormFloat64()*0.7),
		Significance: clamp(q + rng.NormFloat64()*0.7),
		Methodology:  clamp(q + rng.NormFloat64()*0.7),
		Clarity:      clamp(q + rng.NormFloat64()*0.7),
	}
}

func recommend(w float64) string {
	switch {
	case w >= 7.5:
		return "accept"
	case w >= 6:
		return "minor_revision"
	case w >= 4.5:
		return "major_revision"
	}
	return "reject"
}

var commentBits = []string{
	"The problem is well motivated and the related work section is thorough.",
	"The experimental section would benefit from additional ablations.",
	"Some claims in the introduction are not fully supported by the results.",
	"The writing is clear and the figures are informative.",
	"Please clarify the computational cost compared to the baselines.",
	"The dataset description lacks detail needed for reproducibility.",
	"The theoretical analysis is a strong contribution.",
}

func reviewComment(rng *rand.Rand, w float64) string {
	a := commentBits[rng.IntN(len(commentBits))]
	b := commentBits[rng.IntN(len(commentBits))]
	return fmt.Sprintf("%s %s Overall assessment: %.1f/10.", a, b, w)
}

func pickExtra(p assignment.Paper, pool []assignment.Reviewer, cf *assignment.Conflicts, existing []assignment.Pair) int64 {
	ex := map[int64]bool{}
	for _, e := range existing {
		ex[e.ReviewerID] = true
	}
	best, bestS := int64(0), -1.0
	for i := range pool {
		r := &pool[i]
		if ex[r.ID] || cf.Conflict(&p, r) != assignment.COINone {
			continue
		}
		if s := keywords.Coverage(p.Keywords, r.Keywords); s > bestS {
			best, bestS = r.ID, s
		}
	}
	if bestS < 0.2 {
		return 0
	}
	return best
}

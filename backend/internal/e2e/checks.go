package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-pdf/fpdf"

	"github.com/fatmakaplan/kongrehub/internal/domain/blind"
	"github.com/fatmakaplan/kongrehub/internal/pdfmeta"
)

const pw = "Demo123!"

var demo = map[string]string{
	"admin": "admin@kongrehub.test", "editor": "editor@kongrehub.test", "registrar": "registrar@kongrehub.test",
	"author": "author@kongrehub.test", "reviewer": "reviewer@kongrehub.test", "attendee": "attendee@kongrehub.test",
}

// Result is one row of the PASS/FAIL table.
type Result struct {
	ID       string
	Title    string
	Pass     bool
	Detail   string
	Duration time.Duration
}

// Runner holds state shared between checks.
type Runner struct {
	C           *Client
	FrontendURL string
	tok         map[string]string
	Log         func(string, ...any)

	flowSub      int64              // F-1'de oluşturulan bildiri
	flowAuthorTok string            // bu çalıştırma için oluşturulan yazar hesabı
	flowAuthors  []string           // yazar adları/e-postaları (sızıntı taraması)
	flowRevs     map[int64]string   // reviewer id → email
	flowRevNames []string
	assignOf     map[string]int64   // reviewer email → assignment id
	stage        string
}

func NewRunner(base, frontend string) *Runner {
	return &Runner{C: NewClient(base), FrontendURL: frontend, tok: map[string]string{}, flowRevs: map[int64]string{}, assignOf: map[string]int64{},
		Log: func(string, ...any) {}}
}

func (r *Runner) token(role string) string {
	if t, ok := r.tok[role]; ok {
		return t
	}
	t, err := r.C.Login(demo[role], pw)
	if err != nil {
		r.Log("login %s: %v", role, err)
	}
	r.tok[role] = t
	return t
}

func fail(f string, a ...any) error { return fmt.Errorf(f, a...) }

// Checks returns the ordered verification list.
func (r *Runner) Checks() []struct {
	ID, Title string
	Fn        func() (string, error)
} {
	return []struct {
		ID, Title string
		Fn        func() (string, error)
	}{
		{"HEALTH", "GET /health + /metrics", r.checkHealth},
		{"AUTH", "Kimlik doğrulama (yanlış parola, token yenileme/rotasyon)", r.checkAuth},
		{"AUTHZ", "Yetki ihlalleri: tüm uç noktalar × roller (403/401)", r.checkAuthz},
		{"T-1", "Hakem atama (500×150 < 20 sn, ÇÇ, yük, nedenler, eşzamanlılık)", r.checkT1},
		{"F-2", "Hakem havuzu ve elle atama (keyset, ÇÇ reddi)", r.checkF2},
		{"F-1", "Bildiri gönderimi (dosya, yazarlar, anahtar kelimeler)", r.checkF1},
		{"F-3", "Değerlendirme formu, karar ve düzeltme turu", r.checkF3},
		{"T-3", "Ağırlıklı skor, uyuşmazlık→4. hakem, 14 gün süre aşımı, idempotent job", r.checkT3},
		{"T-2", "Çift kör gizlilik: uç nokta × rol sızıntı taraması, PDF metadata", r.checkT2},
		{"F-4", "Program: salon, zaman dilimi, oturum", r.checkF4},
		{"T-4", "Program çizelgeleme + bağımsız doğrulayıcı + çakışma reddi", r.checkT4},
		{"F-5", "Kayıt ve ücretlendirme (idempotent ödeme, grup)", r.checkF5},
		{"T-5", "Kademeli ücret, kur dondurma, iade, webhook idempotansı, boşluksuz fatura", r.checkT5},
		{"F-6", "Sertifika üretimi (PDF + imzalı QR doğrulama)", r.checkF6},
		{"F-7", "Raporlar", r.checkF7},
		{"UI", "Arayüz erişilebilir ve API'ye vekil (proxy)", r.checkUI},
	}
}

// Run executes all checks.
func (r *Runner) Run() []Result {
	var out []Result
	for _, c := range r.Checks() {
		start := time.Now()
		detail, err := func() (d string, err error) {
			defer func() {
				if v := recover(); v != nil {
					err = fmt.Errorf("panic: %v", v)
				}
			}()
			return c.Fn()
		}()
		res := Result{ID: c.ID, Title: c.Title, Pass: err == nil, Detail: detail, Duration: time.Since(start)}
		if err != nil {
			res.Detail = err.Error()
		}
		r.Log("%s %v %s", c.ID, res.Pass, res.Detail)
		out = append(out, res)
	}
	return out
}

// ---- HEALTH / AUTH / AUTHZ --------------------------------------------------------

func (r *Runner) checkHealth() (string, error) {
	h, err := r.C.Expect(200, "GET", "/health", "", nil)
	if err != nil {
		return "", err
	}
	m := h.M()
	if checks, _ := m["checks"].(map[string]any); checks["database"] != "up" {
		return "", fail("veritabanı kontrolü up değil: %s", h.Body)
	}
	met, err := r.C.Expect(200, "GET", "/metrics", "", nil)
	if err != nil {
		return "", err
	}
	if !bytes.Contains(met.Body, []byte("kongrehub_http_requests_total")) {
		return "", fail("/metrics Prometheus serilerini içermiyor")
	}
	if _, err := r.C.Expect(200, "GET", "/openapi.yaml", "", nil); err != nil {
		return "", err
	}
	return "database=up, /metrics ve /openapi.yaml erişilebilir", nil
}

func (r *Runner) checkAuth() (string, error) {
	bad, err := r.C.Expect(401, "POST", "/api/auth/login", "", map[string]string{"email": demo["author"], "password": "yanlis-parola"})
	if err != nil {
		return "", err
	}
	if bad.ErrCode() != "invalid_credentials" {
		return "", fail("hata biçimi: %s", bad.Body)
	}
	unk, err := r.C.Expect(401, "POST", "/api/auth/login", "", map[string]string{"email": "yok@kongrehub.test", "password": "x"})
	if err != nil || unk.ErrCode() != "invalid_credentials" {
		return "", fail("bilinmeyen kullanıcı aynı hatayı almalı: %v", err)
	}
	if _, err := r.C.Expect(401, "GET", "/api/me", "", nil); err != nil {
		return "", err
	}
	if _, err := r.C.Expect(401, "GET", "/api/me", "bozuk.token.degeri", nil); err != nil {
		return "", err
	}
	lr, err := r.C.Expect(200, "POST", "/api/auth/login", "", map[string]string{"email": demo["author"], "password": pw})
	if err != nil {
		return "", err
	}
	var tok struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	_ = lr.JSON(&tok)
	rr, err := r.C.Expect(200, "POST", "/api/auth/refresh", "", map[string]string{"refresh_token": tok.Refresh})
	if err != nil {
		return "", err
	}
	var tok2 struct {
		Refresh string `json:"refresh_token"`
	}
	_ = rr.JSON(&tok2)
	if tok2.Refresh == tok.Refresh {
		return "", fail("refresh token döndürülmedi")
	}
	if _, err := r.C.Expect(401, "POST", "/api/auth/refresh", "", map[string]string{"refresh_token": tok.Refresh}); err != nil {
		return "", fail("eski refresh token tekrar kullanılabildi: %v", err)
	}
	return "yanlış parola 401, bozuk token 401, refresh rotasyonu ve yeniden kullanım reddi", nil
}

type routeSpec struct {
	Method  string   `json:"method"`
	Pattern string   `json:"pattern"`
	Roles   []string `json:"roles"`
	Public  bool     `json:"public"`
}

func (r *Runner) routes() ([]routeSpec, error) {
	res, err := r.C.Expect(200, "GET", "/api/admin/routes", r.token("admin"), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []routeSpec `json:"items"`
	}
	return out.Items, res.JSON(&out)
}

func fill(pattern string, id int64) string {
	p := strings.ReplaceAll(pattern, "{id}", fmt.Sprint(id))
	return strings.ReplaceAll(p, "{sessionId}", fmt.Sprint(id))
}

func (r *Runner) checkAuthz() (string, error) {
	routes, err := r.routes()
	if err != nil {
		return "", err
	}
	roles := []string{"author", "reviewer", "editor", "registrar", "attendee", "admin"}
	calls := 0
	var errs []string
	for _, rt := range routes {
		if rt.Public {
			continue
		}
		path := fill(rt.Pattern, 1)
		// anonim → 401
		res, err := r.C.Do(rt.Method, path, "", []byte("{"))
		calls++
		if err != nil || res.Status != 401 {
			errs = append(errs, fmt.Sprintf("anon %s %s → %v", rt.Method, rt.Pattern, statusOf(res, err)))
		}
		if len(rt.Roles) == 0 {
			continue
		}
		allowed := map[string]bool{}
		for _, ro := range rt.Roles {
			allowed[ro] = true
		}
		for _, ro := range roles {
			if allowed[ro] {
				continue
			}
			res, err := r.C.Do(rt.Method, path, r.token(ro), []byte("{"))
			calls++
			if err != nil || res.Status != 403 || res.ErrCode() != "forbidden" {
				errs = append(errs, fmt.Sprintf("%s %s %s → %v", ro, rt.Method, rt.Pattern, statusOf(res, err)))
			}
		}
	}
	// kaynak düzeyi: yazar başkasının bildirisini, hakem atanmadığı bildiriyi göremez (404)
	if _, err := r.C.Expect(404, "GET", "/api/submissions/300", r.token("author"), nil); err != nil {
		errs = append(errs, "yazar başka bildiriyi görebildi: "+err.Error())
	}
	if _, err := r.C.Expect(404, "GET", "/api/submissions/300/file", r.token("reviewer"), nil); err != nil {
		errs = append(errs, "hakem atanmadığı dosyayı indirebildi: "+err.Error())
	}
	if len(errs) > 0 {
		return "", fail("%d ihlal, ilki: %s", len(errs), errs[0])
	}
	return fmt.Sprintf("%d uç nokta, %d yetkisiz çağrı: hepsi 401/403; kaynak düzeyi 404", len(routes), calls), nil
}

func statusOf(r *Resp, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%d %s", r.Status, r.ErrCode())
}

// ---- T-1 / F-2 ------------------------------------------------------------------------

type assignResp struct {
	Stats struct {
		Papers, Reviewers, Assigned, Requested, LoadMin, LoadMax int
		DurationMS                                               int64 `json:"duration_ms"`
		MeanScore                                                float64 `json:"mean_score"`
	} `json:"stats"`
	Unassigned []struct {
		PaperID int64  `json:"paper_id"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"unassigned"`
	Violations []string `json:"violations"`
	Created    int      `json:"created"`
}

func (a *assignResp) fix() {
	// JSON alan adları snake_case
}

func (r *Runner) autoAssign(body map[string]any) (*assignResp, *Resp, error) {
	res, err := r.C.Do("POST", "/api/assignments/auto", r.token("editor"), body, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return nil, nil, err
	}
	var out assignResp
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(res.Body, &raw)
	_ = json.Unmarshal(res.Body, &out)
	var st map[string]any
	_ = json.Unmarshal(raw["stats"], &st)
	num := func(k string) float64 { f, _ := st[k].(float64); return f }
	out.Stats.Papers, out.Stats.Reviewers = int(num("papers")), int(num("reviewers"))
	out.Stats.Assigned, out.Stats.Requested = int(num("assigned")), int(num("requested"))
	out.Stats.LoadMin, out.Stats.LoadMax = int(num("load_min")), int(num("load_max"))
	out.Stats.DurationMS, out.Stats.MeanScore = int64(num("duration_ms")), num("mean_score")
	return &out, res, nil
}

func (r *Runner) checkT1() (string, error) {
	start := time.Now()
	dry, res, err := r.autoAssign(map[string]any{"dry_run": true, "scope": "all"})
	if err != nil || res.Status != 200 {
		return "", fail("dry-run: %v %s", err, trunc(res.Body))
	}
	elapsed := time.Since(start)
	if dry.Stats.Papers < 500 || dry.Stats.Reviewers < 150 {
		return "", fail("ölçek yetersiz: %d bildiri %d hakem", dry.Stats.Papers, dry.Stats.Reviewers)
	}
	if elapsed > 20*time.Second {
		return "", fail("atama %v sürdü (> 20 sn)", elapsed)
	}
	if len(dry.Violations) > 0 {
		return "", fail("doğrulayıcı ihlal buldu: %s", dry.Violations[0])
	}
	reasons := map[string]int{}
	for _, u := range dry.Unassigned {
		if u.Reason == "" || u.Message == "" {
			return "", fail("bildiri %d nedensiz atanamadı", u.PaperID)
		}
		reasons[u.Reason]++
	}
	// gerçek çalıştırma (eksik hakemli bildiriler) — 4 eşzamanlı istek: biri çalışır, diğerleri 409 ya da boş iş
	var wg sync.WaitGroup
	codes := make([]int, 4)
	created := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, res, err := r.autoAssign(map[string]any{})
			if err == nil {
				codes[i] = res.Status
				created[i] = out.Created
			}
		}(i)
	}
	wg.Wait()
	ok409 := 0
	for _, c := range codes {
		switch c {
		case 200:
		case 409:
			ok409++
		default:
			return "", fail("eşzamanlı atama beklenmeyen durum: %v", codes)
		}
	}
	// sonrasında bekleyen iş kalmamalı ve hiçbir bildiri 3'ten fazla hakem almamalı
	after, res, err := r.autoAssign(map[string]any{"dry_run": true})
	if err != nil || res.Status != 200 || len(after.Violations) > 0 {
		return "", fail("eşzamanlı çalıştırma sonrası tutarsızlık: %v %v", err, after.Violations)
	}
	return fmt.Sprintf("%d bildiri/%d hakem: %d/%d atama %d ms (istemci %v), ort. skor %.3f, yük %d..%d, atanamayan %d %v; eşzamanlı 4 çalıştırma kodları %v (409=%d), oluşturulan %v",
		dry.Stats.Papers, dry.Stats.Reviewers, dry.Stats.Assigned, dry.Stats.Requested, dry.Stats.DurationMS, elapsed.Round(time.Millisecond),
		dry.Stats.MeanScore, dry.Stats.LoadMin, dry.Stats.LoadMax, len(dry.Unassigned), reasons, codes, ok409, created), nil
}

type reviewerRow struct {
	ID          int64    `json:"id"`
	FullName    string   `json:"full_name"`
	Email       string   `json:"email"`
	Institution string   `json:"institution"`
	Keywords    []string `json:"keywords"`
	MaxLoad     int      `json:"max_load"`
	CurrentLoad int      `json:"current_load"`
}

func (r *Runner) reviewers(keyword string) ([]reviewerRow, int, error) {
	var all []reviewerRow
	pages := 0
	cursor := ""
	for {
		q := "/api/reviewers?limit=40"
		if keyword != "" {
			q += "&keyword=" + url.QueryEscape(keyword)
		}
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		res, err := r.C.Expect(200, "GET", q, r.token("editor"), nil)
		if err != nil {
			return nil, pages, err
		}
		var page struct {
			Items      []reviewerRow `json:"items"`
			NextCursor string        `json:"next_cursor"`
		}
		_ = res.JSON(&page)
		all = append(all, page.Items...)
		pages++
		if page.NextCursor == "" {
			return all, pages, nil
		}
		cursor = page.NextCursor
	}
}

func (r *Runner) checkF2() (string, error) {
	all, pages, err := r.reviewers("")
	if err != nil {
		return "", err
	}
	if len(all) < 150 || pages < 4 {
		return "", fail("hakem havuzu %d kişi, %d sayfa", len(all), pages)
	}
	seen := map[int64]bool{}
	for _, rv := range all {
		if seen[rv.ID] {
			return "", fail("keyset sayfalamada tekrar: %d", rv.ID)
		}
		seen[rv.ID] = true
		if rv.CurrentLoad > rv.MaxLoad {
			return "", fail("hakem %d yükü %d > %d", rv.ID, rv.CurrentLoad, rv.MaxLoad)
		}
	}
	// niş bildiri (500): uzmanlardan en az biri çıkar çatışması nedeniyle reddedilmeli
	niche, _, err := r.reviewers("underwater acoustics")
	if err != nil {
		return "", err
	}
	coi := 0
	for _, rv := range niche {
		res, err := r.C.Do("POST", "/api/submissions/500/assignments", r.token("editor"), map[string]int64{"reviewer_id": rv.ID})
		if err == nil && res.Status == 409 && res.ErrCode() == "conflict_of_interest" {
			coi++
		}
	}
	if coi == 0 {
		return "", fail("çıkar çatışmalı elle atama reddedilmedi")
	}
	return fmt.Sprintf("%d hakem %d sayfada (keyset, tekrar yok), yük ≤ azami; elle atamada %d çıkar çatışması reddi (409)", len(all), pages, coi), nil
}

// ---- F-1 ------------------------------------------------------------------------------------

// pdfWithAuthor builds a PDF whose metadata carries the author's identity.
func pdfWithAuthor(author, title string) []byte {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetAuthor(author, false)
	p.SetCreator(author+" workstation", false)
	p.SetTitle(title, false)
	p.AddPage()
	p.SetFont("Helvetica", "", 12)
	p.MultiCell(0, 6, title+"\n\nAnonymous manuscript body.", "", "L", false)
	var b bytes.Buffer
	_ = p.Output(&b)
	return b.Bytes()
}

func (r *Runner) authorTok() (string, error) {
	if r.flowAuthorTok == "" {
		// her çalıştırmada yeni yazar: kimliği programda/başka yerde hiç görünmemiş olur
		_, tok, err := r.newUser("author", false)
		if err != nil {
			return "", err
		}
		r.flowAuthorTok = tok
	}
	return r.flowAuthorTok, nil
}

func (r *Runner) createSubmission(title string, kws []string, coauthor map[string]any) (int64, error) {
	atok, err := r.authorTok()
	if err != nil {
		return 0, err
	}
	me, err := r.C.Expect(200, "GET", "/api/me", atok, nil)
	if err != nil {
		return 0, err
	}
	var u struct {
		Email, FullName string `json:"-"`
	}
	m := me.M()
	u.Email, _ = m["email"].(string)
	u.FullName, _ = m["full_name"].(string)
	authors := []map[string]any{{"full_name": u.FullName, "email": u.Email, "institution_id": 3, "is_presenter": true}}
	if coauthor != nil {
		authors = append(authors, coauthor)
	}
	meta, _ := json.Marshal(map[string]any{"title": title, "abstract": "We propose a method and evaluate it carefully on several public benchmarks.",
		"keywords": kws, "duration_minutes": 20, "authors": authors})
	res, err := r.C.Expect(201, "POST", "/api/submissions", atok,
		Multipart(map[string]string{"metadata": string(meta)}, u.FullName+" - draft.pdf", pdfWithAuthor(u.FullName, title)))
	if err != nil {
		return 0, err
	}
	id, _ := res.M()["id"].(float64)
	r.flowAuthors = []string{u.FullName, u.Email}
	if coauthor != nil {
		r.flowAuthors = append(r.flowAuthors, coauthor["full_name"].(string), coauthor["email"].(string))
	}
	return int64(id), nil
}

func (r *Runner) checkF1() (string, error) {
	// doğrulama hatası: dosya yok → tutarlı hata biçimi
	meta := `{"title":"Eksik dosya","abstract":"short","keywords":["x"],"authors":[]}`
	bad, err := r.C.Expect(400, "POST", "/api/submissions", r.token("author"), Multipart(map[string]string{"metadata": meta}, "", nil))
	if err != nil {
		return "", err
	}
	if bad.ErrCode() != "validation_error" {
		return "", fail("hata biçimi {error:{code,message}} değil: %s", bad.Body)
	}
	id, err := r.createSubmission(fmt.Sprintf("Explainable Transfer Learning for Medical Imaging (verify %s)", r.C.runID[len(r.C.runID)-6:]),
		[]string{"Deep Learning", "medical imaging", "transfer learning"},
		map[string]any{"full_name": "Kemal Ortakyazar", "email": fmt.Sprintf("kemal.%s@ortak.example", r.C.runID), "institution_id": 7})
	if err != nil {
		return "", err
	}
	r.flowSub = id
	d, err := r.C.Expect(200, "GET", fmt.Sprintf("/api/submissions/%d", id), r.flowAuthorTok, nil)
	if err != nil {
		return "", err
	}
	var sub struct {
		Keywords []string `json:"keywords"`
		Authors  []struct {
			FullName    string `json:"full_name"`
			IsPresenter bool   `json:"is_presenter"`
		} `json:"authors"`
		Files []struct {
			Version int `json:"version"`
		} `json:"files"`
		Status string `json:"status"`
	}
	_ = d.JSON(&sub)
	if len(sub.Authors) != 2 || len(sub.Files) != 1 || sub.Status != "submitted" || sub.Keywords[0] != "deep learning" {
		return "", fail("bildiri içeriği beklenen gibi değil: %s", trunc(d.Body))
	}
	f, err := r.C.Expect(200, "GET", fmt.Sprintf("/api/submissions/%d/file", id), r.flowAuthorTok, nil)
	if err != nil {
		return "", err
	}
	if a, _ := pdfmeta.Author(f.Body); a != "" {
		return "", fail("yüklenen PDF'te yazar metadata'sı kaldı: %q", a)
	}
	mine, err := r.C.Expect(200, "GET", "/api/submissions/mine", r.flowAuthorTok, nil)
	if err != nil || !bytes.Contains(mine.Body, []byte(fmt.Sprintf(`"id":%d`, id))) {
		return "", fail("bildirilerim listesinde yok")
	}
	return fmt.Sprintf("bildiri #%d: 2 yazar, 3 anahtar kelime (normalize), PDF yüklendi ve /Author temizlendi; eksik dosya 400 validation_error", id), nil
}

// ---- F-3 / T-3 ---------------------------------------------------------------------------------

func (r *Runner) assignFlowReviewers(n int) error {
	cands, _, err := r.reviewers("medical imaging")
	if err != nil {
		return err
	}
	more, _, _ := r.reviewers("deep learning")
	cands = append(cands, more...)
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].MaxLoad-cands[i].CurrentLoad > cands[j].MaxLoad-cands[j].CurrentLoad })
	for _, c := range cands {
		if len(r.flowRevs) >= n {
			break
		}
		if _, dup := r.flowRevs[c.ID]; dup || c.Email == demo["reviewer"] {
			continue
		}
		res, err := r.C.Do("POST", fmt.Sprintf("/api/submissions/%d/assignments", r.flowSub), r.token("editor"), map[string]int64{"reviewer_id": c.ID})
		if err != nil {
			return err
		}
		if res.Status == 201 {
			r.flowRevs[c.ID] = c.Email
			r.flowRevNames = append(r.flowRevNames, c.FullName, c.Email)
		}
	}
	if len(r.flowRevs) < n {
		return fail("yalnızca %d hakem atanabildi", len(r.flowRevs))
	}
	return nil
}

type editorView struct {
	Status      string `json:"status"`
	Round       int    `json:"round"`
	Disagree    *bool  `json:"disagreement_flag"`
	FinalScore  *float64 `json:"final_score"`
	Assignments []struct {
		ID         int64  `json:"id"`
		ReviewerID int64  `json:"reviewer_id"`
		Name       string `json:"reviewer_name"`
		Round      int    `json:"round"`
		Status     string `json:"status"`
		Reason     string `json:"reason"`
	} `json:"assignments"`
	Reviews []struct {
		Reviewer string  `json:"reviewer"`
		Weighted float64 `json:"weighted_score"`
		Round    int     `json:"round"`
	} `json:"reviews"`
	Aggregate struct {
		Mean         float64 `json:"mean"`
		Spread       float64 `json:"spread"`
		Disagreement bool    `json:"disagreement"`
	} `json:"aggregate"`
}

func (r *Runner) editorView(id int64) (*editorView, error) {
	res, err := r.C.Expect(200, "GET", fmt.Sprintf("/api/submissions/%d", id), r.token("editor"), nil)
	if err != nil {
		return nil, err
	}
	var v editorView
	return &v, res.JSON(&v)
}

func (r *Runner) reviewerAssignment(email string, sub int64) (int64, string, error) {
	tok, err := r.C.Login(email, pw)
	if err != nil {
		return 0, "", err
	}
	res, err := r.C.Expect(200, "GET", "/api/reviewer/assignments?status=assigned", tok, nil)
	if err != nil {
		return 0, "", err
	}
	var page struct {
		Items []struct {
			ID           int64 `json:"id"`
			SubmissionID int64 `json:"submission_id"`
		} `json:"items"`
	}
	_ = res.JSON(&page)
	for _, it := range page.Items {
		if it.SubmissionID == sub {
			return it.ID, tok, nil
		}
	}
	return 0, tok, fail("%s için açık atama yok", email)
}

func (r *Runner) submitReview(email string, score int, rec string) error {
	aid, tok, err := r.reviewerAssignment(email, r.flowSub)
	if err != nil {
		return err
	}
	r.assignOf[email] = aid
	body := map[string]any{"originality": score, "significance": score, "methodology": score, "clarity": score,
		"recommendation": rec, "comments_to_author": "Detaylı değerlendirme: yöntem açık, deneyler genişletilmeli.", "comments_to_editor": "gizli not"}
	_, err = r.C.Expect(201, "POST", fmt.Sprintf("/api/reviewer/assignments/%d/review", aid), tok, body, "Idempotency-Key", r.C.NewKey())
	return err
}

// checkF3 phase 1: 3 reviews (one outlier) → 4th reviewer → decision major revision → revision upload.
func (r *Runner) checkF3() (string, error) {
	if r.flowSub == 0 {
		return "", errors.New("önkoşul F-1 başarısız")
	}
	if err := r.assignFlowReviewers(3); err != nil {
		return "", err
	}
	emails := []string{}
	for _, e := range r.flowRevs {
		emails = append(emails, e)
	}
	sort.Strings(emails)
	// form doğrulaması: 11 puan → 400
	aid, tok, err := r.reviewerAssignment(emails[0], r.flowSub)
	if err != nil {
		return "", err
	}
	if _, err := r.C.Expect(400, "POST", fmt.Sprintf("/api/reviewer/assignments/%d/review", aid), tok,
		map[string]any{"originality": 11, "significance": 5, "methodology": 5, "clarity": 5, "recommendation": "accept", "comments_to_author": "yeterince uzun yorum"},
		"Idempotency-Key", r.C.NewKey()); err != nil {
		return "", err
	}
	for i, e := range emails {
		score, rec := 9, "accept"
		if i == 2 {
			score, rec = 3, "reject"
		}
		if err := r.submitReview(e, score, rec); err != nil {
			return "", err
		}
	}
	v, err := r.editorView(r.flowSub)
	if err != nil {
		return "", err
	}
	var extra string
	for _, a := range v.Assignments {
		if a.Reason == "disagreement" && a.Status == "assigned" {
			for id, e := range r.flowRevs {
				_ = id
				_ = e
			}
			extra = r.emailOf(a.ReviewerID)
			r.flowRevs[a.ReviewerID] = extra
			r.flowRevNames = append(r.flowRevNames, a.Name, extra)
		}
	}
	if extra == "" || v.Status != "under_review" {
		return "", fail("uyuşmazlıkta 4. hakem atanmadı (durum %s)", v.Status)
	}
	r.stage = "fourth"
	if err := r.submitReview(extra, 7, "major_revision"); err != nil {
		return "", err
	}
	v, _ = r.editorView(r.flowSub)
	if v.Status != "reviews_complete" {
		return "", fail("4 değerlendirme sonrası durum %s", v.Status)
	}
	if _, err := r.C.Expect(200, "POST", fmt.Sprintf("/api/submissions/%d/decision", r.flowSub), r.token("editor"),
		map[string]string{"decision": "major_revision", "note_to_author": "Deneyleri genişletin."}, "Idempotency-Key", r.C.NewKey()); err != nil {
		return "", err
	}
	// yazar düzeltilmiş sürümü yükler
	if _, err := r.C.Expect(201, "POST", fmt.Sprintf("/api/submissions/%d/revisions", r.flowSub), r.flowAuthorTok,
		Multipart(map[string]string{"response_letter": "Tüm hakem yorumlarını ele aldık; yeni deneyler eklendi."}, "v2.pdf", pdfWithAuthor(r.flowAuthors[0], "Revised"))); err != nil {
		return "", err
	}
	v, _ = r.editorView(r.flowSub)
	revAssign := 0
	for _, a := range v.Assignments {
		if a.Round == 2 && a.Status == "assigned" {
			revAssign++
		}
	}
	if v.Status != "under_review" || v.Round != 2 || revAssign == 0 {
		return "", fail("düzeltme turu başlamadı: durum=%s tur=%d atama=%d", v.Status, v.Round, revAssign)
	}
	r.stage = "round2"
	return fmt.Sprintf("form doğrulaması (11 puan → 400), 3+1 değerlendirme, karar=major_revision, v2 yüklendi → tur 2, %d hakem tekrar bakıyor", revAssign), nil
}

func (r *Runner) emailOf(reviewerID int64) string {
	if e, ok := r.flowRevs[reviewerID]; ok {
		return e
	}
	all, _, _ := r.reviewers("")
	for _, rv := range all {
		if rv.ID == reviewerID {
			return rv.Email
		}
	}
	return ""
}

// checkT3: skor/uyuşmazlık doğrulaması + süre aşımı + idempotent job + geçersiz geçiş; tur 2'yi tamamlar.
func (r *Runner) checkT3() (string, error) {
	if r.stage != "round2" {
		return "", errors.New("önkoşul F-3 başarısız")
	}
	v, err := r.editorView(r.flowSub)
	if err != nil {
		return "", err
	}
	var r1 []float64
	for _, rv := range v.Reviews {
		if rv.Round == 1 {
			r1 = append(r1, rv.Weighted)
		}
	}
	if len(r1) != 4 || v.Disagree == nil || !*v.Disagree {
		return "", fail("tur 1: %d değerlendirme, uyuşmazlık bayrağı %v", len(r1), v.Disagree)
	}
	sort.Float64s(r1)
	if r1[0] != 3 || r1[3] != 9 {
		return "", fail("ağırlıklı skorlar beklenmedik: %v", r1)
	}
	// süre aşımı: ayrı bir bildiri, tek hakem; atama 15 gün geriye alınır
	expSub, err := r.createSubmission(fmt.Sprintf("Federated Learning Deadline Probe (verify %s)", r.C.runID[len(r.C.runID)-6:]), []string{"federated learning", "deep learning", "privacy"}, nil)
	if err != nil {
		return "", err
	}
	cands, _, _ := r.reviewers("federated learning")
	var first int64
	for _, c := range cands {
		res, _ := r.C.Do("POST", fmt.Sprintf("/api/submissions/%d/assignments", expSub), r.token("editor"), map[string]int64{"reviewer_id": c.ID})
		if res != nil && res.Status == 201 {
			id, _ := res.M()["assignment_id"].(float64)
			first = int64(id)
			break
		}
	}
	if first == 0 {
		return "", fail("süre testi için hakem atanamadı")
	}
	if _, err := r.C.Expect(200, "POST", "/api/admin/demo/backdate-assignment", r.token("admin"), map[string]any{"assignment_id": first, "days": 15}); err != nil {
		return "", err
	}
	job1, err := r.C.Expect(200, "POST", "/api/jobs/reminders/run", r.token("editor"), map[string]any{})
	if err != nil {
		return "", err
	}
	job2, err := r.C.Expect(200, "POST", "/api/jobs/reminders/run", r.token("editor"), map[string]any{})
	if err != nil {
		return "", err
	}
	ev, _ := r.editorView(expSub)
	expired, replacements := 0, 0
	for _, a := range ev.Assignments {
		if a.ID == first && a.Status == "expired" {
			expired++
		}
		if a.Reason == "replacement" {
			replacements++
		}
	}
	j2 := job2.M()
	if expired != 1 || replacements != 1 || j2["expired"].(float64) != 0 {
		return "", fail("süre aşımı: expired=%d replacement=%d, 2. job=%s", expired, replacements, job2.Body)
	}
	// geçersiz geçiş: incelemedeki bildiriye karar verilemez
	inv, err := r.C.Expect(409, "POST", fmt.Sprintf("/api/submissions/%d/decision", r.flowSub), r.token("editor"),
		map[string]string{"decision": "accept"}, "Idempotency-Key", r.C.NewKey())
	if err != nil || inv.ErrCode() != "invalid_transition" {
		return "", fail("durum makinesi geçersiz geçişe izin verdi: %v", err)
	}
	// tur 2'yi tamamla ve kabul et
	for _, a := range v.Assignments {
		if a.Round == 2 && a.Status == "assigned" {
			if err := r.submitReview(r.emailOf(a.ReviewerID), 8, "accept"); err != nil {
				return "", err
			}
		}
	}
	v, _ = r.editorView(r.flowSub)
	if v.Status != "reviews_complete" {
		return "", fail("tur 2 sonrası durum %s", v.Status)
	}
	if _, err := r.C.Expect(200, "POST", fmt.Sprintf("/api/submissions/%d/decision", r.flowSub), r.token("editor"),
		map[string]string{"decision": "accept", "note_to_author": "Tebrikler"}, "Idempotency-Key", r.C.NewKey()); err != nil {
		return "", err
	}
	r.stage = "accepted"
	return fmt.Sprintf("skorlar %v (fark %.1f > 3 → 4. hakem), 15 gün geriye alınan atama süresi doldu ve yerine 1 hakem atandı; job 1=%s job 2 expired=0 (idempotent); geçersiz karar 409; tur 2 → kabul",
		r1, r1[3]-r1[0], strings.TrimSpace(string(job1.Body))[:min(90, len(job1.Body))]), nil
}

// ---- T-2 -----------------------------------------------------------------------------------------

func (r *Runner) checkT2() (string, error) {
	if r.flowSub == 0 || len(r.flowRevs) == 0 {
		return "", errors.New("önkoşul F-1/F-3 başarısız")
	}
	routes, err := r.routes()
	if err != nil {
		return "", err
	}
	authorForbidden := map[string]string{}
	for _, t := range r.flowRevNames {
		authorForbidden[t] = "reviewer_identity"
	}
	reviewerForbidden := map[string]string{}
	for _, t := range r.flowAuthors {
		reviewerForbidden[t] = "author_identity"
	}
	type actor struct {
		label     string
		token     string
		role      string
		forbidden map[string]string
		assignID  int64
	}
	actors := []actor{{"yazar", r.flowAuthorTok, "author", authorForbidden, 0}}
	for _, e := range r.flowRevs {
		tok, err := r.C.Login(e, pw)
		if err != nil {
			return "", err
		}
		aid := r.assignOf[e]
		if aid == 0 {
			aid, _, _ = r.reviewerAssignment(e, r.flowSub)
		}
		actors = append(actors, actor{"hakem " + e, tok, "reviewer", reviewerForbidden, aid})
	}
	scanned, findings := 0, []string{}
	for _, ac := range actors {
		for _, rt := range routes {
			if rt.Method != "GET" || rt.Public || strings.HasSuffix(rt.Pattern, ".ics") {
				continue
			}
			allowed := len(rt.Roles) == 0
			for _, ro := range rt.Roles {
				if ro == ac.role {
					allowed = true
				}
			}
			if !allowed {
				continue
			}
			id := r.flowSub
			if strings.HasPrefix(rt.Pattern, "/api/reviewer/assignments/") {
				id = ac.assignID
			}
			if strings.Contains(rt.Pattern, "/certificates/") {
				continue
			}
			res, err := r.C.Do("GET", fill(rt.Pattern, id), ac.token, nil)
			if err != nil {
				return "", err
			}
			scanned++
			if strings.HasSuffix(rt.Pattern, "/file") {
				if a, _ := pdfmeta.Author(res.Body); a != "" {
					findings = append(findings, fmt.Sprintf("%s: PDF /Author=%q", ac.label, a))
				}
			}
			for _, f := range blind.Scan(res.Body, ac.forbidden) {
				findings = append(findings, fmt.Sprintf("%s GET %s sızdırdı (%s)", ac.label, rt.Pattern, f.Kind))
			}
			if cd := res.Header.Get("Content-Disposition"); cd != "" {
				for _, f := range blind.Scan([]byte(cd), ac.forbidden) {
					findings = append(findings, fmt.Sprintf("%s dosya adı sızdırdı (%s)", ac.label, f.Kind))
				}
			}
		}
		// hata mesajları da taranır
		for _, p := range []string{"/api/submissions/999999", fmt.Sprintf("/api/submissions/%d/history", r.flowSub), "/api/reviewer/assignments/999999"} {
			res, _ := r.C.Do("GET", p, ac.token, nil)
			if res != nil {
				scanned++
				for _, f := range blind.Scan(res.Body, ac.forbidden) {
					findings = append(findings, fmt.Sprintf("%s hata yanıtı %s sızdırdı (%s)", ac.label, p, f.Kind))
				}
			}
		}
	}
	// yazar değerlendirmeleri takma adla görür; editör gerçek kimliği görür
	ar, _ := r.C.Expect(200, "GET", fmt.Sprintf("/api/submissions/%d", r.flowSub), r.flowAuthorTok, nil)
	if ar == nil || !bytes.Contains(ar.Body, []byte(`"reviewer":"Hakem A"`)) {
		findings = append(findings, "yazar takma adlı değerlendirmeleri göremiyor")
	}
	ed, _ := r.C.Expect(200, "GET", fmt.Sprintf("/api/submissions/%d", r.flowSub), r.token("editor"), nil)
	if ed == nil || len(blind.Scan(ed.Body, authorForbidden)) == 0 || len(blind.Scan(ed.Body, reviewerForbidden)) == 0 {
		findings = append(findings, "editör iki tarafın kimliğini de görmeli")
	}
	if len(findings) > 0 {
		return "", fail("%d sızıntı, ilki: %s", len(findings), findings[0])
	}
	return fmt.Sprintf("%d aktör × uç nokta, %d yanıt tarandı (gövde, dosya adı, PDF metadata, hata mesajları): sızıntı yok; yazar 'Hakem A…' görür, editör iki tarafı görür", len(actors), scanned), nil
}

// ---- F-4 / T-4 -----------------------------------------------------------------------------------

func (r *Runner) checkF4() (string, error) {
	rooms, err := r.C.Expect(200, "GET", "/api/rooms", r.token("attendee"), nil)
	if err != nil {
		return "", err
	}
	slots, err := r.C.Expect(200, "GET", "/api/slots", r.token("attendee"), nil)
	if err != nil {
		return "", err
	}
	prog, err := r.C.Expect(200, "GET", "/api/program", r.token("attendee"), nil)
	if err != nil {
		return "", err
	}
	var p struct {
		Sessions []struct {
			ID    int64 `json:"id"`
			Talks []any `json:"talks"`
			Chair string `json:"chair"`
		} `json:"sessions"`
	}
	_ = prog.JSON(&p)
	nr := len(rooms.M()["items"].([]any))
	ns := len(slots.M()["items"].([]any))
	if nr == 0 || ns == 0 || len(p.Sessions) == 0 {
		return "", fail("salon=%d dilim=%d oturum=%d", nr, ns, len(p.Sessions))
	}
	// kişisel program + iCal (bonus)
	sid := p.Sessions[0].ID
	if _, err := r.C.Expect(201, "POST", "/api/agenda", r.token("attendee"), map[string]int64{"session_id": sid}); err != nil {
		return "", err
	}
	ics, err := r.C.Expect(200, "GET", "/api/agenda.ics", r.token("attendee"), nil)
	if err != nil || !bytes.Contains(ics.Body, []byte("BEGIN:VEVENT")) {
		return "", fail("iCal dışa aktarma çalışmıyor")
	}
	return fmt.Sprintf("%d salon, %d zaman dilimi, %d oturum; kişisel programa ekleme ve iCal dışa aktarma", nr, ns, len(p.Sessions)), nil
}

func (r *Runner) checkT4() (string, error) {
	dry, err := r.C.Expect(200, "POST", "/api/program/generate", r.token("editor"), map[string]bool{"dry_run": true}, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return "", err
	}
	gen, err := r.C.Expect(200, "POST", "/api/program/generate", r.token("editor"), map[string]bool{"dry_run": false}, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return "", err
	}
	var g struct {
		Stats struct {
			Scheduled int     `json:"scheduled"`
			Sessions  int     `json:"sessions"`
			Sim       float64 `json:"avg_intra_session_similarity"`
			DurMS     int64   `json:"duration_ms"`
		} `json:"stats"`
		Violations  []any `json:"violations"`
		Unscheduled []struct {
			PaperID int64  `json:"paper_id"`
			Reason  string `json:"reason"`
		} `json:"unscheduled"`
	}
	_ = gen.JSON(&g)
	_ = dry
	if len(g.Violations) != 0 || g.Stats.Scheduled == 0 {
		return "", fail("program üretimi: %s", trunc(gen.Body))
	}
	reasons := map[string]int{}
	flowScheduled := true
	for _, u := range g.Unscheduled {
		reasons[u.Reason]++
		if u.PaperID == r.flowSub {
			flowScheduled = false
		}
	}
	if reasons["no_paid_author"] == 0 {
		return "", fail("ödemesi olmayan kabul edilmiş bildiriler programdan dışlanmadı")
	}
	val, err := r.C.Expect(200, "GET", "/api/program/validate", r.token("editor"), nil)
	if err != nil || val.M()["valid"] != true {
		return "", fail("bağımsız doğrulayıcı: %s", val.Body)
	}
	// çakışma reddi: bir oturumu başka bir oturumun hücresine taşı
	prog, _ := r.C.Expect(200, "GET", "/api/program", r.token("editor"), nil)
	var p struct {
		Sessions []struct {
			ID     int64 `json:"id"`
			RoomID int64 `json:"room_id"`
			SlotID int64 `json:"slot_id"`
		} `json:"sessions"`
	}
	_ = prog.JSON(&p)
	if len(p.Sessions) < 2 {
		return "", fail("çakışma testi için yeterli oturum yok")
	}
	a, b := p.Sessions[0], p.Sessions[1]
	cf, err := r.C.Expect(409, "PUT", fmt.Sprintf("/api/program/sessions/%d", a.ID), r.token("editor"), map[string]int64{"room_id": b.RoomID, "slot_id": b.SlotID})
	if err != nil || cf.ErrCode() != "schedule_conflict" {
		return "", fail("dolu hücreye taşıma reddedilmedi: %v", err)
	}
	_ = flowScheduled
	return fmt.Sprintf("%d oturum, %d bildiri, oturum içi benzerlik %.3f, %d ms; ihlal 0, /validate geçerli; dışarıda kalanlar %v; dolu hücreye taşıma 409",
		g.Stats.Sessions, g.Stats.Scheduled, g.Stats.Sim, g.Stats.DurMS, reasons), nil
}

// ---- F-5 / T-5 / F-6 ---------------------------------------------------------------------------

func (r *Runner) newUser(role string, student bool) (string, string, error) {
	email := fmt.Sprintf("%s.%s.%d@verify.example", role, r.C.runID, r.C.keySeq.Add(1))
	_, err := r.C.Expect(201, "POST", "/api/admin/users", r.token("admin"), map[string]any{"email": email, "password": pw, "full_name": "Doğrulama Kullanıcısı " + email[:12],
		"role": role, "institution_id": 1, "is_student": student})
	if err != nil {
		return "", "", err
	}
	tok, err := r.C.Login(email, pw)
	return email, tok, err
}

type regView struct {
	ID         int64  `json:"id"`
	Tier       string `json:"tier"`
	Currency   string `json:"currency"`
	FXRate     string `json:"fx_rate"`
	TotalCents int64  `json:"total_cents"`
	Status     string `json:"status"`
	Invoice    *string `json:"invoice_number"`
	Breakdown  []struct {
		Code string `json:"code"`
	} `json:"breakdown"`
}

func (r *Runner) register(tok, currency string) (*regView, error) {
	res, err := r.C.Expect(201, "POST", "/api/registrations", tok, map[string]string{"currency": currency}, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return nil, err
	}
	var v regView
	return &v, res.JSON(&v)
}

var paidAttendee string // F-6 için

func (r *Runner) checkF5() (string, error) {
	email, tok, err := r.newUser("attendee", false)
	if err != nil {
		return "", err
	}
	reg, err := r.register(tok, "EUR")
	if err != nil {
		return "", err
	}
	key := r.C.NewKey()
	p1, err := r.C.Expect(201, "POST", fmt.Sprintf("/api/registrations/%d/payments", reg.ID), tok, map[string]string{"outcome": "success"}, "Idempotency-Key", key)
	if err != nil {
		return "", err
	}
	p2, err := r.C.Expect(201, "POST", fmt.Sprintf("/api/registrations/%d/payments", reg.ID), tok, map[string]string{"outcome": "success"}, "Idempotency-Key", key)
	if err != nil {
		return "", err
	}
	if p2.Header.Get("Idempotent-Replayed") != "true" || !bytes.Equal(p1.Body, p2.Body) {
		return "", fail("aynı Idempotency-Key aynı yanıtı döndürmedi")
	}
	if _, err := r.C.Expect(422, "POST", fmt.Sprintf("/api/registrations/%d/payments", reg.ID), tok, map[string]string{"outcome": "fail"}, "Idempotency-Key", key); err != nil {
		return "", fail("aynı anahtar farklı gövde 422 olmalı: %v", err)
	}
	if _, err := r.C.Expect(400, "POST", fmt.Sprintf("/api/registrations/%d/payments", reg.ID), tok, map[string]string{"outcome": "success"}); err != nil {
		return "", fail("Idempotency-Key zorunlu olmalı: %v", err)
	}
	var paid struct {
		Outcome      string  `json:"outcome"`
		Registration regView `json:"registration"`
	}
	_ = p1.JSON(&paid)
	if paid.Outcome != "paid" || paid.Registration.Invoice == nil {
		return "", fail("ödeme sonucu: %s", trunc(p1.Body))
	}
	paidAttendee = email
	// eşzamanlı ödeme denemeleri: yalnızca biri başarılı
	_, tok2, err := r.newUser("attendee", true)
	if err != nil {
		return "", err
	}
	reg2, err := r.register(tok2, "EUR")
	if err != nil {
		return "", err
	}
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := r.C.Do("POST", fmt.Sprintf("/api/registrations/%d/payments", reg2.ID), tok2, map[string]string{"outcome": "success"}, "Idempotency-Key", r.C.NewKey())
			if err == nil {
				codes[i] = res.Status
			}
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == 201 {
			ok++
		} else if c != 409 {
			return "", fail("eşzamanlı ödeme beklenmeyen kod: %v", codes)
		}
	}
	if ok != 1 {
		return "", fail("eşzamanlı 8 ödemeden %d tanesi başarılı (1 olmalı): %v", ok, codes)
	}
	// grup kaydı (5 kişi → grup indirimi)
	var emails []string
	for i := 0; i < 5; i++ {
		e, _, err := r.newUser("attendee", false)
		if err != nil {
			return "", err
		}
		emails = append(emails, e)
	}
	gr, err := r.C.Expect(201, "POST", "/api/registrations/group", r.token("registrar"), map[string]any{"name": "Doğrulama grubu", "currency": "EUR", "emails": emails},
		"Idempotency-Key", r.C.NewKey())
	if err != nil {
		return "", err
	}
	var g struct {
		Registrations []regView `json:"registrations"`
	}
	_ = gr.JSON(&g)
	for _, x := range g.Registrations {
		has := false
		for _, l := range x.Breakdown {
			if l.Code == "group" {
				has = true
			}
		}
		if !has {
			return "", fail("grup indirimi uygulanmadı")
		}
	}
	if _, err := r.C.Expect(200, "GET", "/api/registrations?status=paid&limit=20", r.token("registrar"), nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("kayıt #%d ödendi (fatura %s); aynı anahtar → aynı yanıt (Idempotent-Replayed), farklı gövde 422, anahtarsız 400; 8 eşzamanlı ödemeden 1 başarılı %v; 5 kişilik grupta indirim",
		reg.ID, *paid.Registration.Invoice, codes), nil
}

func (r *Runner) checkT5() (string, error) {
	_, tok, err := r.newUser("attendee", false)
	if err != nil {
		return "", err
	}
	quote := func(at string, extra map[string]any) (map[string]any, error) {
		body := map[string]any{"currency": "EUR", "at": at}
		for k, v := range extra {
			body[k] = v
		}
		res, err := r.C.Expect(200, "POST", "/api/registrations/quote", tok, body)
		if err != nil {
			return nil, err
		}
		return res.M(), nil
	}
	tiers := []struct {
		at, tier string
		total    float64
	}{{"2026-11-01T10:00:00+03:00", "early", 30000}, {"2027-03-01T10:00:00+03:00", "normal", 40000}, {"2027-05-01T10:00:00+03:00", "late", 50000}}
	for _, t := range tiers {
		q, err := quote(t.at, nil)
		if err != nil {
			return "", err
		}
		if q["tier"] != t.tier || q["total_cents"].(float64) != t.total {
			return "", fail("%s: %v", t.at, q)
		}
	}
	disc, err := quote("2027-05-01T10:00:00+03:00", map[string]any{"student": true, "member": true, "group_size": 5})
	if err != nil {
		return "", err
	}
	if disc["total_cents"].(float64) != 18000 {
		return "", fail("öğrenci+üye+grup: %v", disc["total_cents"])
	}
	// kur dondurma
	reg, err := r.register(tok, "TRY")
	if err != nil {
		return "", err
	}
	cur, _ := r.C.Expect(200, "GET", "/api/fx/current", tok, nil)
	newRate := "99.123456"
	if _, err := r.C.Expect(201, "POST", "/api/fx", r.token("registrar"), map[string]string{"rate": newRate}); err != nil {
		return "", err
	}
	payRes, err := r.C.Expect(201, "POST", fmt.Sprintf("/api/registrations/%d/payments", reg.ID), tok, map[string]string{"outcome": "success"}, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return "", err
	}
	var pay struct {
		Amount int64   `json:"amount_cents"`
		Reg    regView `json:"registration"`
	}
	_ = payRes.JSON(&pay)
	if pay.Amount != reg.TotalCents || pay.Reg.FXRate != reg.FXRate || reg.FXRate == newRate {
		return "", fail("kur dondurulmadı: kayıt %s/%d, ödeme %d, yeni kur %s", reg.FXRate, reg.TotalCents, pay.Amount, newRate)
	}
	// kur geri alınsın (simülatör ve diğer kontroller etkilenmesin)
	_, _ = r.C.Do("POST", "/api/fx", r.token("registrar"), map[string]string{"rate": fmt.Sprint(cur.M()["rate"])})
	// kademeli iade
	pcts := []float64{}
	for _, at := range []string{"2027-01-01T12:00:00+03:00", "2027-03-25T12:00:00+03:00", "2027-04-25T12:00:00+03:00", "2027-05-08T12:00:00+03:00"} {
		res, err := r.C.Expect(200, "GET", fmt.Sprintf("/api/registrations/%d/refund-quote?at=%s", reg.ID, url.QueryEscape(at)), tok, nil)
		if err != nil {
			return "", err
		}
		pcts = append(pcts, res.M()["rule"].(map[string]any)["percent"].(float64))
	}
	if fmt.Sprint(pcts) != "[90 50 25 0]" {
		return "", fail("iade kademeleri %v", pcts)
	}
	cancel, err := r.C.Expect(200, "POST", fmt.Sprintf("/api/registrations/%d/cancel", reg.ID), tok, map[string]string{"reason": "test"}, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return "", err
	}
	if cancel.M()["refund_cents"].(float64) != float64(reg.TotalCents*90/100) {
		return "", fail("iade tutarı %v", cancel.M()["refund_cents"])
	}
	// webhook: async ödeme, imzalı olay iki kez, sonra geç gelen 'failed'
	_, tok3, err := r.newUser("attendee", false)
	if err != nil {
		return "", err
	}
	reg3, err := r.register(tok3, "EUR")
	if err != nil {
		return "", err
	}
	asy, err := r.C.Expect(201, "POST", fmt.Sprintf("/api/registrations/%d/payments", reg3.ID), tok3, map[string]string{"outcome": "async"}, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return "", err
	}
	ref, _ := asy.M()["provider_ref"].(string)
	outcomes := []string{}
	for _, ev := range []map[string]any{
		{"event_id": "evt_" + ref, "type": "payment.succeeded", "provider_ref": ref, "amount_cents": reg3.TotalCents, "currency": "EUR"},
		{"event_id": "evt_" + ref, "type": "payment.succeeded", "provider_ref": ref, "amount_cents": reg3.TotalCents, "currency": "EUR"},
		{"event_id": "evt_" + ref + "_late", "type": "payment.failed", "provider_ref": ref, "amount_cents": reg3.TotalCents, "currency": "EUR"},
	} {
		res, err := r.webhook(ev)
		if err != nil {
			return "", err
		}
		outcomes = append(outcomes, fmt.Sprint(res.M()["outcome"]))
	}
	if fmt.Sprint(outcomes) != "[paid duplicate already_succeeded]" {
		return "", fail("webhook sonuçları %v", outcomes)
	}
	bad, _ := r.C.Do("POST", "/api/payments/webhook", "", []byte(`{"event_id":"x"}`), "X-Signature", "deadbeef")
	if bad == nil || bad.Status != 401 {
		return "", fail("geçersiz imza reddedilmedi")
	}
	inv, err := r.C.Expect(200, "GET", "/api/invoices?limit=5", r.token("registrar"), nil)
	if err != nil || inv.M()["gap_count"].(float64) != 0 {
		return "", fail("fatura numaralarında boşluk: %s", inv.Body)
	}
	return fmt.Sprintf("kademeler erken/normal/geç 300/400/500€, öğrenci+üye+grup 180€; TRY kur %s dondu (yeni kur %s sonrası ödeme %d kuruş); iade %v%%; webhook %v; geçersiz imza 401; fatura boşluğu 0",
		reg.FXRate, newRate, pay.Amount, pcts, outcomes), nil
}

func (r *Runner) webhook(ev map[string]any) (*Resp, error) {
	b, _ := json.Marshal(ev)
	return r.C.Expect(200, "POST", "/api/payments/webhook", "", b, "X-Signature", r.sign(b))
}

// WebhookSecret is set by the caller (env PAYMENT_WEBHOOK_SECRET).
var WebhookSecret = ""
var Signer = func(secret string, body []byte) string { return "" }

func (r *Runner) sign(b []byte) string { return Signer(WebhookSecret, b) }

func (r *Runner) checkF6() (string, error) {
	if paidAttendee == "" {
		return "", errors.New("önkoşul F-5 başarısız")
	}
	tok, err := r.C.Login(paidAttendee, pw)
	if err != nil {
		return "", err
	}
	_, unpaid, err := r.newUser("attendee", false)
	if err != nil {
		return "", err
	}
	if _, err := r.C.Expect(422, "POST", "/api/certificates", unpaid, map[string]string{"kind": "attendance"}, "Idempotency-Key", r.C.NewKey()); err != nil {
		return "", fail("ödemesiz kullanıcıya sertifika verildi: %v", err)
	}
	res, err := r.C.Expect(201, "POST", "/api/certificates", tok, map[string]string{"kind": "attendance"}, "Idempotency-Key", r.C.NewKey())
	if err != nil {
		return "", err
	}
	var c struct {
		ID, Signature, VerifyURL string
	}
	m := res.M()
	c.ID, _ = m["id"].(string)
	c.Signature, _ = m["signature"].(string)
	c.VerifyURL, _ = m["verify_url"].(string)
	pdf, err := r.C.Expect(200, "GET", "/api/certificates/"+c.ID+"/pdf", tok, nil)
	if err != nil || !bytes.HasPrefix(pdf.Body, []byte("%PDF")) || !bytes.Contains(pdf.Body, []byte("/Image")) {
		return "", fail("sertifika PDF'i (QR görseli ile) üretilmedi")
	}
	v, err := r.C.Expect(200, "GET", "/api/certificates/verify?id="+c.ID+"&sig="+c.Signature, "", nil)
	if err != nil || v.M()["valid"] != true {
		return "", fail("geçerli imza doğrulanmadı: %s", v.Body)
	}
	tampered := c.Signature[:len(c.Signature)-2] + "AA"
	tv, _ := r.C.Expect(200, "GET", "/api/certificates/verify?id="+c.ID+"&sig="+tampered, "", nil)
	if tv == nil || tv.M()["valid"] != false {
		return "", fail("değiştirilmiş imza kabul edildi")
	}
	return fmt.Sprintf("sertifika %s: PDF + QR (%s…), imza geçerli, değiştirilmiş imza geçersiz, ödemesiz kullanıcı 422", c.ID[:8], c.VerifyURL[:min(40, len(c.VerifyURL))]), nil
}

func (r *Runner) checkF7() (string, error) {
	ov, err := r.C.Expect(200, "GET", "/api/reports/overview", r.token("editor"), nil)
	if err != nil {
		return "", err
	}
	if _, ok := ov.M()["submissions_by_status"]; !ok {
		return "", fail("genel rapor eksik")
	}
	rv, err := r.C.Expect(200, "GET", "/api/reports/reviewers", r.token("editor"), nil)
	if err != nil {
		return "", err
	}
	fin, err := r.C.Expect(200, "GET", "/api/reports/finance", r.token("registrar"), nil)
	if err != nil {
		return "", err
	}
	if inv, _ := fin.M()["invoices"].(map[string]any); inv["gap_free"] != true {
		return "", fail("finans raporu fatura boşluğu bildiriyor: %s", fin.Body)
	}
	csv, err := r.C.Expect(200, "GET", "/api/reports/submissions.csv", r.token("editor"), nil)
	if err != nil || !strings.HasPrefix(csv.Header.Get("Content-Type"), "text/csv") {
		return "", fail("CSV dışa aktarma")
	}
	lines := bytes.Count(csv.Body, []byte("\n"))
	nrev := len(rv.M()["items"].([]any))
	return fmt.Sprintf("genel durum, hakem performansı (%d hakem), finans (boşluksuz fatura), CSV (%d satır)", nrev, lines), nil
}

func (r *Runner) checkUI() (string, error) {
	if r.FrontendURL == "" {
		return "FRONTEND_URL verilmedi; arayüz kontrolü atlandı", nil
	}
	fc := NewClient(r.FrontendURL)
	idx, err := fc.Expect(200, "GET", "/", "", nil)
	if err != nil {
		return "", err
	}
	if !bytes.Contains(idx.Body, []byte(`id="root"`)) {
		return "", fail("index.html React kökünü içermiyor")
	}
	deep, err := fc.Expect(200, "GET", "/editor/assignments", "", nil)
	if err != nil || !bytes.Contains(deep.Body, []byte(`id="root"`)) {
		return "", fail("SPA derin bağlantı (history fallback) çalışmıyor")
	}
	px, err := fc.Do("GET", "/api/me", "", nil)
	if err != nil || px.Status != http.StatusUnauthorized {
		return "", fail("nginx /api vekili çalışmıyor: %v", statusOf(px, err))
	}
	return "index, SPA derin bağlantı ve /api vekili çalışıyor", nil
}

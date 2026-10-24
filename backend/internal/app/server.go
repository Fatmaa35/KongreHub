// Package app wires HTTP handlers, services and background jobs.
package app

import (
	"context"
	"errors"
	"net"
	"syscall"

	"crypto/rand"
	"encoding/hex"
	"github.com/jackc/pgx/v5/pgconn"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fatmakaplan/kongrehub/internal/auth"
	"github.com/fatmakaplan/kongrehub/internal/clock"
	"github.com/fatmakaplan/kongrehub/internal/config"
	"github.com/fatmakaplan/kongrehub/internal/domain/certsign"
	"github.com/fatmakaplan/kongrehub/internal/domain/pricing"
	"github.com/fatmakaplan/kongrehub/internal/httpx"
	"github.com/fatmakaplan/kongrehub/internal/idem"
	"github.com/fatmakaplan/kongrehub/internal/metrics"
	"github.com/fatmakaplan/kongrehub/internal/storage"
)

// Roles.
const (
	RAuthor    = "author"
	RReviewer  = "reviewer"
	REditor    = "editor"
	RRegistrar = "registrar"
	RAttendee  = "attendee"
	RAdmin     = "admin"
)

// AllRoles lists every role.
var AllRoles = []string{RAuthor, RReviewer, REditor, RRegistrar, RAttendee, RAdmin}

// RouteSpec documents one endpoint and who may call it (RBAC matrisi).
// Testler ve make verify bu kaydı okuyarak tüm uç noktalar × roller taraması yapar.
type RouteSpec struct {
	Method     string   `json:"method"`
	Pattern    string   `json:"pattern"`
	Roles      []string `json:"roles"`  // boş + Public=false → her oturumlu kullanıcı
	Public     bool     `json:"public"` // oturum gerekmez
	Idempotent bool     `json:"idempotency_key"`
}

// App holds dependencies.
type App struct {
	Cfg      config.Config
	Pool     *pgxpool.Pool
	Log      *slog.Logger
	Tokens   *auth.Tokens
	Clock    *clock.Clock
	Signer   *certsign.Signer
	Store    *storage.Store
	Calendar pricing.Calendar
	Metrics  *Metrics
	Routes   []RouteSpec

	jobMu sync.Mutex // aynı süreçte job'ların üst üste binmesini önler (DB kilidi ayrıca var)
}

// Metrics bundles the app's Prometheus series.
type Metrics struct {
	Reg            *metrics.Registry
	Requests       metrics.Counter
	Latency        metrics.Histogram
	AssignDuration metrics.Histogram
	JobRuns        metrics.Counter
	PaymentEvents  metrics.Counter
	Reminders      metrics.Counter
	PoolConns      metrics.Gauge
}

func newMetrics(pool *pgxpool.Pool) *Metrics {
	reg := metrics.NewRegistry()
	m := &Metrics{
		Reg:            reg,
		Requests:       reg.Counter("kongrehub_http_requests_total", "HTTP istek sayısı", "method", "route", "status"),
		Latency:        reg.Histogram("kongrehub_http_request_duration_seconds", "HTTP istek süresi", metrics.DefBuckets, "method", "route"),
		AssignDuration: reg.Histogram("kongrehub_assignment_solve_seconds", "Hakem atama çözüm süresi", metrics.DefBuckets),
		JobRuns:        reg.Counter("kongrehub_job_runs_total", "Arka plan job çalıştırmaları", "job", "result"),
		PaymentEvents:  reg.Counter("kongrehub_payment_events_total", "Ödeme sağlayıcı olayları", "outcome"),
		Reminders:      reg.Counter("kongrehub_review_reminders_total", "Gönderilen hakem hatırlatmaları", "kind"),
		PoolConns:      reg.Gauge("kongrehub_db_pool_connections", "Veritabanı havuzu bağlantıları", "state"),
	}
	reg.OnCollect(func() {
		if pool == nil {
			return
		}
		s := pool.Stat()
		m.PoolConns.Set(float64(s.AcquiredConns()), "acquired")
		m.PoolConns.Set(float64(s.IdleConns()), "idle")
		m.PoolConns.Set(float64(s.TotalConns()), "total")
	})
	return m
}

// New builds the app.
func New(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (*App, error) {
	signer, err := certsign.NewSigner(cfg.CertSigningSeed)
	if err != nil {
		return nil, err
	}
	st, err := storage.New(cfg.StorageDir)
	if err != nil {
		return nil, err
	}
	a := &App{
		Cfg: cfg, Pool: pool, Log: log, Tokens: auth.NewTokens(cfg.JWTSecret, cfg.AccessTTL),
		Clock: clock.New(), Signer: signer, Store: st, Calendar: pricing.DefaultCalendar(), Metrics: newMetrics(pool),
	}
	httpx.Classify = func(err error) *httpx.Error {
		var ce *pgconn.ConnectError
		var ne net.Error
		if errors.As(err, &ce) || errors.As(err, &ne) || errors.Is(err, syscall.ECONNREFUSED) {
			return httpx.ErrUnavailable
		}
		return nil
	}
	httpx.ErrorReporter = func(r *http.Request, err error) {
		log.Error("istek hatası", "route", routePattern(r), "request_id", r.Header.Get("X-Request-ID"), "err", err.Error())
	}
	return a, nil
}

type routeOpt func(*RouteSpec)

func public() routeOpt     { return func(s *RouteSpec) { s.Public = true } }
func idempotent() routeOpt { return func(s *RouteSpec) { s.Idempotent = true } }
func roles(rs ...string) routeOpt {
	return func(s *RouteSpec) { s.Roles = rs }
}

// handle registers a route with its RBAC rule. Rol kontrolü her uç noktada
// router seviyesinde yapılır; kaynak sahipliği (kendi bildirisi, kendi ataması)
// ise handler içinde ayrıca kontrol edilir.
func (a *App) handle(r chi.Router, method, pattern string, h httpx.Handler, opts ...routeOpt) {
	spec := RouteSpec{Method: method, Pattern: pattern}
	for _, o := range opts {
		o(&spec)
	}
	var hh http.Handler = h
	if spec.Idempotent {
		hh = idem.Middleware(a.Pool, true)(hh)
	}
	if !spec.Public {
		hh = auth.Require(spec.Roles...)(hh)
	}
	a.Routes = append(a.Routes, spec)
	r.Method(method, pattern, hh)
}

// Router builds the full HTTP handler.
func (a *App) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(a.requestID, a.recoverer, a.cors, a.observe, a.timeout, a.Tokens.Middleware)

	r.Get("/health", a.health)
	r.Method("GET", "/metrics", a.Metrics.Reg.Handler())
	r.Get("/openapi.yaml", serveOpenAPI)
	r.Get("/docs", serveDocs)

	a.registerAuth(r)
	a.registerSubmissions(r)
	a.registerAssignments(r)
	a.registerReviews(r)
	a.registerProgram(r)
	a.registerRegistrations(r)
	a.registerCertificates(r)
	a.registerReports(r)
	a.registerAdmin(r)

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { httpx.WriteError(w, httpx.ErrNotFound) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, httpx.E(http.StatusMethodNotAllowed, "method_not_allowed", "bu yöntem desteklenmiyor"))
	})
	sort.SliceStable(a.Routes, func(i, j int) bool { return a.Routes[i].Pattern < a.Routes[j].Pattern })
	return r
}

// ---- middleware -------------------------------------------------------------

func (a *App) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
			r.Header.Set("X-Request-ID", id)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

func (a *App) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				a.Log.Error("panic", "route", routePattern(r), "panic", v, "stack", string(debug.Stack()))
				httpx.WriteError(w, httpx.ErrInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *App) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range a.Cfg.CORSOrigins {
		allowed[strings.TrimSpace(o)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && allowed[o] {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Idempotent-Replayed")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	return s.ResponseWriter.Write(b)
}

func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
		return rc.RoutePattern()
	}
	return "unmatched"
}

// observe logs (yapılandırılmış JSON; token/parola asla) and records metrics.
func (a *App) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = 200
		}
		route := routePattern(r)
		d := time.Since(start)
		a.Metrics.Requests.Inc(r.Method, route, strconv.Itoa(sw.status))
		a.Metrics.Latency.Observe(d.Seconds(), r.Method, route)
		if route == "/health" || route == "/metrics" {
			return
		}
		attrs := []any{"method", r.Method, "route", route, "status", sw.status, "duration_ms", d.Milliseconds(), "request_id", r.Header.Get("X-Request-ID")}
		if p, ok := auth.From(r.Context()); ok {
			attrs = append(attrs, "user_id", p.UserID, "role", p.Role)
		}
		level := slog.LevelInfo
		if sw.status >= 500 {
			level = slog.LevelError
		}
		a.Log.Log(r.Context(), level, "http", attrs...)
	})
}

func (a *App) timeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), a.Cfg.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---- health -----------------------------------------------------------------

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{}
	status := http.StatusOK
	var one int
	if err := a.Pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		checks["database"] = "down"
		status = http.StatusServiceUnavailable
	} else {
		checks["database"] = "up"
	}
	if err := a.Store.Ping(); err != nil {
		checks["storage"] = "down"
		status = http.StatusServiceUnavailable
	} else {
		checks["storage"] = "up"
	}
	st := "ok"
	if status != http.StatusOK {
		st = "degraded"
	}
	httpx.WriteJSON(w, status, map[string]any{"status": st, "checks": checks, "time": a.Clock.Now().UTC()})
}

// principal returns the caller (handlers behind auth.Require always have one).
func principal(r *http.Request) auth.Principal {
	p, _ := auth.From(r.Context())
	return p
}

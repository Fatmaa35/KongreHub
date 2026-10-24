package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fatmakaplan/kongrehub/internal/httpx"
)

// Principal is the authenticated caller.
type Principal struct {
	UserID int64
	Role   string
	Name   string
}

type ctxKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal (ok=false for anonymous).
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Tokens issues and parses access tokens.
type Tokens struct {
	secret    []byte
	accessTTL time.Duration
	now       func() time.Time
}

func NewTokens(secret string, ttl time.Duration) *Tokens {
	return &Tokens{secret: []byte(secret), accessTTL: ttl, now: time.Now}
}

type claims struct {
	Role string `json:"role"`
	Name string `json:"name"`
	jwt.RegisteredClaims
}

// Access creates a signed HS256 JWT.
func (t *Tokens) Access(p Principal) (string, time.Time, error) {
	exp := t.now().Add(t.accessTTL)
	c := claims{Role: p.Role, Name: p.Name, RegisteredClaims: jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(p.UserID, 10),
		IssuedAt:  jwt.NewNumericDate(t.now()),
		ExpiresAt: jwt.NewNumericDate(exp),
		Issuer:    "kongrehub",
	}}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(t.secret)
	return s, exp, err
}

var ErrInvalidToken = errors.New("invalid token")

// Parse validates a token and returns the principal.
func (t *Tokens) Parse(s string) (Principal, error) {
	var c claims
	_, err := jwt.ParseWithClaims(s, &c, func(tok *jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("kongrehub"), jwt.WithTimeFunc(t.now))
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	return Principal{UserID: id, Role: c.Role, Name: c.Name}, nil
}

// NewRefreshToken returns an opaque token and its SHA-256 (only the hash is stored).
func NewRefreshToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	return tok, HashRefresh(tok), nil
}

func HashRefresh(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

// Middleware authenticates a Bearer token if present (anonymous otherwise).
func (t *Tokens) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if h == "" {
			next.ServeHTTP(w, r)
			return
		}
		raw, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			httpx.WriteError(w, httpx.ErrUnauthorized)
			return
		}
		p, err := t.Parse(raw)
		if err != nil {
			httpx.WriteError(w, httpx.E(http.StatusUnauthorized, "invalid_token", "oturum geçersiz veya süresi dolmuş"))
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// Require allows only the given roles (empty = any authenticated user).
// 401: kimlik yok; 403: rol yetkisiz.
func Require(roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := From(r.Context())
			if !ok {
				httpx.WriteError(w, httpx.ErrUnauthorized)
				return
			}
			if len(allowed) > 0 && !allowed[p.Role] {
				httpx.WriteError(w, httpx.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

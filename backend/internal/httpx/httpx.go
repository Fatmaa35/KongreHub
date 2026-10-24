// Package httpx contains the HTTP plumbing shared by all handlers: the
// consistent error envelope, JSON helpers, keyset cursors and middleware.
package httpx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Error is the API error. Mesajlar kullanıcıya gösterilebilir ve asla kişisel
// veri (ad, e-posta, kurum) içermez — çift kör kuralının hata yüzü.
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func E(status int, code, msg string) *Error { return &Error{Status: status, Code: code, Message: msg} }

func (e *Error) With(k string, v any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[k] = v
	return e
}

var (
	ErrUnauthorized = E(http.StatusUnauthorized, "unauthorized", "kimlik doğrulaması gerekli")
	ErrForbidden    = E(http.StatusForbidden, "forbidden", "bu işlem için yetkiniz yok")
	ErrNotFound     = E(http.StatusNotFound, "not_found", "kaynak bulunamadı")
	ErrInternal     = E(http.StatusInternalServerError, "internal", "beklenmeyen bir hata oluştu")
)

func Validation(msg string) *Error { return E(http.StatusBadRequest, "validation_error", msg) }
func Conflict(code, msg string) *Error { return E(http.StatusConflict, code, msg) }

// WriteJSON writes v with status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError renders any error as {"error":{code,message}}; unknown errors
// become 500 without leaking internals.
func WriteError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = ErrInternal
	}
	WriteJSON(w, e.Status, map[string]any{"error": e})
}

// Handler is an http handler returning an error.
type Handler func(w http.ResponseWriter, r *http.Request) error

// ErrorReporter receives internal (non-API) errors for logging.
var ErrorReporter = func(r *http.Request, err error) {}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		var e *Error
		if !errors.As(err, &e) {
			ErrorReporter(r, err)
		}
		WriteError(w, err)
	}
}

// DecodeJSON strictly decodes the body (bilinmeyen alan → 400).
func DecodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return Validation("istek gövdesi boş")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var se *json.SyntaxError
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &se), errors.Is(err, io.ErrUnexpectedEOF):
			return Validation("geçersiz JSON")
		case errors.As(err, &te):
			return Validation(fmt.Sprintf("'%s' alanının türü hatalı", te.Field))
		case errors.Is(err, io.EOF):
			return Validation("istek gövdesi boş")
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			return Validation("bilinmeyen alan: " + strings.TrimPrefix(err.Error(), "json: unknown field "))
		}
		return Validation("geçersiz istek gövdesi")
	}
	return nil
}

// Page is the keyset pagination envelope.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// PageParams parses ?limit=&cursor= (cursor = son görülen id, base64).
func PageParams(r *http.Request) (limit int, after int64, err error) {
	limit = 50
	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, Validation("limit 1 ile 200 arasında olmalı")
		}
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		b, derr := base64.RawURLEncoding.DecodeString(c)
		if derr != nil {
			return 0, 0, Validation("geçersiz cursor")
		}
		after, err = strconv.ParseInt(string(b), 10, 64)
		if err != nil || after <= 0 {
			return 0, 0, Validation("geçersiz cursor")
		}
	}
	return limit, after, nil
}

// Cursor encodes an id cursor.
func Cursor(id int64) string { return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10))) }

// IDParam parses a positive integer path value.
func IDParam(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrNotFound
	}
	return id, nil
}
